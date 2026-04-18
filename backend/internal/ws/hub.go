package ws

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

type Event struct {
	Type      string         `json:"type"`
	SessionID string         `json:"sessionId"`
	Timestamp time.Time      `json:"timestamp"`
	Data      map[string]any `json:"data"`
}

type Hub struct {
	upgrader websocket.Upgrader
	mu       sync.RWMutex
	clients  map[*websocket.Conn]struct{}
}

func NewHub() *Hub {
	return &Hub{
		upgrader: websocket.Upgrader{
			CheckOrigin: func(_ *http.Request) bool { return true },
		},
		clients: make(map[*websocket.Conn]struct{}),
	}
}

func (h *Hub) ServeWS(c *gin.Context) {
	conn, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}

	h.mu.Lock()
	h.clients[conn] = struct{}{}
	h.mu.Unlock()

	go h.readLoop(conn)
}

func (h *Hub) Broadcast(eventType string, sessionID string, payload map[string]any) {
	event := Event{
		Type:      eventType,
		SessionID: sessionID,
		Timestamp: time.Now().UTC(),
		Data:      payload,
	}

	message, err := json.Marshal(event)
	if err != nil {
		log.Printf("marshal websocket event: %v", err)
		return
	}

	h.mu.RLock()
	clients := make([]*websocket.Conn, 0, len(h.clients))
	for client := range h.clients {
		clients = append(clients, client)
	}
	h.mu.RUnlock()

	for _, client := range clients {
		if err := client.WriteMessage(websocket.TextMessage, message); err != nil {
			h.remove(client)
		}
	}
}

func (h *Hub) readLoop(conn *websocket.Conn) {
	defer h.remove(conn)

	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

func (h *Hub) remove(conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()

	delete(h.clients, conn)
	_ = conn.Close()
}
