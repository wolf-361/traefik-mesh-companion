package config

import (
	"os"
	"strings"
)

// NetbirdConfig holds the settings required to communicate with NetBird.
type NetbirdConfig struct {
	Token  string
	APIURL string
	Target string
}

// loadNetbirdConfig populates NetbirdConfig from environment variables.
func loadNetbirdConfig() *NetbirdConfig {
	token := os.Getenv("NETBIRD_API_TOKEN")
	target := os.Getenv("NETBIRD_TARGET_IP")

	// Return nil if core requirements are missing to skip provider initialization
	if token == "" || target == "" {
		return nil
	}

	return &NetbirdConfig{
		Token:  token,
		APIURL: strings.TrimRight(getEnvOrDefault("NETBIRD_API_URL", "https://api.netbird.io/api"), "/"),
		Target: target,
	}
}
