package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Event represents an SSE event sent to clients.
type Event struct {
	Type      string    `json:"type"` // "status", "issue_update", "session_update", "log"
	Timestamp time.Time `json:"timestamp"`
	Data      any       `json:"data"`
}

// NewEvent creates a new event with the current timestamp.
func NewEvent(eventType string, data any) Event {
	return Event{
		Type:      eventType,
		Timestamp: time.Now().UTC(),
		Data:      data,
	}
}

// SSEClient represents a connected SSE client.
type SSEClient struct {
	id      string
	events  chan Event
	done    chan struct{}
	once    sync.Once
}

// newSSEClient creates a new SSE client.
func newSSEClient(id string) *SSEClient {
	return &SSEClient{
		id:     id,
		events: make(chan Event, 100),
		done:   make(chan struct{}),
	}
}

// close closes the client connection.
func (c *SSEClient) close() {
	c.once.Do(func() {
		close(c.done)
	})
}

// SSEHub manages all connected SSE clients.
type SSEHub struct {
	clients    map[*SSEClient]bool
	register   chan *SSEClient
	unregister chan *SSEClient
	broadcast  chan Event
	done       chan struct{}
	mu         sync.RWMutex
	nextID     int
}

// NewSSEHub creates a new SSE hub.
func NewSSEHub() *SSEHub {
	return &SSEHub{
		clients:    make(map[*SSEClient]bool),
		register:   make(chan *SSEClient),
		unregister: make(chan *SSEClient),
		broadcast:  make(chan Event, 100),
		done:       make(chan struct{}),
	}
}

// Run starts the SSE hub event loop. Should be run in a goroutine.
func (h *SSEHub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.events)
			}
			h.mu.Unlock()

		case event := <-h.broadcast:
			h.mu.RLock()
			for client := range h.clients {
				select {
				case client.events <- event:
				default:
					// Client buffer full, skip this event
				}
			}
			h.mu.RUnlock()

		case <-h.done:
			h.mu.Lock()
			for client := range h.clients {
				close(client.events)
				delete(h.clients, client)
			}
			h.mu.Unlock()
			return
		}
	}
}

// Close shuts down the SSE hub.
func (h *SSEHub) Close() {
	select {
	case <-h.done:
		// Already closed
	default:
		close(h.done)
	}
}

// Broadcast sends an event to all connected clients.
func (h *SSEHub) Broadcast(event Event) {
	select {
	case h.broadcast <- event:
	case <-h.done:
		// Hub is closed
	default:
		// Buffer full, skip
	}
}

// ClientCount returns the number of connected clients.
func (h *SSEHub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// ServeHTTP implements http.Handler for the SSE endpoint.
func (h *SSEHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Check for Flusher support
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Create client
	h.mu.Lock()
	h.nextID++
	clientID := fmt.Sprintf("client-%d", h.nextID)
	h.mu.Unlock()

	client := newSSEClient(clientID)

	// Register client
	h.register <- client

	// Ensure cleanup on exit
	defer func() {
		client.close()
		h.unregister <- client
	}()

	// Send initial connection event
	connEvent := NewEvent("connected", map[string]string{"client_id": clientID})
	writeSSE(w, connEvent)
	flusher.Flush()

	// Listen for events
	ctx := r.Context()
	for {
		select {
		case event, ok := <-client.events:
			if !ok {
				return
			}
			writeSSE(w, event)
			flusher.Flush()

		case <-client.done:
			return

		case <-ctx.Done():
			return

		case <-h.done:
			return
		}
	}
}

// writeSSE writes an SSE event to the response writer.
func writeSSE(w http.ResponseWriter, event Event) {
	data, err := json.Marshal(event)
	if err != nil {
		return
	}

	// SSE format: data: {json}\n\n
	fmt.Fprintf(w, "data: %s\n\n", data)
}
