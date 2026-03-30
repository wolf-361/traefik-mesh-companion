package config

import (
	"os"
	"strings"
)

// NetbirdConfig holds the settings required to communicate with NetBird.
type NetbirdConfig struct {
	Token  string
	APIURL string
	Zone   string
	Target string
}

// loadNetbirdConfig populates NetbirdConfig from environment variables.
func loadNetbirdConfig() *NetbirdConfig {
	return &NetbirdConfig{
		Token:  os.Getenv("NETBIRD_API_TOKEN"),
		APIURL: strings.TrimRight(getEnvOrDefault("NETBIRD_API_URL", "https://api.netbird.io/api"), "/"),
		Zone:   os.Getenv("NETBIRD_ZONE_NAME"),
		Target: os.Getenv("NETBIRD_TARGET_IP"),
	}
}
