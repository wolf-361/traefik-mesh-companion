package provider

import "github.com/wolf-361/traefik-mesh-companion/internal/config"

// DNSProvider defines the standard interface for all supported DNS/Mesh backends.
type DNSProvider interface {
	Init(cfg *config.Config) error
	Sync(activeHosts map[string]bool, ignoredHosts map[string]bool, target string, cleanup bool) error
}
