// Package pluginmarket reads a plugin marketplace index (.claude-plugin/
// marketplace.json) from a local directory, a file:// URL, or an HTTP(S) URL.
//
// It is called only from the explicit settings marketplace page: boot and tool
// discovery never touch the network. Remote responses are cached with
// ETag/Last-Modified validators; when a network read fails the last good index
// is returned with a cached/offline marker so the page stays useful without
// pretending stale data is live.
package pluginmarket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"pondera/internal/config"
	"pondera/internal/fileutil"
	"pondera/internal/pluginpkg"
)

const (
	// IndexFile is the Claude-compatible marketplace manifest path.
	IndexFile = ".claude-plugin/marketplace.json"

	maxIndexBytes = 8 << 20
	maxPlugins    = 64
)

// ErrSourceUnconfigured reports that no marketplace index source was set.
var ErrSourceUnconfigured = errors.New("plugin marketplace index source is not configured")

var fullGitSHA = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

// Entry is one installable plugin reduced to the fields the settings UI needs.
type Entry struct {
	Name          string   `json:"name"`
	Description   string   `json:"description,omitempty"`
	Author        string   `json:"author,omitempty"`
	AuthorURL     string   `json:"authorUrl,omitempty"`
	Version       string   `json:"version,omitempty"`
	Category      string   `json:"category"`
	Icon          string   `json:"icon,omitempty"`
	Source        string   `json:"source,omitempty"`
	SHA           string   `json:"sha,omitempty"`
	InstallSource string   `json:"installSource,omitempty"`
	Featured      bool     `json:"featured,omitempty"`
	Permissions   []string `json:"permissions,omitempty"`
}

// View is a parsed marketplace index plus its freshness markers.
type View struct {
	Name       string    `json:"name"`
	Source     string    `json:"source"`
	SourceKind string    `json:"sourceKind"`
	Entries    []Entry   `json:"entries"`
	Cached     bool      `json:"cached"`
	Offline    bool      `json:"offline"`
	Warning    string    `json:"warning,omitempty"`
	FetchedAt  time.Time `json:"fetchedAt,omitempty"`
}

// Client reads one marketplace index source.
type Client struct {
	Source    string
	HTTP      *http.Client
	CachePath string
	Now       func() time.Time
}

// New returns a client for source. cachePath may be empty to disable caching.
func New(source, cachePath string) *Client {
	return &Client{Source: source, CachePath: cachePath, Now: time.Now}
}

func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 20 * time.Second}
}

// Load fetches and parses the configured index. It returns an error for an
// unconfigured or malformed index; only transport/read failures fall back to
// the cache.
func (c *Client) Load(ctx context.Context) (View, error) {
	source := strings.TrimSpace(c.Source)
	if source == "" {
		return View{}, ErrSourceUnconfigured
	}
	if localPath, kind, ok := localPathFromSource(source); ok {
		return c.loadLocal(localPath, kind)
	}
	if isHTTPURL(source) {
		return c.loadRemote(ctx, source)
	}
	return c.loadLocal(source, "local-dir")
}

// SourceKind classifies a configured source for the settings UI.
func SourceKind(source string) string {
	source = strings.TrimSpace(source)
	if source == "" {
		return ""
	}
	if _, kind, ok := localPathFromSource(source); ok {
		return kind
	}
	if isHTTPURL(source) {
		return "http"
	}
	return "local-dir"
}

func isHTTPURL(source string) bool {
	u, err := url.Parse(source)
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return true
	}
	return false
}

// localPathFromSource resolves a local directory, a plain file path, or a
// file:// URL. kind is "local-dir" or "local-file".
func localPathFromSource(source string) (string, string, bool) {
	if isHTTPURL(source) {
		return "", "", false
	}
	// A Windows drive letter (C:\...) parses as a one-letter URL scheme, so the
	// file-URL branch must not consume it.
	if u, err := url.Parse(source); err == nil && strings.EqualFold(u.Scheme, "file") {
		p, pathErr := fileURLPath(u)
		if pathErr != nil {
			return "", "", false
		}
		if info, statErr := os.Stat(p); statErr == nil && info.IsDir() {
			return p, "local-dir", true
		}
		return p, "local-file", true
	}
	if info, statErr := os.Stat(source); statErr == nil {
		if info.IsDir() {
			return source, "local-dir", true
		}
		return source, "local-file", true
	}
	if strings.HasSuffix(strings.ToLower(source), ".json") {
		return source, "local-file", true
	}
	return source, "local-dir", true
}

