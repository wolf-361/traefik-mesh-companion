package gatus

import "os"

type Config struct {
	BridgeURL  string
	APIKey     string
	AutoEnable bool
}

func LoadConfig() *Config {
	url := os.Getenv("GATUS_BRIDGE_URL")
	if url == "" {
		return nil
	}
	return &Config{
		BridgeURL:  url,
		APIKey:     os.Getenv("GATUS_API_KEY"),
		AutoEnable: os.Getenv("GATUS_AUTO_ENABLE") == "true",
	}
}
