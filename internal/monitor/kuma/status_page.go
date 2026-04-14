package kuma

import (
	"context"
	"log/slog"
	"strings"

	kumaClient "github.com/breml/go-uptime-kuma-client"
	"github.com/breml/go-uptime-kuma-client/statuspage"
)

type StatusPageManager struct {
	client          *kumaClient.Client
	cfg             *Config
	pageGroupsCache map[string][]statuspage.PublicGroup
}

func NewStatusPageManager(client *kumaClient.Client, cfg *Config) *StatusPageManager {
	return &StatusPageManager{
		client:          client,
		cfg:             cfg,
		pageGroupsCache: make(map[string][]statuspage.PublicGroup),
	}
}

// ProcessStatusPages handles the "Clean URL" logic and monitor attachment
func (m *StatusPageManager) ProcessStatusPages(ctx context.Context, monitorID int64, monitorName string, svcHosts []string, labels map[string]string) {
	if strings.ToLower(labels["mesh.kuma.hide_status"]) == "true" {
		return
	}

	pagesToAttach := m.getPagesFromLabels(labels)
	targetGroupName := m.getGroupName(labels)

	for pageSlug := range pagesToAttach {
		page, err := m.ensurePage(ctx, pageSlug)
		if err != nil || page == nil {
			continue
		}

		m.attachToGroup(ctx, page, monitorID, monitorName, targetGroupName)
	}
}

// SyncState populates the local cache and enforces global domain mapping
func (m *StatusPageManager) SyncState(ctx context.Context) error {
	slog.Info("Syncing status page groups from Uptime Kuma...")
	
	pages, err := m.client.GetStatusPages(ctx)
	if err != nil {
		return err
	}

	for _, p := range pages {
		fullPage, err := m.client.GetStatusPage(ctx, p.Slug)
		if err != nil {
			continue
		}

		// Hydrate the cache
		m.pageGroupsCache[p.Slug] = fullPage.PublicGroupList

		// Enforce domain mapping using the HYDRATED page object
		if p.Slug == m.cfg.GlobalStatusPageSlug && m.cfg.GlobalStatusPageDomain != "" {
			m.ensureDomainMapping(ctx, fullPage, m.cfg.GlobalStatusPageDomain)
		}
	}

	slog.Info("Status page cache synchronized", "pages", len(m.pageGroupsCache))
	return nil
}

func (m *StatusPageManager) ensurePage(ctx context.Context, slug string) (*statuspage.StatusPage, error) {
	page, err := m.client.GetStatusPage(ctx, slug)
	if err != nil {
		title := formatTitleFromSlug(slug)
		slog.Info("Creating missing status page", "slug", slug, "title", title)
		if err := m.client.AddStatusPage(ctx, title, slug); err != nil {
			return nil, err
		}
		return m.client.GetStatusPage(ctx, slug)
	}
	return page, nil
}

func (m *StatusPageManager) ensureDomainMapping(ctx context.Context, page *statuspage.StatusPage, host string) {
	// Safety: Strip protocol if user accidentally included it in env
	cleanHost := strings.TrimPrefix(strings.TrimPrefix(host, "https://"), "http://")

	// Check if domain is already in the list to avoid duplicate API calls
	found := false
	for _, d := range page.DomainNameList {
		if d == host {
			found = true
			break
		}
	}

	if !found {
		slog.Info("Enforcing domain mapping", "slug", page.Slug, "domain", cleanHost)
		page.DomainNameList = append(page.DomainNameList, cleanHost)
		
		// Force group restoration before save to prevent wipe-out
		if cached, ok := m.pageGroupsCache[page.Slug]; ok {
			page.PublicGroupList = cached
		}

		if _, err := m.client.SaveStatusPage(ctx, page); err != nil {
			slog.Error("Failed to map domain", "error", err)
		}
	}
}

func (m *StatusPageManager) attachToGroup(ctx context.Context, page *statuspage.StatusPage, monitorID int64, monitorName, groupName string) {
	if cached, ok := m.pageGroupsCache[page.Slug]; ok {
		page.PublicGroupList = cached
	}

	targetIdx := -1
	for i, g := range page.PublicGroupList {
		if strings.EqualFold(g.Name, groupName) {
			targetIdx = i
			break
		}
	}

	if targetIdx == -1 {
		page.PublicGroupList = append(page.PublicGroupList, statuspage.PublicGroup{
			Name:   groupName,
			Weight: len(page.PublicGroupList) + 1,
		})
		targetIdx = len(page.PublicGroupList) - 1
	}

	group := &page.PublicGroupList[targetIdx]
	for _, mon := range group.MonitorList {
		if mon.ID == monitorID {
			return // Already attached
		}
	}

	group.MonitorList = append(group.MonitorList, statuspage.PublicMonitor{ID: monitorID})
	updated, err := m.client.SaveStatusPage(ctx, page)
	if err != nil {
		slog.Error("Failed to attach monitor to status page", "monitor", monitorName, "error", err)
	} else {
		// Update the cache with the new group state (including IDs)
		m.pageGroupsCache[page.Slug] = updated
	}
}

func (m *StatusPageManager) getPagesFromLabels(labels map[string]string) map[string]bool {
	pages := make(map[string]bool)
	if m.cfg.GlobalStatusPageSlug != "" && m.cfg.GlobalStatusPageSlug != "none" {
		pages[m.cfg.GlobalStatusPageSlug] = true
	}
	if val, ok := labels["mesh.kuma.pages"]; ok {
		for _, p := range strings.Split(val, ",") {
			if clean := strings.TrimSpace(p); clean != "" {
				pages[clean] = true
			}
		}
	}
	return pages
}

func (m *StatusPageManager) getGroupName(labels map[string]string) string {
	if val, ok := labels["mesh.kuma.group"]; ok && val != "" {
		return val
	}
	return "Services"
}