package gatus

import (
	"strings"
)

type Router struct {
	Name string
	Rule string
}

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
	if len(routers) == 0 {
		routers = append(routers, Router{Name: "default", Rule: ""})
	}
	return routers
}
