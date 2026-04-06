package kuma

import "os"

// Config holds settings required specifically for Uptime Kuma.
type Config struct {
	URL        string
	APIKey     string
	AutoEnable bool
}

// LoadConfig fetches Kuma variables. Returns nil if core requirements are missing.
func LoadConfig() *Config {
	url := os.Getenv("KUMA_URL")
	apiKey := os.Getenv("KUMA_API_KEY")

	if url == "" || apiKey == "" {
		return nil
	}

	return &Config{
		URL:        url,
		APIKey:     apiKey,
		AutoEnable: os.Getenv("KUMA_AUTO_ENABLE") == "true",
	}
}