func fileURLPath(u *url.URL) (string, error) {
	if !strings.EqualFold(u.Hostname(), "") && !strings.EqualFold(u.Hostname(), "localhost") {
		return "", fmt.Errorf("file URL host %q is not supported", u.Hostname())
	}
	p, err := url.PathUnescape(u.Path)
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" {
		p = strings.TrimPrefix(p, "/")
		// UNC path: file://server/share becomes //server/share.
		if strings.HasPrefix(p, "/") {
			p = "/" + p
		}
		p = filepath.FromSlash(p)
	}
	if p == "" {
		return "", fmt.Errorf("file URL has no path")
	}
	return p, nil
}

func (c *Client) loadLocal(sourcePath, kind string) (View, error) {
	indexPath, root, err := localIndexFiles(sourcePath, kind)
	if err != nil {
		return c.fallback(kind, sourcePath, err)
	}
	body, err := os.ReadFile(indexPath)
	if err != nil {
		return c.fallback(kind, sourcePath, fmt.Errorf("read %s: %w", indexPath, err))
	}
	view, err := parseIndex(body, func(source, pluginRoot string) (string, error) {
		return resolveLocalInstallSource(root, pluginRoot, source)
	})
	if err != nil {
		return View{}, err
	}
	view.Source = sourcePath
	view.SourceKind = kind
	view.FetchedAt = c.now()
	// Local reads have no validators; the cache is only a read-failure fallback
	// (for example an unmounted network directory).
	c.writeCache(sourcePath, cacheRecord{
		Source:    sourcePath,
		FetchedAt: view.FetchedAt,
		Name:      view.Name,
		Entries:   view.Entries,
	})
	return view, nil
}

func localIndexFiles(sourcePath, kind string) (indexPath, root string, err error) {
	info, statErr := os.Stat(sourcePath)
	if statErr == nil && info.IsDir() {
		return filepath.Join(sourcePath, filepath.FromSlash(IndexFile)), sourcePath, nil
	}
	if statErr == nil && !info.IsDir() {
		return sourcePath, marketplaceRootForFile(sourcePath), nil
	}
	if kind == "local-dir" || strings.EqualFold(filepath.Base(sourcePath), filepath.Base(IndexFile)) {
		return filepath.Join(sourcePath, filepath.FromSlash(IndexFile)), sourcePath, nil
	}
	return "", "", fmt.Errorf("marketplace source %s does not exist", sourcePath)
}

// marketplaceRootForFile maps .../.claude-plugin/marketplace.json to its repo
// root; any other file uses its own directory.
func marketplaceRootForFile(p string) string {
	dir := filepath.Dir(p)
	if strings.EqualFold(filepath.Base(dir), ".claude-plugin") {
		return filepath.Dir(dir)
	}
	return dir
}

func (c *Client) loadRemote(ctx context.Context, source string) (View, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return View{}, fmt.Errorf("marketplace index URL: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "pondera-plugin-marketplace/dev")
	if cache, ok := c.readCache(source); ok {
		if cache.ETag != "" {
			req.Header.Set("If-None-Match", cache.ETag)
		}
		if cache.LastModified != "" {
			req.Header.Set("If-Modified-Since", cache.LastModified)
		}
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return c.fallback("http", source, fmt.Errorf("fetch marketplace index: %w", err))
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		cache, ok := c.readCache(source)
		if !ok {
			return View{}, fmt.Errorf("marketplace index returned 304 but no cached copy exists")
		}
		return cachedView(cache, false, false, ""), nil
	}
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return c.fallback("http", source, fmt.Errorf("fetch marketplace index: http %d: %s", resp.StatusCode, strings.TrimSpace(string(body))))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxIndexBytes))
	if err != nil {
		return c.fallback("http", source, fmt.Errorf("read marketplace index: %w", err))
	}
	view, err := parseIndex(body, resolveRemoteInstallSource(source))
	if err != nil {
		return View{}, err
	}
	view.Source = source
	view.SourceKind = "http"
	view.FetchedAt = c.now()
	c.writeCache(source, cacheRecord{
		Source:       source,
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		FetchedAt:    view.FetchedAt,
		Name:         view.Name,
		Entries:      view.Entries,
	})
	return view, nil
}

func (c *Client) fallback(kind, source string, cause error) (View, error) {
	cache, ok := c.readCache(source)
	if !ok {
		return View{}, cause
	}
	view := cachedView(cache, true, true, cause.Error())
	view.Source = source
	view.SourceKind = kind
	return view, nil
}

