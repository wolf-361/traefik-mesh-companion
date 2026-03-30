package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/wolf-361/traefik-mesh-companion/internal/config"
)

const (
	// Base URL for the Cloudflare API
	cloudflareAPIBase = "https://api.cloudflare.com/client/v4"
)

type CloudflareProvider struct {
	client *http.Client
	cfg    *config.Config
}

type cfRecord struct {
	ID      string `json:"id,omitempty"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
	TTL     int    `json:"ttl"`
}

type cfResponse struct {
	Success bool       `json:"success"`
	Result  []cfRecord `json:"result"`
}

// Init sets up the HTTP client and validates the configuration.
func (c *CloudflareProvider) Init(cfg *config.Config) error {
	c.cfg = cfg
	c.client = &http.Client{Timeout: 10 * time.Second}

	if c.cfg.Cloudflare == nil {
		return fmt.Errorf("cloudflare configuration is missing but provider was initialized")
	}

	slog.Info("Initialized Cloudflare Provider", "zone_id", c.cfg.Cloudflare.ZoneID)
	return nil
}

// Sync ensures Cloudflare records match the active external Traefik containers.
func (c *CloudflareProvider) Sync(activeHosts map[string]bool, target string) error {
	recordType := "CNAME"
	if net.ParseIP(target) != nil {
		recordType = "A"
	}

	url := fmt.Sprintf("%s/zones/%s/dns_records?per_page=100", cloudflareAPIBase, c.cfg.Cloudflare.ZoneID)
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Add("Authorization", "Bearer "+c.cfg.Cloudflare.Token)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch records: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("cloudflare API returned status %d: %s", resp.StatusCode, string(body))
	}

	var cfResp cfResponse
	if err := json.NewDecoder(resp.Body).Decode(&cfResp); err != nil {
		return fmt.Errorf("failed to decode JSON: %w", err)
	}

	for host := range activeHosts {
		exists := false
		for _, rec := range cfResp.Result {
			if rec.Name == host {
				exists = true
				if rec.Content != target || rec.Type != recordType {
					c.upsertRecord(http.MethodPut, rec.ID, host, target, recordType)
				}
				break
			}
		}
		if !exists {
			c.upsertRecord(http.MethodPost, "", host, target, recordType)
		}
	}

	return nil
}

func (c *CloudflareProvider) upsertRecord(method, recordID, host, target, recordType string) {
	rec := cfRecord{
		Name:    host,
		Type:    recordType,
		Content: target,
		Proxied: true, // Force Cloudflare Proxy for all external routing
		TTL:     1,    // 1 indicates 'Auto' in Cloudflare
	}
	body, _ := json.Marshal(rec)

	url := fmt.Sprintf("%s/zones/%s/dns_records", cloudflareAPIBase, c.cfg.Cloudflare.ZoneID)
	if method == http.MethodPut {
		url = fmt.Sprintf("%s/%s", url, recordID)
	}

	req, _ := http.NewRequest(method, url, bytes.NewBuffer(body))
	req.Header.Add("Authorization", "Bearer "+c.cfg.Cloudflare.Token)
	req.Header.Add("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err == nil && resp.StatusCode == http.StatusOK {
		action := "Created"
		if method == http.MethodPut {
			action = "Updated"
		}
		slog.Info("Synced Cloudflare record",
			"action", action,
			"type", recordType,
			"host", host,
			"target", target,
			"proxied", true,
		)
	} else {
		statusCode := 0
		if resp != nil {
			statusCode = resp.StatusCode
		}
		slog.Error("Failed to sync Cloudflare record",
			"host", host,
			"status_code", statusCode,
		)
	}

	if resp != nil {
		resp.Body.Close()
	}
}
