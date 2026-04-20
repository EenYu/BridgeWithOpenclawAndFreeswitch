package freeswitch

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"bridgewithclawandfreeswitch/backend/internal/pipeline"
	"bridgewithclawandfreeswitch/backend/internal/session"
	"bridgewithclawandfreeswitch/backend/internal/tts"
	bridgews "bridgewithclawandfreeswitch/backend/internal/ws"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

type StreamEventHandler interface {
	HandleStreamStart(ctx context.Context, req pipeline.StreamStartRequest) (*session.Session, error)
	HandleAudioFrame(ctx context.Context, sessionID string, pcm []byte) error
	HandleHangup(ctx context.Context, sessionID string, reason string) error
}

type StreamServer interface {
	RegisterRoutes(group gin.IRoutes)
}

type WebSocketStreamServer struct {
	handler  StreamEventHandler
	upgrader websocket.Upgrader
	mu       sync.RWMutex
	outputs  map[string]*streamOutput
	policy   bridgews.AccessPolicy
}

type streamOutput struct {
	callID string
	conn   *websocket.Conn
	stream session.StreamMeta
	mu     sync.Mutex
}

func NewWebSocketStreamServer(handler StreamEventHandler, policy bridgews.AccessPolicy) *WebSocketStreamServer {
	return &WebSocketStreamServer{
		handler: handler,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(_ *http.Request) bool { return true },
		},
		outputs: make(map[string]*streamOutput),
		policy:  policy,
	}
}

func (s *WebSocketStreamServer) SetHandler(handler StreamEventHandler) {
	s.handler = handler
}

func (s *WebSocketStreamServer) RegisterRoutes(group gin.IRoutes) {
	group.GET("/freeswitch/stream", s.handleWebSocket)
}

func (s *WebSocketStreamServer) handleWebSocket(c *gin.Context) {
	if s.handler == nil {
		c.Status(http.StatusServiceUnavailable)
		return
	}
	if status, err := s.policy.Validate(c.Request); err != nil {
		c.AbortWithStatusJSON(status, gin.H{"error": err.Error()})
		return
	}

	conn, err := s.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}

	var sessionID string
	var closed bool

	defer func() {
		if sessionID != "" {
			s.unregisterOutput(sessionID)
		}
		_ = conn.Close()
	}()

	for {
		messageType, payload, err := conn.ReadMessage()
		if err != nil {
			if sessionID != "" && !closed {
				_ = s.handler.HandleHangup(c.Request.Context(), sessionID, bridgews.ReasonWebSocketDisconnected)
			}
			return
		}

		switch messageType {
		case websocket.TextMessage:
			control, err := parseControlMessage(payload)
			if err != nil {
				s.writeProtocolError(conn, err)
				return
			}

			switch control.Type {
			case ControlTypeStreamStart:
				if sessionID != "" {
					s.writeProtocolError(conn, errStreamAlreadyStarted)
					return
				}

				created, err := s.handler.HandleStreamStart(c.Request.Context(), pipeline.StreamStartRequest{
					CallID: control.CallID,
					Caller: control.Caller,
					Stream: control.Stream,
				})
				if err != nil {
					s.writeProtocolError(conn, err)
					return
				}

				sessionID = created.ID
				s.registerOutput(sessionID, control.CallID, conn, control.Stream)
				_ = conn.WriteJSON(ServerMessage{
					Type:      ServerTypeStreamAck,
					Accepted:  true,
					SessionID: sessionID,
				})
			case ControlTypeStreamStop:
				if sessionID == "" {
					s.writeProtocolError(conn, errStreamStopBeforeStart)
					return
				}

				_ = s.handler.HandleHangup(c.Request.Context(), sessionID, normalizeStopReason(control.Reason))
				closed = true
				s.unregisterOutput(sessionID)
				return
			default:
				s.writeProtocolError(conn, errInvalidControlMessage)
				return
			}
		case websocket.BinaryMessage:
			if sessionID == "" {
				s.writeProtocolError(conn, errStreamStartRequired)
				return
			}

			if err := s.handler.HandleAudioFrame(c.Request.Context(), sessionID, payload); err != nil {
				s.writeProtocolError(conn, err)
				return
			}
		default:
			s.writeProtocolError(conn, errUnsupportedFrame)
			return
		}
	}
}

