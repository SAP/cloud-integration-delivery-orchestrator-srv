package service

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func TestEventBus_SubscribeAndPublish(t *testing.T) {
	eb := NewEventBus()
	sub := eb.Subscribe(8)
	defer eb.Unsubscribe(sub)

	evt := Event{Type: EventCounts, Payload: json.RawMessage(`{"total":1}`)}
	eb.Publish(evt)

	select {
	case got := <-sub.Ch:
		if got.Type != evt.Type {
			t.Errorf("type = %q, want %q", got.Type, evt.Type)
		}
		if string(got.Payload) != string(evt.Payload) {
			t.Errorf("payload = %s, want %s", got.Payload, evt.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestEventBus_MultipleSubscribers(t *testing.T) {
	eb := NewEventBus()
	sub1 := eb.Subscribe(8)
	sub2 := eb.Subscribe(8)
	defer eb.Unsubscribe(sub1)
	defer eb.Unsubscribe(sub2)

	evt := Event{Type: EventDrStatus, Payload: json.RawMessage(`{"drId":1}`)}
	eb.Publish(evt)

	for i, sub := range []*Subscriber{sub1, sub2} {
		select {
		case got := <-sub.Ch:
			if got.Type != evt.Type {
				t.Errorf("sub%d: type = %q, want %q", i, got.Type, evt.Type)
			}
		case <-time.After(time.Second):
			t.Fatalf("sub%d: timed out waiting for event", i)
		}
	}
}

func TestEventBus_UnsubscribeStopsDelivery(t *testing.T) {
	eb := NewEventBus()
	sub := eb.Subscribe(8)
	eb.Unsubscribe(sub)

	evt := Event{Type: EventCounts, Payload: json.RawMessage(`{}`)}
	eb.Publish(evt)

	select {
	case _, ok := <-sub.Ch:
		if ok {
			t.Error("received event after unsubscribe")
		}
	default:
	}
}

func TestEventBus_UnsubscribeNilSafe(t *testing.T) {
	eb := NewEventBus()
	eb.Unsubscribe(nil) // should not panic
}

func TestEventBus_DoubleUnsubscribe(t *testing.T) {
	eb := NewEventBus()
	sub := eb.Subscribe(8)
	eb.Unsubscribe(sub)
	eb.Unsubscribe(sub) // should not panic or double-close
}

func TestEventBus_PublishDrainsOldestWhenFull(t *testing.T) {
	eb := NewEventBus()
	sub := eb.Subscribe(2) // buffer size 2
	defer eb.Unsubscribe(sub)

	e1 := Event{Type: EventDrOps, Payload: json.RawMessage(`{"seq":1}`)}
	e2 := Event{Type: EventDrOps, Payload: json.RawMessage(`{"seq":2}`)}
	e3 := Event{Type: EventDrOps, Payload: json.RawMessage(`{"seq":3}`)}

	eb.Publish(e1)
	eb.Publish(e2)
	// Buffer is now full [e1, e2]. Publishing e3 should drain e1 and insert e3.
	eb.Publish(e3)

	got1 := <-sub.Ch
	got2 := <-sub.Ch

	// After drain-oldest, we expect e2 and e3 (e1 was drained)
	if string(got1.Payload) != `{"seq":2}` {
		t.Errorf("first event = %s, want seq:2", got1.Payload)
	}
	if string(got2.Payload) != `{"seq":3}` {
		t.Errorf("second event = %s, want seq:3", got2.Payload)
	}
}

func TestEventBus_PublishNeverBlocks(t *testing.T) {
	eb := NewEventBus()
	sub := eb.Subscribe(1) // minimal buffer
	defer eb.Unsubscribe(sub)

	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			eb.Publish(Event{Type: EventCounts, Payload: json.RawMessage(`{}`)})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Publish blocked — should never happen")
	}
}

func TestEventBus_DefaultBufferSize(t *testing.T) {
	eb := NewEventBus()
	sub := eb.Subscribe(0) // should default to 64
	defer eb.Unsubscribe(sub)

	if cap(sub.Ch) != 64 {
		t.Errorf("channel capacity = %d, want 64", cap(sub.Ch))
	}

	sub2 := eb.Subscribe(-1) // negative should also default
	defer eb.Unsubscribe(sub2)

	if cap(sub2.Ch) != 64 {
		t.Errorf("channel capacity = %d, want 64", cap(sub2.Ch))
	}
}

func TestEventBus_ConcurrentPublishSubscribe(t *testing.T) {
	eb := NewEventBus()
	var wg sync.WaitGroup

	// Concurrent subscribers
	subs := make([]*Subscriber, 10)
	for i := range subs {
		subs[i] = eb.Subscribe(32)
	}

	// Concurrent publishers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				eb.Publish(Event{Type: EventCounts, Payload: json.RawMessage(`{}`)})
			}
		}(i)
	}

	// Concurrent subscribe/unsubscribe
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				s := eb.Subscribe(4)
				eb.Unsubscribe(s)
			}
		}()
	}

	wg.Wait()

	for _, sub := range subs {
		eb.Unsubscribe(sub)
	}
}

func TestEventBus_NoSubscribersPublishSafe(t *testing.T) {
	eb := NewEventBus()
	eb.Publish(Event{Type: EventCounts, Payload: json.RawMessage(`{}`)})
	// should not panic
}

func TestSameOps(t *testing.T) {
	tests := []struct {
		name string
		a, b []opSnapshot
		want bool
	}{
		{"both nil", nil, nil, true},
		{"both empty", []opSnapshot{}, []opSnapshot{}, true},
		{"identical", []opSnapshot{{ID: 1, ImportState: "A", DeployState: "B"}}, []opSnapshot{{ID: 1, ImportState: "A", DeployState: "B"}}, true},
		{"different length", []opSnapshot{{ID: 1}}, []opSnapshot{{ID: 1}, {ID: 2}}, false},
		{"different import", []opSnapshot{{ID: 1, ImportState: "A"}}, []opSnapshot{{ID: 1, ImportState: "B"}}, false},
		{"different deploy", []opSnapshot{{ID: 1, DeployState: "A"}}, []opSnapshot{{ID: 1, DeployState: "B"}}, false},
		{"different order same content", []opSnapshot{{ID: 2}, {ID: 1}}, []opSnapshot{{ID: 1}, {ID: 2}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sameOps(tt.a, tt.b); got != tt.want {
				t.Errorf("sameOps() = %v, want %v", got, tt.want)
			}
		})
	}
}
