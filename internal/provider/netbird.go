package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/wolf-361/traefik-mesh-companion/internal/config"
)

type NetbirdProvider struct {
	client *http.Client
	cfg    *config.Config
	zoneID string
}

type netbirdZone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type netbirdRecord struct {
	ID      string `json:"id,omitempty"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
}

// Init verifies the API token and fetches the specific Zone ID for the given domain.
func (n *NetbirdProvider) Init(cfg *config.Config) error {
	n.cfg = cfg
	n.client = &http.Client{Timeout: 10 * time.Second}

	if n.cfg.Netbird == nil {
		return fmt.Errorf("netbird configuration is missing but provider was initialized")
	}

	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/dns/zones", n.cfg.Netbird.APIURL), nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Add("Authorization", "Token "+n.cfg.Netbird.Token)

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to NetBird API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("netbird API returned status %d: %s", resp.StatusCode, string(body))
	}

	var zones []netbirdZone
	if err := json.NewDecoder(resp.Body).Decode(&zones); err != nil {
		return fmt.Errorf("failed to decode zones JSON: %w", err)
	}

	for _, z := range zones {
		if z.Name == n.cfg.Netbird.Zone {
			n.zoneID = z.ID
			slog.Info("Initialized NetBird Provider", "zone", z.Name, "zone_id", n.zoneID)
			return nil
		}
	}

	return fmt.Errorf("zone '%s' not found in your NetBird account", n.cfg.Netbird.Zone)
}

// Sync ensures the NetBird records perfectly match the active internal Traefik containers.
func (n *NetbirdProvider) Sync(activeHosts map[string]bool, targetIP string) error {
	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/dns/zones/%s/records", n.cfg.Netbird.APIURL, n.zoneID), nil)
	req.Header.Add("Authorization", "Token "+n.cfg.Netbird.Token)

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch records: %w", err)
	}
	defer resp.Body.Close()

	var currentRecords []netbirdRecord
	if err := json.NewDecoder(resp.Body).Decode(&currentRecords); err != nil {
		return fmt.Errorf("failed to decode records JSON: %w", err)
	}

	for host := range activeHosts {
		exists := false
		for _, rec := range currentRecords {
			if rec.Name == host {
				exists = true
				if rec.Content != targetIP {
					n.upsertRecord(http.MethodPut, rec.ID, host, targetIP)
				}
				break
			}
		}
		if !exists {
			n.upsertRecord(http.MethodPost, "", host, targetIP)
		}
	}

	return nil
}

func (n *NetbirdProvider) upsertRecord(method, recordID, host, ip string) {
	rec := netbirdRecord{Name: host, Type: "A", Content: ip, TTL: 300}
	body, _ := json.Marshal(rec)

	url := fmt.Sprintf("%s/dns/zones/%s/records", n.cfg.Netbird.APIURL, n.zoneID)
	if method == http.MethodPut {
		url = fmt.Sprintf("%s/%s", url, recordID)
	}

	req, _ := http.NewRequest(method, url, bytes.NewBuffer(body))
	req.Header.Add("Authorization", "Token "+n.cfg.Netbird.Token)
	req.Header.Add("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err == nil && resp.StatusCode == http.StatusOK {
		action := "Created"
		if method == http.MethodPut {
			action = "Updated"
		}
		slog.Info("Synced NetBird record",
			"action", action,
			"type", "A",
			"host", host,
			"target", ip,
		)
	} else {
		statusCode := 0
		if resp != nil {
			statusCode = resp.StatusCode
		}
		slog.Error("Failed to sync NetBird record",
			"host", host,
			"status_code", statusCode,
		)
	}

	if resp != nil {
		resp.Body.Close()
	}
}
