package netbird

import (
	"os"
	"strings"
)

// Config holds settings required specifically for NetBird.
type Config struct {
	Token  string
	APIURL string
	Target string
}

// LoadConfig fetches NetBird variables. Returns nil if core requirements are missing.
func LoadConfig() *Config {
	token := os.Getenv("NETBIRD_API_TOKEN")
	target := os.Getenv("NETBIRD_TARGET_IP")

	if token == "" || target == "" {
		return nil
	}

	apiURL := "https://api.netbird.io/api"
	if customURL := os.Getenv("NETBIRD_API_URL"); customURL != "" {
		apiURL = customURL
	}

	return &Config{
		Token:  token,
		APIURL: strings.TrimRight(apiURL, "/"),
		Target: target,
	}
}
