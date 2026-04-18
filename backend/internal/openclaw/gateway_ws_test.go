package openclaw

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"bridgewithclawandfreeswitch/backend/internal/config"

	"github.com/gorilla/websocket"
)

func TestGatewayWSClientReplyUsesAgentLifecycleFallback(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade gateway ws: %v", err)
		}
		defer conn.Close()

		if err := conn.WriteJSON(map[string]any{
			"type":  "event",
			"event": "connect.challenge",
			"payload": map[string]any{
				"nonce": "nonce-1",
				"ts":    123,
			},
		}); err != nil {
			t.Fatalf("write challenge: %v", err)
		}

		var connectReq testGatewayRequest
		if err := conn.ReadJSON(&connectReq); err != nil {
			t.Fatalf("read connect request: %v", err)
		}
		if err := conn.WriteJSON(map[string]any{
			"type": "res",
			"id":   connectReq.ID,
			"ok":   true,
			"payload": map[string]any{
				"type":     "hello-ok",
				"protocol": 3,
			},
		}); err != nil {
			t.Fatalf("write connect response: %v", err)
		}

		var sessionReq testGatewayRequest
		if err := conn.ReadJSON(&sessionReq); err != nil {
			t.Fatalf("read session create request: %v", err)
		}
		sessionKey := "agent:main:" + sessionReq.Params["key"].(string)
		if err := conn.WriteJSON(map[string]any{
			"type": "res",
			"id":   sessionReq.ID,
			"ok":   true,
			"payload": map[string]any{
				"key": sessionKey,
			},
		}); err != nil {
			t.Fatalf("write session create response: %v", err)
		}

		var chatReq testGatewayRequest
		if err := conn.ReadJSON(&chatReq); err != nil {
			t.Fatalf("read chat request: %v", err)
		}
		if err := conn.WriteJSON(map[string]any{
			"type": "res",
			"id":   chatReq.ID,
			"ok":   true,
			"payload": map[string]any{
				"runId": "run-1",
			},
		}); err != nil {
			t.Fatalf("write chat.send response: %v", err)
		}
		if err := conn.WriteJSON(map[string]any{
			"type":  "event",
			"event": "agent",
			"payload": map[string]any{
				"runId":      "run-1",
				"sessionKey": sessionKey,
				"stream":     "assistant",
				"data": map[string]any{
					"text":  "agent reply",
					"delta": "agent reply",
				},
			},
		}); err != nil {
			t.Fatalf("write agent assistant event: %v", err)
		}
		if err := conn.WriteJSON(map[string]any{
			"type":  "event",
			"event": "agent",
			"payload": map[string]any{
				"runId":      "run-1",
				"sessionKey": sessionKey,
				"stream":     "lifecycle",
				"data": map[string]any{
					"phase": "end",
				},
			},
		}); err != nil {
			t.Fatalf("write agent lifecycle end event: %v", err)
		}
	}))
	defer server.Close()

	cfg := config.ProviderConfig{
		Vendor:   "openclaw-gateway-ws",
		Endpoint: "ws" + server.URL[len("http"):],
		Enabled:  true,
		APIKey:   "oc-token",
		Origin:   "http://127.0.0.1",
		Timeout:  3 * time.Second,
	}

	client := NewGatewayWSClient(cfg)
	reply, err := client.Reply(context.Background(), "sess-1", "need help")
	if err != nil {
		t.Fatalf("Reply returned error: %v", err)
	}
	if reply != "agent reply" {
		t.Fatalf("unexpected reply %q", reply)
	}
}

func TestGatewayWSClientReplyWaitsPastEmptyChatFinal(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("upgrade gateway ws: %v", err)
		}
		defer conn.Close()

		if err := conn.WriteJSON(map[string]any{
			"type":  "event",
			"event": "connect.challenge",
			"payload": map[string]any{
				"nonce": "nonce-1",
				"ts":    123,
			},
		}); err != nil {
			t.Fatalf("write challenge: %v", err)
		}

		var connectReq testGatewayRequest
		if err := conn.ReadJSON(&connectReq); err != nil {
			t.Fatalf("read connect request: %v", err)
		}
		if err := conn.WriteJSON(map[string]any{
			"type": "res",
			"id":   connectReq.ID,
			"ok":   true,
			"payload": map[string]any{
				"type":     "hello-ok",
				"protocol": 3,
			},
		}); err != nil {
			t.Fatalf("write connect response: %v", err)
		}

		var sessionReq testGatewayRequest
		if err := conn.ReadJSON(&sessionReq); err != nil {
			t.Fatalf("read session create request: %v", err)
		}
		sessionKey := "agent:main:" + sessionReq.Params["key"].(string)
		if err := conn.WriteJSON(map[string]any{
			"type": "res",
			"id":   sessionReq.ID,
			"ok":   true,
			"payload": map[string]any{
				"key": sessionKey,
			},
		}); err != nil {
			t.Fatalf("write session create response: %v", err)
		}

		var chatReq testGatewayRequest
		if err := conn.ReadJSON(&chatReq); err != nil {
			t.Fatalf("read chat request: %v", err)
		}
		if err := conn.WriteJSON(map[string]any{
			"type": "res",
			"id":   chatReq.ID,
			"ok":   true,
			"payload": map[string]any{
				"runId": "run-1",
			},
		}); err != nil {
			t.Fatalf("write chat.send response: %v", err)
		}
		if err := conn.WriteJSON(map[string]any{
			"type":  "event",
			"event": "chat",
			"payload": map[string]any{
				"runId":      "run-1",
				"sessionKey": sessionKey,
				"state":      "final",
				"message": map[string]any{
					"role":    "assistant",
					"content": []map[string]any{},
				},
			},
		}); err != nil {
			t.Fatalf("write empty chat final event: %v", err)
		}
		if err := conn.WriteJSON(map[string]any{
			"type":  "event",
			"event": "agent",
			"payload": map[string]any{
				"runId":      "run-1",
				"sessionKey": sessionKey,
				"stream":     "assistant",
				"data": map[string]any{
					"text": "agent reply",
				},
			},
		}); err != nil {
			t.Fatalf("write agent assistant event: %v", err)
		}
		if err := conn.WriteJSON(map[string]any{
			"type":  "event",
			"event": "agent",
			"payload": map[string]any{
				"runId":      "run-1",
				"sessionKey": sessionKey,
				"stream":     "lifecycle",
				"data": map[string]any{
					"phase": "end",
				},
			},
		}); err != nil {
			t.Fatalf("write agent lifecycle end event: %v", err)
		}
	}))
	defer server.Close()

	cfg := config.ProviderConfig{
		Vendor:   "openclaw-gateway-ws",
		Endpoint: "ws" + server.URL[len("http"):],
		Enabled:  true,
		APIKey:   "oc-token",
		Origin:   "http://127.0.0.1",
		Timeout:  3 * time.Second,
	}

	client := NewGatewayWSClient(cfg)
	reply, err := client.Reply(context.Background(), "sess-2", "need help")
	if err != nil {
		t.Fatalf("Reply returned error: %v", err)
	}
	if reply != "agent reply" {
		t.Fatalf("unexpected reply %q", reply)
	}
}

type testGatewayRequest struct {
	Type   string         `json:"type"`
	ID     string         `json:"id"`
	Method string         `json:"method"`
	Params map[string]any `json:"params"`
}
