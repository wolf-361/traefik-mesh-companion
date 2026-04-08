package kuma

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/wolf-361/traefik-mesh-companion/internal/core"
)

// Ensure Client implements the core.Processor interface at compile time
var _ core.Processor = (*Client)(nil)

type Client struct {
	cfg        *Config
	exec       *core.Executor
	http       *http.Client
	tracked    map[string]bool
	groupCache map[string]int
}

// New initializes the Kuma client, loading its own config automatically.
func New(exec *core.Executor) *Client {
	cfg := LoadConfig()
	if cfg == nil {
		slog.Debug("Uptime Kuma Client config not found, skipping initialization")
		return nil
	}

	return &Client{
		cfg:        cfg,
		exec:       exec,
		http:       &http.Client{Timeout: 10 * time.Second},
		tracked:    make(map[string]bool),
		groupCache: make(map[string]int),
	}
}

func (c *Client) Name() string { return "Uptime Kuma" }

func (c *Client) SyncState() error {
	if c.cfg == nil {
		return nil
	}

	slog.Info("Syncing existing state from Uptime Kuma...")

	req, _ := http.NewRequest(http.MethodGet, c.cfg.URL+"/api/monitor", nil)
	req.SetBasicAuth("", c.cfg.APIKey)

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

	var monitors []MonitorResponse
	if err := json.NewDecoder(resp.Body).Decode(&monitors); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	for _, m := range monitors {
		if m.Type == "group" {
			c.groupCache[m.Name] = m.ID
		} else {
			cacheKey := m.URL + m.Name
			c.tracked[cacheKey] = true
		}
	}

	slog.Info("Uptime Kuma state synchronized", "monitors", len(monitors), "groups", len(c.groupCache))
	return nil
}

func (c *Client) Process(services []core.Service) error {
	if c.cfg == nil {
		return nil
	}

	for _, svc := range services {
		kumaEnabled := c.cfg.AutoEnable
		if val, exists := svc.Labels["mesh.kuma.enable"]; exists {
			kumaEnabled = strings.ToLower(val) == "true"
		}

		if !kumaEnabled {
			continue
		}

		payload := c.buildPayload(svc)

		if payload.URL == "" || payload.URL == "https://" {
			slog.Debug("Skipping Kuma monitor: no valid URL found", "container", svc.ContainerName)
			continue
		}

		cacheKey := payload.URL + payload.Name
		if c.tracked[cacheKey] {
			continue
		}

		if err := c.AddMonitor(payload); err == nil {
			c.tracked[cacheKey] = true
		}
	}

	return nil
}

func (c *Client) buildPayload(svc core.Service) MonitorPayload {
	labels := svc.Labels

	payload := MonitorPayload{
		Type:          "http",
		Name:          svc.ContainerName,
		Method:        "GET",
		Interval:      60,
		RetryInterval: 60,
		MaxRetries:    3,
		AcceptedCodes: []string{"200-299"},
		IgnoreTLS:     false,
	}

	if len(svc.Hosts) > 0 {
		payload.URL = "https://" + svc.Hosts[0]
	}

	if val := labels["mesh.kuma.name"]; val != "" {
		payload.Name = val
	}
	if val := labels["mesh.kuma.url"]; val != "" {
		payload.URL = val
	}
	if val := labels["mesh.kuma.method"]; val != "" {
		payload.Method = strings.ToUpper(val)
	}
	if val := labels["mesh.kuma.description"]; val != "" {
		payload.Description = val
	}
	if val := labels["mesh.kuma.ignore_tls"]; val != "" {
		payload.IgnoreTLS = strings.ToLower(val) == "true"
	}
	if val := labels["mesh.kuma.upside_down"]; val != "" {
		payload.UpsideDown = strings.ToLower(val) == "true"
	}
	if val := labels["mesh.kuma.accepted_status_codes"]; val != "" {
		payload.AcceptedCodes = strings.Split(val, ",")
	}

	if val := labels["mesh.kuma.interval"]; val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			payload.Interval = i
		}
	}
	if val := labels["mesh.kuma.retry_interval"]; val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			payload.RetryInterval = i
		}
	}
	if val := labels["mesh.kuma.max_retries"]; val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			payload.MaxRetries = i
		}
	}

	if groupName := labels["mesh.kuma.group"]; groupName != "" {
		if id, err := c.resolveGroupID(groupName); err == nil {
			payload.Parent = &id
		}
	}

	return payload
}

func (c *Client) resolveGroupID(name string) (int, error) {
	if id, exists := c.groupCache[name]; exists {
		return id, nil
	}

	var groupID int
	err := c.exec.Run("create Uptime Kuma group", func() error {
		groupPayload := map[string]interface{}{"type": "group", "name": name}
		body, _ := json.Marshal(groupPayload)

		req, _ := http.NewRequest(http.MethodPost, c.cfg.URL+"/api/monitor", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.SetBasicAuth("", c.cfg.APIKey)

		resp, err := c.http.Do(req)
		if err != nil {
			return err
		}
		defer func() {
			if closeErr := resp.Body.Close(); closeErr != nil {
				slog.Debug("failed to close response body", "error", closeErr)
			}
		}()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("API returned status %d", resp.StatusCode)
		}

		var result MonitorResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return fmt.Errorf("failed to parse group creation response: %w", err)
		}

		groupID = result.ID
		return nil
	}, "name", name)

	if err != nil {
		return 0, err
	}

	c.groupCache[name] = groupID
	return groupID, nil
}

func (c *Client) AddMonitor(payload MonitorPayload) error {
	return c.exec.Run("create Uptime Kuma monitor", func() error {
		body, _ := json.Marshal(payload)

		req, _ := http.NewRequest(http.MethodPost, c.cfg.URL+"/api/monitor", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.SetBasicAuth("", c.cfg.APIKey)

		resp, err := c.http.Do(req)
		if err != nil {
			return err
		}
		defer func() {
			if closeErr := resp.Body.Close(); closeErr != nil {
				slog.Debug("failed to close response body", "error", closeErr)
			}
		}()

		if resp.StatusCode == http.StatusOK {
			return nil
		}

		resBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API returned %d: %s", resp.StatusCode, string(resBody))
	}, "name", payload.Name, "url", payload.URL)
}
