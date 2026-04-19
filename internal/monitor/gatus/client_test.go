package gatus

import (
	"reflect"
	"testing"

	"github.com/wolf-361/traefik-mesh-companion/internal/core"
)

func TestClientInitializationWithoutConfig(t *testing.T) {
	t.Setenv("GATUS_BRIDGE_URL", "")
	client := New(nil)

	if client != nil {
		t.Errorf("Expected New() to return nil when config is missing")
	}
}

func TestBuildPayload_AdvancedOverrides(t *testing.T) {
	c := &Client{} // Empty client to test the pure function

	svc := core.Service{
		ContainerName: "core-database",
		Hosts:         []string{"db.local"},
		Labels: map[string]string{
			"mesh.gatus.name":           "Primary DB",
			"mesh.gatus.group":          "Backend",
			"mesh.gatus.url":            "/healthz", // Test relative path append!
			"mesh.gatus.method":         "post",     // Should auto-uppercase
			"mesh.gatus.interval":       "30s",
			"mesh.gatus.body":           `{"query": "SELECT 1"}`,
			"mesh.gatus.conditions":     "[STATUS] == 200, [RESPONSE_TIME] < 100",
			"mesh.gatus.insecure":       "true",
			"mesh.gatus.ui.description": "Database check",
			"mesh.gatus.headers":        "Authorization: Bearer token, Content-Type: application/json",
		},
	}
	router := Router{Name: "api", Rule: "Host(`db.local`)"}

	payload := c.buildPayload(svc, router)

	// Basic Overrides
	if payload.Name != "Primary DB" {
		t.Errorf("Expected Name 'Primary DB', got '%s'", payload.Name)
	}
	if payload.Group != "Backend" {
		t.Errorf("Expected Group 'Backend', got '%s'", payload.Group)
	}
	if payload.Method != "POST" {
		t.Errorf("Expected Method 'POST', got '%s'", payload.Method)
	}
	if payload.Interval != "30s" {
		t.Errorf("Expected Interval '30s', got '%s'", payload.Interval)
	}

	// Relative URL Appending
	if payload.URL != "https://db.local/healthz" {
		t.Errorf("Expected URL 'https://db.local/healthz', got '%s'", payload.URL)
	}

	// Slice/Comma parsing
	expectedConditions := []string{"[STATUS] == 200", "[RESPONSE_TIME] < 100"}
	if !reflect.DeepEqual(payload.Conditions, expectedConditions) {
		t.Errorf("Expected Conditions %v, got %v", expectedConditions, payload.Conditions)
	}

	// Nested Structs (Client & UI)
	if payload.Client == nil || !payload.Client.Insecure {
		t.Errorf("Expected Insecure Client true")
	}
	if payload.UI == nil || payload.UI.Description != "Database check" {
		t.Errorf("Expected UI Description match")
	}

	// Map/Header parsing
	expectedHeaders := map[string]string{
		"Authorization": "Bearer token",
		"Content-Type":  "application/json",
	}
	if !reflect.DeepEqual(payload.Headers, expectedHeaders) {
		t.Errorf("Expected Headers %v, got %v", expectedHeaders, payload.Headers)
	}
}

func TestBuildPayload_Defaults(t *testing.T) {
	c := &Client{}
	svc := core.Service{
		ContainerName: "default-app",
		Hosts:         []string{"app.local"},
		Labels:        map[string]string{}, // No Gatus labels
	}
	router := Router{Name: "web", Rule: ""}

	payload := c.buildPayload(svc, router)

	// Verify fallback formatting
	if payload.Name != "default-app-web" {
		t.Errorf("Expected formatted name 'default-app-web', got '%s'", payload.Name)
	}
	if payload.Group != "Infrastructure" {
		t.Errorf("Expected default Group 'Infrastructure', got '%s'", payload.Group)
	}
	if payload.URL != "https://app.local" {
		t.Errorf("Expected default URL 'https://app.local', got '%s'", payload.URL)
	}
	if payload.Method != "GET" {
		t.Errorf("Expected default Method 'GET', got '%s'", payload.Method)
	}
}
