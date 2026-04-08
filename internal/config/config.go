package config

import (
	"log/slog"
	"os"
	"strings"
	"time"
)

// Load reads the core environment variables.
func Load() *Config {
	cfg := &Config{
		LogLevel:        getEnvOrDefault("LOG_LEVEL", "info"),
		DryRun:          strings.ToLower(os.Getenv("DRY_RUN")) == "true",
		SyncInterval:    parseDuration(os.Getenv("SYNC_INTERVAL"), 1*time.Minute),
		MonitorProvider: strings.ToLower(getEnvOrDefault("MONITOR_PROVIDER", "none")),
	}

	cfg.Internal = Pipeline{
		Provider:    strings.ToLower(getEnvOrDefault("INTERNAL_PROVIDER", "netbird")),
		FilterLabel: getEnvOrDefault("INTERNAL_FILTER_LABEL", "traefik.http.routers.*.entrypoints"),
		FilterValue: getEnvOrDefault("INTERNAL_FILTER", "traefik"),
		Cleanup:     strings.ToLower(os.Getenv("INTERNAL_CLEANUP")) == "true",
	}
	cfg.Internal.Enabled = cfg.Internal.Provider != "none"

	cfg.External = Pipeline{
		Provider:    strings.ToLower(getEnvOrDefault("EXTERNAL_PROVIDER", "none")),
		FilterLabel: getEnvOrDefault("EXTERNAL_FILTER_LABEL", "traefik.http.routers.*.entrypoints"),
		FilterValue: getEnvOrDefault("EXTERNAL_FILTER", "https"),
		Cleanup:     strings.ToLower(os.Getenv("EXTERNAL_CLEANUP")) == "true",
	}
	cfg.External.Enabled = cfg.External.Provider != "none"

	// --- LOG THE FINAL CORE CONFIG ---
	slog.Debug("Core Configuration Loaded",
		"monitor_provider", cfg.MonitorProvider,
		"internal_provider", cfg.Internal.Provider,
		"external_provider", cfg.External.Provider,
		"dry_run", cfg.DryRun,
		"sync_interval", cfg.SyncInterval,
	)

	return cfg
}

func getEnvOrDefault(key, fallback string) string {
	if val, exists := os.LookupEnv(key); exists && val != "" {
		return val
	}
	return fallback
}

func parseDuration(raw string, fallback time.Duration) time.Duration {
	if d, err := time.ParseDuration(raw); err == nil {
		return d
	}
	return fallback
}
