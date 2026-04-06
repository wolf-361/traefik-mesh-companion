// Package config handles the loading and validation of environment variables.
package config

import (
	"os"
	"strings"
	"time"
)

// Pipeline defines how to filter Traefik labels for a specific sync path.
type Pipeline struct {
	Enabled     bool
	Provider    string
	FilterLabel string
	FilterValue string // Supports comma-separated values (e.g., "internal,https")
	Cleanup     bool
}

// Config is the main entrypoint for app settings.
// Provider configs are pointers and will be nil if not in use.
type Config struct {
	LogLevel string
	DryRun   bool

	SyncInterval time.Duration
	Internal     Pipeline
	External     Pipeline

	Netbird    *NetbirdConfig
	Cloudflare *CloudflareConfig

	Kuma *KumaConfig
}

// Load reads environment variables and returns a populated Config.
func Load() *Config {
	cfg := &Config{}

	cfg.LogLevel = getEnvOrDefault("LOG_LEVEL", "info")
	cfg.DryRun = strings.ToLower(os.Getenv("DRY_RUN")) == "true"

	// Default to 1m if SYNC_INTERVAL is missing or invalid
	cfg.SyncInterval = parseDuration(os.Getenv("SYNC_INTERVAL"), 1*time.Minute)

	// Internal Pipeline: Defaults to NetBird enabled.
	cfg.Internal.Provider = strings.ToLower(getEnvOrDefault("INTERNAL_PROVIDER", "netbird"))
	cfg.Internal.Enabled = cfg.Internal.Provider != "none"
	cfg.Internal.FilterLabel = getEnvOrDefault("INTERNAL_FILTER_LABEL", "traefik.http.routers.*.entrypoints")
	cfg.Internal.FilterValue = getEnvOrDefault("INTERNAL_FILTER", "traefik")
	cfg.Internal.Cleanup = strings.ToLower(os.Getenv("INTERNAL_CLEANUP")) == "true"

	// External Pipeline: Defaults to Disabled.
	cfg.External.Provider = strings.ToLower(getEnvOrDefault("EXTERNAL_PROVIDER", "none"))
	cfg.External.Enabled = cfg.External.Provider != "none"
	cfg.External.FilterLabel = getEnvOrDefault("EXTERNAL_FILTER_LABEL", "traefik.http.routers.*.entrypoints")
	cfg.External.FilterValue = getEnvOrDefault("EXTERNAL_FILTER", "https")
	cfg.External.Cleanup = strings.ToLower(os.Getenv("EXTERNAL_CLEANUP")) == "true"

	// Only load provider details if they are referenced in a pipeline
	if cfg.isProviderUsed("netbird") {
		cfg.Netbird = loadNetbirdConfig()
	}
	if cfg.isProviderUsed("cloudflare") {
		cfg.Cloudflare = loadCloudflareConfig()
	}

	// Check for kuma
	cfg.Kuma = loadKumaConfig()

	return cfg
}

// isProviderUsed checks if a provider is assigned to either pipeline.
func (c *Config) isProviderUsed(name string) bool {
	return (c.Internal.Enabled && c.Internal.Provider == name) ||
		(c.External.Enabled && c.External.Provider == name)
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
