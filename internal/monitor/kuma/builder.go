package kuma

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/breml/go-uptime-kuma-client/monitor"
	"github.com/wolf-361/traefik-mesh-companion/internal/core"
)

// buildHTTPMonitor translates our mesh config into a Kuma API payload
func buildHTTPMonitor(cfg *Config, svc core.Service, router Router, resolvedURL string) *monitor.HTTP {
	mon := &monitor.HTTP{
		Base: monitor.Base{
			Interval:      cfg.DefaultInterval,
			MaxRetries:    cfg.DefaultMaxRetries,
			RetryInterval: cfg.DefaultRetryInterval,
			IsActive:      true,
		},
		HTTPDetails: monitor.HTTPDetails{
			Method:              "GET",
			AcceptedStatusCodes: cfg.DefaultAcceptedStatusCodes,
			MaxRedirects:        cfg.DefaultMaxRedirects,
			IgnoreTLS:           false,
		},
	}

	mon.URL = resolvedURL
	if name := getMeshLabel(svc, router.Name, "name"); name != "" {
		mon.Name = name
	} else {
		mon.Name = fmt.Sprintf("%s (%s)", svc.ContainerName, router.Name)
	}

	// Advanced configuration mapping
	if val := getMeshLabel(svc, router.Name, "description"); val != "" {
		mon.Description = &val
	}
	if val := getMeshLabel(svc, router.Name, "method"); val != "" {
		mon.Method = strings.ToUpper(val)
	}
	if val := getMeshLabel(svc, router.Name, "body"); val != "" {
		mon.Body = val
	}
	if val := getMeshLabel(svc, router.Name, "headers"); val != "" {
		mon.Headers = val
	}
	if val := getMeshLabel(svc, router.Name, "basic_auth_user"); val != "" {
		mon.BasicAuthUser = val
	}
	if val := getMeshLabel(svc, router.Name, "basic_auth_pass"); val != "" {
		mon.BasicAuthPass = val
	}
	if val := getMeshLabel(svc, router.Name, "ignore_tls"); val != "" {
		mon.IgnoreTLS = strings.ToLower(val) == "true"
	}
	if val := getMeshLabel(svc, router.Name, "upside_down"); val != "" {
		mon.UpsideDown = strings.ToLower(val) == "true"
	}
	if val := getMeshLabel(svc, router.Name, "interval"); val != "" {
		if i, err := strconv.ParseInt(val, 10, 64); err == nil {
			mon.Interval = i
		}
	}
	if val := getMeshLabel(svc, router.Name, "retry_interval"); val != "" {
		if i, err := strconv.ParseInt(val, 10, 64); err == nil {
			mon.RetryInterval = i
		}
	}
	if val := getMeshLabel(svc, router.Name, "max_retries"); val != "" {
		if i, err := strconv.ParseInt(val, 10, 64); err == nil {
			mon.MaxRetries = i
		}
	}
	if val := getMeshLabel(svc, router.Name, "accepted_status_codes"); val != "" {
		rawCodes := strings.Split(val, ",")
		cleanCodes := make([]string, 0, len(rawCodes))
		for _, code := range rawCodes {
			cleanCodes = append(cleanCodes, strings.TrimSpace(code))
		}
		mon.AcceptedStatusCodes = cleanCodes
	}

	return mon
}

// formatTitleFromSlug formats slugs like "status-page" to "Status Page"
func formatTitleFromSlug(slug string) string {
	parts := strings.Split(slug, "-")
	for i, part := range parts {
		if len(part) > 0 {
			parts[i] = strings.ToUpper(part[:1]) + part[1:]
		}
	}
	return strings.Join(parts, " ")
}
