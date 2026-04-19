package cloudflare

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/wolf-361/traefik-mesh-companion/internal/config"
	"github.com/wolf-361/traefik-mesh-companion/internal/core"
)

func TestClientInitializationWithoutConfig(t *testing.T) {
	t.Setenv("CLOUDFLARE_API_TOKEN", "")
	t.Setenv("CLOUDFLARE_TARGET_DOMAIN", "")

	pipelineCfg := &config.Pipeline{}
	client := New(pipelineCfg, nil)

	if client != nil {
		t.Errorf("Expected New() to return nil when config is missing")
	}
}

func TestExtractMeshHosts_WithFilters(t *testing.T) {
	// Simulate the pipeline config filter (e.g., "traefik.http.routers.*.mesh.public" = "true")
	filterLabel := "traefik.http.routers.*.mesh.public"
	escaped := regexp.QuoteMeta(filterLabel)
	pattern := "^" + strings.ReplaceAll(escaped, "\\*", "([^.]+)") + "$"
	mockFilterRegex := regexp.MustCompile(pattern)
	mockFilterValue := "true"

	services := []core.Service{
		{
			ContainerName: "public-website",
			Hosts:         []string{"www.public.com"},
			Labels: map[string]string{
				"traefik.http.routers.web.rule":        "Host(`www.public.com`)",
				"traefik.http.routers.web.mesh.public": "true", // Will match filter!
			},
		},
		{
			ContainerName: "internal-database",
			Hosts:         []string{"db.internal.local"},
			Labels: map[string]string{
				"traefik.http.routers.db.rule":        "Host(`db.internal.local`)",
				"traefik.http.routers.db.mesh.public": "false", // Will be dropped by filter!
			},
		},
		{
			ContainerName: "ignored-public-api",
			Hosts:         []string{"api.public.com"},
			Labels: map[string]string{
				"traefik.http.routers.api.rule":         "Host(`api.public.com`)",
				"traefik.http.routers.api.mesh.public":  "true",  // Passes filter...
				"traefik.http.routers.api.mesh.managed": "false", // ...but explicitly ignored!
			},
		},
	}

	active, ignored := extractMeshHosts(services, mockFilterRegex, mockFilterValue)

	expectedActive := map[string]bool{
		"www.public.com": true,
	}
	expectedIgnored := map[string]bool{
		"api.public.com": true,
	}

	if !reflect.DeepEqual(active, expectedActive) {
		t.Errorf("Active Hosts Failed.\nExpected: %v\nGot: %v", expectedActive, active)
	}
	if !reflect.DeepEqual(ignored, expectedIgnored) {
		t.Errorf("Ignored Hosts Failed.\nExpected: %v\nGot: %v", expectedIgnored, ignored)
	}
}
