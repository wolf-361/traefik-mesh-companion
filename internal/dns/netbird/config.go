package netbird

import (
	"log/slog"
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
	customURL := os.Getenv("NETBIRD_API_URL")

	slog.Debug("Raw NetBird Environment Variables",
		"target", target,
		"custom_url", customURL,
		"token_length", len(token),
	)

	if token == "" || target == "" {
		return nil
	}

	apiURL := "https://api.netbird.io/api"
	if customURL != "" {
		apiURL = customURL
	}

	return &Config{
		Token:  token,
		APIURL: strings.TrimRight(apiURL, "/"),
		Target: target,
	}
}
