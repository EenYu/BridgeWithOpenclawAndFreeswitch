package ws

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"bridgewithclawandfreeswitch/backend/internal/config"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func TestHubServeWSAcceptsAuthorizedClientAndBroadcasts(t *testing.T) {
	hub := NewHub(
		NewAccessPolicy("dashboard", config.WebSocketEndpointConfig{
			AuthToken:      "dashboard-secret",
			AllowedOrigins: []string{"https://console.example.com"},
		}),
		2,
		time.Second,
	)
	conn, cleanup := newHubTestConnection(t, hub, "?access_token=dashboard-secret", http.Header{
		"Origin": []string{"https://console.example.com"},
	})
	defer cleanup()

	hub.Broadcast("session.created", "sess-1", map[string]any{"accepted": true})

	var event Event
	if err := conn.ReadJSON(&event); err != nil {
		t.Fatalf("failed to read websocket event: %v", err)
	}
	if event.Type != "session.created" || event.SessionID != "sess-1" {
		t.Fatalf("unexpected websocket event %+v", event)
	}
}

func TestHubServeWSRejectsUnauthorizedClient(t *testing.T) {
	hub := NewHub(
		NewAccessPolicy("dashboard", config.WebSocketEndpointConfig{
			AuthToken: "dashboard-secret",
		}),
		2,
		time.Second,
	)
	testServer := newHubTestServer(hub)
	defer testServer.Close()

	wsURL := "ws" + strings.TrimPrefix(testServer.URL, "http") + "/ws"
	_, response, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("expected unauthorized websocket dial to fail")
	}
	if response == nil || response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 unauthorized, got %#v", response)
	}
}

func TestHubServeWSRejectsDisallowedOrigin(t *testing.T) {
	hub := NewHub(
		NewAccessPolicy("dashboard", config.WebSocketEndpointConfig{
			AllowedOrigins: []string{"https://console.example.com"},
		}),
		2,
		time.Second,
	)
	testServer := newHubTestServer(hub)
	defer testServer.Close()

	wsURL := "ws" + strings.TrimPrefix(testServer.URL, "http") + "/ws"
	headers := http.Header{
		"Origin": []string{"https://evil.example.com"},
	}
	_, response, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err == nil {
		t.Fatal("expected forbidden websocket dial to fail")
	}
	if response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 forbidden, got %#v", response)
	}
}

func TestHubBroadcastDropsBackpressuredClientWithoutBlocking(t *testing.T) {
	hub := NewHub(AccessPolicy{}, 1, time.Second)
	slowClient := &hubClient{send: make(chan []byte, 1)}
	slowClient.send <- []byte("busy")
	fastClient := &hubClient{send: make(chan []byte, 1)}
	hub.addClient(slowClient)
	hub.addClient(fastClient)

	start := time.Now()
	hub.Broadcast("session.updated", "sess-2", map[string]any{"state": "listening"})
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("expected non-blocking broadcast, took %s", elapsed)
	}

	if len(hub.snapshotClients()) != 1 {
		t.Fatalf("expected slow client to be dropped, got %d active clients", len(hub.snapshotClients()))
	}

	select {
	case message := <-fastClient.send:
		var event Event
		if err := json.Unmarshal(message, &event); err != nil {
			t.Fatalf("failed to decode fast client event: %v", err)
		}
		if event.Type != "session.updated" || event.SessionID != "sess-2" {
			t.Fatalf("unexpected fast client event %+v", event)
		}
	default:
		t.Fatal("expected fast client to receive broadcast")
	}
}

func newHubTestServer(hub *Hub) *httptest.Server {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/ws", hub.ServeWS)
	return httptest.NewServer(engine)
}

func newHubTestConnection(t *testing.T, hub *Hub, query string, headers http.Header) (*websocket.Conn, func()) {
	t.Helper()

	testServer := newHubTestServer(hub)
	wsURL := "ws" + strings.TrimPrefix(testServer.URL, "http") + "/ws" + query

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		testServer.Close()
		t.Fatalf("failed to dial websocket server: %v", err)
	}

	return conn, func() {
		_ = conn.Close()
		testServer.Close()
	}
}
