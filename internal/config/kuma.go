package config

import "os"

// KumaConfig holds the settings required to communicate with Uptime Kuma.
type KumaConfig struct {
	URL        string
	APIKey     string
	AutoEnable bool
}

// loadKumaConfig populates KumaConfig from environment variables.
func loadKumaConfig() *KumaConfig {
	url := os.Getenv("KUMA_URL")
	apiKey := os.Getenv("KUMA_API_KEY")
	autoEnable := os.Getenv("KUMA_AUTO_ENABLE") == "true"

	if url == "" || apiKey == "" {
		return nil
	}

	return &KumaConfig{
		URL:        url,
		APIKey:     apiKey,
		AutoEnable: autoEnable,
	}
}
