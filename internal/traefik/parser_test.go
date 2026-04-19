package traefik

import (
	"reflect"
	"testing"
)

func TestParseRule(t *testing.T) {
	tests := []struct {
		name          string
		rule          string
		expectedHosts []string
		expectedPaths []string
	}{
		{
			name:          "Empty Rule",
			rule:          "",
			expectedHosts: nil,
			expectedPaths: nil,
		},
		{
			name:          "Irrelevant Matcher (Ignored)",
			rule:          "Headers(`Foo`, `Bar`)",
			expectedHosts: nil,
			expectedPaths: nil,
		},
		{
			name:          "Standard Backticks",
			rule:          "Host(`app.local`)",
			expectedHosts: []string{"app.local"},
			expectedPaths: nil,
		},
		{
			name:          "Double Quotes",
			rule:          `Host("app.local")`,
			expectedHosts: []string{"app.local"},
			expectedPaths: nil,
		},
		{
			name:          "Single Quotes",
			rule:          "Host('app.local')",
			expectedHosts: []string{"app.local"},
			expectedPaths: nil,
		},
		{
			name:          "Multiple Domains in One Block",
			rule:          "Host(`app1.local`, `app2.local`)",
			expectedHosts: []string{"app1.local", "app2.local"},
			expectedPaths: nil,
		},
		{
			name:          "Multiple Host Blocks (OR)",
			rule:          "Host(`app1.local`) || Host(`app2.local`)",
			expectedHosts: []string{"app1.local", "app2.local"},
			expectedPaths: nil,
		},
		{
			name:          "PathPrefix Extraction",
			rule:          "PathPrefix(`/api`)",
			expectedHosts: nil,
			expectedPaths: []string{"/api"},
		},
		{
			name:          "Exact Path Extraction",
			rule:          "Path(`/health`)",
			expectedHosts: nil,
			expectedPaths: []string{"/health"},
		},
		{
			name:          "Host AND PathPrefix",
			rule:          "Host(`app.local`) && PathPrefix(`/api`)",
			expectedHosts: []string{"app.local"},
			expectedPaths: []string{"/api"},
		},
		{
			name:          "Complex Nested Logic",
			rule:          "(Host(`a.local`) || Host(`b.local`)) && (PathPrefix(`/v1`) || Path(`/v2`))",
			expectedHosts: []string{"a.local", "b.local"},
			expectedPaths: []string{"/v1", "/v2"},
		},
		{
			name:          "Messy Spacing Resilience",
			rule:          "Host( `app.local` ,  `app.remote` ) && PathPrefix( `/api` )",
			expectedHosts: []string{"app.local", "app.remote"},
			expectedPaths: []string{"/api"},
		},
		{
			name:          "Mixed Matchers (Extract only what we need)",
			rule:          "Host(`app.local`) && Headers(`Auth`, `true`) && PathPrefix(`/api`)",
			expectedHosts: []string{"app.local"},
			expectedPaths: []string{"/api"},
		},
		{
			name:          "Ensure HostRegexp is safely bypassed",
			rule:          "HostRegexp(`{subdomain:[a-z]+}.app.local`)",
			expectedHosts: nil, // We only care about static Host() routing
			expectedPaths: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hosts, paths := ParseRule(tt.rule)

			if !reflect.DeepEqual(hosts, tt.expectedHosts) {
				t.Errorf("\nHosts mismatch for rule: %s\nExpected: %v\nGot:      %v", tt.rule, tt.expectedHosts, hosts)
			}

			if !reflect.DeepEqual(paths, tt.expectedPaths) {
				t.Errorf("\nPaths mismatch for rule: %s\nExpected: %v\nGot:      %v", tt.rule, tt.expectedPaths, paths)
			}
		})
	}
}
