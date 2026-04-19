package kuma

import (
	"testing"
)

func TestCoordinatorInitialization(t *testing.T) {
	cfg := &Config{
		CoordinatorMode: "client", // Act as a client to avoid spinning up the HTTP server
	}

	// We can pass nil for the StatusPageManager because we aren't executing the queue
	coord := NewCoordinator(cfg, nil)

	// Ensure the queue is safely initialized with the correct buffer
	if coord.attachQueue == nil {
		t.Fatalf("attachQueue was not initialized, will cause nil pointer dereference")
	}

	if cap(coord.attachQueue) != 100 {
		t.Errorf("Expected attachQueue capacity of 100, got %d", cap(coord.attachQueue))
	}
}
