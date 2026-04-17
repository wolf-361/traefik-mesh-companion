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
	"github.com/wolf-361/traefik-mesh-companion/internal/traefik"
)

var _ core.Processor = (*Client)(nil)

type Router struct {
	Name string
	Rule string
}

type Client struct {
	cfg             *Config
	exec            *core.Executor
	client          *kumaClient.Client
	statusManager   *StatusPageManager
	tagManager      *TagManager
	coordinator   	*Coordinator

	trackedURLs map[string]int64 
	tracked     map[string]int64

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
		cfg:         cfg,
		exec:        exec,
		trackedURLs: make(map[string]int64),
		tracked:     make(map[string]int64),
	}

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
			// Track globally to prevent duplicate creations
			c.trackedURLs[httpMon.URL] = m.ID
			c.tracked[httpMon.URL+httpMon.Name] = m.ID

			// Extract the tag IDs already attached to this monitor on the server
            var existingTags []int64
            for _, t := range httpMon.Tags {
                existingTags = append(existingTags, t.TagID)
            }
            // Seed the TagManager's cache so it knows they already exist!
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

	cycleURLs := make(map[string]int64)

	for _, svc := range services {
		routers := extractRouters(svc.Labels)

		for _, router := range routers {
			// Status Page Binding
			if bindingSlug := c.getMeshLabel(svc, router.Name, "status_page_binding"); bindingSlug != "" {
				hosts, _ := traefik.ParseRule(router.Rule)
				if len(hosts) > 0 {
					domain := hosts[0]
					if err := c.statusManager.BindDomain(context.Background(), bindingSlug, domain); err != nil {
						slog.Warn("Failed to bind domain", "slug", bindingSlug, "domain", domain, "error", err)
					}
				}
			}

			// Enabled Check
			if !c.isEnabled(svc, router.Name) {
				continue
			}

			// Resolve the URL and build monitor
			monitorURL := c.resolveMonitorURL(svc, router)
			if monitorURL == "" || monitorURL == "https://" {
				continue
			}
			httpMonitor := c.buildHTTPMonitor(svc, router, monitorURL)

			if httpMonitor.URL == "" || httpMonitor.URL == "https://" {
				continue
			}

			var monitorID int64
			var exists bool
			allowDupes := strings.ToLower(c.getMeshLabel(svc, router.Name, "allow_duplicates")) == "true"

			if allowDupes {
				// Fallback to legacy URL+Name tracking
				monitorID, exists = c.tracked[monitorURL+httpMonitor.Name]
			} else {
				// Check global state first, then current cycle state
				monitorID, exists = c.trackedURLs[monitorURL]
				if !exists {
					monitorID, exists = cycleURLs[monitorURL]
				}
			}

			// If it doesn't exist, create it
			if !exists {
				err := c.exec.Run("create Uptime Kuma monitor", func() error {
					createdMon, err := c.client.CreateMonitor(context.Background(), httpMonitor)
					if err != nil {
						return err
					}
					monitorID = createdMon
					
					// Cache it so subsequent routers in this cycle don't recreate it
					c.trackedURLs[monitorURL] = monitorID
					c.tracked[monitorURL+httpMonitor.Name] = monitorID
					cycleURLs[monitorURL] = monitorID
					return nil
				}, "name", httpMonitor.Name)
				
				if err != nil {
					continue
				}
			} else {
				slog.Debug("Monitor already exists for URL, skipping creation", "url", monitorURL)
			}

			// Attach Tags and Status Pages
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

func (c *Client) buildHTTPMonitor(svc core.Service, router Router, resolvedURL string) *monitor.HTTP {
	mon := &monitor.HTTP{
		Base: monitor.Base{
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

	mon.URL = resolvedURL
	if name := c.getMeshLabel(svc, router.Name, "name"); name != "" {
		mon.Name = name
	} else {
		mon.Name = fmt.Sprintf("%s (%s)", svc.ContainerName, router.Name)
	}

	// Advanced configuration mapping
	if val := c.getMeshLabel(svc, router.Name, "description"); val != "" {
        mon.Description = &val
    }
	if val := c.getMeshLabel(svc, router.Name, "method"); val != "" {
		mon.Method = strings.ToUpper(val)
	}
	if val := c.getMeshLabel(svc, router.Name, "body"); val != "" {
		mon.Body = val
	}
	if val := c.getMeshLabel(svc, router.Name, "headers"); val != "" {
		mon.Headers = val
	}
	if val := c.getMeshLabel(svc, router.Name, "basic_auth_user"); val != "" {
		mon.BasicAuthUser = val
	}
	if val := c.getMeshLabel(svc, router.Name, "basic_auth_pass"); val != "" {
		mon.BasicAuthPass = val
	}
	if val := c.getMeshLabel(svc, router.Name, "ignore_tls"); val != "" {
		mon.IgnoreTLS = strings.ToLower(val) == "true"
	}
	if val := c.getMeshLabel(svc, router.Name, "upside_down"); val != "" {
		mon.UpsideDown = strings.ToLower(val) == "true"
	}
	if val := c.getMeshLabel(svc, router.Name, "interval"); val != "" {
		if i, err := strconv.ParseInt(val, 10, 64); err == nil {
			mon.Interval = i
		}
	}
	if val := c.getMeshLabel(svc, router.Name, "retry_interval"); val != "" {
		if i, err := strconv.ParseInt(val, 10, 64); err == nil {
			mon.RetryInterval = i
		}
	}
	if val := c.getMeshLabel(svc, router.Name, "max_retries"); val != "" {
		if i, err := strconv.ParseInt(val, 10, 64); err == nil {
			mon.MaxRetries = i
		}
	}
	if val := c.getMeshLabel(svc, router.Name, "accepted_status_codes"); val != "" {
		rawCodes := strings.Split(val, ",")
		cleanCodes := make([]string, 0, len(rawCodes))
		for _, code := range rawCodes {
			cleanCodes = append(cleanCodes, strings.TrimSpace(code))
		}
		mon.AcceptedStatusCodes = cleanCodes
	}

	return mon
}

// getMeshLabel handles the hierarchy: Router Override > Global Service Label
func (c *Client) getMeshLabel(svc core.Service, routerName string, key string) string {
	if routerName != "default" {
		// Priority 1: Router-specific Kuma key (e.g., mesh.routers.kuma-status.kuma.enable)
		routerKumaKey := fmt.Sprintf("mesh.routers.%s.kuma.%s", routerName, key)
		if val, ok := svc.Labels[routerKumaKey]; ok {
			return val
		}

		// Priority 2: Router-specific Generic key (e.g., mesh.routers.kuma-status.managed)
		routerMeshKey := fmt.Sprintf("mesh.routers.%s.%s", routerName, key)
		if val, ok := svc.Labels[routerMeshKey]; ok {
			return val
		}
	}

	// Priority 3: Global Kuma key (e.g., mesh.kuma.enable)
	globalKumaKey := fmt.Sprintf("mesh.kuma.%s", key)
	if val, ok := svc.Labels[globalKumaKey]; ok {
		return val
	}

	// Lowest Priority: Global Generic key (e.g., mesh.managed)
	globalMeshKey := fmt.Sprintf("mesh.%s", key)
	if val, ok := svc.Labels[globalMeshKey]; ok {
		return val
	}

	return ""
}

// resolveMonitorURL purely handles AST parsing and relative/absolute overrides
func (c *Client) resolveMonitorURL(svc core.Service, router Router) string {
	hosts, paths := traefik.ParseRule(router.Rule)
	basePath := ""
	if len(paths) > 0 {
		basePath = paths[0]
	}
	
	detectedURL := ""
	if len(hosts) > 0 {
		detectedURL = "https://" + hosts[0] + basePath
	} else if len(svc.Hosts) > 0 {
		detectedURL = "https://" + svc.Hosts[0]
	}

	if urlOverride := c.getMeshLabel(svc, router.Name, "url"); urlOverride != "" {
		if strings.HasPrefix(urlOverride, "/") {
			// Relative Override (e.g. "/health")
			return strings.TrimRight(detectedURL, "/") + urlOverride
		}
		// Absolute Override (e.g. "https://api.com")
		return urlOverride
	}
	
	return detectedURL
}

// Check if Kuma is enabled for this router
func (c *Client) isEnabled(svc core.Service, routerName string) bool {
	// Check specific 'enable' first
	if val := c.getMeshLabel(svc, routerName, "enable"); val != "" {
		return strings.ToLower(val) == "true"
	}
	// Fall back to generic 'managed'
	if val := c.getMeshLabel(svc, routerName, "managed"); val != "" {
		return strings.ToLower(val) == "true"
	}
	return c.cfg.AutoEnable
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

// Helper to extract routers dynamically from labels so we don't need to modify core.Service
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
	// If no routers found, create a dummy one so the global processing loop still runs for "worker" containers
	if len(routers) == 0 {
		routers = append(routers, Router{Name: "default", Rule: ""})
	}
	return routers
}