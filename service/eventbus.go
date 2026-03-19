package service

import (
	"encoding/json"
	"sync"
)

type EventType string

const (
	EventDrOps    EventType = "dr-ops"    // Op-level mutations within a DR: state progress, insert, update, or delete
	EventDrStatus EventType = "dr-status" // DR aggregate status transition (e.g. IMPORTING → AWAITING_DEPLOY)
	EventCounts   EventType = "counts"    // Global DR count/distribution changed (create, delete, status shift)
)

type Event struct {
	Type    EventType       `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type Subscriber struct {
	Ch chan Event
}

type EventBus struct {
	mu          sync.RWMutex
	subscribers map[*Subscriber]struct{}
}

func NewEventBus() *EventBus {
	return &EventBus{
		subscribers: make(map[*Subscriber]struct{}),
	}
}

func (eb *EventBus) Subscribe(bufSize int) *Subscriber {
	if bufSize <= 0 {
		bufSize = 64
	}
	sub := &Subscriber{
		Ch: make(chan Event, bufSize),
	}
	eb.mu.Lock()
	eb.subscribers[sub] = struct{}{}
	eb.mu.Unlock()
	return sub
}

func (eb *EventBus) Unsubscribe(sub *Subscriber) {
	if sub == nil {
		return
	}
	eb.mu.Lock()
	if _, ok := eb.subscribers[sub]; ok {
		delete(eb.subscribers, sub)
		close(sub.Ch)
	}
	eb.mu.Unlock()
}

// Publish fans out evt to every subscriber without blocking. The three-layer
// select ensures zero-blocking under RLock:
//
//  1. Try send — if the channel has room, done.
//  2. Buffer full — drain the oldest message (or skip if a concurrent reader
//     already emptied the channel).
//  3. Retry send — write the new event into the freed slot (or drop if the
//     channel was refilled by another publisher in the meantime).
//
// This guarantees that a single slow consumer can never stall the publish loop
// or cause unbounded lock hold time.
func (eb *EventBus) Publish(evt Event) {
	eb.mu.RLock()
	defer eb.mu.RUnlock()

	for sub := range eb.subscribers {
		select {
		case sub.Ch <- evt:
			continue
		default:
			select {
			case <-sub.Ch:
			default:
			}
			select {
			case sub.Ch <- evt:
			default:
			}
		}
	}
}
