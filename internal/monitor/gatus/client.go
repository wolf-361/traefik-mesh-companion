package gatus

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/wolf-361/traefik-mesh-companion/internal/core"
	"github.com/wolf-361/traefik-mesh-companion/internal/traefik"
)

var _ core.Processor = (*Client)(nil)

type Client struct {
	cfg     *Config
	exec    *core.Executor
	http    *http.Client
	tracked map[string]bool
}

func New(exec *core.Executor) *Client {
	cfg := LoadConfig()
	if cfg == nil {
		slog.Debug("Gatus Bridge config not found, skipping initialization")
		return nil
	}

	return &Client{
		cfg:     cfg,
		exec:    exec,
		http:    &http.Client{Timeout: 10 * time.Second},
		tracked: make(map[string]bool),
	}
}

func (c *Client) Name() string { return "Gatus Bridge" }

func (c *Client) SyncState() error {
	if c.cfg == nil {
		return nil
	}

	slog.Info("Syncing existing state from Gatus Bridge...")

	req, _ := http.NewRequest(http.MethodGet, c.cfg.BridgeURL+"/api/v1/endpoints", nil)
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey) // <-- Auth Fixed!
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Debug("failed to close response body", "error", closeErr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("api returned status %d", resp.StatusCode)
	}

	var endpoints []GatusEndpoint
	if err := json.NewDecoder(resp.Body).Decode(&endpoints); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	for _, ep := range endpoints {
		cacheKey := ep.Group + "/" + ep.Name
		c.tracked[cacheKey] = true
	}

	slog.Info("Gatus state synchronized", "endpoints", len(endpoints))
	return nil
}

func (c *Client) Process(services []core.Service) error {
	if c.cfg == nil {
		return nil
	}

	// Track what we see in THIS sync cycle to detect orphans
	activeKeys := make(map[string]bool)

	for _, svc := range services {
		gatusEnabled := c.cfg.AutoEnable
		if val, exists := svc.Labels["mesh.gatus.enable"]; exists {
			gatusEnabled = strings.ToLower(val) == "true"
		}

		if !gatusEnabled {
			continue
		}

		routers := extractRouters(svc.Labels)
		for _, router := range routers {
			payload := c.buildPayload(svc, router)

			if payload.URL == "" || payload.URL == "https://" {
				continue
			}

			cacheKey := payload.Group + "/" + payload.Name
			activeKeys[cacheKey] = true

			if c.tracked[cacheKey] {
				continue
			}

			if err := c.AddEndpoint(payload); err == nil {
				c.tracked[cacheKey] = true
			}
		}
	}

	// --- ORPHAN CLEANUP LOGIC ---
	for trackedKey := range c.tracked {
		if !activeKeys[trackedKey] {
			parts := strings.SplitN(trackedKey, "/", 2)
			if len(parts) == 2 {
				group, name := parts[0], parts[1]
				if err := c.DeleteEndpoint(name, group); err == nil {
					delete(c.tracked, trackedKey) // Remove from cache so we don't try again
				}
			}
		}
	}

	return nil
}

func (c *Client) buildPayload(svc core.Service, router Router) GatusEndpoint {
	labels := svc.Labels

	payload := GatusEndpoint{
		Name:     fmt.Sprintf("%s-%s", svc.ContainerName, router.Name),
		Group:    "Infrastructure",
		Method:   "GET",
		Interval: "60s",
		Conditions: []string{
			"[STATUS] == 200",
			"[RESPONSE_TIME] < 500",
		},
	}

	hosts, paths := traefik.ParseRule(router.Rule)
	basePath := ""
	if len(paths) > 0 {
		basePath = paths[0]
	}

	if len(hosts) > 0 {
		payload.URL = "https://" + hosts[0] + basePath
	} else if len(svc.Hosts) > 0 {
		payload.URL = "https://" + svc.Hosts[0]
	}

	if val := labels["mesh.gatus.name"]; val != "" {
		payload.Name = val
	}
	if val := labels["mesh.gatus.group"]; val != "" {
		payload.Group = val
	}
	if val := labels["mesh.gatus.url"]; val != "" {
		// Relative url is appended to the host, absolute url replaces it
		if strings.HasPrefix(val, "/") {
			payload.URL = strings.TrimRight(payload.URL, "/") + val
		} else {
			payload.URL = val
		}
	}
	if val := labels["mesh.gatus.method"]; val != "" {
		payload.Method = strings.ToUpper(val)
	}
	if val := labels["mesh.gatus.interval"]; val != "" {
		payload.Interval = val
	}
	if val := labels["mesh.gatus.body"]; val != "" {
		payload.Body = val
	}

	if val := labels["mesh.gatus.conditions"]; val != "" {
		parts := strings.Split(val, ",")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		payload.Conditions = parts
	}

	if val := labels["mesh.gatus.insecure"]; val != "" {
		if strings.ToLower(val) == "true" {
			payload.Client = &GatusClient{Insecure: true}
		}
	}

	if val := labels["mesh.gatus.ui.description"]; val != "" {
		if payload.UI == nil {
			payload.UI = &GatusUI{}
		}
		payload.UI.Description = val
	}

	if val := labels["mesh.gatus.headers"]; val != "" {
		payload.Headers = make(map[string]string)
		pairs := strings.Split(val, ",")
		for _, pair := range pairs {
			parts := strings.SplitN(pair, ":", 2)
			if len(parts) == 2 {
				payload.Headers[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
			}
		}
	}

	return payload
}

func (c *Client) AddEndpoint(payload GatusEndpoint) error {
	return c.exec.Run("create Gatus endpoint", func() error {
		body, _ := json.Marshal(payload)

		req, _ := http.NewRequest(http.MethodPost, c.cfg.BridgeURL+"/api/v1/endpoints", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		if c.cfg.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey) // <-- Auth Fixed!
		}

		resp, err := c.http.Do(req)
		if err != nil {
			return err
		}
		defer func() {
			if closeErr := resp.Body.Close(); closeErr != nil {
				slog.Debug("failed to close response body", "error", closeErr)
			}
		}()

		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
			return nil
		}

		resBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("bridge returned %d: %s", resp.StatusCode, string(resBody))
	}, "name", payload.Name, "group", payload.Group)
}

// --- NEW ORPHAN CLEANUP METHOD ---
func (c *Client) DeleteEndpoint(name, group string) error {
	return c.exec.Run("delete Gatus endpoint", func() error {
		// Pass name and group as URL query parameters
		url := fmt.Sprintf("%s/api/v1/endpoints?name=%s&group=%s", c.cfg.BridgeURL, name, group)

		req, _ := http.NewRequest(http.MethodDelete, url, nil)
		if c.cfg.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
		}

		resp, err := c.http.Do(req)
		if err != nil {
			return err
		}
		defer func() {
			if closeErr := resp.Body.Close(); closeErr != nil {
				slog.Debug("failed to close response body", "error", closeErr)
			}
		}()

		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
			// If it's 404 Not Found, Gatus already forgot about it, so we consider it a success
			return nil
		}

		resBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("bridge returned %d: %s", resp.StatusCode, string(resBody))
	}, "name", name, "group", group)
}