func cachedView(cache cacheRecord, cached, offline bool, warning string) View {
	return View{
		Name:      cache.Name,
		Entries:   append([]Entry(nil), cache.Entries...),
		Cached:    cached,
		Offline:   offline,
		Warning:   warning,
		FetchedAt: cache.FetchedAt,
	}
}

// --- index parsing -----------------------------------------------------------

type authorField struct {
	Name string
	URL  string
}

func (a *authorField) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		a.Name = strings.TrimSpace(s)
		return nil
	}
	var obj struct {
		Name  string `json:"name"`
		URL   string `json:"url"`
		Email string `json:"email"`
	}
	if err := json.Unmarshal(b, &obj); err != nil {
		return err
	}
	a.Name = strings.TrimSpace(obj.Name)
	if a.Name == "" {
		a.Name = strings.TrimSpace(obj.Email)
	}
	a.URL = strings.TrimSpace(obj.URL)
	return nil
}

type indexFile struct {
	Name     string          `json:"name"`
	Owner    json.RawMessage `json:"owner"`
	Metadata struct {
		Description string `json:"description"`
		Version     string `json:"version"`
		PluginRoot  string `json:"pluginRoot"`
	} `json:"metadata"`
	Plugins []entryFile `json:"plugins"`
}

type entryFile struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Version     string          `json:"version"`
	Author      json.RawMessage `json:"author"`
	Category    string          `json:"category"`
	Icon        string          `json:"icon"`
	Logo        string          `json:"logo"`
	Featured    bool            `json:"featured"`
	Permissions []string        `json:"permissions"`
	SHA         string          `json:"sha"`
	Source      json.RawMessage `json:"source"`
}

type urlSource struct {
	Source string `json:"source"`
	URL    string `json:"url"`
	SHA    string `json:"sha"`
}

// installSourceFunc resolves an entry source (plus the marketplace's
// metadata.pluginRoot) to the exact string InstallPlugin accepts.
type installSourceFunc func(source, pluginRoot string) (string, error)

func parseIndex(body []byte, resolve installSourceFunc) (View, error) {
	var idx indexFile
	if err := json.Unmarshal(body, &idx); err != nil {
		return View{}, fmt.Errorf("parse %s: %w", IndexFile, err)
	}
	if strings.TrimSpace(idx.Name) == "" {
		return View{}, fmt.Errorf("%s has no marketplace name", IndexFile)
	}
	if len(idx.Plugins) == 0 {
		return View{}, fmt.Errorf("%s contains no plugins", IndexFile)
	}
	if len(idx.Plugins) > maxPlugins {
		return View{}, fmt.Errorf("%s contains %d plugins; limit is %d", IndexFile, len(idx.Plugins), maxPlugins)
	}
	idx.Metadata.PluginRoot = strings.TrimSpace(idx.Metadata.PluginRoot)

	view := View{Name: strings.TrimSpace(idx.Name), Entries: make([]Entry, 0, len(idx.Plugins))}
	seen := make(map[string]bool, len(idx.Plugins))
	var warnings []string
	for _, raw := range idx.Plugins {
		name := strings.TrimSpace(raw.Name)
		if name == "" {
			warnings = append(warnings, "skipped marketplace entry with an empty name")
			continue
		}
		if !pluginpkg.IsValidName(name) {
			warnings = append(warnings, fmt.Sprintf("skipped marketplace plugin %q: not a valid plugin name", name))
			continue
		}
		if seen[name] {
			return View{}, fmt.Errorf("%s contains duplicate plugin name %q", IndexFile, name)
		}
		seen[name] = true
		entry, err := mapEntry(raw, idx.Metadata.PluginRoot, resolve)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("skipped marketplace plugin %q: %v", name, err))
			continue
		}
		view.Entries = append(view.Entries, entry)
	}
	if len(warnings) > 0 {
		view.Warning = strings.Join(warnings, "; ")
	}
	sort.SliceStable(view.Entries, func(i, j int) bool {
		return strings.ToLower(view.Entries[i].Name) < strings.ToLower(view.Entries[j].Name)
	})
	return view, nil
}

