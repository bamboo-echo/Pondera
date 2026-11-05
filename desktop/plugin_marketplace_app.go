package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"pondera/internal/config"
	"pondera/internal/pluginmarket"
)

type PluginMarketplaceSourceView struct {
	Source string `json:"source"`
	Kind   string `json:"kind"`
}

type PluginMarketplaceEntryView struct {
	Name             string   `json:"name"`
	Description      string   `json:"description,omitempty"`
	Author           string   `json:"author,omitempty"`
	AuthorURL        string   `json:"authorUrl,omitempty"`
	Version          string   `json:"version,omitempty"`
	Category         string   `json:"category"`
	Domain           string   `json:"domain,omitempty"`
	Icon             string   `json:"icon,omitempty"`
	Source           string   `json:"source,omitempty"`
	SHA              string   `json:"sha,omitempty"`
	InstallSource    string   `json:"installSource,omitempty"`
	Featured         bool     `json:"featured,omitempty"`
	Permissions      []string `json:"permissions,omitempty"`
	Installed        bool     `json:"installed"`
	InstalledVersion string   `json:"installedVersion,omitempty"`
}

type PluginMarketplaceView struct {
	Name       string                       `json:"name,omitempty"`
	Source     string                       `json:"source,omitempty"`
	SourceKind string                       `json:"sourceKind,omitempty"`
	Configured bool                         `json:"configured"`
	Entries    []PluginMarketplaceEntryView `json:"entries"`
	Cached     bool                         `json:"cached"`
	Offline    bool                         `json:"offline"`
	Warning    string                       `json:"warning,omitempty"`
	FetchedAt  string                       `json:"fetchedAt,omitempty"`
}

func pluginMarketplaceCachePath() string {
	if dir := config.CacheDir(); dir != "" {
		return filepath.Join(dir, "plugin-marketplace-index.json")
	}
	return ""
}

// PluginMarketplaceSource returns the configured index source for the settings
// marketplace page. It reads local state only; no network access.
func (a *App) PluginMarketplaceSource() PluginMarketplaceSourceView {
	source := pluginmarket.LoadSource()
	return PluginMarketplaceSourceView{Source: source, Kind: pluginmarket.SourceKind(source)}
}

// SetPluginMarketplaceSource persists the index source. An empty value clears
// the setting; the panel then shows its setup state.
func (a *App) SetPluginMarketplaceSource(source string) error {
	return pluginmarket.SaveSource(strings.TrimSpace(source))
}

// PluginMarketplace reads the configured plugin marketplace index in the
// desktop host. The renderer never fetches the index itself; the host applies
// ETag/Last-Modified revalidation and falls back to the last cached index with
// explicit cached/offline markers when the read fails.
func (a *App) PluginMarketplace() (PluginMarketplaceView, error) {
	source := pluginmarket.LoadSource()
	if source == "" {
		return PluginMarketplaceView{Configured: false, Entries: []PluginMarketplaceEntryView{}}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client := pluginmarket.New(source, pluginMarketplaceCachePath())
	view, err := client.Load(ctx)
	if err != nil {
		if errors.Is(err, pluginmarket.ErrSourceUnconfigured) {
			return PluginMarketplaceView{Configured: false, Entries: []PluginMarketplaceEntryView{}}, nil
		}
		return PluginMarketplaceView{Configured: true, Source: source, SourceKind: pluginmarket.SourceKind(source), Entries: []PluginMarketplaceEntryView{}}, err
	}
	return a.pluginMarketplaceView(view), nil
}

func (a *App) pluginMarketplaceView(view pluginmarket.View) PluginMarketplaceView {
	installed := make(map[string]string)
	for _, plugin := range a.Plugins() {
		installed[plugin.Name] = plugin.Version
	}
	entries := make([]PluginMarketplaceEntryView, 0, len(view.Entries))
	for _, entry := range view.Entries {
		version, ok := installed[entry.Name]
		entries = append(entries, PluginMarketplaceEntryView{
			Name:             entry.Name,
			Description:      entry.Description,
			Author:           entry.Author,
			AuthorURL:        entry.AuthorURL,
			Version:          entry.Version,
			Category:         entry.Category,
			Domain:           entry.Domain,
			Icon:             entry.Icon,
			Source:           entry.Source,
			SHA:              entry.SHA,
			InstallSource:    entry.InstallSource,
			Featured:         entry.Featured,
			Permissions:      append([]string(nil), entry.Permissions...),
			Installed:        ok,
			InstalledVersion: version,
		})
	}
	fetchedAt := ""
	if !view.FetchedAt.IsZero() {
		fetchedAt = view.FetchedAt.Format(time.RFC3339)
	}
	return PluginMarketplaceView{
		Name:       view.Name,
		Source:     view.Source,
		SourceKind: view.SourceKind,
		Configured: true,
		Entries:    entries,
		Cached:     view.Cached,
		Offline:    view.Offline,
		Warning:    view.Warning,
		FetchedAt:  fetchedAt,
	}
}
