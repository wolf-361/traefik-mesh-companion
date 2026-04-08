package config

import "time"

// Pipeline defines how to filter Traefik labels for a specific sync path.
type Pipeline struct {
	Enabled     bool
	Provider    string
	FilterLabel string
	FilterValue string
	Cleanup     bool
}

// Config holds ONLY the universal application settings.
type Config struct {
	LogLevel string
	DryRun   bool

	SyncInterval time.Duration

	Internal        Pipeline
	External        Pipeline
	MonitorProvider string
}
