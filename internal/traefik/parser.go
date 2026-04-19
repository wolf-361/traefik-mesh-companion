package traefik

import (
	"regexp"
	"strings"
)

var (
	// These lazy matchers safely extract anything inside the parentheses
	hostRegex       = regexp.MustCompile(`(?i)Host\((.*?)\)`)
	pathPrefixRegex = regexp.MustCompile(`(?i)PathPrefix\((.*?)\)`)
	pathRegex       = regexp.MustCompile(`(?i)Path\((.*?)\)`)
)

// ParseRule takes a raw Traefik router rule string and extracts all Hosts and PathPrefixes.
// It uses a lightweight regex engine to avoid importing Traefik's internal AST,
// keeping the companion fast, robust, and completely independent.
func ParseRule(ruleStr string) ([]string, []string) {
	if ruleStr == "" {
		return nil, nil
	}

	var hosts []string
	var paths []string

	hosts = append(hosts, extractValues(hostRegex, ruleStr)...)
	paths = append(paths, extractValues(pathPrefixRegex, ruleStr)...)
	paths = append(paths, extractValues(pathRegex, ruleStr)...)

	return hosts, paths
}

// extractValues finds all matches, splits them by comma, and cleans up the syntax
func extractValues(re *regexp.Regexp, ruleStr string) []string {
	var results []string
	matches := re.FindAllStringSubmatch(ruleStr, -1)

	for _, match := range matches {
		if len(match) > 1 {
			// e.g., match[1] = "`app.local`, `app.remote`"
			parts := strings.Split(match[1], ",")
			for _, part := range parts {
				// Remove spaces, backticks, and quotes
				clean := strings.TrimSpace(part)
				clean = strings.Trim(clean, "`'\"")
				if clean != "" {
					results = append(results, clean)
				}
			}
		}
	}
	return results
}
