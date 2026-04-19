package gatus

import (
	"reflect"
	"testing"
)

func TestExtractRouters(t *testing.T) {
	tests := []struct {
		name     string
		labels   map[string]string
		expected []Router
	}{
		{
			name: "Standard Traefik Router",
			labels: map[string]string{
				"traefik.http.routers.frontend.rule": "Host(`app.local`)",
			},
			expected: []Router{{Name: "frontend", Rule: "Host(`app.local`)"}},
		},
		{
			name: "No Routers (Worker Container)",
			labels: map[string]string{
				"mesh.gatus.enable": "true",
			},
			expected: []Router{{Name: "default", Rule: ""}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := extractRouters(tt.labels)
			if !reflect.DeepEqual(actual, tt.expected) {
				t.Errorf("\nExpected: %+v\nGot: %+v", tt.expected, actual)
			}
		})
	}
}
