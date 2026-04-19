package kuma

import (
	"fmt"
	"strings"

	"github.com/wolf-361/traefik-mesh-companion/internal/core"
	"github.com/wolf-361/traefik-mesh-companion/internal/traefik"
)

type Router struct {
	Name string
	Rule string
}

// extractRouters parses raw Traefik labels into a list of Routers.
func extractRouters(labels map[string]string) []Router {
	var routers []Router
	for k, v := range labels {
		if strings.HasPrefix(k, "traefik.http.routers.") && strings.HasSuffix(k, ".rule") {
			parts := strings.Split(k, ".")
			if len(parts) >= 5 {
				routers = append(routers, Router{
					Name: parts[3],
					Rule: v,
				})
			}
		}
	}
	// Dummy router for worker containers without HTTP rules
	if len(routers) == 0 {
		routers = append(routers, Router{Name: "default", Rule: ""})
	}
	return routers
}

// getMeshLabel handles the hierarchy: Router Override > Global Service Label
func getMeshLabel(svc core.Service, routerName string, key string) string {
	if routerName != "default" {
		routerKumaKey := fmt.Sprintf("mesh.routers.%s.kuma.%s", routerName, key)
		if val, ok := svc.Labels[routerKumaKey]; ok {
			return val
		}
		routerMeshKey := fmt.Sprintf("mesh.routers.%s.%s", routerName, key)
		if val, ok := svc.Labels[routerMeshKey]; ok {
			return val
		}
	}

	globalKumaKey := fmt.Sprintf("mesh.kuma.%s", key)
	if val, ok := svc.Labels[globalKumaKey]; ok {
		return val
	}

	globalMeshKey := fmt.Sprintf("mesh.%s", key)
	if val, ok := svc.Labels[globalMeshKey]; ok {
		return val
	}
	return ""
}

// resolveMonitorURL purely handles AST parsing and relative/absolute overrides
func resolveMonitorURL(svc core.Service, router Router) string {
	hosts, paths := traefik.ParseRule(router.Rule)
	basePath := ""
	if len(paths) > 0 {
		basePath = paths[0]
	}

	detectedURL := ""
	if len(hosts) > 0 {
		detectedURL = "https://" + hosts[0] + basePath
	} else if len(svc.Hosts) > 0 {
		detectedURL = "https://" + svc.Hosts[0]
	}

	if urlOverride := getMeshLabel(svc, router.Name, "url"); urlOverride != "" {
		if strings.HasPrefix(urlOverride, "/") {
			return strings.TrimRight(detectedURL, "/") + urlOverride
		}
		return urlOverride
	}
	return detectedURL
}

// isEnabled checks if Kuma is active for this router
func isEnabled(svc core.Service, routerName string, autoEnable bool) bool {
	if val := getMeshLabel(svc, routerName, "enable"); val != "" {
		return strings.ToLower(val) == "true"
	}
	if val := getMeshLabel(svc, routerName, "managed"); val != "" {
		return strings.ToLower(val) == "true"
	}
	return autoEnable
}
