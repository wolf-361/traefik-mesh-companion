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
	for pageSlug, targetGroupName := range pagesToAttach {
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

// BindDomain acts as a public wrapper to safely attach domains to a specific status page
func (m *StatusPageManager) BindDomain(ctx context.Context, slug string, domain string) error {
	page, err := m.ensurePage(ctx, slug)
	if err != nil || page == nil {
		return err
	}
	m.ensureDomainMapping(ctx, page, domain)
	return nil
}

func (m *StatusPageManager) attachToGroup(ctx context.Context, page *statuspage.StatusPage, monitorID int64, monitorName, groupName string) {
	if cached, ok := m.pageGroupsCache[page.Slug]; ok {
		page.PublicGroupList = cached
	}

	changed := false
	targetIdx := -1

	// Find target index and PRUNE monitor from all other groups
    for i := range page.PublicGroupList {
        group := &page.PublicGroupList[i]
        
        if strings.EqualFold(group.Name, groupName) {
            targetIdx = i
            continue
        }

        // Remove monitor if it exists in a different group
        for j, mon := range group.MonitorList {
            if mon.ID == monitorID {
                group.MonitorList = append(group.MonitorList[:j], group.MonitorList[j+1:]...)
                changed = true
                break
            }
        }
    }

    // Create group if it doesn't exist
    if targetIdx == -1 {
        page.PublicGroupList = append(page.PublicGroupList, statuspage.PublicGroup{
            Name:   groupName,
            Weight: len(page.PublicGroupList) + 1,
        })
        targetIdx = len(page.PublicGroupList) - 1
        changed = true
    }

    // Add to target group if not already there
    existsInTarget := false
    for _, mon := range page.PublicGroupList[targetIdx].MonitorList {
        if mon.ID == monitorID {
            existsInTarget = true
            break
        }
    }

    if !existsInTarget {
        page.PublicGroupList[targetIdx].MonitorList = append(page.PublicGroupList[targetIdx].MonitorList, statuspage.PublicMonitor{ID: monitorID})
        changed = true
    }

    if changed {
        updated, err := m.client.SaveStatusPage(ctx, page)
        if err != nil {
            slog.Error("Failed to update status page layout", "page", page.Slug, "error", err)
        } else {
            m.pageGroupsCache[page.Slug] = updated
        }
    }
}

func (m *StatusPageManager) getPagesFromLabels(labels map[string]string) map[string]string {
	pages := make(map[string]string)
	
	// Fallback group if none is specified
	defaultGroup := "Services"
	if val, ok := labels["mesh.kuma.group"]; ok && val != "" {
		defaultGroup = strings.TrimSpace(val)
	}

	//  Add the Global Page (using the default group)
	if m.cfg.GlobalStatusPageSlug != "" && m.cfg.GlobalStatusPageSlug != "none" {
		pages[m.cfg.GlobalStatusPageSlug] = defaultGroup
	}

	// Parse specific pages (Format: "slug:Group Name" or just "slug")
	if val, ok := labels["mesh.kuma.pages"]; ok {
		for _, p := range strings.Split(val, ",") {
			clean := strings.TrimSpace(p)
			if clean == "" {
				continue
			}

			// Check for the colon separator
			parts := strings.SplitN(clean, ":", 2)
			slug := strings.TrimSpace(parts[0])

			if len(parts) == 2 {
				// They specified a group for this specific page!
				pages[slug] = strings.TrimSpace(parts[1])
			} else {
				// No specific group, use the default
				pages[slug] = defaultGroup
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