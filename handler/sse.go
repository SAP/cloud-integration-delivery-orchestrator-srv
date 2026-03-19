package handler

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// SSEHandler bridges the in-process EventBus to the browser via a long-lived
// HTTP response. The response never "completes" — the for-select loop keeps
// writing SSE frames (events + periodic heartbeats) into the same ResponseWriter
// until the client disconnects (ctx.Done) or the subscriber channel is closed.
// Each connected browser tab corresponds to one goroutine + one EventBus subscriber.
//
// Flow: EventBus.Publish → subscriber channel → fmt.Fprintf → Flusher.Flush → network
func (h *Handler) SSEHandler(c *gin.Context) {
	if h.eventBus == nil {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"status": "fail", "code": 503, "error": "event bus is not available"})
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache, no-transform")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"status": "fail", "code": 500, "error": "streaming unsupported"})
		return
	}

	sub := h.eventBus.Subscribe(64)
	defer h.eventBus.Unsubscribe(sub)

	fmt.Fprintf(c.Writer, ": heartbeat\n\n")
	flusher.Flush()

	ctx := c.Request.Context()
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-sub.Ch:
			if !ok {
				return
			}
			fmt.Fprintf(c.Writer, "event: %s\n", evt.Type)
			fmt.Fprintf(c.Writer, "data: %s\n\n", evt.Payload)
			flusher.Flush()
		case <-heartbeat.C:
			fmt.Fprintf(c.Writer, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}
