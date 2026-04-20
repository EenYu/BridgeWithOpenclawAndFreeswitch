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
	upgrader         websocket.Upgrader
	mu               sync.RWMutex
	clients          map[*hubClient]struct{}
	policy           AccessPolicy
	broadcastBufSize int
	writeTimeout     time.Duration
}

type hubClient struct {
	conn   *websocket.Conn
	send   chan []byte
	mu     sync.Mutex
	closed bool
}

func NewHub(policy AccessPolicy, broadcastBufSize int, writeTimeout time.Duration) *Hub {
	if broadcastBufSize <= 0 {
		broadcastBufSize = 1
	}
	if writeTimeout <= 0 {
		writeTimeout = 2 * time.Second
	}

	return &Hub{
		upgrader: websocket.Upgrader{
			CheckOrigin: func(_ *http.Request) bool { return true },
		},
		clients:          make(map[*hubClient]struct{}),
		policy:           policy,
		broadcastBufSize: broadcastBufSize,
		writeTimeout:     writeTimeout,
	}
}

func (h *Hub) ServeWS(c *gin.Context) {
	if status, err := h.policy.Validate(c.Request); err != nil {
		c.AbortWithStatusJSON(status, gin.H{"error": err.Error()})
		return
	}

	conn, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}

	client := &hubClient{
		conn: conn,
		send: make(chan []byte, h.broadcastBufSize),
	}
	h.addClient(client)

	go h.writeLoop(client)
	go h.readLoop(client)
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

	for _, client := range h.snapshotClients() {
		if client.enqueue(message) {
			continue
		}
		log.Printf("drop slow websocket client while broadcasting %s", eventType)
		h.closeClient(client)
	}
}

func (h *Hub) readLoop(client *hubClient) {
	defer h.closeClient(client)

	for {
		if _, _, err := client.conn.ReadMessage(); err != nil {
			return
		}
	}
}

func (h *Hub) writeLoop(client *hubClient) {
	defer h.closeClient(client)

	for message := range client.send {
		if err := client.conn.SetWriteDeadline(time.Now().Add(h.writeTimeout)); err != nil {
			return
		}
		if err := client.conn.WriteMessage(websocket.TextMessage, message); err != nil {
			return
		}
	}
}

func (h *Hub) addClient(client *hubClient) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.clients[client] = struct{}{}
}

func (h *Hub) snapshotClients() []*hubClient {
	h.mu.RLock()
	defer h.mu.RUnlock()

	clients := make([]*hubClient, 0, len(h.clients))
	for client := range h.clients {
		clients = append(clients, client)
	}
	return clients
}

func (h *Hub) closeClient(client *hubClient) {
	_ = client.closeSend()

	h.mu.Lock()
	delete(h.clients, client)
	h.mu.Unlock()

	if client.conn != nil {
		_ = client.conn.Close()
	}
}

func (c *hubClient) enqueue(message []byte) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return false
	}

	select {
	case c.send <- message:
		return true
	default:
		c.closed = true
		close(c.send)
		return false
	}
}

func (c *hubClient) closeSend() bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return false
	}
	c.closed = true
	close(c.send)
	return true
}
