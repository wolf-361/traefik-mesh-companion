package kuma

import (
	"context"
	"log/slog"
	"strings"

	kumaClient "github.com/breml/go-uptime-kuma-client"
	"github.com/breml/go-uptime-kuma-client/tag"
)

// TagDef is our internal representation for UI tags
type TagDef struct {
	Name  string
	Color string
}

type TagManager struct {
	client    *kumaClient.Client
	cfg       *Config
	tagsCache map[string]int64
}

func NewTagManager(client *kumaClient.Client, cfg *Config) *TagManager {
	return &TagManager{
		client:    client,
		cfg:       cfg,
		tagsCache: make(map[string]int64),
	}
}

// SyncState populates the local cache of tags from Uptime Kuma
func (m *TagManager) SyncState(ctx context.Context) error {
	slog.Info("Syncing tags from Uptime Kuma...")
	tags, err := m.client.GetTags(ctx)
	if err != nil {
		return err
	}

	for _, t := range tags {
		m.tagsCache[t.Name] = t.ID
	}

	slog.Info("Tag cache synchronized", "tags", len(m.tagsCache))
	return nil
}

// ensureTag checks if a tag exists, and if not, creates it in Uptime Kuma
func (m *TagManager) ensureTag(ctx context.Context, name, color string) (int64, error) {
	if id, exists := m.tagsCache[name]; exists {
		return id, nil
	}

	slog.Info("Creating missing tag", "tag", name)
	newTag := tag.Tag{
		Name:  name,
		Color: color,
	}

	id, err := m.client.CreateTag(ctx, newTag)
	if err != nil {
		// If another node created it at the exact same millisecond, catch the uniqueness error safely
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return 0, err
		}
		return 0, err
	}

	m.tagsCache[name] = id
	return id, nil
}

// ProcessTags builds the tags, ensures they exist, and attaches them to the monitor
func (m *TagManager) ProcessTags(ctx context.Context, monitorID int64, labels map[string]string) {
	desiredTags := m.buildTagDefs(labels)

	for _, tDef := range desiredTags {
		tagID, err := m.ensureTag(ctx, tDef.Name, tDef.Color)
		if err != nil || tagID == 0 {
			continue
		}

		// Attach tag to monitor
		_, err = m.client.AddMonitorTag(ctx, tagID, monitorID, "")
		if err != nil {
			// Ignore errors if the tag is already attached to this monitor (standard SQLite constraint)
			errStr := strings.ToLower(err.Error())
			if !strings.Contains(errStr, "unique constraint") && !strings.Contains(errStr, "already") {
				slog.Warn("Failed to attach tag to monitor", "tag", tDef.Name, "monitor_id", monitorID, "error", err)
			}
		}
	}
}

// buildTagDefs parses default tags, container labels, and color overrides
func (m *TagManager) buildTagDefs(labels map[string]string) []TagDef {
	var tags []TagDef
	tagTracker := make(map[string]bool)

	processTag := func(rawTag string) {
		rawTag = strings.TrimSpace(rawTag)
		if rawTag == "" {
			return
		}

		tagName := rawTag
		tagColor := ""

		parts := strings.SplitN(rawTag, ":", 2)
		if len(parts) == 2 {
			tagName = strings.TrimSpace(parts[0])
			tagColor = strings.TrimSpace(parts[1])
		}

		if !tagTracker[tagName] {
			tagTracker[tagName] = true

			if tagColor == "" {
				tagColor = getAutoColor(tagName)
			}

			tags = append(tags, TagDef{
				Name:  tagName,
				Color: tagColor,
			})
		}
	}

	for _, t := range m.cfg.DefaultTags {
		processTag(t)
	}

	if val := labels["mesh.kuma.tags"]; val != "" {
		for _, t := range strings.Split(val, ",") {
			processTag(t)
		}
	}

	return tags
}

// getAutoColor assigns a consistent, repeatable color based on the tag's text
func getAutoColor(tag string) string {
	colors := []string{
		"#ef4444", "#f97316", "#f59e0b", "#eab308", "#84cc16", "#22c55e",
		"#10b981", "#14b8a6", "#06b6d4", "#0ea5e9", "#3b82f6", "#6366f1",
		"#8b5cf6", "#a855f7", "#d946ef", "#ec4899", "#f43f5e",
		"#dc2626", "#ea580c", "#d97706", "#ca8a04", "#65a30d", "#16a34a",
		"#059669", "#0d9488", "#0891b2", "#0284c7", "#2563eb", "#4f46e5",
		"#7c3aed", "#9333ea", "#c026d3", "#db2777", "#e11d48",
	}

	hash := 5381
	for _, char := range tag {
		hash = ((hash << 5) + hash) + int(char)
	}
	if hash < 0 {
		hash = -hash
	}
	return colors[hash%len(colors)]
}