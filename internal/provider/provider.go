package provider

import "github.com/wolf-361/traefik-mesh-companion/internal/config"

// DNSProvider defines the exact methods any DNS or VPN backend must implement.
type DNSProvider interface {
	// Init establishes the connection, validates tokens, and prepares the provider.
	Init(cfg *config.Config) error

	// Sync compares the active Traefik hosts against the provider's records,
	// creating missing records and updating changed IPs/CNAMEs.
	Sync(activeHosts map[string]bool, target string) error
}
