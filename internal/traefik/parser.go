package traefik

import (
	"log/slog"

	"github.com/traefik/traefik/v3/pkg/rules"
)

// ParseRule takes a raw Traefik router rule string and extracts all Hosts and PathPrefixes.
// It uses Traefik's native AST parser so it perfectly handles complex logic like && and ||.
func ParseRule(ruleStr string) ([]string, []string) {
	if ruleStr == "" {
		return nil, nil
	}

	// Initialize Traefik's official parser with the matchers we care about
	parser, err := rules.NewParser([]string{
		"Host", "HostRegexp", "Path", "PathPrefix", "PathRegexp", "Headers", "Method", "Query",
	})
	if err != nil {
		slog.Error("Failed to initialize Traefik AST parser", "error", err)
		return nil, nil
	}

	// Parse the string into an Abstract Syntax Tree (AST)
	parsedRaw, err := parser.Parse(ruleStr)
	if err != nil {
		slog.Warn("Failed to parse Traefik rule", "rule", ruleStr, "error", err)
		return nil, nil
	}

	tree, ok := parsedRaw.(*rules.Tree)
	if !ok {
		return nil, nil
	}

	var hosts []string
	var paths []string

	// Recursively walk the AST branches to pull out values
	var walk func(t *rules.Tree)
	walk = func(t *rules.Tree) {
		if t == nil {
			return
		}

		if t.Matcher == "Host" {
			hosts = append(hosts, t.Value...)
		}
		if t.Matcher == "PathPrefix" || t.Matcher == "Path" {
			paths = append(paths, t.Value...)
		}

		// Traverse down logical operators
		walk(t.RuleLeft)
		walk(t.RuleRight)
	}

	walk(tree)

	return hosts, paths
}