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
func (c *CloudflareProvider) Sync(activeHosts map[string]bool, ignoredHosts map[string]bool, target string, cleanup bool) error {
	recordType := "CNAME"
	if net.ParseIP(target) != nil { // Use A records for IP's
		recordType = "A"
	}

	activeByZone := make(map[string]map[string]bool)
	ignoredByZone := make(map[string]map[string]bool)
	zonesToSync := make(map[string]bool)

	// If cleanup is enabled, we must check ALL known zones to find orphans
	if cleanup {
		for _, zoneID := range c.zoneMap {
			zonesToSync[zoneID] = true
		}
	}

	// Helper to bucket hosts by their Cloudflare zone
	bucketHosts := func(hosts map[string]bool, targetBucket map[string]map[string]bool) {
		for host := range hosts {
			matched := false
			for domain, zoneID := range c.zoneMap {
				if host == domain || strings.HasSuffix(host, "."+domain) {
					if targetBucket[zoneID] == nil {
						targetBucket[zoneID] = make(map[string]bool)
					}
					targetBucket[zoneID][host] = true
					zonesToSync[zoneID] = true
					matched = true
					break
				}
			}
			if !matched {
				slog.Warn("Skipping host, no matching Cloudflare zone found for domain", "host", host)
			}
		}
	}

	bucketHosts(activeHosts, activeByZone)
	bucketHosts(ignoredHosts, ignoredByZone)

	// Sync each discovered zone independently
	ctx := context.Background()
	for zoneID := range zonesToSync {
		if err := c.syncZone(ctx, zoneID, activeByZone[zoneID], ignoredByZone[zoneID], target, recordType, cleanup); err != nil {
			slog.Error("Failed to sync Cloudflare zone", "zone_id", zoneID, "error", err)
		}
	}

	return nil
}

// syncZone handles the API logic for a specific Zone ID using the official SDK
func (c *CloudflareProvider) syncZone(ctx context.Context, zoneID string, activeHosts map[string]bool, ignoredHosts map[string]bool, target string, recordType string, cleanup bool) error {
	rc := cloudflare.ZoneIdentifier(zoneID)

	// Fetch existing records for this zone
	records, _, err := c.api.ListDNSRecords(ctx, rc, cloudflare.ListDNSRecordsParams{})
	if err != nil {
		return fmt.Errorf("failed to fetch records: %w", err)
	}

	proxied := true

	// Process Updates and Creations
	for host := range activeHosts {
		exists := false
		for _, rec := range records {
			if rec.Name == host {
				exists = true
				if rec.Content != target || rec.Type != recordType {
					if c.cfg.DryRun {
						slog.Info("[DRY RUN] Would update Cloudflare record", "host", host, "target", target, "type", recordType)
					} else {
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
				}
				break
			}
		}

		if !exists {
			if c.cfg.DryRun {
				slog.Info("[DRY RUN] Would create Cloudflare record", "host", host, "target", target, "type", recordType)
			} else {
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
	}

	// Process Safe Deletions (Cleanup)
	if cleanup {
		for _, rec := range records {
			if !activeHosts[rec.Name] && !ignoredHosts[rec.Name] {
				// SAFETY LOCK: Only delete if pointing to our specific target (UUID.cfargotunnel.com)
				if rec.Content == target && rec.Type == recordType {
					if c.cfg.DryRun {
						slog.Info("[DRY RUN] Would delete orphaned Cloudflare record", "host", rec.Name)
					} else {
						err := c.api.DeleteDNSRecord(ctx, rc, rec.ID)
						if err != nil {
							slog.Error("Failed to delete orphaned Cloudflare record", "host", rec.Name, "error", err)
						} else {
							slog.Info("Cleaned up orphaned Cloudflare record", "host", rec.Name)
						}
					}
				}
			}
		}
	}

	return nil
}
