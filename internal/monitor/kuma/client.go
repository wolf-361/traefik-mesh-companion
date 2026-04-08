package kuma

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	kumaClient "github.com/breml/go-uptime-kuma-client"
	"github.com/breml/go-uptime-kuma-client/monitor"
	"github.com/wolf-361/traefik-mesh-companion/internal/core"
)

var _ core.Processor = (*Client)(nil)

type Client struct {
	cfg     *Config
	exec    *core.Executor
	client  *kumaClient.Client
	tracked map[string]bool
}

func New(exec *core.Executor) *Client {
	cfg := LoadConfig()
	if cfg == nil {
		slog.Debug("Uptime Kuma Client config not found, skipping initialization")
		return nil
	}

	// We MUST use context.Background() here.
	// The socket.io client uses this context to keep the WebSocket alive permanently.
	client, err := kumaClient.New(context.Background(), cfg.URL, cfg.Username, cfg.Password)
	if err != nil {
		slog.Error("Failed to connect to Uptime Kuma Socket.io", "error", err)
		return nil
	}

	return &Client{
		cfg:     cfg,
		exec:    exec,
		client:  client,
		tracked: make(map[string]bool),
	}
}

func (c *Client) Name() string { return "Uptime Kuma" }

func (c *Client) SyncState() error {
	if c.client == nil {
		return nil
	}
	slog.Info("Syncing existing state from Uptime Kuma...")

	monitors, err := c.client.GetMonitors(context.Background())
	if err != nil {
		return fmt.Errorf("failed to fetch monitors: %w", err)
	}

	for _, m := range monitors {
		var httpMon monitor.HTTP

		if err := m.As(&httpMon); err != nil {
			continue
		}

		if httpMon.URL != "" {
			cacheKey := httpMon.URL + httpMon.Name
			c.tracked[cacheKey] = true
		}
	}

	slog.Info("Uptime Kuma state synchronized", "monitors", len(c.tracked))
	return nil
}

func (c *Client) Process(services []core.Service) error {
	if c.client == nil {
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

		httpMonitor := c.buildHTTPMonitor(svc)

		if httpMonitor.URL == "" || httpMonitor.URL == "https://" {
			continue
		}

		cacheKey := httpMonitor.URL + httpMonitor.Name
		if c.tracked[cacheKey] {
			continue
		}

		err := c.exec.Run("create Uptime Kuma monitor", func() error {
			_, err := c.client.CreateMonitor(context.Background(), httpMonitor)
			return err
		}, "name", httpMonitor.Name, "url", httpMonitor.URL)

		if err == nil {
			c.tracked[cacheKey] = true
		}
	}

	return nil
}

func (c *Client) buildHTTPMonitor(svc core.Service) *monitor.HTTP {
	labels := svc.Labels

	mon := &monitor.HTTP{
		Base: monitor.Base{
			Name:          svc.ContainerName,
			Interval:      60,
			MaxRetries:    3,
			RetryInterval: 60,
			IsActive:      true,
		},
		HTTPDetails: monitor.HTTPDetails{
			Method:              "GET",
			AcceptedStatusCodes: []string{"200-299"},
			IgnoreTLS:           false,
		},
	}

	if len(svc.Hosts) > 0 {
		mon.URL = "https://" + svc.Hosts[0]
	}

	if val := labels["mesh.kuma.name"]; val != "" {
		mon.Name = val
	}
	if val := labels["mesh.kuma.url"]; val != "" {
		mon.URL = val
	}
	if val := labels["mesh.kuma.description"]; val != "" {
		mon.Description = &val
	}
	if val := labels["mesh.kuma.method"]; val != "" {
		mon.Method = strings.ToUpper(val)
	}
	if val := labels["mesh.kuma.body"]; val != "" {
		mon.Body = val
	}
	if val := labels["mesh.kuma.headers"]; val != "" {
		// Kuma expects headers as a JSON string e.g., `{"Authorization": "Bearer token"}`
		mon.Headers = val
	}
	if val := labels["mesh.kuma.basic_auth_user"]; val != "" {
		mon.BasicAuthUser = val
	}
	if val := labels["mesh.kuma.basic_auth_pass"]; val != "" {
		mon.BasicAuthPass = val
	}

	if val := labels["mesh.kuma.ignore_tls"]; val != "" {
		mon.IgnoreTLS = strings.ToLower(val) == "true"
	}
	if val := labels["mesh.kuma.upside_down"]; val != "" {
		mon.UpsideDown = strings.ToLower(val) == "true" // True means "I want this to be DOWN"
	}

	if val := labels["mesh.kuma.interval"]; val != "" {
		if i, err := strconv.ParseInt(val, 10, 64); err == nil {
			mon.Interval = i
		}
	}
	if val := labels["mesh.kuma.retry_interval"]; val != "" {
		if i, err := strconv.ParseInt(val, 10, 64); err == nil {
			mon.RetryInterval = i
		}
	}
	if val := labels["mesh.kuma.max_retries"]; val != "" {
		if i, err := strconv.ParseInt(val, 10, 64); err == nil {
			mon.MaxRetries = i
		}
	}
	if val := labels["mesh.kuma.resend_interval"]; val != "" {
		if i, err := strconv.ParseInt(val, 10, 64); err == nil {
			mon.ResendInterval = i
		}
	}
	if val := labels["mesh.kuma.timeout"]; val != "" {
		if i, err := strconv.ParseInt(val, 10, 64); err == nil {
			mon.Timeout = i
		}
	}
	if val := labels["mesh.kuma.max_redirects"]; val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			mon.MaxRedirects = i
		}
	}

	if val := labels["mesh.kuma.accepted_status_codes"]; val != "" {
		// Allows users to pass "200-299, 401, 403" and cleanly parses it
		rawCodes := strings.Split(val, ",")
		cleanCodes := make([]string, 0, len(rawCodes))
		for _, code := range rawCodes {
			cleanCodes = append(cleanCodes, strings.TrimSpace(code))
		}
		mon.AcceptedStatusCodes = cleanCodes
	}

	return mon
}
