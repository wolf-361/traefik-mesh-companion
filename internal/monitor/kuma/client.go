package kuma

import (
	"context"
	"fmt"
	"log/slog"
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

	// We establish the socket connection during initialization
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
		m.As(&httpMon)

		// If it successfully casted an HTTP monitor, the URL will be populated
		if httpMon.HTTPDetails.URL != "" {
			cacheKey := httpMon.HTTPDetails.URL + httpMon.Name
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

		// Build the native struct provided by the breml package
		httpMonitor := c.buildHTTPMonitor(svc)

		if httpMonitor.URL == "" || httpMonitor.URL == "https://" {
			continue
		}

		cacheKey := httpMonitor.URL + httpMonitor.Name
		if c.tracked[cacheKey] {
			continue // Already exists
		}

		err := c.exec.Run("create Uptime Kuma monitor", func() error {
			// Actually create it via Socket.io
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

	// Setup defaults using the breml package types
	mon := &monitor.HTTP{
		Base: monitor.Base{
			Name:          svc.ContainerName,
			Interval:      60,
			MaxRetries:    3,
			RetryInterval: 60,
		},
		HTTPDetails: monitor.HTTPDetails{
			Method:              "GET",
			AcceptedStatusCodes: []string{"200-299"},
			IgnoreTLS:           false,
		},
	}

	if len(svc.Hosts) > 0 {
		mon.HTTPDetails.URL = "https://" + svc.Hosts[0]
	}

	// Apply label overrides
	if val := labels["mesh.kuma.name"]; val != "" {
		mon.Base.Name = val
	}
	if val := labels["mesh.kuma.url"]; val != "" {
		mon.HTTPDetails.URL = val
	}

	return mon
}
