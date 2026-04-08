package gatus

import (
	"log/slog"
	"os"
)

type Config struct {
	BridgeURL  string
	APIKey     string
	AutoEnable bool
}

func LoadConfig() *Config {
	url := os.Getenv("GATUS_BRIDGE_URL")
	apiKey := os.Getenv("GATUS_API_KEY")

	slog.Debug("Raw Gatus Environment Variables",
		"bridge_url", url,
		"api_key_length", len(apiKey),
		"auto_enable", os.Getenv("GATUS_AUTO_ENABLE"),
	)

	if url == "" {
		return nil
	}
	return &Config{
		BridgeURL:  url,
		APIKey:     apiKey,
		AutoEnable: os.Getenv("GATUS_AUTO_ENABLE") == "true",
	}
}
