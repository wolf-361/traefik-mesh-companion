package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/wolf-361/traefik-mesh-companion/internal/config"
)

const (
	// Base URL for the Cloudflare API
	cloudflareAPIBase = "https://api.cloudflare.com/client/v4"
)

type CloudflareProvider struct {
	client  *http.Client
	cfg     *config.Config
	zoneMap map[string]string // Maps root domain (e.g., "wolf-361.ca") to its Zone ID
}

type cfZone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type cfZonesResponse struct {
	Success bool     `json:"success"`
	Result  []cfZone `json:"result"`
}

type cfRecord struct {
	ID      string `json:"id,omitempty"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
	TTL     int    `json:"ttl"`
}

type cfRecordsResponse struct {
	Success bool       `json:"success"`
	Result  []cfRecord `json:"result"`
}

// Init sets up the HTTP client and dynamically fetches all available Cloudflare Zones.
func (c *CloudflareProvider) Init(cfg *config.Config) error {
	c.cfg = cfg
	c.client = &http.Client{Timeout: 10 * time.Second}
	c.zoneMap = make(map[string]string)

	if c.cfg.Cloudflare == nil {
		return fmt.Errorf("cloudflare configuration is missing but provider was initialized")
	}

	// Dynamically fetch all zones associated with this API Token
	url := fmt.Sprintf("%s/zones?per_page=50", cloudflareAPIBase)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create zones request: %w", err)
	}
	req.Header.Add("Authorization", "Bearer "+c.cfg.Cloudflare.Token)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch zones: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Debug("failed to close response body", "error", closeErr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return fmt.Errorf("cloudflare API returned status %d and failed to read body: %w", resp.StatusCode, readErr)
		}
		return fmt.Errorf("cloudflare API returned status %d: %s", resp.StatusCode, string(body))
	}

	var zResp cfZonesResponse
	if err := json.NewDecoder(resp.Body).Decode(&zResp); err != nil {
		return fmt.Errorf("failed to decode zones JSON: %w", err)
	}

	// Build the mapping dictionary in memory
	var loadedDomains []string
	for _, z := range zResp.Result {
		c.zoneMap[z.Name] = z.ID
		loadedDomains = append(loadedDomains, z.Name)
	}

	slog.Info("Initialized Cloudflare Provider with auto-discovery", "discovered_zones", len(loadedDomains), "domains", loadedDomains)
	return nil
}

// Sync ensures Cloudflare records match the active external Traefik containers.
func (c *CloudflareProvider) Sync(activeHosts map[string]bool, target string) error {
	recordType := "CNAME"
	if net.ParseIP(target) != nil {
		recordType = "A"
	}

	// Step 1: Group the active Traefik hosts by their respective Cloudflare Zone
	hostsByZone := make(map[string][]string)
	for host := range activeHosts {
		matched := false
		for domain, zoneID := range c.zoneMap {
			// Match exact domain (wolf-361.ca) or subdomain (app.wolf-361.ca) safely
			if host == domain || strings.HasSuffix(host, "."+domain) {
				hostsByZone[zoneID] = append(hostsByZone[zoneID], host)
				matched = true
				break
			}
		}
		if !matched {
			slog.Warn("Skipping host, no matching Cloudflare zone found for domain", "host", host)
		}
	}

	// Step 2: Sync each discovered zone independently
	for zoneID, hosts := range hostsByZone {
		if err := c.syncZone(zoneID, hosts, target, recordType); err != nil {
			slog.Error("Failed to sync Cloudflare zone", "zone_id", zoneID, "error", err)
		}
	}

	return nil
}

// syncZone handles the API logic for a specific Zone ID
func (c *CloudflareProvider) syncZone(zoneID string, hosts []string, target string, recordType string) error {
	url := fmt.Sprintf("%s/zones/%s/dns_records?per_page=100", cloudflareAPIBase, zoneID)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Add("Authorization", "Bearer "+c.cfg.Cloudflare.Token)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch records: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Debug("failed to close response body", "error", closeErr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return fmt.Errorf("cloudflare API returned status %d and failed to read body: %w", resp.StatusCode, readErr)
		}
		return fmt.Errorf("cloudflare API returned status %d: %s", resp.StatusCode, string(body))
	}

	var cfResp cfRecordsResponse
	if err := json.NewDecoder(resp.Body).Decode(&cfResp); err != nil {
		return fmt.Errorf("failed to decode JSON: %w", err)
	}

	for _, host := range hosts {
		exists := false
		for _, rec := range cfResp.Result {
			if rec.Name == host {
				exists = true
				if rec.Content != target || rec.Type != recordType {
					c.upsertRecord(http.MethodPut, rec.ID, host, target, recordType, zoneID)
				}
				break
			}
		}
		if !exists {
			c.upsertRecord(http.MethodPost, "", host, target, recordType, zoneID)
		}
	}

	return nil
}

func (c *CloudflareProvider) upsertRecord(method, recordID, host, target, recordType, zoneID string) {
	rec := cfRecord{
		Name:    host,
		Type:    recordType,
		Content: target,
		Proxied: true,
		TTL:     1,
	}

	body, err := json.Marshal(rec)
	if err != nil {
		slog.Error("Failed to marshal Cloudflare record", "host", host, "error", err)
		return
	}

	url := fmt.Sprintf("%s/zones/%s/dns_records", cloudflareAPIBase, zoneID)
	if method == http.MethodPut {
		url = fmt.Sprintf("%s/%s", url, recordID)
	}

	req, err := http.NewRequest(method, url, bytes.NewBuffer(body))
	if err != nil {
		slog.Error("Failed to create Cloudflare request", "host", host, "error", err)
		return
	}
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
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Debug("failed to close response body", "error", closeErr)
		}
	}
}
