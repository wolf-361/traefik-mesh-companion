package watcher

import (
	"context"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"github.com/wolf-361/traefik-mesh-companion/internal/config"
	"github.com/wolf-361/traefik-mesh-companion/internal/core"
	"github.com/wolf-361/traefik-mesh-companion/internal/traefik"
)

// DockerClient is an interface that allows us to mock the Docker socket during testing
type DockerClient interface {
	ContainerList(ctx context.Context, options container.ListOptions) ([]container.Summary, error)
	Events(ctx context.Context, options events.ListOptions) (<-chan events.Message, <-chan error)
}

type Watcher struct {
	cli        DockerClient
	cfg        *config.Config
	processors []core.Processor
	isHealthy  *atomic.Bool
}

func NewWatcher(cfg *config.Config, processors []core.Processor, isHealthy *atomic.Bool) (*Watcher, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}

	return &Watcher{
		cli:        cli,
		cfg:        cfg,
		processors: processors,
		isHealthy:  isHealthy,
	}, nil
}

func (w *Watcher) Start(ctx context.Context) {
	slog.Info("Starting Docker socket watcher...")
	w.SyncAll()

	ticker := time.NewTicker(w.cfg.SyncInterval)
	defer ticker.Stop()

	eventFilter := filters.NewArgs()
	eventFilter.Add("type", "container")
	eventFilter.Add("event", "start")
	eventFilter.Add("event", "die")
	eventFilter.Add("event", "destroy")

	msgs, errs := w.cli.Events(ctx, events.ListOptions{Filters: eventFilter})

	for {
		select {
		case <-ctx.Done(): // <-- The Kill Switch for tests and graceful shutdowns!
			slog.Info("Stopping Docker watcher gracefully")
			return
		case err := <-errs:
			if err != nil {
				slog.Error("Docker event stream error", "error", err)
				w.isHealthy.Store(false)
				time.Sleep(1 * time.Second) // Prevent log spam
			}
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
		w.isHealthy.Store(false)
		return
	}

	// If we successfully communicated with Docker, we are healthy!
	w.isHealthy.Store(true)

	var services []core.Service

	for _, c := range containers {
		if c.Labels["traefik.enable"] != "true" {
			continue
		}

		svc := core.Service{
			// Safely handle missing names
			ContainerName: "unknown",
			Labels:        c.Labels,
		}
		if len(c.Names) > 0 {
			svc.ContainerName = strings.TrimPrefix(c.Names[0], "/")
		}

		for key, val := range c.Labels {
			if strings.HasPrefix(key, "traefik.http.routers.") && strings.HasSuffix(key, ".rule") {
				hosts, _ := traefik.ParseRule(val)
				svc.Hosts = append(svc.Hosts, hosts...)
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
