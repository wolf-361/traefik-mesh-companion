package provider

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"

	"github.com/cloudflare/cloudflare-go"
	"github.com/wolf-361/traefik-mesh-companion/internal/config"
)

// CloudflareProvider manages DNS records via the official Cloudflare Go SDK.
type CloudflareProvider struct {
	api     *cloudflare.API
	cfg     *config.Config
	zoneMap map[string]string // Maps root domain (e.g., "wolf-361.ca") to its Zone ID
}

// Init sets up the Cloudflare SDK client and dynamically fetches all available zones.
func (c *CloudflareProvider) Init(cfg *config.Config) error {
	c.cfg = cfg
	c.zoneMap = make(map[string]string)

	if c.cfg.Cloudflare == nil {
		return fmt.Errorf("cloudflare configuration is missing but provider was initialized")
	}

	var err error
	// Initialize the official Cloudflare API client
	c.api, err = cloudflare.NewWithAPIToken(c.cfg.Cloudflare.Token)
	if err != nil {
		return fmt.Errorf("failed to initialize cloudflare client: %w", err)
	}

	// Fetch all zones associated with this API Token
	ctx := context.Background()
	zones, err := c.api.ListZones(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch zones from cloudflare: %w", err)
	}

	// Build the mapping dictionary in memory
	var loadedDomains []string
	for _, z := range zones {
		c.zoneMap[z.Name] = z.ID
		loadedDomains = append(loadedDomains, z.Name)
	}

	slog.Info("Initialized Cloudflare Provider with SDK", "discovered_zones", len(loadedDomains), "domains", loadedDomains)
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
	ctx := context.Background()
	for zoneID, hosts := range hostsByZone {
		if err := c.syncZone(ctx, zoneID, hosts, target, recordType); err != nil {
			slog.Error("Failed to sync Cloudflare zone", "zone_id", zoneID, "error", err)
		}
	}

	return nil
}

// syncZone handles the API logic for a specific Zone ID using the official SDK
func (c *CloudflareProvider) syncZone(ctx context.Context, zoneID string, hosts []string, target string, recordType string) error {
	rc := cloudflare.ZoneIdentifier(zoneID)

	// Fetch existing records for this zone
	records, _, err := c.api.ListDNSRecords(ctx, rc, cloudflare.ListDNSRecordsParams{})
	if err != nil {
		return fmt.Errorf("failed to fetch records: %w", err)
	}

	proxied := true

	for _, host := range hosts {
		exists := false
		for _, rec := range records {
			if rec.Name == host {
				exists = true
				if rec.Content != target || rec.Type != recordType {

					// Update existing record
					params := cloudflare.UpdateDNSRecordParams{
						ID:      rec.ID,
						Type:    recordType,
						Name:    host,
						Content: target,
						Proxied: &proxied,
						TTL:     1, // 1 means 'Auto' in Cloudflare
					}

					_, err := c.api.UpdateDNSRecord(ctx, rc, params)
					if err != nil {
						slog.Error("Failed to update Cloudflare record", "host", host, "error", err)
					} else {
						slog.Info("Synced Cloudflare record", "action", "Updated", "type", recordType, "host", host, "target", target)
					}
				}
				break
			}
		}

		if !exists {
			// Create new record
			params := cloudflare.CreateDNSRecordParams{
				Type:    recordType,
				Name:    host,
				Content: target,
				Proxied: &proxied,
				TTL:     1,
			}

			_, err := c.api.CreateDNSRecord(ctx, rc, params)
			if err != nil {
				slog.Error("Failed to create Cloudflare record", "host", host, "error", err)
			} else {
				slog.Info("Synced Cloudflare record", "action", "Created", "type", recordType, "host", host, "target", target)
			}
		}
	}

	return nil
}
