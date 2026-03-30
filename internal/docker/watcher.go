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

// SyncAll scans all containers, evaluates Traefik labels, and dispatches to providers if state changed.
func (w *Watcher) SyncAll() {
	containers, err := w.cli.ContainerList(context.Background(), container.ListOptions{All: true})
	if err != nil {
		slog.Error("Error listing containers", "error", err)
		return
	}

	internalHosts := make(map[string]bool)
	externalHosts := make(map[string]bool)

	for _, c := range containers {
		if c.Labels["traefik.enable"] != "true" {
			continue
		}

		internalRouters := make(map[string]bool)
		externalRouters := make(map[string]bool)

		for key, val := range c.Labels {
			if w.cfg.Internal.Enabled && w.internalRegex != nil {
				matches := w.internalRegex.FindStringSubmatch(key)
				if len(matches) > 1 && strings.Contains(val, w.cfg.Internal.FilterValue) {
					internalRouters[matches[1]] = true
				}
			}

			if w.cfg.External.Enabled && w.externalRegex != nil {
				matches := w.externalRegex.FindStringSubmatch(key)
				if len(matches) > 1 && strings.Contains(val, w.cfg.External.FilterValue) {
					externalRouters[matches[1]] = true
				}
			}
		}

		for key, val := range c.Labels {
			for routerName := range internalRouters {
				if key == "traefik.http.routers."+routerName+".rule" && w.hostRegex.MatchString(val) {
					w.extractDomains(val, w.hostRegex, internalHosts)
				}
			}
			for routerName := range externalRouters {
				if key == "traefik.http.routers."+routerName+".rule" && w.hostRegex.MatchString(val) {
					w.extractDomains(val, w.hostRegex, externalHosts)
				}
			}
		}
	}

	// Dispatch ONLY if the maps have changed since the last run
	if w.cfg.Internal.Enabled && !reflect.DeepEqual(w.lastInternalHosts, internalHosts) {
		w.dispatch(w.cfg.Internal.Provider, internalHosts, w.cfg.Netbird.Target)
		w.lastInternalHosts = internalHosts
	}

	if w.cfg.External.Enabled && !reflect.DeepEqual(w.lastExternalHosts, externalHosts) {
		w.dispatch(w.cfg.External.Provider, externalHosts, w.cfg.Cloudflare.Target)
		w.lastExternalHosts = externalHosts
	}
}

// extractDomains parses comma-separated domains from a Traefik Host() rule and adds them to the target map.
func (w *Watcher) extractDomains(rule string, regex *regexp.Regexp, targetMap map[string]bool) {
	matches := regex.FindStringSubmatch(rule)
	if len(matches) > 1 {
		domains := strings.Split(matches[1], ",")
		for _, domain := range domains {
			targetMap[strings.TrimSpace(domain)] = true
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
