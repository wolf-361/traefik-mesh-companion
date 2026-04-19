package gatus

import (
	"testing"
)

func TestLoadConfig(t *testing.T) {
	// Missing URL should gracefully return nil
	t.Setenv("GATUS_BRIDGE_URL", "")
	if cfg := LoadConfig(); cfg != nil {
		t.Errorf("Expected LoadConfig to return nil when URL is missing, got %+v", cfg)
	}

	// Valid variables are parsed correctly
	t.Setenv("GATUS_BRIDGE_URL", "http://gatus-bridge:8080")
	t.Setenv("GATUS_API_KEY", "supersecret-token")
	t.Setenv("GATUS_AUTO_ENABLE", "true")

	cfg := LoadConfig()
	if cfg == nil {
		t.Fatalf("Expected valid config, got nil")
	}

	if cfg.BridgeURL != "http://gatus-bridge:8080" {
		t.Errorf("Expected BridgeURL 'http://gatus-bridge:8080', got '%s'", cfg.BridgeURL)
	}
	if cfg.APIKey != "supersecret-token" {
		t.Errorf("Expected APIKey 'supersecret-token', got '%s'", cfg.APIKey)
	}
	if !cfg.AutoEnable {
		t.Errorf("Expected AutoEnable to be true")
	}
}
