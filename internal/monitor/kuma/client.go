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
	"github.com/breml/go-uptime-kuma-client/statuspage"
	"github.com/wolf-361/traefik-mesh-companion/internal/core"
)

var _ core.Processor = (*Client)(nil)

type Client struct {
	cfg             *Config
	exec            *core.Executor
	client          *kumaClient.Client
	mu              sync.Mutex // Protects the client during reconnection
	tracked         map[string]bool
	pageGroupsCache map[string][]statuspage.PublicGroup
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
		tracked:         make(map[string]bool),
		pageGroupsCache: make(map[string][]statuspage.PublicGroup),
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
			createdMon, err := c.client.CreateMonitor(context.Background(), httpMonitor)
			if err != nil {
				// If the error looks like a network drop, flag for reset
				if strings.Contains(err.Error(), "closed network connection") {
					c.resetClient()
				}
				return err
			}

			c.handleStatusPages(context.Background(), createdMon, httpMonitor.Name, svc.Labels)

			return nil
		}, "name", httpMonitor.Name, "url", httpMonitor.URL)

		if err == nil {
			c.tracked[cacheKey] = true
		}
	}

	return nil
}

// handleStatusPages routes the newly created monitor to the correct public dashboards
func (c *Client) handleStatusPages(ctx context.Context, monitorID int64, monitorName string, labels map[string]string) {
	if strings.ToLower(labels["mesh.kuma.hide_status"]) == "true" {
		slog.Debug("Monitor flagged as hidden, skipping status page attachment", "monitor", monitorName)
		return
	}

	pagesToAttach := make(map[string]bool)

	// Global default
	if c.cfg.GlobalStatusPageSlug != "none" && c.cfg.GlobalStatusPageSlug != "" {
		pagesToAttach[c.cfg.GlobalStatusPageSlug] = true
	}

	// Added status pages
	if val, exists := labels["mesh.kuma.pages"]; exists {
		for _, p := range strings.Split(val, ",") {
			if cleanPage := strings.TrimSpace(p); cleanPage != "" {
				pagesToAttach[cleanPage] = true
			}
		}
	}

	if len(pagesToAttach) == 0 {
		return
	}

	// Determine the target group from labels (Default to "Services")
	targetGroupName := "Services"
	if val, exists := labels["mesh.kuma.group"]; exists && strings.TrimSpace(val) != "" {
		targetGroupName = strings.TrimSpace(val)
	}

	for pageSlug := range pagesToAttach {
		slog.Debug("Processing status page attachment", "monitor", monitorName, "page", pageSlug)

		// Fetch the specific status page by slug
		page, err := c.client.GetStatusPage(ctx, pageSlug)
		if err != nil {
			title := formatTitleFromSlug(pageSlug)
			slog.Info("Status page not found, attempting to create new one", "slug", pageSlug, "title", title)

			err = c.client.AddStatusPage(ctx, title, pageSlug)
			if err != nil {
				slog.Error("Failed to auto-create status page", "slug", pageSlug, "error", err)
				continue
			}

			// Fetch the newly created page to get the base StatusPage struct
			page, err = c.client.GetStatusPage(ctx, pageSlug)
			if err != nil {
				slog.Error("Failed to retrieve status page after creation", "slug", pageSlug, "error", err)
				continue
			}
		}

		if page == nil {
			continue
		}

		// Hydrate the missing PublicGroupList from our local cache
		// (Because GetStatusPage always returns this empty)
		if cachedGroups, exists := c.pageGroupsCache[pageSlug]; exists {
			page.PublicGroupList = cachedGroups
		} else {
			// If it's a brand new page or not in cache, initialize the default group
			page.PublicGroupList = []statuspage.PublicGroup{
				{
					Name:   "Services",
					Weight: 1,
				},
			}
		}

		// Find the requested group by name, or create it if it doesn't exist
		targetGroupIdx := -1
		for i, g := range page.PublicGroupList {
			// Case-insensitive match (so "databases" matches "Databases")
			if strings.EqualFold(g.Name, targetGroupName) {
				targetGroupIdx = i
				break
			}
		}

		if targetGroupIdx == -1 {
			slog.Info("Group not found on status page, creating it", "group", targetGroupName, "page", pageSlug)
			newGroup := statuspage.PublicGroup{
				Name:   targetGroupName,
				Weight: len(page.PublicGroupList) + 1, // Dynamically set weight to put it at the bottom
			}
			page.PublicGroupList = append(page.PublicGroupList, newGroup)
			targetGroupIdx = len(page.PublicGroupList) - 1
		}

		// Get a pointer to the specific group we are targeting
		targetGroup := &page.PublicGroupList[targetGroupIdx]

		// Check if the monitor is already in the group to prevent duplicates
		alreadyAttached := false
		for _, existingMon := range targetGroup.MonitorList {
			if existingMon.ID == monitorID {
				alreadyAttached = true
				break
			}
		}

		if !alreadyAttached {
			slog.Info("Attaching monitor to status page", "monitor", monitorName, "page", pageSlug)

			// Using the exact struct from the docs
			targetGroup.MonitorList = append(targetGroup.MonitorList, statuspage.PublicMonitor{
				ID: monitorID,
			})

			// Save updates Kuma AND returns the actual group list with IDs
			updatedGroups, err := c.client.SaveStatusPage(ctx, page)
			if err != nil {
				slog.Error("Failed to save monitor to status page", "page", pageSlug, "error", err)
			} else {
				// Update the local cache so the next container doesn't wipe this one
				c.pageGroupsCache[pageSlug] = updatedGroups
			}
		}
	}
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
