package handler

import (
	"context"
	"encoding/json"
	"mmt-delivery/service"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

func WsHandler(c *gin.Context) {
	_ = service.UserID(c)
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		// handle error
		return
	}
	defer conn.Close()

	for {
		msgType, msg, err := conn.ReadMessage()
		if err != nil {
			break
		}
		_ = msg

		err = conn.WriteMessage(msgType, msg)
		if err != nil {
			break
		}
	}
}

func (h *Hub) scanToken(ctx context.Context) {
	ticker := time.NewTicker(time.Second * 5)
	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker.C:
			now := time.Now()
			h.RLock()
			for uID, client := range h.clients {
				if now.Before(client.exp) {
					continue
				}
				h.unregister <- uID
			}
			h.RUnlock()

		}
	}
}

func (h *Hub) run(ctx context.Context) {
	for {
		select {
		case cli := <-h.register:
			h.Lock()
			h.clients[cli.userID] = cli
			h.Unlock()
		case uID := <-h.unregister:
			h.Lock()
			if cli, ok := h.clients[uID]; ok {
				delete(h.clients, uID)
				cli.conn.Close()
			}
			h.Unlock()
		}
	}
}

type Hub struct {
	sync.RWMutex
	clients    map[string]*client
	register   chan *client
	unregister chan string
	broadcast  chan Event
}

type client struct {
	conn   *websocket.Conn
	userID string
	exp    time.Time
}

type Event struct {
	Type string          `json:"type"` // system | chat | deploy …
	From string          `json:"from"`
	Data json.RawMessage `json:"data"`
}
