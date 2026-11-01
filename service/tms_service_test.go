package service

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/tms"
)

// tmsFactoryFunc is a local alias replacing the removed TmsClientFunc type.
// Used only in these caching-pattern tests.
type tmsFactoryFunc = func(ctx context.Context) (tms.TransportService, error)

func TestTmsClientFunc_ReturnsSameInstance(t *testing.T) {
	mock := &mockTMSClient{}
	cached := cachedFactory(func(ctx context.Context) (tms.TransportService, error) {
		return mock, nil
	})

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
}

func TestTmsClientFunc_RetriesAfterFailure(t *testing.T) {
	var callCount atomic.Int32
	mock := &mockTMSClient{}

	factory := func(ctx context.Context) (tms.TransportService, error) {
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

func TestTmsClientFunc_ConcurrentAccess(t *testing.T) {
	var callCount atomic.Int32
	mock := &mockTMSClient{}

	factory := func(ctx context.Context) (tms.TransportService, error) {
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

// cachedFactory replicates the caching logic of the old TmsSvc closure for testing.
func cachedFactory(inner tmsFactoryFunc) tmsFactoryFunc {
	var (
		mu     sync.Mutex
		client tms.TransportService
	)
	return func(ctx context.Context) (tms.TransportService, error) {
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
