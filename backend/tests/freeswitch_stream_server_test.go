package tests

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bridgewithclawandfreeswitch/backend/internal/freeswitch"
	"bridgewithclawandfreeswitch/backend/internal/pipeline"
	"bridgewithclawandfreeswitch/backend/internal/session"
	"bridgewithclawandfreeswitch/backend/internal/tts"
	bridgews "bridgewithclawandfreeswitch/backend/internal/ws"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

type fakeStreamHandler struct {
	startCalls []pipeline.StreamStartRequest
	audioCalls []audioPush
	hangups    []string
	sessions   []*session.Session
}

func (f *fakeStreamHandler) HandleStreamStart(_ context.Context, req pipeline.StreamStartRequest) (*session.Session, error) {
	f.startCalls = append(f.startCalls, req)
	current := &session.Session{
		ID:        "sess-test",
		CallID:    req.CallID,
		Caller:    req.Caller,
		State:     session.StateListening,
		StartedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		Stream:    req.Stream,
	}
	f.sessions = append(f.sessions, current)
	return current, nil
}

func (f *fakeStreamHandler) HandleAudioFrame(_ context.Context, sessionID string, pcm []byte) error {
	f.audioCalls = append(f.audioCalls, audioPush{
		sessionID: sessionID,
		pcm:       append([]byte(nil), pcm...),
	})
	return nil
}

func (f *fakeStreamHandler) HandleHangup(_ context.Context, sessionID string, reason string) error {
	f.hangups = append(f.hangups, sessionID+":"+reason)
	return nil
}

