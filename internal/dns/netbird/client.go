package netbird

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/wolf-361/traefik-mesh-companion/internal/config"
	"github.com/wolf-361/traefik-mesh-companion/internal/core"
)

// Ensure Client implements the core.Processor interface at compile time
var _ core.Processor = (*Client)(nil)

type Client struct {
	nbCfg       *Config
	pipelineCfg *config.Pipeline
	exec        *core.Executor

	httpClient *http.Client
	zoneMap    map[string]string

	filterRegex *regexp.Regexp
	hostRegex   *regexp.Regexp

	lastHosts   map[string]bool
	lastIgnored map[string]bool
}

// New initializes the client. It requires the pipeline instructions and the global executor.
func New(pipelineCfg *config.Pipeline, exec *core.Executor) *Client {
	nbCfg := LoadConfig()
	if nbCfg == nil {
		slog.Debug("NetBird configuration missing, skipping initialization")
		return nil
	}

	c := &Client{
		nbCfg:       nbCfg,
		pipelineCfg: pipelineCfg,
		exec:        exec,
		httpClient:  &http.Client{Timeout: 10 * time.Second},
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

func (c *Client) Name() string { return "NetBird" }

func (c *Client) Init() error {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/dns/zones", c.nbCfg.APIURL), nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Add("Authorization", "Token "+c.nbCfg.Token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to NetBird API: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Debug("failed to close response body", "error", closeErr)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("netbird API returned status %d", resp.StatusCode)
	}

	var zones []Zone
	if err := json.NewDecoder(resp.Body).Decode(&zones); err != nil {
		return fmt.Errorf("failed to decode zones JSON: %w", err)
	}

	for _, z := range zones {
		c.zoneMap[z.Name] = z.ID
	}

	slog.Info("Initialized NetBird Provider", "discovered_zones", len(c.zoneMap))
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
		err := c.Sync(activeHosts, ignoredHosts, c.nbCfg.Target, c.pipelineCfg.Cleanup)
		c.lastHosts = activeHosts
		c.lastIgnored = ignoredHosts
		return err
	}

	return nil
}

func (c *Client) Sync(activeHosts map[string]bool, ignoredHosts map[string]bool, targetIP string, cleanup bool) error {
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
			for zoneName, zoneID := range c.zoneMap {
				if host == zoneName || strings.HasSuffix(host, "."+zoneName) {
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
				slog.Warn("Skipping host: no matching NetBird zone found", "host", host)
			}
		}
	}

	bucketHosts(activeHosts, activeByZone)
	bucketHosts(ignoredHosts, ignoredByZone)

	for zoneID := range zonesToSync {
		if err := c.syncZone(zoneID, activeByZone[zoneID], ignoredByZone[zoneID], targetIP, cleanup); err != nil {
			slog.Error("Failed to sync NetBird zone", "zone_id", zoneID, "error", err)
		}
	}

	return nil
}

func (c *Client) syncZone(zoneID string, activeHosts map[string]bool, ignoredHosts map[string]bool, targetIP string, cleanup bool) error {
	url := fmt.Sprintf("%s/dns/zones/%s/records", c.nbCfg.APIURL, zoneID)
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Add("Authorization", "Token "+c.nbCfg.Token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Debug("failed to close response body", "error", closeErr)
		}
	}()

	var currentRecords []Record
	if err := json.NewDecoder(resp.Body).Decode(&currentRecords); err != nil {
		return err
	}

	for host := range activeHosts {
		exists := false
		for _, rec := range currentRecords {
			if rec.Name == host {
				exists = true
				if rec.Content != targetIP {
					c.upsertRecord(http.MethodPut, rec.ID, host, targetIP, zoneID)
				}
				break
			}
		}
		if !exists {
			c.upsertRecord(http.MethodPost, "", host, targetIP, zoneID)
		}
	}

	if cleanup {
		for _, rec := range currentRecords {
			if !activeHosts[rec.Name] && !ignoredHosts[rec.Name] {
				if rec.Content == targetIP && rec.Type == "A" {
					c.deleteRecord(rec.ID, rec.Name, zoneID)
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

func (c *Client) upsertRecord(method, recordID, host, ip, zoneID string) {
	action := "create NetBird record"
	if method == http.MethodPut {
		action = "update NetBird record"
	}

	_ = c.exec.Run(action, func() error {
		rec := Record{Name: host, Type: "A", Content: ip, TTL: 300}
		body, _ := json.Marshal(rec)

		url := fmt.Sprintf("%s/dns/zones/%s/records", c.nbCfg.APIURL, zoneID)
		if method == http.MethodPut {
			url = fmt.Sprintf("%s/%s", url, recordID)
		}

		req, _ := http.NewRequest(method, url, bytes.NewBuffer(body))
		req.Header.Add("Authorization", "Token "+c.nbCfg.Token)
		req.Header.Add("Content-Type", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return err
		}
		defer func() {
			if closeErr := resp.Body.Close(); closeErr != nil {
				slog.Debug("failed to close response body", "error", closeErr)
			}
		}()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("API returned status %d", resp.StatusCode)
		}

		return nil
	}, "host", host, "target", ip)
}

func (c *Client) deleteRecord(recordID, host, zoneID string) {
	_ = c.exec.Run("delete NetBird record", func() error {
		url := fmt.Sprintf("%s/dns/zones/%s/records/%s", c.nbCfg.APIURL, zoneID, recordID)
		req, _ := http.NewRequest(http.MethodDelete, url, nil)
		req.Header.Add("Authorization", "Token "+c.nbCfg.Token)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return err
		}
		defer func() {
			if closeErr := resp.Body.Close(); closeErr != nil {
				slog.Debug("failed to close response body", "error", closeErr)
			}
		}()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("API returned status %d", resp.StatusCode)
		}

		return nil
	}, "host", host)
}
