package monitor

import "github.com/wolf-361/traefik-mesh-companion/internal/core"

// Provider ensures that any monitoring client (Gatus, Kuma, etc.)
// can sync its initial state and hook into the main event watcher.
type Provider interface {
	core.Processor
	SyncState() error
}

// Endpoint represents the generic payload we extract from Traefik labels
type Endpoint struct {
	Name  string
	URL   string
	Group string
}
