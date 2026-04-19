package kuma

import (
	"reflect"
	"testing"

	"github.com/wolf-361/traefik-mesh-companion/internal/core"
)

func TestBuildHTTPMonitor(t *testing.T) {
	mockCfg := &Config{
		DefaultInterval:            60,
		DefaultMaxRetries:          3,
		DefaultAcceptedStatusCodes: []string{"200-299"},
	}

	svc := core.Service{
		ContainerName: "backend-api",
		Labels: map[string]string{
			"mesh.routers.api.kuma.name":                  "My Awesome API",
			"mesh.routers.api.kuma.method":                "post",
			"mesh.routers.api.kuma.interval":              "120",
			"mesh.routers.api.kuma.accepted_status_codes": "200-299, 401",
			"mesh.routers.api.kuma.ignore_tls":            "true",
		},
	}
	router := Router{Name: "api"}
	resolvedURL := "https://api.wolf-infra.local"

	mon := buildHTTPMonitor(mockCfg, svc, router, resolvedURL)

	// Check basic overrides
	if mon.Name != "My Awesome API" {
		t.Errorf("Expected Name 'My Awesome API', got '%s'", mon.Name)
	}
	if mon.URL != resolvedURL {
		t.Errorf("Expected URL '%s', got '%s'", resolvedURL, mon.URL)
	}

	// Check type conversions
	if mon.Method != "POST" { // Should auto-capitalize
		t.Errorf("Expected Method 'POST', got '%s'", mon.Method)
	}
	if mon.Interval != 120 {
		t.Errorf("Expected Interval 120, got %d", mon.Interval)
	}
	if !mon.IgnoreTLS {
		t.Errorf("Expected IgnoreTLS to be true")
	}

	// Check slice parsing
	expectedCodes := []string{"200-299", "401"}
	if !reflect.DeepEqual(mon.AcceptedStatusCodes, expectedCodes) {
		t.Errorf("Expected codes %v, got %v", expectedCodes, mon.AcceptedStatusCodes)
	}
}

func TestFormatTitleFromSlug(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"status-page", "Status Page"},
		{"internal-core-api", "Internal Core Api"},
		{"", ""},
		{"simple", "Simple"},
	}

	for _, tt := range tests {
		actual := formatTitleFromSlug(tt.input)
		if actual != tt.expected {
			t.Errorf("formatTitleFromSlug(%q): expected %q, got %q", tt.input, tt.expected, actual)
		}
	}
}
