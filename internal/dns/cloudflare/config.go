package cloudflare

import "os"

// Config holds settings required specifically for Cloudflare.
type Config struct {
	Token  string
	Target string
}

// LoadConfig fetches Cloudflare variables. Returns nil if core requirements are missing.
func LoadConfig() *Config {
	token := os.Getenv("CLOUDFLARE_API_TOKEN")
	target := os.Getenv("CLOUDFLARE_TARGET_DOMAIN")

	if token == "" || target == "" {
		return nil
	}

	return &Config{
		Token:  token,
		Target: target,
	}
}
