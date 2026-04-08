package kuma

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"

	kumaClient "github.com/breml/go-uptime-kuma-client"
	"github.com/breml/go-uptime-kuma-client/monitor"
	"github.com/wolf-361/traefik-mesh-companion/internal/core"
)

var _ core.Processor = (*Client)(nil)

type Client struct {
	cfg     *Config
	exec    *core.Executor
	client  *kumaClient.Client
	mu      sync.Mutex // Protects the client during reconnection
	tracked map[string]bool
}

func New(exec *core.Executor) *Client {
	cfg := LoadConfig()
	if cfg == nil {
		slog.Debug("Uptime Kuma Client config not found, skipping initialization")
		return nil
	}

	c := &Client{
		cfg:     cfg,
		exec:    exec,
		tracked: make(map[string]bool),
	}

	// Try initial connection, but don't kill the app if it fails (lazy load)
	if err := c.ensureConnected(); err != nil {
		slog.Warn("Initial Uptime Kuma connection failed, will retry on next sync", "error", err)
	}

	return c
}

// ensureConnected checks if we have an active client or creates a new one
func (c *Client) ensureConnected() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client != nil {
		return nil
	}

	slog.Debug("Connecting to Uptime Kuma Socket.io...", "url", c.cfg.URL)

	// Use Background context so the WebSocket lifecycle matches the app
	client, err := kumaClient.New(context.Background(), c.cfg.URL, c.cfg.Username, c.cfg.Password)
	if err != nil {
		return err
	}

	c.client = client
	return nil
}

// resetClient is called when we detect the connection is dead
func (c *Client) resetClient() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.client = nil
}

func (c *Client) Name() string { return "Uptime Kuma" }

func (c *Client) SyncState() error {
	if err := c.ensureConnected(); err != nil {
		return fmt.Errorf("sync failed: could not connect: %w", err)
	}

	slog.Info("Syncing existing state from Uptime Kuma...")

	monitors, err := c.client.GetMonitors(context.Background())
	if err != nil {
		c.resetClient() // Connection likely died
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
	// We call ensureConnected at the start of every process cycle
	if err := c.ensureConnected(); err != nil {
		slog.Error("Uptime Kuma disconnected, skipping process cycle", "error", err)
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
			if err := c.ensureConnected(); err != nil {
				return err
			}
			_, err := c.client.CreateMonitor(context.Background(), httpMonitor)
			if err != nil {
				// If the error looks like a network drop, flag for reset
				if strings.Contains(err.Error(), "closed network connection") {
					c.resetClient()
				}
				return err
			}
			return nil
		}, "name", httpMonitor.Name, "url", httpMonitor.URL)

		if err == nil {
			c.tracked[cacheKey] = true
		}
	}

	return nil
}

func (c *Client) buildHTTPMonitor(svc core.Service) *monitor.HTTP {
	labels := svc.Labels

	// Initialize with Global Defaults from Config
	mon := &monitor.HTTP{
		Base: monitor.Base{
			Name:          svc.ContainerName,
			Interval:      c.cfg.DefaultInterval,
			MaxRetries:    c.cfg.DefaultMaxRetries,
			RetryInterval: c.cfg.DefaultRetryInterval,
			IsActive:      true,
		},
		HTTPDetails: monitor.HTTPDetails{
			Method:              "GET",
			AcceptedStatusCodes: c.cfg.DefaultAcceptedStatusCodes,
			MaxRedirects:        c.cfg.DefaultMaxRedirects,
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
		mon.UpsideDown = strings.ToLower(val) == "true"
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
		rawCodes := strings.Split(val, ",")
		cleanCodes := make([]string, 0, len(rawCodes))
		for _, code := range rawCodes {
			cleanCodes = append(cleanCodes, strings.TrimSpace(code))
		}
		mon.AcceptedStatusCodes = cleanCodes
	}

	return mon
}
