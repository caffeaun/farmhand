package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/caffeaun/farmhand/internal/db"
	"github.com/caffeaun/farmhand/internal/events"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- fakes ---

type fakeWSDeviceRepo struct {
	devices []db.Device
}

func (f *fakeWSDeviceRepo) FindAll(_ db.DeviceFilter) ([]db.Device, error) {
	return f.devices, nil
}

// --- helpers ---

// newWSTestServer creates an httptest.Server backed by a gin engine with the
// hub's WS handler registered.
func newWSTestServer(hub *Hub) *httptest.Server {
	r := gin.New()
	r.GET("/ws", hub.HandleWS)
	return httptest.NewServer(r)
}

// dialHub starts an httptest.Server with the hub's HandleWS, dials it, and
// returns the connection plus a cleanup function.
func dialHub(t *testing.T, hub *Hub) (*websocket.Conn, func()) {
	t.Helper()

	server := newWSTestServer(hub)
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)

	cleanup := func() {
		conn.Close()
		server.Close()
	}
	return conn, cleanup
}

// testLogger returns a disabled zerolog logger for tests.
func testLogger() zerolog.Logger {
	return zerolog.Nop()
}

// --- tests ---

// TestHub_InitialSnapshot verifies that a new client receives a device_snapshot.
func TestHub_InitialSnapshot(t *testing.T) {
	bus := events.New()
	defer bus.Close()

	devices := []db.Device{
		{ID: "dev-1", Platform: "android", Status: "online"},
	}
	repo := &fakeWSDeviceRepo{devices: devices}
	hub := NewHub(repo, bus, testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)

	conn, cleanup := dialHub(t, hub)
	defer cleanup()

	// Read the first message — should be device_snapshot.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := conn.ReadMessage()
	require.NoError(t, err)

	var wsMsg WSMessage
	require.NoError(t, json.Unmarshal(msg, &wsMsg))
	assert.Equal(t, "device_snapshot", wsMsg.Type)
}

// TestHub_EventBusForwarding verifies that publishing a device event to the
// EventBus results in a device_update message on the WebSocket.
func TestHub_EventBusForwarding(t *testing.T) {
	bus := events.New()
	defer bus.Close()

	repo := &fakeWSDeviceRepo{}
	hub := NewHub(repo, bus, testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)

	conn, cleanup := dialHub(t, hub)
	defer cleanup()

	// Drain the snapshot message.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	conn.ReadMessage() //nolint:errcheck

	// Publish a device event.
	bus.Publish(events.Event{
		Type:      events.DeviceOnline,
		Payload:   db.Device{ID: "dev-new", Status: "online"},
		Timestamp: time.Now().UTC(),
	})

	// Read the forwarded message.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, msg, err := conn.ReadMessage()
	require.NoError(t, err)

	var wsMsg WSMessage
	require.NoError(t, json.Unmarshal(msg, &wsMsg))
	assert.Equal(t, "device_update", wsMsg.Type)
}

// TestHub_ClientDisconnect verifies that a disconnected client is cleaned up.
func TestHub_ClientDisconnect(t *testing.T) {
	bus := events.New()
	defer bus.Close()

	repo := &fakeWSDeviceRepo{}
	hub := NewHub(repo, bus, testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)

	conn, cleanup := dialHub(t, hub)

	// Give the hub time to register the client.
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 1, hub.ClientCount())

	// Close the connection.
	conn.Close()
	cleanup()

	// Give the hub time to detect the disconnect.
	assert.Eventually(t, func() bool {
		return hub.ClientCount() == 0
	}, 2*time.Second, 50*time.Millisecond)
}

// TestHub_ConnectionLimit verifies the 503 response when max connections reached.
func TestHub_ConnectionLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping connection limit test in short mode")
	}

	bus := events.New()
	defer bus.Close()

	repo := &fakeWSDeviceRepo{}
	hub := NewHub(repo, bus, testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)

	server := newWSTestServer(hub)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"

	// Open maxWSConnections connections.
	conns := make([]*websocket.Conn, 0, maxWSConnections)
	for i := range maxWSConnections {
		c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("failed to connect client %d: %v", i, err)
		}
		conns = append(conns, c)
	}
	defer func() {
		for _, c := range conns {
			c.Close()
		}
	}()

	// Give hub time to register all clients.
	time.Sleep(200 * time.Millisecond)

	// The next connection should be rejected with 503.
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("expected connection to be rejected")
	}
	if resp != nil {
		assert.Equal(t, 503, resp.StatusCode)
	}
}
