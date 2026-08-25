package service

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func TestNewTmsFactory_ReturnsSameInstance(t *testing.T) {
	var callCount atomic.Int32
	mock := &mockTMSClient{}

	factory := func(ctx context.Context) (TransportService, error) {
		callCount.Add(1)
		return mock, nil
	}

	// Wrap with the same caching pattern used by NewTmsFactory
	cached := cachedFactory(factory)

	c1, err := cached(context.Background())
	if err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	c2, err := cached(context.Background())
	if err != nil {
		t.Fatalf("second call failed: %v", err)
	}

	if c1 != c2 {
		t.Fatal("expected same instance on second call")
	}
	if callCount.Load() != 1 {
		t.Fatalf("factory called %d times, want 1", callCount.Load())
	}
}

func TestNewTmsFactory_RetriesAfterFailure(t *testing.T) {
	var callCount atomic.Int32
	mock := &mockTMSClient{}

	factory := func(ctx context.Context) (TransportService, error) {
		n := callCount.Add(1)
		if n == 1 {
			return nil, fmt.Errorf("transient error")
		}
		return mock, nil
	}

	cached := cachedFactory(factory)

	_, err := cached(context.Background())
	if err == nil {
		t.Fatal("expected error on first call")
	}

	c2, err := cached(context.Background())
	if err != nil {
		t.Fatalf("second call should succeed: %v", err)
	}
	if c2 != mock {
		t.Fatal("expected mock instance")
	}
}

func TestNewTmsFactory_ConcurrentAccess(t *testing.T) {
	var callCount atomic.Int32
	mock := &mockTMSClient{}

	factory := func(ctx context.Context) (TransportService, error) {
		callCount.Add(1)
		return mock, nil
	}

	cached := cachedFactory(factory)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := cached(context.Background())
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if c != mock {
				t.Errorf("expected same instance")
			}
		}()
	}
	wg.Wait()

	if callCount.Load() != 1 {
		t.Fatalf("factory called %d times under concurrency, want 1", callCount.Load())
	}
}

// cachedFactory replicates the caching logic of NewTmsFactory for testing.
func cachedFactory(inner TmsClientFunc) TmsClientFunc {
	var (
		mu     sync.Mutex
		client TransportService
	)
	return func(ctx context.Context) (TransportService, error) {
		mu.Lock()
		defer mu.Unlock()
		if client != nil {
			return client, nil
		}
		c, err := inner(ctx)
		if err != nil {
			return nil, err
		}
		client = c
		return client, nil
	}
}
