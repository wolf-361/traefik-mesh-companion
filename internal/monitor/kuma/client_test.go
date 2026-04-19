package kuma

import (
	"testing"
)

func TestClientInitializationWithoutConfig(t *testing.T) {
	t.Setenv("KUMA_URL", "")

	// If config is missing (because URL is blank), New() should gracefully return nil
	client := New(nil)

	if client != nil {
		t.Errorf("Expected New() to return nil when config is missing, got a client")
	}
}
