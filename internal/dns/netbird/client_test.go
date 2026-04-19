package netbird

import (
	"reflect"
	"testing"

	"github.com/wolf-361/traefik-mesh-companion/internal/config"
	"github.com/wolf-361/traefik-mesh-companion/internal/core"
)

func TestClientInitializationWithoutConfig(t *testing.T) {
	t.Setenv("NETBIRD_API_TOKEN", "")
	t.Setenv("NETBIRD_TARGET_IP", "")

	pipelineCfg := &config.Pipeline{}
	client := New(pipelineCfg, nil)

	if client != nil {
		t.Errorf("Expected New() to return nil when config is missing")
	}
}

func TestProcessLogic_ASTParsing(t *testing.T) {
	// Simulate services hitting the pure extractor
	services := []core.Service{
		{
			ContainerName: "app-1",
			Hosts:         []string{"app.internal.zone"}, // Fallback array
			Labels: map[string]string{
				// Standard active router
				"traefik.http.routers.web.rule": "Host(`app.internal.zone`)",
			},
		},
		{
			ContainerName: "app-2",
			Hosts:         []string{"api.internal.zone"}, // Fallback array
			Labels: map[string]string{
				// Explicitly ignored router
				"traefik.http.routers.api.rule":         "Host(`api.internal.zone`)",
				"traefik.http.routers.api.mesh.managed": "false",
			},
		},
		{
			ContainerName: "app-3",
			Hosts:         []string{"admin1.zone", "admin2.zone"}, // Fallback array
			Labels: map[string]string{
				// Complex AST rule with multiple hosts
				"traefik.http.routers.admin.rule": "Host(`admin1.zone`, `admin2.zone`) && PathPrefix(`/`)",
			},
		},
	}

	// Test the pure extraction logic (No HTTP calls!)
	active, ignored := extractMeshHosts(services)

	expectedActive := map[string]bool{
		"app.internal.zone": true,
		"admin1.zone":       true,
		"admin2.zone":       true,
	}

	expectedIgnored := map[string]bool{
		"api.internal.zone": true,
	}

	if !reflect.DeepEqual(active, expectedActive) {
		t.Errorf("AST Parsing Active Hosts Failed.\nExpected: %v\nGot: %v", expectedActive, active)
	}

	if !reflect.DeepEqual(ignored, expectedIgnored) {
		t.Errorf("AST Parsing Ignored Hosts Failed.\nExpected: %v\nGot: %v", expectedIgnored, ignored)
	}
}
