// Package provider implements the DNS update logic for various backends.
package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/wolf-361/traefik-mesh-companion/internal/config"
)

// NetbirdProvider manages internal DNS records within a NetBird Mesh network.
// It automatically discovers available DNS zones and maps Traefik hostnames
// to the appropriate zone ID for synchronization.
type NetbirdProvider struct {
	client  *http.Client
	cfg     *config.Config
	zoneMap map[string]string // Mapping of zone names (e.g. "wolf-361.ca") to IDs
}

// netbirdZone represents the structure of a DNS zone in the NetBird API.
type netbirdZone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// netbirdRecord represents a single DNS record within a NetBird zone.
type netbirdRecord struct {
	ID      string `json:"id,omitempty"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
}

// Init initializes the HTTP client and fetches all available DNS zones from the NetBird API.
// This allows the provider to dynamically handle multiple domains without explicit configuration.
func (n *NetbirdProvider) Init(cfg *config.Config) error {
	n.cfg = cfg
	n.client = &http.Client{Timeout: 10 * time.Second}
	n.zoneMap = make(map[string]string)

	if n.cfg.Netbird == nil {
		return fmt.Errorf("netbird configuration is missing but provider was initialized")
	}

	// Fetch all DNS zones authorized for the provided token
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/dns/zones", n.cfg.Netbird.APIURL), nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Add("Authorization", "Token "+n.cfg.Netbird.Token)

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to NetBird API: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Debug("failed to close response body", "error", closeErr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return fmt.Errorf("netbird API returned status %d and failed to read body: %w", resp.StatusCode, readErr)
		}
		return fmt.Errorf("netbird API returned status %d: %s", resp.StatusCode, string(body))
	}

	var zones []netbirdZone
	if err := json.NewDecoder(resp.Body).Decode(&zones); err != nil {
		return fmt.Errorf("failed to decode zones JSON: %w", err)
	}

	// Build the internal map for host-to-zone routing
	for _, z := range zones {
		n.zoneMap[z.Name] = z.ID
	}

	slog.Info("Initialized NetBird Provider", "discovered_zones", len(n.zoneMap))
	return nil
}

// Sync performs a full synchronization between active Traefik hosts and NetBird DNS records.
// It identifies which hosts belong to which NetBird zones and updates/creates records as needed.
func (n *NetbirdProvider) Sync(activeHosts map[string]bool, targetIP string) error {
	// Group active hosts by their corresponding NetBird Zone ID
	hostsByZone := make(map[string][]string)
	for host := range activeHosts {
		matched := false
		for zoneName, zoneID := range n.zoneMap {
			// Match exact domain or subdomains via suffix
			if host == zoneName || strings.HasSuffix(host, "."+zoneName) {
				hostsByZone[zoneID] = append(hostsByZone[zoneID], host)
				matched = true
				break
			}
		}
		if !matched {
			slog.Warn("Skipping host: no matching NetBird zone found", "host", host)
		}
	}

	// Process each zone independently
	for zoneID, hosts := range hostsByZone {
		if err := n.syncZone(zoneID, hosts, targetIP); err != nil {
			slog.Error("Failed to sync NetBird zone", "zone_id", zoneID, "error", err)
		}
	}

	return nil
}

// syncZone fetches current records for a specific zone and determines required API actions.
func (n *NetbirdProvider) syncZone(zoneID string, hosts []string, targetIP string) error {
	url := fmt.Sprintf("%s/dns/zones/%s/records", n.cfg.Netbird.APIURL, zoneID)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Add("Authorization", "Token "+n.cfg.Netbird.Token)

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch records: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Debug("failed to close response body", "error", closeErr)
		}
	}()

	var currentRecords []netbirdRecord
	if err := json.NewDecoder(resp.Body).Decode(&currentRecords); err != nil {
		return fmt.Errorf("failed to decode records JSON: %w", err)
	}

	// Reconcile Traefik hosts with existing NetBird records
	for _, host := range hosts {
		exists := false
		for _, rec := range currentRecords {
			if rec.Name == host {
				exists = true
				// If IP changed, update the record
				if rec.Content != targetIP {
					n.upsertRecord(http.MethodPut, rec.ID, host, targetIP, zoneID)
				}
				break
			}
		}
		// If record is missing, create it
		if !exists {
			n.upsertRecord(http.MethodPost, "", host, targetIP, zoneID)
		}
	}

	return nil
}

// upsertRecord handles the physical POST/PUT request to the NetBird API to create or update a record.
func (n *NetbirdProvider) upsertRecord(method, recordID, host, ip, zoneID string) {
	rec := netbirdRecord{
		Name:    host,
		Type:    "A",
		Content: ip,
		TTL:     300,
	}

	body, err := json.Marshal(rec)
	if err != nil {
		slog.Error("Failed to marshal NetBird record", "host", host, "error", err)
		return
	}

	url := fmt.Sprintf("%s/dns/zones/%s/records", n.cfg.Netbird.APIURL, zoneID)
	if method == http.MethodPut {
		url = fmt.Sprintf("%s/%s", url, recordID)
	}

	req, err := http.NewRequest(method, url, bytes.NewBuffer(body))
	if err != nil {
		slog.Error("Failed to create NetBird request", "host", host, "error", err)
		return
	}
	req.Header.Add("Authorization", "Token "+n.cfg.Netbird.Token)
	req.Header.Add("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err == nil && resp.StatusCode == http.StatusOK {
		action := "Created"
		if method == http.MethodPut {
			action = "Updated"
		}
		slog.Info("Synced NetBird record", "action", action, "host", host, "target", ip)
	} else {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		slog.Error("Failed to sync NetBird record", "host", host, "status_code", status)
	}

	if resp != nil {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Debug("failed to close response body", "error", closeErr)
		}
	}
}
