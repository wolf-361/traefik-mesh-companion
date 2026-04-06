package provider

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"reflect"
	"regexp"
	"strings"

	"github.com/cloudflare/cloudflare-go"
	"github.com/wolf-361/traefik-mesh-companion/internal/config"
	"github.com/wolf-361/traefik-mesh-companion/internal/mesh"
)

// Ensure CloudflareProvider implements the mesh.Processor interface at compile time
var _ mesh.Processor = (*CloudflareProvider)(nil)

type CloudflareProvider struct {
	api     *cloudflare.API
	cfg     *config.Config
	zoneMap map[string]string // Maps root domain to its Zone ID

	filterRegex *regexp.Regexp
	hostRegex   *regexp.Regexp

	// State caching to prevent API spam
	lastHosts   map[string]bool
	lastIgnored map[string]bool
}

func (c *CloudflareProvider) Name() string { return "Cloudflare" }

func (c *CloudflareProvider) Init(cfg *config.Config) error {
	c.cfg = cfg
	c.zoneMap = make(map[string]string)
	c.hostRegex = regexp.MustCompile(`Host\([` + "`" + `'](.+?)[` + "`" + `']\)`)

	if c.cfg.Cloudflare == nil {
		return fmt.Errorf("cloudflare configuration is missing but provider was initialized")
	}

	// Compile the external filter label regex (e.g. traefik.http.routers.*.entrypoints)
	if cfg.External.FilterLabel != "" {
		escaped := regexp.QuoteMeta(cfg.External.FilterLabel)
		pattern := "^" + strings.ReplaceAll(escaped, "\\*", "([^.]+)") + "$"
		c.filterRegex = regexp.MustCompile(pattern)
	}

	var err error
	c.api, err = cloudflare.NewWithAPIToken(c.cfg.Cloudflare.Token)
	if err != nil {
		return fmt.Errorf("failed to initialize cloudflare client: %w", err)
	}

	// Fetch available zones
	ctx := context.Background()
	zones, err := c.api.ListZones(ctx)
	if err != nil {
		return fmt.Errorf("failed to fetch zones from cloudflare: %w", err)
	}

	for _, z := range zones {
		c.zoneMap[z.Name] = z.ID
	}

	slog.Info("Initialized Cloudflare Provider", "discovered_zones", len(c.zoneMap))
	return nil
}

// Process satisfies the mesh.Processor interface.
func (c *CloudflareProvider) Process(services []mesh.Service) error {
	activeHosts := make(map[string]bool)
	ignoredHosts := make(map[string]bool)

	for _, svc := range services {
		// Group labels by router for this service
		type routerData struct {
			rule      string
			managed   string
			filterVal string
		}
		routers := make(map[string]*routerData)

		getRouter := func(name string) *routerData {
			if _, exists := routers[name]; !exists {
				routers[name] = &routerData{managed: "true"}
			}
			return routers[name]
		}

		for key, val := range svc.Labels {
			if strings.HasPrefix(key, "traefik.http.routers.") && strings.HasSuffix(key, ".rule") {
				name := strings.TrimSuffix(strings.TrimPrefix(key, "traefik.http.routers."), ".rule")
				getRouter(name).rule = val
			}
			if strings.HasPrefix(key, "traefik.http.routers.") && strings.HasSuffix(key, ".mesh.managed") {
				name := strings.TrimSuffix(strings.TrimPrefix(key, "traefik.http.routers."), ".mesh.managed")
				getRouter(name).managed = val
			}
			if c.filterRegex != nil {
				if matches := c.filterRegex.FindStringSubmatch(key); len(matches) > 1 {
					getRouter(matches[1]).filterVal = val
				}
			}
		}

		// Evaluate extracted routers against External filter criteria
		for _, data := range routers {
			if data.rule == "" {
				continue
			}

			if c.matchFilter(data.filterVal, c.cfg.External.FilterValue) {
				if data.managed != "false" {
					c.extractDomains(data.rule, activeHosts)
				} else {
					c.extractDomains(data.rule, ignoredHosts)
				}
			}
		}
	}

	// Only sync if the state has actually changed
	if !reflect.DeepEqual(c.lastHosts, activeHosts) || !reflect.DeepEqual(c.lastIgnored, ignoredHosts) {
		err := c.Sync(activeHosts, ignoredHosts, c.cfg.Cloudflare.Target, c.cfg.External.Cleanup)
		c.lastHosts = activeHosts
		c.lastIgnored = ignoredHosts
		return err
	}

	return nil
}

