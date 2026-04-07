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
	"github.com/wolf-361/traefik-mesh-companion/internal/core"
	"github.com/wolf-361/traefik-mesh-companion/internal/dns/cloudflare"
	"github.com/wolf-361/traefik-mesh-companion/internal/dns/netbird"
	"github.com/wolf-361/traefik-mesh-companion/internal/monitor/gatus"
	"github.com/wolf-361/traefik-mesh-companion/internal/server"
	"github.com/wolf-361/traefik-mesh-companion/internal/watcher"
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
	// --- CLI Flags & Health Check ---
	isHealthCheck := flag.Bool("health", false, "Run health check against the companion")
	flag.Parse()

	if *isHealthCheck {
		if err := server.Check(); err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	// --- Global Setup ---
	cfg := config.Load()
	setupLogger(cfg.LogLevel)

	slog.Info("Starting Traefik Mesh Companion", "version", "1.1.0", "log_level", cfg.LogLevel)

	if cfg.DryRun {
		slog.Warn("DRY RUN MODE ENABLED. No remote state will be altered.")
	}

	// Create the Global Executor
	exec := core.NewExecutor(cfg.DryRun)

	// --- Processor Assembly ---
	var processors []core.Processor

	// Assemble Internal Pipeline (DNS)
	if cfg.Internal.Enabled {
		switch cfg.Internal.Provider {
		case "netbird":
			slog.Debug("Booting NetBird Provider (Internal)...")
			if nb := netbird.New(&cfg.Internal, exec); nb != nil {
				if err := nb.Init(); err != nil {
					slog.Error("Failed to initialize NetBird", "error", err)
					os.Exit(1)
				}
				processors = append(processors, nb)
			}
		default:
			slog.Warn("Unknown internal provider requested", "provider", cfg.Internal.Provider)
		}
	}

	// Assemble External Pipeline (DNS)
	if cfg.External.Enabled {
		switch cfg.External.Provider {
		case "cloudflare":
			slog.Debug("Booting Cloudflare Provider (External)...")
			if cf := cloudflare.New(&cfg.External, exec); cf != nil {
				if err := cf.Init(); err != nil {
					slog.Error("Failed to initialize Cloudflare", "error", err)
					os.Exit(1)
				}
				processors = append(processors, cf)
			}
		default:
			slog.Warn("Unknown external provider requested", "provider", cfg.External.Provider)
		}
	}

	// Assemble Monitoring
	slog.Debug("Checking for Gatus Bridge configuration...")
	if gc := gatus.New(exec); gc != nil {
		if err := gc.SyncState(); err != nil {
			slog.Warn("Gatus initial sync failed, continuing anyway", "error", err)
		}
		processors = append(processors, gc)
	}

	// Sanity Check
	if len(processors) == 0 {
		slog.Error("No processors enabled or configured correctly. Please check your environment variables.")
		os.Exit(1)
	}

	// --- Watcher Initialization ---
	w, err := watcher.NewWatcher(cfg, processors)
	if err != nil {
		slog.Error("Failed to initialize Docker watcher", "error", err)
		os.Exit(1)
	}

	// --- Background Services ---
	healthServer := server.NewServer()
	go func() {
		slog.Debug("Starting background health server", "port", server.Port)
		if err := healthServer.Start(); err != nil {
			slog.Error("Health server crashed", "error", err)
		}
	}()

	go w.Start()

	// --- Graceful Shutdown ---
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
