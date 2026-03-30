package config

import "os"

// CloudflareConfig holds the settings required to communicate with Cloudflare.
type CloudflareConfig struct {
	Token  string
	Target string
}

// loadCloudflareConfig populates CloudflareConfig from environment variables.
func loadCloudflareConfig() *CloudflareConfig {
	token := os.Getenv("CLOUDFLARE_API_TOKEN")
	target := os.Getenv("CLOUDFLARE_TARGET_DOMAIN")

	// Return nil if core requirements are missing to skip provider initialization
	if token == "" || target == "" {
		return nil
	}

	return &CloudflareConfig{
		Token:  token,
		Target: target,
	}
}
