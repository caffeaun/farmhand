package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/caffeaun/farmhand/internal/db"
	"github.com/caffeaun/farmhand/internal/events"
	"github.com/rs/zerolog"
)

const maxWSConnections = 100

// wsDeviceRepoAPI provides the initial device snapshot on connect.
type wsDeviceRepoAPI interface {
	FindAll(filter db.DeviceFilter) ([]db.Device, error)
}

// WSMessage is the JSON envelope sent to WebSocket clients.
type WSMessage struct {
	Type    string      `json:"type"`
	Payload interface{} `json:"payload"`
}

// wsClient is a single WebSocket connection.
type wsClient struct {
	conn *websocket.Conn
	send chan []byte
}

// Hub manages WebSocket connections and broadcasts events from the EventBus
// to all connected clients.
type Hub struct {
	mu         sync.Mutex
	clients    map[*wsClient]struct{}
	deviceRepo wsDeviceRepoAPI
	bus        *events.Bus
	logger     zerolog.Logger
}

// NewHub creates a new WebSocket hub.
func NewHub(deviceRepo wsDeviceRepoAPI, bus *events.Bus, logger zerolog.Logger) *Hub {
	return &Hub{
		clients:    make(map[*wsClient]struct{}),
		deviceRepo: deviceRepo,
		bus:        bus,
		logger:     logger,
	}
}

// Run starts the hub's event forwarding loop. It subscribes to the EventBus
// and broadcasts relevant events to all connected WebSocket clients.
// Blocks until ctx is cancelled.
func (h *Hub) Run(ctx context.Context) {
	sub := h.bus.Subscribe()
	defer h.bus.Unsubscribe(sub)

	for {
		select {
		case event := <-sub:
			var msgType string
			switch event.Type {
			case events.DeviceOnline, events.DeviceOffline, events.DeviceStatusChanged:
				msgType = "device_update"
			case events.JobStarted, events.JobCompleted, events.JobFailed:
				msgType = "job_update"
			default:
				continue
			}
			msg := WSMessage{Type: msgType, Payload: event.Payload}
			data, err := json.Marshal(msg)
			if err != nil {
				h.logger.Error().Err(err).Msg("ws hub: marshal event")
				continue
			}
			h.broadcast(data)

		case <-ctx.Done():
			h.mu.Lock()
			for c := range h.clients {
				close(c.send)
				delete(h.clients, c)
			}
			h.mu.Unlock()
			return
		}
	}
}

// broadcast sends data to all connected clients. Slow clients are dropped.
func (h *Hub) broadcast(data []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for c := range h.clients {
		select {
		case c.send <- data:
		default:
			// Client too slow — drop it.
			close(c.send)
			delete(h.clients, c)
		}
	}
}

// ClientCount returns the current number of connected clients.
func (h *Hub) ClientCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// HandleWS is the gin handler that upgrades HTTP to WebSocket.
func (h *Hub) HandleWS(c *gin.Context) {
	h.mu.Lock()
	count := len(h.clients)
	h.mu.Unlock()

	if count >= maxWSConnections {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "too many connections"})
		return
	}

	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}

	client := &wsClient{
		conn: conn,
		send: make(chan []byte, 256),
	}

	h.mu.Lock()
	h.clients[client] = struct{}{}
	h.mu.Unlock()

	// Send initial device snapshot.
	if devices, err := h.deviceRepo.FindAll(db.DeviceFilter{}); err == nil {
		msg := WSMessage{Type: "device_snapshot", Payload: devices}
		if data, err := json.Marshal(msg); err == nil {
			select {
			case client.send <- data:
			default:
			}
		}
	}

	// Write pump: send queued messages to the WebSocket.
	go h.writePump(client)

	// Read pump: detect client disconnect (blocks).
	h.readPump(client)
}

// writePump drains the send channel and writes each message to the WebSocket.
func (h *Hub) writePump(client *wsClient) {
	defer client.conn.Close()
	for msg := range client.send {
		if err := client.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			break
		}
	}
}

// readPump reads from the WebSocket until the client disconnects, then
// unregisters the client.
func (h *Hub) readPump(client *wsClient) {
	defer func() {
		h.mu.Lock()
		if _, ok := h.clients[client]; ok {
			close(client.send)
			delete(h.clients, client)
		}
		h.mu.Unlock()
		client.conn.Close()
	}()
	for {
		if _, _, err := client.conn.ReadMessage(); err != nil {
			break
		}
	}
}

// RegisterWSRoutes registers the WebSocket endpoint on the given router group.
func RegisterWSRoutes(rg *gin.RouterGroup, hub *Hub) {
	rg.GET("/ws", hub.HandleWS)
}
