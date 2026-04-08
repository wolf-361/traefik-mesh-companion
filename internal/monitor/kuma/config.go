package kuma

import "os"

// Config holds settings required specifically for Uptime Kuma.
type Config struct {
	URL        string
	Username   string
	Password   string
	AutoEnable bool
}

// LoadConfig fetches Kuma variables. Returns nil if core requirements are missing.
func LoadConfig() *Config {
	url := os.Getenv("KUMA_URL")
	username := os.Getenv("KUMA_USERNAME")
	password := os.Getenv("KUMA_PASSWORD")

	if url == "" || username == "" || password == "" {
		return nil
	}

	return &Config{
		URL:        url,
		Username:   username,
		Password:   password,
		AutoEnable: os.Getenv("KUMA_AUTO_ENABLE") == "true",
	}
}
