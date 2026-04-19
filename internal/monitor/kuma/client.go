package kuma

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	kumaClient "github.com/breml/go-uptime-kuma-client"
	"github.com/breml/go-uptime-kuma-client/monitor"
	"github.com/wolf-361/traefik-mesh-companion/internal/core"
	"github.com/wolf-361/traefik-mesh-companion/internal/traefik"
)

var _ core.Processor = (*Client)(nil)

type Client struct {
	cfg           *Config
	exec          *core.Executor
	client        *kumaClient.Client
	statusManager *StatusPageManager
	tagManager    *TagManager
	coordinator   *Coordinator

	trackedURLs map[string]int64
	tracked     map[string]int64

	mu         sync.Mutex
	connecting bool
	synced     bool
}

func New(exec *core.Executor) *Client {
	cfg := LoadConfig()
	if cfg == nil {
		slog.Debug("Uptime Kuma Client config not found, skipping initialization")
		return nil
	}

	c := &Client{
		cfg:         cfg,
		exec:        exec,
		trackedURLs: make(map[string]int64),
		tracked:     make(map[string]int64),
	}

	_ = c.ensureConnected()
	return c
}

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
		c.synced = false
		slog.Info("Successfully connected to Uptime Kuma")
	}()

	return fmt.Errorf("connection started in background")
}

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
		return nil
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
			dedupeKey := httpMon.URL + httpMon.Name
			c.trackedURLs[dedupeKey] = m.ID
			c.tracked[dedupeKey] = m.ID

			var existingTags []int64
			for _, t := range httpMon.Tags {
				existingTags = append(existingTags, t.TagID)
			}
			c.tagManager.SeedMonitorTags(m.ID, existingTags)
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

	cycleURLs := make(map[string]int64)

	for _, svc := range services {
		routers := extractRouters(svc.Labels)

		for _, router := range routers {
			if bindingSlug := getMeshLabel(svc, router.Name, "status_page_binding"); bindingSlug != "" {
				hosts, _ := traefik.ParseRule(router.Rule)
				if len(hosts) > 0 {
					domain := hosts[0]
					if err := c.statusManager.BindDomain(context.Background(), bindingSlug, domain); err != nil {
						slog.Warn("Failed to bind domain", "slug", bindingSlug, "domain", domain, "error", err)
					}
				}
			}

			if !isEnabled(svc, router.Name, c.cfg.AutoEnable) {
				continue
			}

			monitorURL := resolveMonitorURL(svc, router)
			if monitorURL == "" || monitorURL == "https://" {
				continue
			}

			httpMonitor := buildHTTPMonitor(c.cfg, svc, router, monitorURL)
			if httpMonitor.URL == "" || httpMonitor.URL == "https://" {
				continue
			}

			var monitorID int64
			var exists bool

			dedupeKey := monitorURL + httpMonitor.Name

			if strings.ToLower(getMeshLabel(svc, router.Name, "allow_duplicates")) == "true" {
				monitorID, exists = c.tracked[dedupeKey]
			} else {
				monitorID, exists = c.trackedURLs[dedupeKey]
				if !exists {
					monitorID, exists = cycleURLs[dedupeKey]
				}
			}

			if !exists {
				err := c.exec.Run("create Uptime Kuma monitor", func() error {
					createdMon, err := c.client.CreateMonitor(context.Background(), httpMonitor)
					if err != nil {
						return err
					}
					monitorID = createdMon
					c.trackedURLs[dedupeKey] = monitorID
					cycleURLs[dedupeKey] = monitorID
					return nil
				}, "name", httpMonitor.Name)

				if err != nil {
					continue
				}
			} else {
				slog.Debug("Monitor already exists for URL, skipping creation", "url", monitorURL)
			}

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
	}
	return nil
}
