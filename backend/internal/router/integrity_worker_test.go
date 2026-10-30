package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/peasant-labs/redact"

	"github.com/peasant-labs/village/backend/internal/config"
)

// TestOnlyNewWithDrainStartsTheIntegrityCheck: New hands back no drain, so a
// worker it started could never be stopped. Only NewWithDrain starts one, and
// its drain stops it.
func TestOnlyNewWithDrainStartsTheIntegrityCheck(t *testing.T) {
	var healthCalls atomic.Int32
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			healthCalls.Add(1)
		}
		// Every pass ends at health, so the worker touches no database.
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer service.Close()
	titles, err := redact.NewTitlePipeline()
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{FrontendURL: "https://app.example.com", ScryURL: service.URL, ScrySharedSecret: "router-test-secret-0123456789abcdef0123"}

	_ = New(cfg, nil, nil, titles)
	time.Sleep(300 * time.Millisecond)
	if n := healthCalls.Load(); n != 0 {
		t.Fatalf("New started an integrity check it cannot stop: %d health calls", n)
	}

	_, drain := NewWithDrain(cfg, nil, nil, titles)
	deadline := time.Now().Add(5 * time.Second)
	for healthCalls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if healthCalls.Load() == 0 {
		t.Fatal("NewWithDrain did not start the configured integrity check")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := drain(ctx); err != nil {
		t.Fatalf("drain = %v, want the integrity check stopped", err)
	}
}
