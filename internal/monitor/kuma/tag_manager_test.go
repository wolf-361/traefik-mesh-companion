package kuma

import (
	"testing"
)

func TestGetAutoColor(t *testing.T) {
	// Determinism Check: The same string MUST always return the same color
	color1 := getAutoColor("backend-api")
	color2 := getAutoColor("backend-api")
	if color1 != color2 {
		t.Errorf("AutoColor is not deterministic! %s != %s", color1, color2)
	}

	// Format Check: Must be a hex code
	if len(color1) != 7 || color1[0] != '#' {
		t.Errorf("Expected hex color code (e.g., #ff0000), got %s", color1)
	}
}

func TestBuildTagDefs(t *testing.T) {
	manager := &TagManager{
		cfg: &Config{DefaultTags: []string{"global-tag"}},
	}

	labels := map[string]string{
		// Note the duplicate 'api' and the custom color injection
		"mesh.kuma.tags": "api, custom-tag:#ff0000, api",
	}

	tags := manager.buildTagDefs(labels)

	// We expect exactly 3 tags: global-tag, api, custom-tag (duplicates stripped)
	if len(tags) != 3 {
		t.Fatalf("Expected 3 tags, got %d", len(tags))
	}

	// Verify order and overrides
	if tags[0].Name != "global-tag" {
		t.Errorf("Expected first tag 'global-tag', got %s", tags[0].Name)
	}
	if tags[1].Name != "api" {
		t.Errorf("Expected second tag 'api', got %s", tags[1].Name)
	}
	if tags[2].Name != "custom-tag" || tags[2].Color != "#ff0000" {
		t.Errorf("Expected 'custom-tag' with '#ff0000', got %+v", tags[2])
	}
}
