package kuma

import (
	"reflect"
	"testing"
)

func TestGetPagesFromLabels(t *testing.T) {
	// Create a dummy manager just to test the pure function
	m := &StatusPageManager{
		cfg: &Config{GlobalStatusPageSlug: "global-dashboard"},
	}

	tests := []struct {
		name     string
		labels   map[string]string
		expected map[string]string
	}{
		{
			name:   "No labels, only global page",
			labels: map[string]string{},
			expected: map[string]string{
				"global-dashboard": "Services", // Default group
			},
		},
		{
			name: "Specific global group override",
			labels: map[string]string{
				"mesh.kuma.group": "Infrastructure",
			},
			expected: map[string]string{
				"global-dashboard": "Infrastructure",
			},
		},
		{
			name: "Multiple pages with and without specific groups",
			labels: map[string]string{
				"mesh.kuma.pages": "public-api: Public Services, internal-tools, secret-dashboard: Hidden Group",
				"mesh.kuma.group": "Default Apps", // Baseline group
			},
			expected: map[string]string{
				"global-dashboard": "Default Apps",
				"public-api":       "Public Services",
				"internal-tools":   "Default Apps",
				"secret-dashboard": "Hidden Group",
			},
		},
		{
			name: "Messy spacing in labels",
			labels: map[string]string{
				"mesh.kuma.pages": "  messy-slug : Weird Group  ,   clean-slug  ",
			},
			expected: map[string]string{
				"global-dashboard": "Services",
				"messy-slug":       "Weird Group",
				"clean-slug":       "Services",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := m.getPagesFromLabels(tt.labels)
			if !reflect.DeepEqual(actual, tt.expected) {
				t.Errorf("\nExpected: %v\nGot: %v", tt.expected, actual)
			}
		})
	}
}
