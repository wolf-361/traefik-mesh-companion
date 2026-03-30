package docker

import (
	"context"
	"log/slog"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/wolf-361/traefik-mesh-companion/internal/config"
	"github.com/wolf-361/traefik-mesh-companion/internal/provider"
)

// Watcher manages the Docker socket connection and orchestrates the dual-pipeline synchronization.
type Watcher struct {
	cli       *client.Client
	cfg       *config.Config
	providers map[string]provider.DNSProvider

	// Compiled Regexes
	internalRegex *regexp.Regexp
	externalRegex *regexp.Regexp
	hostRegex     *regexp.Regexp // Extracted from SyncAll for performance

	// State caching to prevent API spam
	lastInternalHosts map[string]bool
	lastExternalHosts map[string]bool
}

// NewWatcher initializes the Docker client and compiles the Traefik label regex filters.
func NewWatcher(cfg *config.Config, providers map[string]provider.DNSProvider) (*Watcher, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}

	w := &Watcher{
		cli:       cli,
		cfg:       cfg,
		providers: providers,
		hostRegex: regexp.MustCompile(`Host\([` + "`" + `'](.+?)[` + "`" + `']\)`),
	}

	if cfg.Internal.Enabled {
		regexStr := "^" + strings.ReplaceAll(cfg.Internal.FilterLabel, "*", "([^.]+)") + "$"
		w.internalRegex = regexp.MustCompile(regexStr)
	}

	if cfg.External.Enabled {
		regexStr := "^" + strings.ReplaceAll(cfg.External.FilterLabel, "*", "([^.]+)") + "$"
		w.externalRegex = regexp.MustCompile(regexStr)
	}

	return w, nil
}

// Start begins listening to the Docker event stream and triggers background syncs.
func (w *Watcher) Start() {
	slog.Info("Starting Docker socket watcher...")

	w.SyncAll()

	ticker := time.NewTicker(w.cfg.SyncInterval)
	defer ticker.Stop()

	eventFilter := filters.NewArgs()
	eventFilter.Add("type", "container")
	eventFilter.Add("event", "start")
	eventFilter.Add("event", "die")
	eventFilter.Add("event", "destroy")

	msgs, errs := w.cli.Events(context.Background(), events.ListOptions{Filters: eventFilter})

	for {
		select {
		case err := <-errs:
			slog.Error("Docker event stream error", "error", err)
			time.Sleep(5 * time.Second) // Backoff
		case msg := <-msgs:
			slog.Info("Container event triggered sync",
				"container", msg.Actor.Attributes["name"],
				"action", msg.Action,
			)
			w.SyncAll()
		case <-ticker.C:
			slog.Debug("Running scheduled background synchronization...")
			w.SyncAll()
		}
	}
}

// SyncAll scans all containers, groups labels by router, and evaluates them strictly.
func (w *Watcher) SyncAll() {
	containers, err := w.cli.ContainerList(context.Background(), container.ListOptions{All: true})
	if err != nil {
		slog.Error("Error listing containers", "error", err)
		return
	}

	internalHosts := make(map[string]bool)
	externalHosts := make(map[string]bool)

	for _, c := range containers {
		// Only process containers explicitly enabled for Traefik
		if c.Labels["traefik.enable"] != "true" {
			continue
		}

		// Step 1: Group labels by Router Name for this specific container.
		// This prevents "Router A" labels from affecting "Router B" sync logic.
		type routerData struct {
			rule        string
			filterValue string
		}
		routers := make(map[string]*routerData)

		for key, val := range c.Labels {
			if !strings.HasPrefix(key, "traefik.http.routers.") {
				continue
			}

			// Extract router name and the specific property (e.g., "rule" or "entrypoints")
			parts := strings.Split(strings.TrimPrefix(key, "traefik.http.routers."), ".")
			if len(parts) < 2 {
				continue
			}
			name := parts[0]
			property := parts[1]

			if _, exists := routers[name]; !exists {
				routers[name] = &routerData{}
			}

			if property == "rule" {
				routers[name].rule = val
			}

			// Check if this label is our designated filter label (e.g. "entrypoints")
			if w.cfg.Internal.Enabled && w.internalRegex.MatchString(key) {
				routers[name].filterValue = val
			} else if w.cfg.External.Enabled && w.externalRegex.MatchString(key) {
				// We prioritize the external filter if the labels match both for some reason
				routers[name].filterValue = val
			}
		}

		// Step 2: Evaluate each router independently
		for _, data := range routers {
			if data.rule == "" {
				continue
			}

			// Internal Sync Check
			if w.cfg.Internal.Enabled && strings.Contains(data.filterValue, w.cfg.Internal.FilterValue) {
				w.extractDomains(data.rule, internalHosts)
			}

			// External Sync Check
			// Note: If Coolify has 'internal' and your filter is 'https', this will now correctly return false.
			if w.cfg.External.Enabled && strings.Contains(data.filterValue, w.cfg.External.FilterValue) {
				w.extractDomains(data.rule, externalHosts)
			}
		}
	}

	// Step 3: Dispatch ONLY if state changed to prevent API rate limiting
	if w.cfg.Internal.Enabled && !reflect.DeepEqual(w.lastInternalHosts, internalHosts) {
		w.dispatch(w.cfg.Internal.Provider, internalHosts, w.cfg.Netbird.Target)
		w.lastInternalHosts = internalHosts
	}

	if w.cfg.External.Enabled && !reflect.DeepEqual(w.lastExternalHosts, externalHosts) {
		w.dispatch(w.cfg.External.Provider, externalHosts, w.cfg.Cloudflare.Target)
		w.lastExternalHosts = externalHosts
	}
}

// extractDomains uses FindAllStringSubmatch to catch ALL domains in a rule like Host(`a.com`, `b.com`)
func (w *Watcher) extractDomains(rule string, targetMap map[string]bool) {
	matches := w.hostRegex.FindAllStringSubmatch(rule, -1)
	for _, match := range matches {
		if len(match) > 1 {
			// Split by comma in case of Host(`a.com, b.com`)
			domains := strings.Split(match[1], ",")
			for _, domain := range domains {
				cleanDomain := strings.Trim(strings.TrimSpace(domain), "`'\"")
				if cleanDomain != "" {
					targetMap[cleanDomain] = true
				}
			}
		}
	}
}

// dispatch sends the mapped hosts to the appropriate DNS provider for synchronization.
func (w *Watcher) dispatch(providerName string, hosts map[string]bool, targetIP string) {
	prov, exists := w.providers[providerName]
	if !exists {
		slog.Warn("Provider is enabled but not initialized in registry", "provider", providerName)
		return
	}

	slog.Info("Synchronizing discovered hosts", "provider", providerName, "host_count", len(hosts))
	if err := prov.Sync(hosts, targetIP); err != nil {
		slog.Error("Synchronization failed", "provider", providerName, "error", err)
	}
}
