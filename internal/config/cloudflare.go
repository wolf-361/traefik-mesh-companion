package config

import "os"

// CloudflareConfig holds the settings required to communicate with Cloudflare.
type CloudflareConfig struct {
	Token  string
	ZoneID string
	Target string
}

// loadCloudflareConfig populates CloudflareConfig from environment variables.
func loadCloudflareConfig() *CloudflareConfig {
	return &CloudflareConfig{
		Token:  os.Getenv("CLOUDFLARE_API_TOKEN"),
		ZoneID: os.Getenv("CLOUDFLARE_ZONE_ID"),
		Target: os.Getenv("CLOUDFLARE_TARGET_DOMAIN"),
	}
}
