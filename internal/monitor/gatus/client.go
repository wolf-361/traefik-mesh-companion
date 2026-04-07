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
)

// Ensure Client implements the core.Processor interface at compile time
var _ core.Processor = (*Client)(nil)

type Client struct {
	cfg     *Config
	exec    *core.Executor
	http    *http.Client
	tracked map[string]bool
}

// New initializes the Gatus client, loading its own config automatically.
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

	// Call our new GET /api/v1/endpoints
	req, _ := http.NewRequest(http.MethodGet, c.cfg.BridgeURL+"/api/v1/endpoints", nil)
	if c.cfg.APIKey != "" {
		req.Header.Set("X-API-Key", c.cfg.APIKey)
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

	var endpoints []EndpointPayload
	if err := json.NewDecoder(resp.Body).Decode(&endpoints); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	for _, ep := range endpoints {
		// Use Group + Name as the unique cache key
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

	for _, svc := range services {
		gatusEnabled := c.cfg.AutoEnable
		if val, exists := svc.Labels["mesh.gatus.enable"]; exists {
			gatusEnabled = strings.ToLower(val) == "true"
		}

		if !gatusEnabled {
			continue
		}

		payload := c.buildPayload(svc)

		if payload.URL == "" || payload.URL == "https://" {
			slog.Debug("Skipping Gatus monitor: no valid URL found", "container", svc.ContainerName)
			continue
		}

		cacheKey := payload.Group + "/" + payload.Name
		if c.tracked[cacheKey] {
			continue // Already exists in Gatus, skip HTTP call
		}

		if err := c.AddEndpoint(payload); err == nil {
			c.tracked[cacheKey] = true
		}
	}

	return nil
}

func (c *Client) buildPayload(svc core.Service) EndpointPayload {
	labels := svc.Labels

	// Sane defaults
	payload := EndpointPayload{
		Name:  svc.ContainerName,
		Group: "infrastructure",
	}

	if len(svc.Hosts) > 0 {
		payload.URL = "https://" + svc.Hosts[0]
	}

	// Override with labels
	if val := labels["mesh.gatus.name"]; val != "" {
		payload.Name = val
	}
	if val := labels["mesh.gatus.group"]; val != "" {
		payload.Group = val
	}
	if val := labels["mesh.gatus.url"]; val != "" {
		payload.URL = val
	}
	if val := labels["mesh.gatus.method"]; val != "" {
		payload.Method = strings.ToUpper(val)
	}
	if val := labels["mesh.gatus.interval"]; val != "" {
		payload.Interval = val
	}
	if val := labels["mesh.gatus.conditions"]; val != "" {
		// Gatus expects an array of strings, so we split by comma
		// e.g., "[STATUS] == 200,[RESPONSE_TIME] < 500"
		parts := strings.Split(val, ",")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		payload.Conditions = parts
	}

	return payload
}

func (c *Client) AddEndpoint(payload EndpointPayload) error {
	return c.exec.Run("create Gatus endpoint", func() error {
		body, _ := json.Marshal(payload)

		// POST to our new API
		req, _ := http.NewRequest(http.MethodPost, c.cfg.BridgeURL+"/api/v1/endpoints", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		if c.cfg.APIKey != "" {
			req.Header.Set("X-API-Key", c.cfg.APIKey)
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
