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

// Sync performs a full synchronization, supporting safe deletions and manual overrides.
func (n *NetbirdProvider) Sync(activeHosts map[string]bool, ignoredHosts map[string]bool, targetIP string, cleanup bool) error {
	activeByZone := make(map[string]map[string]bool)
	ignoredByZone := make(map[string]map[string]bool)
	zonesToSync := make(map[string]bool)

	// If cleanup is enabled, we must check ALL known zones to find orphans.
	if cleanup {
		for _, zoneID := range n.zoneMap {
			zonesToSync[zoneID] = true
		}
	}

	// Helper function to bucket hosts into their respective zones
	bucketHosts := func(hosts map[string]bool, targetBucket map[string]map[string]bool) {
		for host := range hosts {
			matched := false
			for zoneName, zoneID := range n.zoneMap {
				if host == zoneName || strings.HasSuffix(host, "."+zoneName) {
					if targetBucket[zoneID] == nil {
						targetBucket[zoneID] = make(map[string]bool)
					}
					targetBucket[zoneID][host] = true
					zonesToSync[zoneID] = true // Ensure this zone is synced
					matched = true
					break
				}
			}
			if !matched {
				slog.Warn("Skipping host: no matching NetBird zone found", "host", host)
			}
		}
	}

	bucketHosts(activeHosts, activeByZone)
	bucketHosts(ignoredHosts, ignoredByZone)

	// Process each zone independently
	for zoneID := range zonesToSync {
		if err := n.syncZone(zoneID, activeByZone[zoneID], ignoredByZone[zoneID], targetIP, cleanup); err != nil {
			slog.Error("Failed to sync NetBird zone", "zone_id", zoneID, "error", err)
		}
	}

	return nil
}

// syncZone fetches current records for a specific zone and determines required API actions.
func (n *NetbirdProvider) syncZone(zoneID string, activeHosts map[string]bool, ignoredHosts map[string]bool, targetIP string, cleanup bool) error {
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

	// Process Updates and Creations
	for host := range activeHosts {
		exists := false
		for _, rec := range currentRecords {
			if rec.Name == host {
				exists = true
				if rec.Content != targetIP {
					n.upsertRecord(http.MethodPut, rec.ID, host, targetIP, zoneID)
				}
				break
			}
		}
		if !exists {
			n.upsertRecord(http.MethodPost, "", host, targetIP, zoneID)
		}
	}

	// Process Safe Deletions (Cleanup)
	if cleanup {
		for _, rec := range currentRecords {
			// If not active and not explicitly ignored by the override label
			if !activeHosts[rec.Name] && !ignoredHosts[rec.Name] {
				// SAFETY LOCK: Only delete if the record is actively pointing to our target IP
				if rec.Content == targetIP && rec.Type == "A" {
					n.deleteRecord(rec.ID, rec.Name, zoneID)
				}
			}
		}
	}

	return nil
}

// upsertRecord handles the physical POST/PUT request to the NetBird API to create or update a record.
func (n *NetbirdProvider) upsertRecord(method, recordID, host, ip, zoneID string) {
	if n.cfg.DryRun {
		action := "Create"
		if method == http.MethodPut {
			action = "Update"
		}
		slog.Info("[DRY RUN] Would sync NetBird record", "action", action, "host", host, "target", ip)
		return
	}

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

// deleteRecord handles the DELETE request to remove an orphaned record.
func (n *NetbirdProvider) deleteRecord(recordID, host, zoneID string) {
	if n.cfg.DryRun {
		slog.Info("[DRY RUN] Would delete orphaned NetBird record", "host", host)
		return
	}

	url := fmt.Sprintf("%s/dns/zones/%s/records/%s", n.cfg.Netbird.APIURL, zoneID, recordID)

	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		slog.Error("Failed to create NetBird delete request", "host", host, "error", err)
		return
	}
	req.Header.Add("Authorization", "Token "+n.cfg.Netbird.Token)

	resp, err := n.client.Do(req)
	if err == nil && resp.StatusCode == http.StatusOK {
		slog.Info("Cleaned up orphaned NetBird record", "host", host)
	} else {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		slog.Error("Failed to delete NetBird record", "host", host, "status_code", status)
	}

	if resp != nil {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Debug("failed to close response body", "error", closeErr)
		}
	}
}
