package docker

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/wolf-361/traefik-mesh-companion/internal/config"
	"github.com/wolf-361/traefik-mesh-companion/internal/mesh"
)

type Watcher struct {
	cli        *client.Client
	cfg        *config.Config
	processors []mesh.Processor
	hostRegex  *regexp.Regexp
}

func NewWatcher(cfg *config.Config, processors []mesh.Processor) (*Watcher, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}

	return &Watcher{
		cli:        cli,
		cfg:        cfg,
		processors: processors,
		hostRegex:  regexp.MustCompile(`Host\([` + "`" + `'](.+?)[` + "`" + `']\)`),
	}, nil
}

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
			time.Sleep(5 * time.Second)
		case msg := <-msgs:
			slog.Info("Container event triggered sync", "container", msg.Actor.Attributes["name"], "action", msg.Action)
			w.SyncAll()
		case <-ticker.C:
			w.SyncAll()
		}
	}
}

func (w *Watcher) SyncAll() {
	containers, err := w.cli.ContainerList(context.Background(), container.ListOptions{All: true})
	if err != nil {
		slog.Error("Error listing containers", "error", err)
		return
	}

	var services []mesh.Service

	for _, c := range containers {
		if c.Labels["traefik.enable"] != "true" {
			continue
		}

		svc := mesh.Service{
			ContainerName: strings.TrimPrefix(c.Names[0], "/"),
			Labels:        c.Labels,
		}

		for key, val := range c.Labels {
			if strings.HasPrefix(key, "traefik.http.routers.") && strings.HasSuffix(key, ".rule") {
				matches := w.hostRegex.FindAllStringSubmatch(val, -1)
				for _, match := range matches {
					if len(match) > 1 {
						for _, domain := range strings.Split(match[1], ",") {
							cleanDomain := strings.Trim(strings.TrimSpace(domain), "`'\"")
							if cleanDomain != "" {
								svc.Hosts = append(svc.Hosts, cleanDomain)
							}
						}
					}
				}
			}
		}
		services = append(services, svc)
	}

	for _, p := range w.processors {
		if err := p.Process(services); err != nil {
			slog.Error("Processor error", "processor", p.Name(), "error", err)
		}
	}
}
