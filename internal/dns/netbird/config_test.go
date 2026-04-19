package netbird

import (
	"testing"
)

func TestLoadConfig(t *testing.T) {
	// Missing core requirements should return nil
	t.Setenv("NETBIRD_API_TOKEN", "")
	t.Setenv("NETBIRD_TARGET_IP", "")
	if cfg := LoadConfig(); cfg != nil {
		t.Errorf("Expected nil config when token/target are missing")
	}

	// Defaults and overrides
	t.Setenv("NETBIRD_API_TOKEN", "secret")
	t.Setenv("NETBIRD_TARGET_IP", "100.64.0.1")
	t.Setenv("NETBIRD_API_URL", "http://custom-api/") // Note the trailing slash

	cfg := LoadConfig()
	if cfg == nil {
		t.Fatalf("Expected valid config")
	}

	// It should automatically trim trailing slashes!
	if cfg.APIURL != "http://custom-api" {
		t.Errorf("Expected 'http://custom-api', got '%s'", cfg.APIURL)
	}
}