func mapEntry(raw entryFile, pluginRoot string, resolve installSourceFunc) (Entry, error) {
	entry := Entry{
		Name:        strings.TrimSpace(raw.Name),
		Description: strings.TrimSpace(raw.Description),
		Version:     strings.TrimSpace(raw.Version),
		Category:    NormalizeCategory(raw.Category),
		Icon:        strings.TrimSpace(raw.Icon),
		Featured:    raw.Featured,
		Permissions: cleanList(raw.Permissions),
	}
	if entry.Icon == "" {
		entry.Icon = strings.TrimSpace(raw.Logo)
	}
	if len(raw.Author) > 0 {
		var author authorField
		if err := json.Unmarshal(raw.Author, &author); err != nil {
			return Entry{}, fmt.Errorf("author: %w", err)
		}
		entry.Author = author.Name
		entry.AuthorURL = author.URL
	}
	var rel string
	var err error
	if strErr := json.Unmarshal(raw.Source, &rel); strErr == nil {
		entry.Source = strings.TrimSpace(rel)
		if strings.TrimSpace(raw.SHA) != "" {
			if !fullGitSHA.MatchString(strings.TrimSpace(raw.SHA)) {
				return Entry{}, fmt.Errorf("sha must be a full 40-character commit")
			}
			entry.SHA = strings.ToLower(strings.TrimSpace(raw.SHA))
		}
		entry.InstallSource, err = resolve(entry.Source, pluginRoot)
		if err != nil {
			return Entry{}, err
		}
		return entry, nil
	}

	var pinned urlSource
	if err := json.Unmarshal(raw.Source, &pinned); err != nil || pinned.Source != "url" {
		return Entry{}, fmt.Errorf("source must be a relative path or a pinned URL object")
	}
	pinnedURL := strings.TrimSpace(pinned.URL)
	pinnedSHA := strings.ToLower(strings.TrimSpace(pinned.SHA))
	if !isGitHubURL(pinnedURL) {
		return Entry{}, fmt.Errorf("pinned URL %q is not a GitHub repository", pinnedURL)
	}
	if !fullGitSHA.MatchString(pinnedSHA) {
		return Entry{}, fmt.Errorf("pinned source requires a full 40-character SHA")
	}
	entry.Source = pinnedURL
	entry.SHA = pinnedSHA
	entry.InstallSource = pinGitHubURL(pinnedURL, pinnedSHA)
	return entry, nil
}

func cleanList(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func resolveLocalInstallSource(root, pluginRoot string, source string) (string, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return "", fmt.Errorf("source is empty")
	}
	if isHTTPURL(source) || strings.HasPrefix(source, "git:") {
		return source, nil
	}
	if filepath.IsAbs(source) {
		return filepath.Clean(source), nil
	}
	base := root
	if rel := cleanRelPath(pluginRoot); rel != "" {
		base = filepath.Join(root, filepath.FromSlash(rel))
	}
	joined := filepath.Join(base, filepath.FromSlash(cleanRelPath(source)))
	relToRoot, err := filepath.Rel(root, joined)
	if err != nil || relToRoot == ".." || strings.HasPrefix(relToRoot, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("source %q escapes the marketplace root", source)
	}
	return joined, nil
}

// resolveRemoteInstallSource resolves relative entry paths against a GitHub
// raw/tree index URL. A non-GitHub index has no resolvable install URL, so the
// entry is skipped with a warning rather than installing the wrong tree.
func resolveRemoteInstallSource(indexURL string) func(string, string) (string, error) {
	return func(source, pluginRoot string) (string, error) {
		source = strings.TrimSpace(source)
		if source == "" {
			return "", fmt.Errorf("source is empty")
		}
		if isGitHubURL(source) {
			return source, nil
		}
		if isHTTPURL(source) || strings.HasPrefix(source, "git:") {
			return source, nil
		}
		owner, repo, ref, ok := githubRepoFromRawURL(indexURL)
		if !ok {
			return "", fmt.Errorf("relative source %q cannot be resolved from index %q", source, indexURL)
		}
		rel := cleanRelPath(joinRel(pluginRoot, source))
		return fmt.Sprintf("https://github.com/%s/%s/tree/%s/%s", owner, repo, ref, rel), nil
	}
}

func joinRel(parts ...string) string {
	var cleaned []string
	for _, p := range parts {
		p = cleanRelPath(p)
		if p != "" {
			cleaned = append(cleaned, p)
		}
	}
	return strings.Join(cleaned, "/")
}

func cleanRelPath(p string) string {
	p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	p = strings.TrimPrefix(p, "./")
	p = path.Clean(p)
	if p == "." || p == "/" {
		return ""
	}
	return strings.TrimPrefix(p, "/")
}

func isGitHubURL(value string) bool {
	u, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Hostname(), "github.com") && strings.Trim(u.Path, "/") != ""
}

