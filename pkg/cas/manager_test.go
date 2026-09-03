package cas

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/SAP/cloud-integration-delivery-orchestrator-srv/pkg/env"
)

func TestNewManager_InitializesClientCache(t *testing.T) {
	manager := NewManager(nil, nil)
	if manager == nil {
		t.Fatal("expected manager instance, got nil")
	}
	if manager.clients == nil {
		t.Fatal("expected client cache to be initialized")
	}
	if len(manager.clients) != 0 {
		t.Fatalf("expected empty client cache, got %d entries", len(manager.clients))
	}
}

func TestManagerGet_ReturnsCachedClientWithoutResolver(t *testing.T) {
	manager := NewManager(nil, nil)
	cached := &CasClient{
		HttpClient: &env.HttpClient{
			ApiURL:      "https://cas.example.test",
			AccessToken: "token",
			TokenExp:    time.Now().Add(time.Hour),
		},
	}
	manager.clients[42] = cached

	got, err := manager.Get(context.Background(), 42)
	if err != nil {
		t.Fatalf("expected cached lookup to succeed, got %v", err)
	}
	if got != cached {
		t.Fatal("expected cached client instance to be returned")
	}
}

func TestManagerGet_ReportsMissingResolverOnCacheMiss(t *testing.T) {
	manager := NewManager(nil, nil)

	_, err := manager.Get(context.Background(), 99)
	if err == nil {
		t.Fatal("expected cache miss without resolver to fail")
	}
	if !strings.Contains(err.Error(), "DestinationServiceClient not injected") {
		t.Fatalf("expected missing resolver error, got %v", err)
	}
}
