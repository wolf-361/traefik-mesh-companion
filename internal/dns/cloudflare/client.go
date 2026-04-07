package cloudflare

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
	"github.com/wolf-361/traefik-mesh-companion/internal/core"
)

// Ensure Client implements the core.Processor interface at compile time
var _ core.Processor = (*Client)(nil)

type Client struct {
	cfCfg       *Config
	pipelineCfg *config.Pipeline
	exec        *core.Executor

	api     *cloudflare.API
	zoneMap map[string]string

	filterRegex *regexp.Regexp
	hostRegex   *regexp.Regexp

	lastHosts   map[string]bool
	lastIgnored map[string]bool
}

// New initializes the client. It requires the pipeline instructions and the global executor.
func New(pipelineCfg *config.Pipeline, exec *core.Executor) *Client {
	cfCfg := LoadConfig()
	if cfCfg == nil {
		slog.Debug("Cloudflare configuration missing, skipping initialization")
		return nil
	}

	c := &Client{
		cfCfg:       cfCfg,
		pipelineCfg: pipelineCfg,
		exec:        exec,
		zoneMap:     make(map[string]string),
		hostRegex:   regexp.MustCompile(`Host\([` + "`" + `'](.+?)[` + "`" + `']\)`),
	}

	if pipelineCfg.FilterLabel != "" {
		escaped := regexp.QuoteMeta(pipelineCfg.FilterLabel)
		pattern := "^" + strings.ReplaceAll(escaped, "\\*", "([^.]+)") + "$"
		c.filterRegex = regexp.MustCompile(pattern)
	}

	return c
}

func (c *Client) Name() string { return "Cloudflare" }

func (c *Client) Init() error {
	var err error
	c.api, err = cloudflare.NewWithAPIToken(c.cfCfg.Token)
	if err != nil {
		return fmt.Errorf("failed to initialize cloudflare client: %w", err)
	}

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

func (c *Client) Process(services []core.Service) error {
	activeHosts := make(map[string]bool)
	ignoredHosts := make(map[string]bool)

	for _, svc := range services {
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

		for _, data := range routers {
			if data.rule == "" {
				continue
			}

			if c.matchFilter(data.filterVal, c.pipelineCfg.FilterValue) {
				if data.managed != "false" {
					c.extractDomains(data.rule, activeHosts)
				} else {
					c.extractDomains(data.rule, ignoredHosts)
				}
			}
		}
	}

	if !reflect.DeepEqual(c.lastHosts, activeHosts) || !reflect.DeepEqual(c.lastIgnored, ignoredHosts) {
		err := c.Sync(activeHosts, ignoredHosts, c.cfCfg.Target, c.pipelineCfg.Cleanup)
		c.lastHosts = activeHosts
		c.lastIgnored = ignoredHosts
		return err
	}

	return nil
}

func (c *Client) Sync(activeHosts map[string]bool, ignoredHosts map[string]bool, target string, cleanup bool) error {
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

func (c *Client) syncZone(ctx context.Context, zoneID string, activeHosts map[string]bool, ignoredHosts map[string]bool, target string, recordType string, cleanup bool) error {
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
					_ = c.exec.Run("update Cloudflare record", func() error {
						params := cloudflare.UpdateDNSRecordParams{
							ID: rec.ID, Type: recordType, Name: host, Content: target, Proxied: &proxied, TTL: 1,
						}
						_, err := c.api.UpdateDNSRecord(ctx, rc, params)
						return err
					}, "host", host, "target", target)
				}
				break
			}
		}

		if !exists {
			_ = c.exec.Run("create Cloudflare record", func() error {
				params := cloudflare.CreateDNSRecordParams{
					Type: recordType, Name: host, Content: target, Proxied: &proxied, TTL: 1,
				}
				_, err := c.api.CreateDNSRecord(ctx, rc, params)
				return err
			}, "host", host, "target", target)
		}
	}

	if cleanup {
		for _, rec := range records {
			if !activeHosts[rec.Name] && !ignoredHosts[rec.Name] {
				if rec.Content == target && rec.Type == recordType {
					_ = c.exec.Run("delete Cloudflare record", func() error {
						return c.api.DeleteDNSRecord(ctx, rc, rec.ID)
					}, "host", rec.Name)
				}
			}
		}
	}

	return nil
}

// --- Helpers ---

func (c *Client) extractDomains(rule string, targetMap map[string]bool) {
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

func (c *Client) matchFilter(labelValue string, envFilter string) bool {
	for _, f := range strings.Split(envFilter, ",") {
		cleanFilter := strings.TrimSpace(f)
		if cleanFilter != "" && strings.Contains(labelValue, cleanFilter) {
			return true
		}
	}
	return false
}