func githubRepoFromRawURL(raw string) (owner, repo, ref string, ok bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", "", "", false
	}
	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	host := strings.ToLower(u.Hostname())
	switch {
	case host == "raw.githubusercontent.com" && len(segments) >= 4:
		return segments[0], segments[1], segments[2], true
	case host == "github.com" && len(segments) >= 4 && (segments[2] == "raw" || segments[2] == "blob"):
		return segments[0], segments[1], segments[3], true
	}
	return "", "", "", false
}

func pinGitHubURL(raw, sha string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return raw
	}
	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segments) >= 2 {
		rest := ""
		if len(segments) >= 5 && (segments[2] == "tree" || segments[2] == "blob") {
			rest = strings.Join(segments[4:], "/")
		}
		u.Path = "/" + segments[0] + "/" + segments[1] + "/tree/" + sha
		if rest != "" {
			u.Path += "/" + rest
		}
		u.RawQuery = ""
		u.Fragment = ""
	}
	return u.String()
}

// NormalizeCategory maps free-form marketplace categories onto the settings
// UI's fixed chip set.
func NormalizeCategory(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.NewReplacer(" ", "-", "_", "-").Replace(value)
	switch value {
	case "dev", "developer", "development", "developer-tools", "开发", "开发工具":
		return "dev"
	case "productivity", "efficiency", "efficiency-tools", "效率", "效率提升":
		return "productivity"
	case "design", "ui", "ux", "ui-design", "ui-ux", "界面", "设计", "界面设计":
		return "design"
	case "research", "analysis", "research-analysis", "调研", "分析", "调研分析":
		return "research"
	case "content", "writing", "content-creation", "创作", "内容", "内容创作":
		return "content"
	}
	return "other"
}

// --- cache -------------------------------------------------------------------

type cacheRecord struct {
	Source       string    `json:"source"`
	ETag         string    `json:"etag,omitempty"`
	LastModified string    `json:"lastModified,omitempty"`
	FetchedAt    time.Time `json:"fetchedAt,omitempty"`
	Name         string    `json:"name"`
	Entries      []Entry   `json:"entries"`
}

type cacheFile struct {
	Records map[string]cacheRecord `json:"records"`
}

func cacheKey(source string) string { return strings.TrimSpace(source) }

func (c *Client) readCache(source string) (cacheRecord, bool) {
	if strings.TrimSpace(c.CachePath) == "" {
		return cacheRecord{}, false
	}
	body, err := os.ReadFile(c.CachePath)
	if err != nil {
		return cacheRecord{}, false
	}
	var file cacheFile
	if err := json.Unmarshal(body, &file); err != nil {
		return cacheRecord{}, false
	}
	record, ok := file.Records[cacheKey(source)]
	if !ok || len(record.Entries) == 0 {
		return cacheRecord{}, false
	}
	return record, true
}

func (c *Client) writeCache(source string, record cacheRecord) {
	if strings.TrimSpace(c.CachePath) == "" {
		return
	}
	file := cacheFile{Records: map[string]cacheRecord{}}
	if body, err := os.ReadFile(c.CachePath); err == nil {
		_ = json.Unmarshal(body, &file)
		if file.Records == nil {
			file.Records = map[string]cacheRecord{}
		}
	}
	file.Records[cacheKey(source)] = record
	body, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(c.CachePath), 0o755)
	_ = fileutil.AtomicWriteFile(c.CachePath, append(body, '\n'), 0o600)
}

// --- source settings ---------------------------------------------------------

// SourceSettingsPath is the per-user JSON file that stores the configured
// marketplace index source.
func SourceSettingsPath() string {
	home := config.PonderaHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, "plugin-marketplace-source.json")
}

type sourceSettings struct {
	Source string `json:"source"`
}

// LoadSource reads the configured index source (empty means unconfigured).
func LoadSource() string {
	p := SourceSettingsPath()
	if p == "" {
		return ""
	}
	body, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	var settings sourceSettings
	if err := json.Unmarshal(body, &settings); err != nil {
		return ""
	}
	return strings.TrimSpace(settings.Source)
}

// SaveSource persists the index source. An empty value clears it.
func SaveSource(source string) error {
	p := SourceSettingsPath()
	if p == "" {
		return errors.New("cannot resolve the Pondera home directory")
	}
	settings := sourceSettings{Source: strings.TrimSpace(source)}
	body, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return fileutil.AtomicWriteFile(p, append(body, '\n'), 0o600)
}