func TestWebSocketStreamServerStartAndStopProtocol(t *testing.T) {
	handler := &fakeStreamHandler{}
	conn, cleanup, _ := newStreamTestConnection(t, handler)
	defer cleanup()

	if err := conn.WriteJSON(freeswitch.ControlMessage{
		Type:   freeswitch.ControlTypeStreamStart,
		CallID: "call-1",
		Caller: "alice",
		Stream: session.StreamMeta{
			Encoding:     "pcm_s16le",
			SampleRateHz: 16000,
			Channels:     1,
		},
	}); err != nil {
		t.Fatalf("failed to send stream.start: %v", err)
	}

	var ack freeswitch.ServerMessage
	if err := conn.ReadJSON(&ack); err != nil {
		t.Fatalf("failed to read stream ack: %v", err)
	}
	if ack.Type != freeswitch.ServerTypeStreamAck || !ack.Accepted || ack.SessionID == "" {
		t.Fatalf("unexpected ack payload: %+v", ack)
	}

	if err := conn.WriteJSON(freeswitch.ControlMessage{
		Type:   freeswitch.ControlTypeStreamStop,
		Reason: bridgews.ReasonHangup,
	}); err != nil {
		t.Fatalf("failed to send stream.stop: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for len(handler.hangups) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if len(handler.startCalls) != 1 {
		t.Fatalf("expected one stream.start call, got %d", len(handler.startCalls))
	}
	if len(handler.hangups) != 1 || handler.hangups[0] != "sess-test:"+bridgews.ReasonHangup {
		t.Fatalf("expected hangup reason to be propagated, got %+v", handler.hangups)
	}
}

func TestWebSocketStreamServerCloseTriggersHangup(t *testing.T) {
	handler := &fakeStreamHandler{}
	conn, cleanup, _ := newStreamTestConnection(t, handler)
	defer cleanup()

	if err := conn.WriteJSON(freeswitch.ControlMessage{
		Type:   freeswitch.ControlTypeStreamStart,
		CallID: "call-2",
		Caller: "bob",
		Stream: session.StreamMeta{
			Encoding:     "pcm_s16le",
			SampleRateHz: 16000,
			Channels:     1,
		},
	}); err != nil {
		t.Fatalf("failed to send stream.start: %v", err)
	}

	var ack freeswitch.ServerMessage
	if err := conn.ReadJSON(&ack); err != nil {
		t.Fatalf("failed to read ack: %v", err)
	}

	if err := conn.Close(); err != nil {
		t.Fatalf("failed to close websocket: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for len(handler.hangups) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if len(handler.hangups) != 1 || handler.hangups[0] != "sess-test:"+bridgews.ReasonWebSocketDisconnected {
		t.Fatalf("expected disconnect hangup, got %+v", handler.hangups)
	}
}

func TestWebSocketStreamServerRejectsInvalidControlMessage(t *testing.T) {
	handler := &fakeStreamHandler{}
	conn, cleanup, _ := newStreamTestConnection(t, handler)
	defer cleanup()

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"stream.start"}`)); err != nil {
		t.Fatalf("failed to write invalid control message: %v", err)
	}

	var message freeswitch.ServerMessage
	if err := conn.ReadJSON(&message); err != nil {
		t.Fatalf("failed to read protocol error: %v", err)
	}

	if message.Type != freeswitch.ServerTypeStreamError || message.Code != freeswitch.ProtocolCodeInvalidControlMessage {
		t.Fatalf("unexpected protocol error payload: %+v", message)
	}
}

func TestWebSocketStreamServerRejectsBinaryBeforeStart(t *testing.T) {
	handler := &fakeStreamHandler{}
	conn, cleanup, _ := newStreamTestConnection(t, handler)
	defer cleanup()

	if err := conn.WriteMessage(websocket.BinaryMessage, []byte{1, 2, 3}); err != nil {
		t.Fatalf("failed to write binary frame: %v", err)
	}

	var message freeswitch.ServerMessage
	if err := conn.ReadJSON(&message); err != nil {
		t.Fatalf("failed to read protocol error: %v", err)
	}

	if message.Type != freeswitch.ServerTypeStreamError || message.Code != freeswitch.ProtocolCodeStreamStartRequired {
		t.Fatalf("unexpected binary-before-start response: %+v", message)
	}
}

func TestWebSocketStreamServerRejectsStopBeforeStart(t *testing.T) {
	handler := &fakeStreamHandler{}
	conn, cleanup, _ := newStreamTestConnection(t, handler)
	defer cleanup()

	if err := conn.WriteJSON(freeswitch.ControlMessage{
		Type: freeswitch.ControlTypeStreamStop,
	}); err != nil {
		t.Fatalf("failed to send stop-before-start: %v", err)
	}

	var message freeswitch.ServerMessage
	if err := conn.ReadJSON(&message); err != nil {
		t.Fatalf("failed to read protocol error: %v", err)
	}

	if message.Type != freeswitch.ServerTypeStreamError || message.Code != freeswitch.ProtocolCodeStreamStopBeforeStart {
		t.Fatalf("unexpected stop-before-start response: %+v", message)
	}
}

func TestWebSocketStreamServerRejectsDuplicateStart(t *testing.T) {
	handler := &fakeStreamHandler{}
	conn, cleanup, _ := newStreamTestConnection(t, handler)
	defer cleanup()

	startMessage := freeswitch.ControlMessage{
		Type:   freeswitch.ControlTypeStreamStart,
		CallID: "call-dup",
		Caller: "eve",
		Stream: session.StreamMeta{
			Encoding:     "pcm_s16le",
			SampleRateHz: 16000,
			Channels:     1,
		},
	}
	if err := conn.WriteJSON(startMessage); err != nil {
		t.Fatalf("failed to send first stream.start: %v", err)
	}

	var ack freeswitch.ServerMessage
	if err := conn.ReadJSON(&ack); err != nil {
		t.Fatalf("failed to read first ack: %v", err)
	}

	if err := conn.WriteJSON(startMessage); err != nil {
		t.Fatalf("failed to send duplicate stream.start: %v", err)
	}

	var message freeswitch.ServerMessage
	if err := conn.ReadJSON(&message); err != nil {
		t.Fatalf("failed to read duplicate-start error: %v", err)
	}

	if message.Type != freeswitch.ServerTypeStreamError || message.Code != freeswitch.ProtocolCodeStreamAlreadyStarted {
		t.Fatalf("unexpected duplicate-start response: %+v", message)
	}
}

func TestWebSocketStreamServerPlayWritesStreamAudioEnvelope(t *testing.T) {
	handler := &fakeStreamHandler{}
	conn, cleanup, server := newStreamTestConnection(t, handler)
	defer cleanup()

	if err := conn.WriteJSON(freeswitch.ControlMessage{
		Type:   freeswitch.ControlTypeStreamStart,
		CallID: "call-play",
		Caller: "mallory",
		Stream: session.StreamMeta{
			Encoding:     "pcm_s16le",
			SampleRateHz: 16000,
			Channels:     1,
		},
	}); err != nil {
		t.Fatalf("failed to send stream.start: %v", err)
	}

	var ack freeswitch.ServerMessage
	if err := conn.ReadJSON(&ack); err != nil {
		t.Fatalf("failed to read ack: %v", err)
	}

	audio := tts.AudioPayload{
		Bytes:        testPCM16MonoWAV(t, []byte{1, 2, 3, 4}),
		Format:       "wav",
		SampleRateHz: 8000,
	}
	if err := server.Play(context.Background(), ack.SessionID, audio); err != nil {
		t.Fatalf("Play returned error: %v", err)
	}

	var message freeswitch.StreamAudioMessage
	if err := conn.ReadJSON(&message); err != nil {
		t.Fatalf("failed to read streamAudio message: %v", err)
	}

	if message.Type != freeswitch.ServerTypeStreamAudio {
		t.Fatalf("expected %s, got %s", freeswitch.ServerTypeStreamAudio, message.Type)
	}
	if message.Data.AudioDataType != "raw" {
		t.Fatalf("unexpected audioDataType: %s", message.Data.AudioDataType)
	}
	if message.Data.SampleRate != 8000 {
		t.Fatalf("unexpected sample rate: %d", message.Data.SampleRate)
	}
	if message.Data.AudioData != base64.StdEncoding.EncodeToString([]byte{1, 2, 3, 4}) {
		t.Fatalf("unexpected audioData payload: %s", message.Data.AudioData)
	}
}

func newStreamTestConnection(t *testing.T, handler *fakeStreamHandler) (*websocket.Conn, func(), *freeswitch.WebSocketStreamServer) {
	t.Helper()

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	server := freeswitch.NewWebSocketStreamServer(handler)
	server.RegisterRoutes(engine.Group("/ws"))

	testServer := httptest.NewServer(engine)
	wsURL := "ws" + strings.TrimPrefix(testServer.URL, "http") + "/ws/freeswitch/stream"

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		testServer.Close()
		t.Fatalf("failed to dial websocket server: %v", err)
	}

	cleanup := func() {
		_ = conn.Close()
		testServer.Close()
	}

	return conn, cleanup, server
}

func testPCM16MonoWAV(t *testing.T, pcm []byte) []byte {
	t.Helper()

	fmtPayload := []byte{
		0x01, 0x00,
		0x01, 0x00,
		0x40, 0x1F, 0x00, 0x00,
		0x80, 0x3E, 0x00, 0x00,
		0x02, 0x00,
		0x10, 0x00,
	}

	payloadSize := 4 + 8 + len(fmtPayload) + 8 + len(pcm)
	out := make([]byte, 0, payloadSize+8)
	out = append(out, []byte("RIFF")...)
	sizeBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(sizeBytes, uint32(payloadSize))
	out = append(out, sizeBytes...)
	out = append(out, []byte("WAVEfmt ")...)
	binary.LittleEndian.PutUint32(sizeBytes, uint32(len(fmtPayload)))
	out = append(out, sizeBytes...)
	out = append(out, fmtPayload...)
	out = append(out, []byte("data")...)
	binary.LittleEndian.PutUint32(sizeBytes, uint32(len(pcm)))
	out = append(out, sizeBytes...)
	out = append(out, pcm...)
	return out
}
