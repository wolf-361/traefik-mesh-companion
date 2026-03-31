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

	internalRegex *regexp.Regexp
	externalRegex *regexp.Regexp
	hostRegex     *regexp.Regexp

	// State caching to prevent API spam
	lastInternalHosts   map[string]bool
	lastIgnoredInternal map[string]bool
	lastExternalHosts   map[string]bool
	lastIgnoredExternal map[string]bool
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

	// Helper to safely compile the wildcard label into a regex capture group
	compileFilter := func(label string) *regexp.Regexp {
		escaped := regexp.QuoteMeta(label) // Escapes all the dots safely
		pattern := "^" + strings.ReplaceAll(escaped, "\\*", "([^.]+)") + "$"
		return regexp.MustCompile(pattern)
	}

	if cfg.Internal.Enabled && cfg.Internal.FilterLabel != "" {
		w.internalRegex = compileFilter(cfg.Internal.FilterLabel)
	}
	if cfg.External.Enabled && cfg.External.FilterLabel != "" {
		w.externalRegex = compileFilter(cfg.External.FilterLabel)
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
	ignoredInternal := make(map[string]bool)
	externalHosts := make(map[string]bool)
	ignoredExternal := make(map[string]bool)

	for _, c := range containers {
		if c.Labels["traefik.enable"] != "true" {
			continue
		}

		type routerData struct {
			rule              string
			managed           string
			internalFilterVal string
			externalFilterVal string
		}
		routers := make(map[string]*routerData)

		getRouter := func(name string) *routerData {
			if _, exists := routers[name]; !exists {
				routers[name] = &routerData{managed: "true"}
			}
			return routers[name]
		}

		for key, val := range c.Labels {
			if strings.HasPrefix(key, "traefik.http.routers.") && strings.HasSuffix(key, ".rule") {
				name := strings.TrimSuffix(strings.TrimPrefix(key, "traefik.http.routers."), ".rule")
				getRouter(name).rule = val
			}
			if strings.HasPrefix(key, "traefik.http.routers.") && strings.HasSuffix(key, ".mesh.managed") {
				name := strings.TrimSuffix(strings.TrimPrefix(key, "traefik.http.routers."), ".mesh.managed")
				getRouter(name).managed = val
			}
			if w.cfg.Internal.Enabled && w.internalRegex != nil {
				if matches := w.internalRegex.FindStringSubmatch(key); len(matches) > 1 {
					getRouter(matches[1]).internalFilterVal = val
				}
			}
			if w.cfg.External.Enabled && w.externalRegex != nil {
				if matches := w.externalRegex.FindStringSubmatch(key); len(matches) > 1 {
					getRouter(matches[1]).externalFilterVal = val
				}
			}
		}

		for _, data := range routers {
			if data.rule == "" {
				continue
			}

			isManaged := data.managed != "false"

			// Use the new matchFilter helper for both internal and external evaluations
			if w.cfg.Internal.Enabled && matchFilter(data.internalFilterVal, w.cfg.Internal.FilterValue) {
				if isManaged {
					w.extractDomains(data.rule, internalHosts)
				} else {
					w.extractDomains(data.rule, ignoredInternal)
				}
			}

			if w.cfg.External.Enabled && matchFilter(data.externalFilterVal, w.cfg.External.FilterValue) {
				if isManaged {
					w.extractDomains(data.rule, externalHosts)
				} else {
					w.extractDomains(data.rule, ignoredExternal)
				}
			}
		}
	}

	// Dispatch ONLY if state changed to prevent API rate limiting
	internalChanged := !reflect.DeepEqual(w.lastInternalHosts, internalHosts) || !reflect.DeepEqual(w.lastIgnoredInternal, ignoredInternal)
	if w.cfg.Internal.Enabled && internalChanged {
		w.dispatch(w.cfg.Internal.Provider, internalHosts, ignoredInternal, w.cfg.Netbird.Target, w.cfg.Internal.Cleanup)
		w.lastInternalHosts = internalHosts
		w.lastIgnoredInternal = ignoredInternal
	}

	externalChanged := !reflect.DeepEqual(w.lastExternalHosts, externalHosts) || !reflect.DeepEqual(w.lastIgnoredExternal, ignoredExternal)
	if w.cfg.External.Enabled && externalChanged {
		w.dispatch(w.cfg.External.Provider, externalHosts, ignoredExternal, w.cfg.Cloudflare.Target, w.cfg.External.Cleanup)
		w.lastExternalHosts = externalHosts
		w.lastIgnoredExternal = ignoredExternal
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

// matchFilter checks if a Traefik label contains ANY of the comma-separated filter values
func matchFilter(labelValue string, envFilter string) bool {
	for _, f := range strings.Split(envFilter, ",") {
		cleanFilter := strings.TrimSpace(f)
		if cleanFilter != "" && strings.Contains(labelValue, cleanFilter) {
			return true
		}
	}
	return false
}

// dispatch sends the mapped hosts to the appropriate DNS provider for synchronization.
func (w *Watcher) dispatch(providerName string, hosts map[string]bool, ignoredHosts map[string]bool, targetIP string, cleanup bool) {
	prov, exists := w.providers[providerName]
	if !exists {
		slog.Warn("Provider is enabled but not initialized in registry", "provider", providerName)
		return
	}

	slog.Info("Synchronizing discovered hosts", "provider", providerName, "active", len(hosts), "ignored", len(ignoredHosts))
	if err := prov.Sync(hosts, ignoredHosts, targetIP, cleanup); err != nil {
		slog.Error("Synchronization failed", "provider", providerName, "error", err)
	}
}