func (s *WebSocketStreamServer) Play(_ context.Context, sessionID string, audio tts.AudioPayload) error {
	output, ok := s.getOutput(sessionID)
	if !ok {
		return nil
	}

	output.mu.Lock()
	defer output.mu.Unlock()

	if s.tryNativePlayback(output.callID, audio) {
		return nil
	}

	if err := output.conn.WriteJSON(newStreamAudioMessage(output.stream, audio)); err != nil {
		s.unregisterOutput(sessionID)
		return err
	}

	return nil
}

func (s *WebSocketStreamServer) Interrupt(_ context.Context, _ string) error {
	return nil
}

func (s *WebSocketStreamServer) writeProtocolError(conn *websocket.Conn, err error) {
	_ = conn.WriteJSON(ServerMessage{
		Type:  ServerTypeStreamError,
		Code:  protocolCode(err),
		Error: err.Error(),
	})
}

func (s *WebSocketStreamServer) registerOutput(sessionID string, callID string, conn *websocket.Conn, stream session.StreamMeta) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.outputs[sessionID] = &streamOutput{
		callID: callID,
		conn:   conn,
		stream: stream,
	}
}

func (s *WebSocketStreamServer) unregisterOutput(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.outputs, sessionID)
}

func (s *WebSocketStreamServer) getOutput(sessionID string) (*streamOutput, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	output, ok := s.outputs[sessionID]
	return output, ok
}

func (s *WebSocketStreamServer) tryNativePlayback(callID string, audio tts.AudioPayload) bool {
	if callID == "" || tts.NormalizeAudioFormat(audio.Format) != "wav" || len(audio.Bytes) == 0 {
		return false
	}

	path, err := writePlaybackWAVFile(callID, audio.Bytes)
	if err != nil {
		return false
	}

	if err := runFSPlaybackCommand(callID, path); err != nil {
		_ = os.Remove(path)
		return false
	}

	go cleanupPlaybackFile(path, 2*time.Minute)
	return true
}

func writePlaybackWAVFile(callID string, audio []byte) (string, error) {
	safeCallID := strings.Map(func(ch rune) rune {
		switch {
		case ch >= 'a' && ch <= 'z':
			return ch
		case ch >= 'A' && ch <= 'Z':
			return ch
		case ch >= '0' && ch <= '9':
			return ch
		case ch == '-', ch == '_':
			return ch
		default:
			return '_'
		}
	}, callID)
	if safeCallID == "" {
		safeCallID = "bridge"
	}

	filename := fmt.Sprintf("%s_%d.wav", safeCallID, time.Now().UnixNano())
	path := filepath.Join(os.TempDir(), filename)
	if err := os.WriteFile(path, audio, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func runFSPlaybackCommand(callID string, path string) error {
	cliPath := "/usr/local/freeswitch/bin/fs_cli"
	if _, err := os.Stat(cliPath); err != nil {
		cliPath = "fs_cli"
	}

	command := exec.Command(cliPath, "-x", fmt.Sprintf("uuid_broadcast %s playback::%s aleg", callID, path))
	output, err := command.CombinedOutput()
	if err != nil {
		return err
	}
	if !strings.Contains(string(output), "+OK") {
		return fmt.Errorf("uuid_broadcast failed: %s", strings.TrimSpace(string(output)))
	}
	return nil
}

func cleanupPlaybackFile(path string, delay time.Duration) {
	time.Sleep(delay)
	_ = os.Remove(path)
}
