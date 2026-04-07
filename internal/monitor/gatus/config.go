package gatus

import "os"

// Config holds settings required to talk to the Gatus API Bridge.
type Config struct {
	BridgeURL  string
	AutoEnable bool
}

// LoadConfig fetches Gatus bridge variables. Returns nil if core requirements are missing.
func LoadConfig() *Config {
	url := os.Getenv("GATUS_BRIDGE_URL")

	if url == "" {
		return nil
	}

	return &Config{
		BridgeURL:  url,
		AutoEnable: os.Getenv("GATUS_AUTO_ENABLE") == "true",
	}
}
