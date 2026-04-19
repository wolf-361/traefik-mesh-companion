package cloudflare

import (
	"testing"
)

func TestLoadConfig(t *testing.T) {
	// Missing core requirements should return nil
	t.Setenv("CLOUDFLARE_API_TOKEN", "")
	t.Setenv("CLOUDFLARE_TARGET_DOMAIN", "")
	if cfg := LoadConfig(); cfg != nil {
		t.Errorf("Expected nil config when token/target are missing")
	}

	// Valid parsing
	t.Setenv("CLOUDFLARE_API_TOKEN", "cf-super-secret-token")
	t.Setenv("CLOUDFLARE_TARGET_DOMAIN", "ingress.mydomain.com")

	cfg := LoadConfig()
	if cfg == nil {
		t.Fatalf("Expected valid config")
	}

	if cfg.Target != "ingress.mydomain.com" {
		t.Errorf("Expected Target 'ingress.mydomain.com', got '%s'", cfg.Target)
	}
}
