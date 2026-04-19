package kuma

import (
	"os"
	"testing"
)

func TestClientInitializationWithoutConfig(t *testing.T) {
	// Temporarily wipe the environment so LoadConfig returns nil
	urlBak := os.Getenv("KUMA_URL")
	os.Unsetenv("KUMA_URL")
	defer os.Setenv("KUMA_URL", urlBak)

	// If config is missing, New() should gracefully return nil instead of panicking
	client := New(nil)

	if client != nil {
		t.Errorf("Expected New() to return nil when config is missing, got a client")
	}
}
