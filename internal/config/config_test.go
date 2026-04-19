package config

import (
	"testing"
	"time"
)

func TestLoad_Defaults(t *testing.T) {
	// Wipe any existing environment variables to ensure we test pure defaults
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("DRY_RUN", "")
	t.Setenv("SYNC_INTERVAL", "")
	t.Setenv("MONITOR_PROVIDER", "")
	t.Setenv("INTERNAL_PROVIDER", "")
	t.Setenv("EXTERNAL_PROVIDER", "")

	cfg := Load()

	// Core Defaults
	if cfg.LogLevel != "info" {
		t.Errorf("Expected default LogLevel 'info', got '%s'", cfg.LogLevel)
	}
	if cfg.DryRun != false {
		t.Errorf("Expected default DryRun false")
	}
	if cfg.SyncInterval != 1*time.Minute {
		t.Errorf("Expected default SyncInterval 1m, got %v", cfg.SyncInterval)
	}
	if cfg.MonitorProvider != "none" {
		t.Errorf("Expected default MonitorProvider 'none', got '%s'", cfg.MonitorProvider)
	}

	// Internal Pipeline Defaults
	if cfg.Internal.Provider != "netbird" {
		t.Errorf("Expected default Internal Provider 'netbird', got '%s'", cfg.Internal.Provider)
	}
	if cfg.Internal.FilterValue != "traefik" {
		t.Errorf("Expected default Internal Filter 'traefik', got '%s'", cfg.Internal.FilterValue)
	}
	if !cfg.Internal.Enabled {
		t.Errorf("Expected Internal pipeline to be Enabled by default")
	}

	// External Pipeline Defaults
	if cfg.External.Provider != "none" {
		t.Errorf("Expected default External Provider 'none', got '%s'", cfg.External.Provider)
	}
	if cfg.External.Enabled {
		t.Errorf("Expected External pipeline to be Disabled by default")
	}
}

func TestLoad_Overrides(t *testing.T) {
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("DRY_RUN", "TRUE") // Test case-insensitivity
	t.Setenv("SYNC_INTERVAL", "5m")
	t.Setenv("MONITOR_PROVIDER", "kuma")

	t.Setenv("INTERNAL_PROVIDER", "custom")
	t.Setenv("INTERNAL_CLEANUP", "true")

	t.Setenv("EXTERNAL_PROVIDER", "cloudflare")
	t.Setenv("EXTERNAL_FILTER", "public")

	cfg := Load()

	if cfg.LogLevel != "debug" {
		t.Errorf("Expected LogLevel 'debug', got '%s'", cfg.LogLevel)
	}
	if !cfg.DryRun {
		t.Errorf("Expected DryRun true")
	}
	if cfg.SyncInterval != 5*time.Minute {
		t.Errorf("Expected SyncInterval 5m, got %v", cfg.SyncInterval)
	}
	if cfg.MonitorProvider != "kuma" {
		t.Errorf("Expected MonitorProvider 'kuma', got '%s'", cfg.MonitorProvider)
	}

	if cfg.Internal.Provider != "custom" {
		t.Errorf("Expected Internal Provider 'custom', got '%s'", cfg.Internal.Provider)
	}
	if !cfg.Internal.Cleanup {
		t.Errorf("Expected Internal Cleanup true")
	}

	if cfg.External.Provider != "cloudflare" {
		t.Errorf("Expected External Provider 'cloudflare', got '%s'", cfg.External.Provider)
	}
	if cfg.External.FilterValue != "public" {
		t.Errorf("Expected External Filter 'public', got '%s'", cfg.External.FilterValue)
	}
	if !cfg.External.Enabled {
		t.Errorf("Expected External pipeline to be Enabled")
	}
}

func TestParseDuration_InvalidFallback(t *testing.T) {
	// If the user types garbage into the SYNC_INTERVAL, it should fall back to 1 minute safely
	t.Setenv("SYNC_INTERVAL", "not-a-real-time")

	cfg := Load()

	if cfg.SyncInterval != 1*time.Minute {
		t.Errorf("Expected fallback 1m for invalid duration, got %v", cfg.SyncInterval)
	}
}
