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
	cfg             *Config
	exec            *core.Executor
	client          *kumaClient.Client
	statusManager   *StatusPageManager
	tagManager      *TagManager
	coordinator   	*Coordinator
	tracked       	map[string]int64
	mu              sync.Mutex // Protects the client during reconnection
	connecting    bool       // Guard to prevent spawning multiple connection attempts
	synced        bool       // Tracks if we need to perform a lazy state sync
}

func New(exec *core.Executor) *Client {
	cfg := LoadConfig()
	if cfg == nil {
		slog.Debug("Uptime Kuma Client config not found, skipping initialization")
		return nil
	}

	c := &Client{
		cfg:             cfg,
		exec:            exec,
		tracked: make(map[string]int64),
	}

	// Trigger background connection immediately
	_ = c.ensureConnected()

	return c
}

// ensureConnected checks if we have an active client or creates a new one asynchronously
func (c *Client) ensureConnected() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client != nil {
		return nil
	}

	if c.connecting {
		return fmt.Errorf("connection currently initializing in background")
	}

	c.connecting = true

	slog.Debug("Connecting to Uptime Kuma Socket.io...", "url", c.cfg.URL)

	go func() {
		// Socket.io handshake can deadlock if the target is a 404 or bad gateway.
		// Running this in a goroutine protects the main DNS sync loops.
		client, err := kumaClient.New(context.Background(), c.cfg.URL, c.cfg.Username, c.cfg.Password)

		c.mu.Lock()
		defer c.mu.Unlock()
		c.connecting = false

		if err != nil {
			slog.Warn("Background Uptime Kuma connection failed, will retry next cycle", "error", err)
			return
		}

		c.client = client
		c.statusManager = NewStatusPageManager(client, c.cfg)
		c.coordinator = NewCoordinator(c.cfg, c.statusManager)
		c.tagManager = NewTagManager(client, c.cfg)
		c.synced = false // Force a sync on the next process cycle
		slog.Info("Successfully connected to Uptime Kuma")
	}()

	return fmt.Errorf("connection started in background")
}

// resetClient is called when we detect the connection is dead
func (c *Client) resetClient() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.client = nil
	c.statusManager = nil
	c.coordinator = nil
	c.tagManager = nil
	c.synced = false
}

func (c *Client) Name() string { return "Uptime Kuma" }

func (c *Client) SyncState() error {
	if err := c.ensureConnected(); err != nil {
		slog.Debug("Uptime Kuma connection not ready during initial sync. Will lazy-sync later.", "status", err)
		return nil // Don't return an error to prevent blocking the main startup loop
	}

	return c.doSync()
}

func (c *Client) doSync() error {
	slog.Info("Syncing existing state from Uptime Kuma...")
	monitors, err := c.client.GetMonitors(context.Background())
	if err != nil {
		c.resetClient()
		return fmt.Errorf("failed to fetch monitors: %w", err)
	}

	for _, m := range monitors {
		var httpMon monitor.HTTP
		if err := m.As(&httpMon); err == nil && httpMon.URL != "" {
			c.tracked[httpMon.URL+httpMon.Name] = m.ID
		}
	}

	if err := c.statusManager.SyncState(context.Background()); err != nil {
		slog.Warn("Failed to sync status page state, UI groupings might be inconsistent", "error", err)
	}

	if err := c.tagManager.SyncState(context.Background()); err != nil {
		slog.Warn("Failed to sync tags", "error", err)
	}

	c.mu.Lock()
	c.synced = true
	c.mu.Unlock()

	slog.Info("Uptime Kuma state synchronized", "monitors", len(c.tracked))
	return nil
}

func (c *Client) Process(services []core.Service) error {
	// We call ensureConnected at the start of every process cycle
	if err := c.ensureConnected(); err != nil {
		return nil
	}

	c.mu.Lock()
	needsSync := !c.synced
	c.mu.Unlock()

	if needsSync {
		if err := c.doSync(); err != nil {
			slog.Error("Failed to perform lazy sync", "error", err)
			return nil
		}
	}

	for _, svc := range services {
        if !c.isEnabled(svc) {
            continue
        }

        httpMonitor := c.buildHTTPMonitor(svc)
		if httpMonitor.URL == "" || httpMonitor.URL == "https://" {
			continue
		}

        cacheKey := httpMonitor.URL + httpMonitor.Name
		monitorID, exists := c.tracked[cacheKey]

        // If it doesn't exist, create it
        if !exists {
            err := c.exec.Run("create Uptime Kuma monitor", func() error {
                createdMon, err := c.client.CreateMonitor(context.Background(), httpMonitor)
                if err != nil {
                    return err
                }
                monitorID = createdMon
				c.tracked[cacheKey] = monitorID
                return nil
            }, "name", httpMonitor.Name)
            if err != nil {
                continue
            }
        }

        // If it's already there, the manager will just skip it.
        if monitorID != 0 {
			c.tagManager.ProcessTags(context.Background(), monitorID, svc.Labels)
			
            c.coordinator.RequestAttach(AttachPayload{
				MonitorID:   monitorID,
				MonitorName: httpMonitor.Name,
				Hosts:       svc.Hosts,
				Labels:      svc.Labels,
			})
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

	// Basic params
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

	// Advanced 
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

// Check if Kuma is enabled
func (c *Client) isEnabled(svc core.Service) bool {
	enabled := c.cfg.AutoEnable
	if val, ok := svc.Labels["mesh.kuma.enable"]; ok {
		enabled = strings.ToLower(val) == "true"
	}
	return enabled
}

// Helper to format slugs
func formatTitleFromSlug(slug string) string {
	parts := strings.Split(slug, "-")
	for i, part := range parts {
		if len(part) > 0 {
			parts[i] = strings.ToUpper(part[:1]) + part[1:]
		}
	}
	return strings.Join(parts, " ")
}