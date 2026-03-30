package docker

import (
	"context"
	"log"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
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
	log.Println("[Docker] Starting socket watcher...")

	w.SyncAll()

	ticker := time.NewTicker(w.cfg.SyncInterval)
	defer ticker.Stop()

	eventFilter := filters.NewArgs()
	eventFilter.Add("type", "container")
	eventFilter.Add("event", "start")
	eventFilter.Add("event", "die")
	eventFilter.Add("event", "destroy")

	msgs, errs := w.cli.Events(context.Background(), types.EventsOptions{Filters: eventFilter})

	for {
		select {
		case err := <-errs:
			log.Printf("[Docker] Event stream error: %v", err)
			time.Sleep(5 * time.Second)
		case msg := <-msgs:
			log.Printf("[Docker] Container '%s' triggered '%s'. Synchronizing pipelines...", msg.Actor.Attributes["name"], msg.Action)
			w.SyncAll()
		case <-ticker.C:
			w.SyncAll()
		}
	}
}

// SyncAll scans all containers, evaluates Traefik labels, and dispatches to providers if state changed.
func (w *Watcher) SyncAll() {
	containers, err := w.cli.ContainerList(context.Background(), types.ContainerListOptions{All: true})
	if err != nil {
		log.Printf("[Docker] Error listing containers: %v", err)
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
		log.Printf("[Warning] Provider '%s' is enabled but not initialized in registry.", providerName)
		return
	}

	log.Printf("[%s] Synchronizing %d discovered hosts...", strings.ToUpper(providerName), len(hosts))
	if err := prov.Sync(hosts, targetIP); err != nil {
		log.Printf("[%s] Synchronization failed: %v", strings.ToUpper(providerName), err)
	}
}