func (c *CloudflareProvider) Sync(activeHosts map[string]bool, ignoredHosts map[string]bool, target string, cleanup bool) error {
	recordType := "CNAME"
	if net.ParseIP(target) != nil {
		recordType = "A"
	}

	activeByZone := make(map[string]map[string]bool)
	ignoredByZone := make(map[string]map[string]bool)
	zonesToSync := make(map[string]bool)

	if cleanup {
		for _, zoneID := range c.zoneMap {
			zonesToSync[zoneID] = true
		}
	}

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
				slog.Warn("Skipping host: no matching Cloudflare zone found", "host", host)
			}
		}
	}

	bucketHosts(activeHosts, activeByZone)
	bucketHosts(ignoredHosts, ignoredByZone)

	ctx := context.Background()
	for zoneID := range zonesToSync {
		if err := c.syncZone(ctx, zoneID, activeByZone[zoneID], ignoredByZone[zoneID], target, recordType, cleanup); err != nil {
			slog.Error("Failed to sync Cloudflare zone", "zone_id", zoneID, "error", err)
		}
	}

	return nil
}

func (c *CloudflareProvider) syncZone(ctx context.Context, zoneID string, activeHosts map[string]bool, ignoredHosts map[string]bool, target string, recordType string, cleanup bool) error {
	rc := cloudflare.ZoneIdentifier(zoneID)
	records, _, err := c.api.ListDNSRecords(ctx, rc, cloudflare.ListDNSRecordsParams{})
	if err != nil {
		return err
	}

	proxied := true

	for host := range activeHosts {
		exists := false
		for _, rec := range records {
			if rec.Name == host {
				exists = true
				if rec.Content != target || rec.Type != recordType {
					if c.cfg.DryRun {
						slog.Info("[DRY RUN] Would update Cloudflare record", "host", host, "target", target)
					} else {
						params := cloudflare.UpdateDNSRecordParams{
							ID: rec.ID, Type: recordType, Name: host, Content: target, Proxied: &proxied, TTL: 1,
						}
						_, err := c.api.UpdateDNSRecord(ctx, rc, params)
						if err != nil {
							slog.Error("Failed to update Cloudflare record", "host", host, "error", err)
						} else {
							slog.Info("Updated Cloudflare record", "host", host)
						}
					}
				}
				break
			}
		}

		if !exists {
			if c.cfg.DryRun {
				slog.Info("[DRY RUN] Would create Cloudflare record", "host", host, "target", target)
			} else {
				params := cloudflare.CreateDNSRecordParams{
					Type: recordType, Name: host, Content: target, Proxied: &proxied, TTL: 1,
				}
				_, err := c.api.CreateDNSRecord(ctx, rc, params)
				if err != nil {
					slog.Error("Failed to create Cloudflare record", "host", host, "error", err)
				} else {
					slog.Info("Created Cloudflare record", "host", host)
				}
			}
		}
	}

	if cleanup {
		for _, rec := range records {
			if !activeHosts[rec.Name] && !ignoredHosts[rec.Name] {
				if rec.Content == target && rec.Type == recordType {
					if c.cfg.DryRun {
						slog.Info("[DRY RUN] Would delete Cloudflare record", "host", rec.Name)
					} else {
						err := c.api.DeleteDNSRecord(ctx, rc, rec.ID)
						if err != nil {
							slog.Error("Failed to delete Cloudflare record", "host", rec.Name, "error", err)
						} else {
							slog.Info("Cleaned up Cloudflare record", "host", rec.Name)
						}
					}
				}
			}
		}
	}

	return nil
}

// --- Helpers ---

func (c *CloudflareProvider) extractDomains(rule string, targetMap map[string]bool) {
	matches := c.hostRegex.FindAllStringSubmatch(rule, -1)
	for _, match := range matches {
		if len(match) > 1 {
			domains := strings.Split(match[1], ",")
			for _, domain := range domains {
				cleanDomain := strings.Trim(strings.TrimSpace(domain), "`'\"")
				if cleanDomain != "" {
					targetMap[cleanDomain] = true
				}
			}
		}
	}
}

func (c *CloudflareProvider) matchFilter(labelValue string, envFilter string) bool {
	for _, f := range strings.Split(envFilter, ",") {
		cleanFilter := strings.TrimSpace(f)
		if cleanFilter != "" && strings.Contains(labelValue, cleanFilter) {
			return true
		}
	}
	return false
}
