package openclaw

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"bridgewithclawandfreeswitch/backend/internal/config"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	openClawConnectMethod       = "connect"
	openClawSessionsCreate      = "sessions.create"
	openClawChatSend            = "chat.send"
	openClawControlUIClientID   = "openclaw-control-ui"
	openClawControlUIClientMode = "ui"
)

type GatewayWSClient struct {
	config      config.ProviderConfig
	dialer      websocket.Dialer
	mu          sync.Mutex
	sessionKeys map[string]string
}

type gatewayFrame struct {
	Type    string          `json:"type"`
	Event   string          `json:"event,omitempty"`
	ID      string          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	OK      bool            `json:"ok,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   *gatewayError   `json:"error,omitempty"`
}

type gatewayError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type gatewayConnectResponse struct {
	Snapshot struct {
		SessionDefaults struct {
			MainSessionKey string `json:"mainSessionKey"`
			DefaultAgentID string `json:"defaultAgentId"`
		} `json:"sessionDefaults"`
	} `json:"snapshot"`
}

type gatewaySessionCreateResponse struct {
	Key string `json:"key"`
}

type gatewayChatSendResponse struct {
	RunID string `json:"runId"`
}

type gatewayChatEvent struct {
	RunID      string `json:"runId"`
	SessionKey string `json:"sessionKey"`
	State      string `json:"state"`
	Message    struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
	ErrorMessage string `json:"errorMessage"`
}

type gatewayAgentEvent struct {
	RunID      string `json:"runId"`
	SessionKey string `json:"sessionKey"`
	Stream     string `json:"stream"`
	Data       struct {
		Text          string `json:"text"`
		Delta         string `json:"delta"`
		Phase         string `json:"phase"`
		LivenessState string `json:"livenessState"`
	} `json:"data"`
	ErrorMessage string `json:"errorMessage"`
}

func NewGatewayWSClient(cfg config.ProviderConfig) *GatewayWSClient {
	return &GatewayWSClient{
		config: cfg,
		dialer: websocket.Dialer{
			HandshakeTimeout: cfg.Timeout,
		},
		sessionKeys: make(map[string]string),
	}
}

func (c *GatewayWSClient) Reply(ctx context.Context, sessionID string, transcript string) (string, error) {
	if c.config.APIKey == "" {
		return "", fmt.Errorf("openclaw gateway requires api key token")
	}

	conn, err := c.dial(ctx)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	if err := c.completeConnect(ctx, conn, sessionID); err != nil {
		return "", err
	}

	sessionKey, err := c.resolveSessionKey(ctx, conn, sessionID)
	if err != nil {
		return "", err
	}

	runID, err := c.sendChat(ctx, conn, sessionKey, transcript)
	if err != nil {
		return "", err
	}

	return c.awaitFinalReply(ctx, conn, sessionKey, runID)
}

func (c *GatewayWSClient) dial(ctx context.Context) (*websocket.Conn, error) {
	headers := http.Header{}
	origin := c.config.Origin
	if origin == "" {
		origin = "http://127.0.0.1"
	}
	headers.Set("Origin", origin)

	conn, _, err := c.dialer.DialContext(ctx, c.config.Endpoint, headers)
	if err != nil {
		return nil, fmt.Errorf("dial openclaw gateway: %w", err)
	}
	return conn, nil
}

func (c *GatewayWSClient) completeConnect(ctx context.Context, conn *websocket.Conn, sessionID string) error {
	challengeSeen := false
	connectID := "connect-" + uuid.NewString()

	for {
		frame, err := c.readFrame(ctx, conn)
		if err != nil {
			return err
		}

		switch {
		case frame.Type == "event" && frame.Event == "connect.challenge":
			challengeSeen = true
			if err := c.writeJSON(ctx, conn, map[string]any{
				"type":   "req",
				"id":     connectID,
				"method": openClawConnectMethod,
				"params": map[string]any{
					"minProtocol": 3,
					"maxProtocol": 3,
					"client": map[string]any{
						"id":          openClawControlUIClientID,
						"displayName": "bridge-server",
						"version":     "0.1.0",
						"platform":    "linux",
						"mode":        openClawControlUIClientMode,
						"instanceId":  "bridge-" + sessionID,
					},
					"role":   "operator",
					"scopes": []string{"operator.read", "operator.write"},
					"auth": map[string]any{
						"token": c.config.APIKey,
					},
				},
			}); err != nil {
				return err
			}
		case frame.Type == "res" && frame.ID == connectID:
			if !frame.OK {
				return c.frameError(frame, "openclaw connect failed")
			}
			return nil
		case frame.Type == "res" && !challengeSeen:
			return fmt.Errorf("unexpected openclaw response before challenge")
		}
	}
}

func (c *GatewayWSClient) resolveSessionKey(ctx context.Context, conn *websocket.Conn, sessionID string) (string, error) {
	c.mu.Lock()
	cached, ok := c.sessionKeys[sessionID]
	c.mu.Unlock()
	if ok && cached != "" {
		return cached, nil
	}

	requestID := "session-create-" + uuid.NewString()
	bridgeKey := "bridge-" + sessionID
	if err := c.writeJSON(ctx, conn, map[string]any{
		"type":   "req",
		"id":     requestID,
		"method": openClawSessionsCreate,
		"params": map[string]any{
			"key":   bridgeKey,
			"label": bridgeKey,
		},
	}); err != nil {
		return "", err
	}

	for {
		frame, err := c.readFrame(ctx, conn)
		if err != nil {
			return "", err
		}
		if frame.Type != "res" || frame.ID != requestID {
			continue
		}
		if !frame.OK {
			return "", c.frameError(frame, "openclaw session create failed")
		}

		var payload gatewaySessionCreateResponse
		if err := json.Unmarshal(frame.Payload, &payload); err != nil {
			return "", fmt.Errorf("decode openclaw session create response: %w", err)
		}
		if payload.Key == "" {
			return "", fmt.Errorf("openclaw session create response missing key")
		}

		c.mu.Lock()
		c.sessionKeys[sessionID] = payload.Key
		c.mu.Unlock()
		return payload.Key, nil
	}
}

func (c *GatewayWSClient) sendChat(ctx context.Context, conn *websocket.Conn, sessionKey string, transcript string) (string, error) {
	requestID := "chat-send-" + uuid.NewString()
	if err := c.writeJSON(ctx, conn, map[string]any{
		"type":   "req",
		"id":     requestID,
		"method": openClawChatSend,
		"params": map[string]any{
			"sessionKey":     sessionKey,
			"message":        transcript,
			"idempotencyKey": "bridge-" + uuid.NewString(),
			"deliver":        false,
			"timeoutMs":      int(maxDuration(c.config.Timeout, 10*time.Second).Milliseconds()),
		},
	}); err != nil {
		return "", err
	}

	for {
		frame, err := c.readFrame(ctx, conn)
		if err != nil {
			return "", err
		}
		if frame.Type != "res" || frame.ID != requestID {
			continue
		}
		if !frame.OK {
			return "", c.frameError(frame, "openclaw chat.send failed")
		}

		var payload gatewayChatSendResponse
		if err := json.Unmarshal(frame.Payload, &payload); err != nil {
			return "", fmt.Errorf("decode openclaw chat.send response: %w", err)
		}
		if payload.RunID == "" {
			return "", fmt.Errorf("openclaw chat.send response missing run id")
		}
		return payload.RunID, nil
	}
}

func (c *GatewayWSClient) awaitFinalReply(ctx context.Context, conn *websocket.Conn, sessionKey string, runID string) (string, error) {
	var latest string

	for {
		frame, err := c.readFrame(ctx, conn)
		if err != nil {
			return "", err
		}
		if frame.Type != "event" {
			continue
		}

		switch frame.Event {
		case "chat":
			var payload gatewayChatEvent
			if err := json.Unmarshal(frame.Payload, &payload); err != nil {
				return "", fmt.Errorf("decode openclaw chat event: %w", err)
			}
			if !gatewayEventMatches(sessionKey, runID, payload.SessionKey, payload.RunID) {
				continue
			}

			text := extractGatewayChatText(payload)
			if text != "" {
				latest = text
			}

			switch payload.State {
			case "final":
				if latest != "" {
					return latest, nil
				}
				// Some gateway versions emit an empty chat final before the
				// assistant stream/lifecycle end arrives. Keep waiting for the
				// matching agent events instead of failing early.
				continue
			case "error", "aborted":
				if payload.ErrorMessage == "" {
					payload.ErrorMessage = "openclaw chat run failed"
				}
				return "", fmt.Errorf(payload.ErrorMessage)
			}
		case "agent":
			var payload gatewayAgentEvent
			if err := json.Unmarshal(frame.Payload, &payload); err != nil {
				return "", fmt.Errorf("decode openclaw agent event: %w", err)
			}
			if !gatewayEventMatches(sessionKey, runID, payload.SessionKey, payload.RunID) {
				continue
			}

			switch payload.Stream {
			case "assistant":
				switch {
				case payload.Data.Text != "":
					latest = payload.Data.Text
				case payload.Data.Delta != "":
					latest += payload.Data.Delta
				}
			case "lifecycle":
				switch payload.Data.Phase {
				case "end", "complete", "finish", "finished":
					if latest == "" {
						return "", fmt.Errorf("openclaw agent lifecycle ended without reply text")
					}
					return latest, nil
				case "error", "aborted":
					if payload.ErrorMessage == "" {
						payload.ErrorMessage = "openclaw agent run failed"
					}
					return "", fmt.Errorf(payload.ErrorMessage)
				}
			}
		}
	}
}

func gatewayEventMatches(expectedSessionKey string, expectedRunID string, actualSessionKey string, actualRunID string) bool {
	if actualSessionKey != "" && expectedSessionKey != "" && actualSessionKey != expectedSessionKey {
		return false
	}
	if actualRunID != "" && expectedRunID != "" && actualRunID != expectedRunID {
		return false
	}
	return true
}

func extractGatewayChatText(payload gatewayChatEvent) string {
	text := ""
	for _, item := range payload.Message.Content {
		if item.Type == "text" && item.Text != "" {
			text += item.Text
		}
	}
	return text
}

func (c *GatewayWSClient) readFrame(ctx context.Context, conn *websocket.Conn) (gatewayFrame, error) {
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetReadDeadline(deadline)
	} else if c.config.Timeout > 0 {
		_ = conn.SetReadDeadline(time.Now().Add(c.config.Timeout))
	}

	_, payload, err := conn.ReadMessage()
	if err != nil {
		return gatewayFrame{}, fmt.Errorf("read openclaw frame: %w", err)
	}

	var frame gatewayFrame
	if err := json.Unmarshal(payload, &frame); err != nil {
		return gatewayFrame{}, fmt.Errorf("decode openclaw frame: %w", err)
	}
	return frame, nil
}

func (c *GatewayWSClient) writeJSON(ctx context.Context, conn *websocket.Conn, payload any) error {
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetWriteDeadline(deadline)
	} else if c.config.Timeout > 0 {
		_ = conn.SetWriteDeadline(time.Now().Add(c.config.Timeout))
	}

	if err := conn.WriteJSON(payload); err != nil {
		return fmt.Errorf("write openclaw frame: %w", err)
	}
	return nil
}

func (c *GatewayWSClient) frameError(frame gatewayFrame, prefix string) error {
	if frame.Error != nil && frame.Error.Message != "" {
		return fmt.Errorf("%s: %s", prefix, frame.Error.Message)
	}
	return fmt.Errorf(prefix)
}

func maxDuration(values ...time.Duration) time.Duration {
	var best time.Duration
	for _, value := range values {
		if value > best {
			best = value
		}
	}
	return best
}
