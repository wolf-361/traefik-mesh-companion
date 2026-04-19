package server

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHealthServer(t *testing.T) {
	// Setup minimal dependencies
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	var isHealthy atomic.Bool

	srv := NewServer(logger, "1.0.0-test", &isHealthy)

	// Start the server in a background goroutine
	go func() {
		// We ignore the error here because http.ErrServerClosed is expected during Stop()
		_ = srv.Start()
	}()

	// Give the server a few milliseconds to bind to port 9999
	time.Sleep(100 * time.Millisecond)

	// Unhealthy State
	isHealthy.Store(false)
	err := Check()
	if err == nil {
		t.Errorf("Expected Check() to fail when isHealthy is false, but it passed")
	} else if !strings.Contains(err.Error(), "unexpected status: 503") {
		t.Errorf("Expected 503 error, got: %v", err)
	}

	// Healthy State
	isHealthy.Store(true)
	err = Check()
	if err != nil {
		t.Errorf("Expected Check() to pass when isHealthy is true, got: %v", err)
	}

	// Graceful Shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := srv.Stop(ctx); err != nil {
		t.Fatalf("Failed to stop server: %v", err)
	}

	// Server Offline
	time.Sleep(100 * time.Millisecond)

	err = Check()
	if err == nil {
		t.Errorf("Expected Check() to fail because the server is stopped, but it passed")
	} else if !strings.Contains(err.Error(), "failed to connect") {
		t.Errorf("Expected connection failure, got: %v", err)
	}
}
