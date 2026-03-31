package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/wolf-361/traefik-mesh-companion/internal/config"
	"github.com/wolf-361/traefik-mesh-companion/internal/docker"
	"github.com/wolf-361/traefik-mesh-companion/internal/health"
	"github.com/wolf-361/traefik-mesh-companion/internal/provider"
)

// setupLogger translates the string config into a global slog instance.
func setupLogger(level string) {
	var slogLevel slog.Level
	switch strings.ToLower(level) {
	case "debug":
		slogLevel = slog.LevelDebug
	case "warn", "warning":
		slogLevel = slog.LevelWarn
	case "error":
		slogLevel = slog.LevelError
	default:
		slogLevel = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{
		Level: slogLevel,
	}

	handler := slog.NewTextHandler(os.Stdout, opts)
	slog.SetDefault(slog.New(handler))
}

func main() {
	isHealthCheck := flag.Bool("health", false, "Run health check against the companion")
	flag.Parse()

	if *isHealthCheck {
		if err := health.Check(); err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	cfg := config.Load()

	setupLogger(cfg.LogLevel)

	slog.Info("Starting Traefik Mesh Companion", "version", "1.0.0", "log_level", cfg.LogLevel)
	slog.Info("Configuration loaded", "sync_interval", cfg.SyncInterval)

	// Initialize Provider Registry
	providers := make(map[string]provider.DNSProvider)

	if cfg.Netbird != nil {
		slog.Debug("Booting NetBird Provider...")
		nb := &provider.NetbirdProvider{}
		if err := nb.Init(cfg); err != nil {
			slog.Error("Failed to initialize NetBird", "error", err)
			os.Exit(1)
		}
		providers["netbird"] = nb
	}

	if cfg.Cloudflare != nil {
		slog.Debug("Booting Cloudflare Provider...")
		cf := &provider.CloudflareProvider{}
		if err := cf.Init(cfg); err != nil {
			slog.Error("Failed to initialize Cloudflare", "error", err)
			os.Exit(1)
		}
		providers["cloudflare"] = cf
	}

	// Sanity Check
	if len(providers) == 0 {
		slog.Error("No providers enabled. Please configure an INTERNAL_PROVIDER or EXTERNAL_PROVIDER.")
		os.Exit(1)
	}

	// Initialize and Start the Docker Watcher
	watcher, err := docker.NewWatcher(cfg, providers)
	if err != nil {
		slog.Error("Failed to initialize Docker watcher", "error", err)
		os.Exit(1)
	}

	healthServer := health.NewServer()
	go func() {
		slog.Debug("Starting background health server", "port", health.Port)
		if err := healthServer.Start(); err != nil {
			slog.Error("Health server crashed", "error", err)
		}
	}()

	go watcher.Start()

	// Graceful Shutdown Trap
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)

	<-stopChan

	slog.Info("Shutting down Traefik Mesh Companion safely...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := healthServer.Stop(ctx); err != nil {
		slog.Error("Failed to stop health server cleanly", "error", err)
	}
}
