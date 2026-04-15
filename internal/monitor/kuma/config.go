package kuma

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
)

// Config holds settings required specifically for Uptime Kuma.
type Config struct {
	URL        string
	Username   string
	Password   string
	AutoEnable bool

	// Coordinator
	CoordinatorMode string // "master", "follower", or ""
	CoordinatorURL  string
	CoordinatorPort string

	// Status Page Configuration
	GlobalStatusPageSlug string
	GlobalStatusPageDomain string

	DefaultTags []string

	// Global Defaults
	DefaultInterval            int64
	DefaultMaxRetries          int64
	DefaultRetryInterval       int64
	DefaultAcceptedStatusCodes []string
	DefaultMaxRedirects        int
}

// LoadConfig fetches Kuma variables. Returns nil if core requirements are missing.
func LoadConfig() *Config {
	url := os.Getenv("KUMA_URL")
	username := os.Getenv("KUMA_USERNAME")
	password := os.Getenv("KUMA_PASSWORD")

	slog.Debug("Raw Kuma Environment Variables",
		"url", url,
		"username", username,
		"password_length", len(password),
	)

	if url == "" || username == "" || password == "" {
		return nil
	}

	return &Config{
		URL:        url,
		Username:   username,
		Password:   password,
		AutoEnable: os.Getenv("KUMA_AUTO_ENABLE") == "true",

		CoordinatorMode: strings.ToLower(getEnvString("KUMA_COORDINATOR_MODE", "client")),
		CoordinatorURL:  getEnvString("KUMA_COORDINATOR_URL", ""),
		CoordinatorPort: getEnvString("KUMA_COORDINATOR_PORT", "8080"),

		GlobalStatusPageSlug: getEnvString("KUMA_GLOBAL_STATUS_PAGE", "none"),
		GlobalStatusPageDomain: getEnvString("KUMA_GLOBAL_STATUS_PAGE_DOMAIN", ""),

		DefaultTags: getEnvStringSlice("KUMA_DEFAULT_TAGS", []string{}),

		DefaultInterval:      getEnvInt64("KUMA_DEFAULT_INTERVAL", 60),
		DefaultMaxRetries:    getEnvInt64("KUMA_DEFAULT_MAX_RETRIES", 3),
		DefaultRetryInterval: getEnvInt64("KUMA_DEFAULT_RETRY_INTERVAL", 60),
		DefaultMaxRedirects:  getEnvInt("KUMA_DEFAULT_MAX_REDIRECTS", 0),
		DefaultAcceptedStatusCodes: getEnvStringSlice("KUMA_DEFAULT_ACCEPTED_STATUS_CODES",
			[]string{"200-299"}),
	}
}

func getEnvString(key string, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func getEnvInt64(key string, fallback int64) int64 {
	if val, err := strconv.ParseInt(os.Getenv(key), 10, 64); err == nil {
		return val
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if val, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return val
	}
	return fallback
}

func getEnvStringSlice(key string, fallback []string) []string {
	val := os.Getenv(key)
	if val == "" {
		return fallback
	}
	parts := strings.Split(val, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}
