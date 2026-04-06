package provider

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
	"github.com/wolf-361/traefik-mesh-companion/internal/mesh"
)

// Ensure NetbirdProvider implements the mesh.Processor interface at compile time
var _ mesh.Processor = (*NetbirdProvider)(nil)

type NetbirdProvider struct {
	client  *http.Client
	cfg     *config.Config
	zoneMap map[string]string // Mapping of zone names to IDs

	filterRegex *regexp.Regexp
	hostRegex   *regexp.Regexp

	// State caching to prevent API spam
	lastHosts   map[string]bool
	lastIgnored map[string]bool
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

func (n *NetbirdProvider) Name() string { return "NetBird" }

func (n *NetbirdProvider) Init(cfg *config.Config) error {
	n.cfg = cfg
	n.client = &http.Client{Timeout: 10 * time.Second}
	n.zoneMap = make(map[string]string)
	n.hostRegex = regexp.MustCompile(`Host\([` + "`" + `'](.+?)[` + "`" + `']\)`)

	if n.cfg.Netbird == nil {
		return fmt.Errorf("netbird configuration is missing but provider was initialized")
	}

	// Compile the internal filter label regex
	if cfg.Internal.FilterLabel != "" {
		escaped := regexp.QuoteMeta(cfg.Internal.FilterLabel)
		pattern := "^" + strings.ReplaceAll(escaped, "\\*", "([^.]+)") + "$"
		n.filterRegex = regexp.MustCompile(pattern)
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
		return fmt.Errorf("netbird API returned status %d", resp.StatusCode)
	}

	var zones []netbirdZone
	if err := json.NewDecoder(resp.Body).Decode(&zones); err != nil {
		return fmt.Errorf("failed to decode zones JSON: %w", err)
	}

	for _, z := range zones {
		n.zoneMap[z.Name] = z.ID
	}

	slog.Info("Initialized NetBird Provider", "discovered_zones", len(n.zoneMap))
	return nil
}

// Process satisfies the mesh.Processor interface.
func (n *NetbirdProvider) Process(services []mesh.Service) error {
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
			if n.filterRegex != nil {
				if matches := n.filterRegex.FindStringSubmatch(key); len(matches) > 1 {
					getRouter(matches[1]).filterVal = val
				}
			}
		}

		for _, data := range routers {
			if data.rule == "" {
				continue
			}

			if n.matchFilter(data.filterVal, n.cfg.Internal.FilterValue) {
				if data.managed != "false" {
					n.extractDomains(data.rule, activeHosts)
				} else {
					n.extractDomains(data.rule, ignoredHosts)
				}
			}
		}
	}

	// Dispatch ONLY if the state changed
	if !reflect.DeepEqual(n.lastHosts, activeHosts) || !reflect.DeepEqual(n.lastIgnored, ignoredHosts) {
		err := n.Sync(activeHosts, ignoredHosts, n.cfg.Netbird.Target, n.cfg.Internal.Cleanup)
		n.lastHosts = activeHosts
		n.lastIgnored = ignoredHosts
		return err
	}

	return nil
}

func (n *NetbirdProvider) Sync(activeHosts map[string]bool, ignoredHosts map[string]bool, targetIP string, cleanup bool) error {
	activeByZone := make(map[string]map[string]bool)
	ignoredByZone := make(map[string]map[string]bool)
	zonesToSync := make(map[string]bool)

	if cleanup {
		for _, zoneID := range n.zoneMap {
			zonesToSync[zoneID] = true
		}
	}

	bucketHosts := func(hosts map[string]bool, targetBucket map[string]map[string]bool) {
		for host := range hosts {
			matched := false
			for zoneName, zoneID := range n.zoneMap {
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
		if err := n.syncZone(zoneID, activeByZone[zoneID], ignoredByZone[zoneID], targetIP, cleanup); err != nil {
			slog.Error("Failed to sync NetBird zone", "zone_id", zoneID, "error", err)
		}
	}

	return nil
}

func (n *NetbirdProvider) syncZone(zoneID string, activeHosts map[string]bool, ignoredHosts map[string]bool, targetIP string, cleanup bool) error {
	url := fmt.Sprintf("%s/dns/zones/%s/records", n.cfg.Netbird.APIURL, zoneID)
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Add("Authorization", "Token "+n.cfg.Netbird.Token)

	resp, err := n.client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Debug("failed to close response body", "error", closeErr)
		}
	}()

	var currentRecords []netbirdRecord
	if err := json.NewDecoder(resp.Body).Decode(&currentRecords); err != nil {
		return err
	}

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

	if cleanup {
		for _, rec := range currentRecords {
			if !activeHosts[rec.Name] && !ignoredHosts[rec.Name] {
				if rec.Content == targetIP && rec.Type == "A" {
					n.deleteRecord(rec.ID, rec.Name, zoneID)
				}
			}
		}
	}

	return nil
}

// --- Helpers ---

func (n *NetbirdProvider) extractDomains(rule string, targetMap map[string]bool) {
	matches := n.hostRegex.FindAllStringSubmatch(rule, -1)
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

func (n *NetbirdProvider) matchFilter(labelValue string, envFilter string) bool {
	for _, f := range strings.Split(envFilter, ",") {
		cleanFilter := strings.TrimSpace(f)
		if cleanFilter != "" && strings.Contains(labelValue, cleanFilter) {
			return true
		}
	}
	return false
}

func (n *NetbirdProvider) upsertRecord(method, recordID, host, ip, zoneID string) {
	if n.cfg.DryRun {
		slog.Info("[DRY RUN] Would sync NetBird record", "method", method, "host", host, "target", ip)
		return
	}

	rec := netbirdRecord{Name: host, Type: "A", Content: ip, TTL: 300}
	body, _ := json.Marshal(rec)

	url := fmt.Sprintf("%s/dns/zones/%s/records", n.cfg.Netbird.APIURL, zoneID)
	if method == http.MethodPut {
		url = fmt.Sprintf("%s/%s", url, recordID)
	}

	req, _ := http.NewRequest(method, url, bytes.NewBuffer(body))
	req.Header.Add("Authorization", "Token "+n.cfg.Netbird.Token)
	req.Header.Add("Content-Type", "application/json")

	resp, err := n.client.Do(req)
	if err == nil && resp.StatusCode == http.StatusOK {
		slog.Info("Synced NetBird record", "action", method, "host", host)
	} else {
		slog.Error("Failed to sync NetBird record", "host", host)
	}
	if resp != nil {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Debug("failed to close response body", "error", closeErr)
		}
	}
}

func (n *NetbirdProvider) deleteRecord(recordID, host, zoneID string) {
	if n.cfg.DryRun {
		slog.Info("[DRY RUN] Would delete NetBird record", "host", host)
		return
	}

	url := fmt.Sprintf("%s/dns/zones/%s/records/%s", n.cfg.Netbird.APIURL, zoneID, recordID)
	req, _ := http.NewRequest(http.MethodDelete, url, nil)
	req.Header.Add("Authorization", "Token "+n.cfg.Netbird.Token)

	resp, err := n.client.Do(req)
	if err == nil && resp.StatusCode == http.StatusOK {
		slog.Info("Cleaned up NetBird record", "host", host)
	}
	if resp != nil {
		if closeErr := resp.Body.Close(); closeErr != nil {
			slog.Debug("failed to close response body", "error", closeErr)
		}
	}
}
