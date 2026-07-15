package service

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/coder/websocket"
	"go.uber.org/zap"
)

// WSHub manages per-DR subscription routing for WebSocket push.
// It is a pure routing/push component — it does NOT drive sync.
type WSHub struct {
	mu         sync.RWMutex
	drWatchers map[uint]map[*WSConn]bool // drId → set of connections watching it
	logger     *zap.SugaredLogger
}

// WSConn represents a single WebSocket connection.
type WSConn struct {
	conn      *websocket.Conn
	hub       *WSHub
	send      chan []byte
	closeOnce sync.Once
	mu        sync.Mutex    // protects watching
	watching  map[uint]bool // DRs this connection is watching
}

// wsMessage represents a client→server message.
type wsMessage struct {
	Action string `json:"action"` // "subscribe" | "unsubscribe"
	DrID   uint   `json:"drId"`
}

// wsEvent is the envelope for server→client messages.
type wsEvent struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}

func NewWSHub(logger *zap.SugaredLogger) *WSHub {
	return &WSHub{
		drWatchers: make(map[uint]map[*WSConn]bool),
		logger:     logger,
	}
}

// NewConn creates a WSConn managed by this hub.
func (h *WSHub) NewConn(conn *websocket.Conn) *WSConn {
	h.logger.Debugw("new connection", "component", "ws")
	return &WSConn{
		conn:     conn,
		hub:      h,
		send:     make(chan []byte, 64),
		watching: make(map[uint]bool),
	}
}

// Disconnect cleans up all subscriptions for this connection and closes the send channel.
// Safe to call multiple times.
func (c *WSConn) Disconnect() {
	c.closeOnce.Do(func() {
		c.hub.logger.Infow("connection disconnected", "component", "ws")
		h := c.hub
		h.mu.Lock()
		c.mu.Lock()
		for drID := range c.watching {
			if watchers, ok := h.drWatchers[drID]; ok {
				delete(watchers, c)
				if len(watchers) == 0 {
					delete(h.drWatchers, drID)
				}
			}
		}
		c.watching = nil
		c.mu.Unlock()
		h.mu.Unlock()
		close(c.send)
	})
}

// Subscribe registers interest in a specific DR for this connection.
func (h *WSHub) Subscribe(c *WSConn, drID uint) {
	h.logger.Debugw("subscribe", "component", "ws", "dr_id", drID)
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.drWatchers[drID]; !ok {
		h.drWatchers[drID] = make(map[*WSConn]bool)
	}
	h.drWatchers[drID][c] = true

	c.mu.Lock()
	c.watching[drID] = true
	c.mu.Unlock()
}

// Unsubscribe removes interest in a specific DR for this connection.
func (h *WSHub) Unsubscribe(c *WSConn, drID uint) {
	h.logger.Debugw("unsubscribe", "component", "ws", "dr_id", drID)
	h.mu.Lock()
	defer h.mu.Unlock()

	if watchers, ok := h.drWatchers[drID]; ok {
		delete(watchers, c)
		if len(watchers) == 0 {
			delete(h.drWatchers, drID)
		}
	}

	c.mu.Lock()
	delete(c.watching, drID)
	c.mu.Unlock()
}

// PublishDrEvent sends an event to all connections watching the given DR.
// If no connections are watching, this is a no-op.
func (h *WSHub) PublishDrEvent(drID uint, eventType string, payload json.RawMessage) {
	h.mu.RLock()
	watchers := h.drWatchers[drID]
	if len(watchers) == 0 {
		h.mu.RUnlock()
		h.logger.Debugw("publish — no watchers, skipped", "component", "ws", "dr_id", drID, "event", eventType)
		return
	}

	h.logger.Debugw("publish", "component", "ws", "dr_id", drID, "event", eventType, "watchers", len(watchers))

	evt := wsEvent{Event: eventType, Data: payload}
	data, err := json.Marshal(evt)
	if err != nil {
		h.mu.RUnlock()
		return
	}

	for conn := range watchers {
		select {
		case conn.send <- data:
		default:
			// drop if buffer full — subscriber is slow
		}
	}
	h.mu.RUnlock()
}

// ReadPump reads messages from the WebSocket connection until error/close.
func (c *WSConn) ReadPump() {
	defer c.Disconnect()
	for {
		_, data, err := c.conn.Read(context.Background())
		if err != nil {
			return
		}
		var msg wsMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		switch msg.Action {
		case "subscribe":
			c.hub.Subscribe(c, msg.DrID)
		case "unsubscribe":
			c.hub.Unsubscribe(c, msg.DrID)
		}
	}
}

// WritePump writes messages from the send channel and sends periodic pings.
func (c *WSConn) WritePump() {
	pingTicker := time.NewTicker(30 * time.Second)
	defer pingTicker.Stop()
	defer c.conn.Close(websocket.StatusNormalClosure, "")

	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := c.conn.Write(ctx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				return
			}
		case <-pingTicker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := c.conn.Ping(ctx)
			cancel()
			if err != nil {
				return
			}
		}
	}
}

// NotifyDrUpdated pushes a minimal "dr-updated" event to all subscribers of this DR.
// The payload only contains the drId — clients do a full HTTP refresh.
func (s *Service) NotifyDrUpdated(drID uint) {
	if s.Hub == nil {
		return
	}
	data, _ := json.Marshal(map[string]uint{"drId": drID})
	s.Hub.PublishDrEvent(drID, "dr-updated", data)
}
