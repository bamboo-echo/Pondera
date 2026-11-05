package pluginmarket

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleIndex = `{
  "name": "pondera-demo",
  "owner": {"name": "Demo"},
  "metadata": {"pluginRoot": "./plugins"},
  "plugins": [
    {
      "name": "alpha",
      "source": "./alpha",
      "description": "Alpha tools",
      "version": "1.2.0",
      "author": {"name": "Ada", "url": "https://example.com/ada"},
      "category": "Developer Tools",
      "icon": "https://example.com/alpha.png",
      "featured": true,
      "permissions": ["filesystem", "network"]
    },
    {
      "name": "beta",
      "source": {"source": "url", "url": "https://github.com/acme/beta", "sha": "0123456789abcdef0123456789abcdef01234567"},
      "description": "Beta writing",
      "version": "2.0.0",
      "author": "Grace",
      "category": "内容创作",
      "logo": "https://example.com/beta.png"
    }
  ]
}`

func writeIndex(t *testing.T, root string) string {
	t.Helper()
	indexPath := filepath.Join(root, ".claude-plugin", "marketplace.json")
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexPath, []byte(sampleIndex), 0o600); err != nil {
		t.Fatal(err)
	}
	return indexPath
}

func TestLocalDirectoryMapsEntries(t *testing.T) {
	root := t.TempDir()
	writeIndex(t, root)

	view, err := New(root, filepath.Join(t.TempDir(), "cache.json")).Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if view.Name != "pondera-demo" || view.SourceKind != "local-dir" {
		t.Fatalf("view identity = %q/%q", view.Name, view.SourceKind)
	}
	if len(view.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(view.Entries))
	}
	byName := map[string]Entry{}
	for _, entry := range view.Entries {
		byName[entry.Name] = entry
	}
	alpha := byName["alpha"]
	if alpha.Description != "Alpha tools" || alpha.Author != "Ada" || alpha.Version != "1.2.0" {
		t.Fatalf("alpha metadata = %+v", alpha)
	}
	if alpha.Category != "dev" || !alpha.Featured || len(alpha.Permissions) != 2 {
		t.Fatalf("alpha category/featured/permissions = %+v", alpha)
	}
	wantInstall := filepath.Join(root, "plugins", "alpha")
	if alpha.InstallSource != wantInstall {
		t.Fatalf("alpha install source = %q, want %q", alpha.InstallSource, wantInstall)
	}
	beta := byName["beta"]
	if beta.Category != "content" || beta.Author != "Grace" || beta.Icon != "https://example.com/beta.png" {
		t.Fatalf("beta metadata = %+v", beta)
	}
	if beta.SHA != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("beta sha = %q", beta.SHA)
	}
	if want := "https://github.com/acme/beta/tree/0123456789abcdef0123456789abcdef01234567"; beta.InstallSource != want {
		t.Fatalf("beta install source = %q, want %q", beta.InstallSource, want)
	}
}

func TestDomainIsOptionalAndBackwardCompatible(t *testing.T) {
	root := t.TempDir()
	indexPath := filepath.Join(root, ".claude-plugin", "marketplace.json")
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{
	  "name": "domain-demo",
	  "metadata": {"pluginRoot": "./plugins"},
	  "plugins": [
	    {"name": "cad-tool", "source": "./cad-tool", "category": "开发工具", "domain": "  CAD  "},
	    {"name": "legacy-tool", "source": "./legacy-tool", "category": "开发工具"}
	  ]
	}`
	if err := os.WriteFile(indexPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	view, err := New(root, filepath.Join(t.TempDir(), "cache.json")).Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if view.Warning != "" {
		t.Fatalf("missing domain produced a warning: %q", view.Warning)
	}
	if len(view.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(view.Entries))
	}
	byName := map[string]Entry{}
	for _, entry := range view.Entries {
		byName[entry.Name] = entry
	}
	if got := byName["cad-tool"].Domain; got != "CAD" {
		t.Fatalf("cad domain = %q, want CAD", got)
	}
	if got := byName["legacy-tool"].Domain; got != "" {
		t.Fatalf("legacy domain = %q, want empty", got)
	}
}

func TestFileURLSource(t *testing.T) {
	root := t.TempDir()
	indexPath := writeIndex(t, root)
	fileURL := "file:///" + strings.TrimPrefix(filepath.ToSlash(indexPath), "/")

	view, err := New(fileURL, "").Load(context.Background())
	if err != nil {
		t.Fatalf("Load file URL: %v", err)
	}
	if view.SourceKind != "local-file" {
		t.Fatalf("source kind = %q, want local-file", view.SourceKind)
	}
	if got := view.Entries[0].InstallSource; !filepath.IsAbs(got) {
		t.Fatalf("install source = %q, want an absolute path", got)
	}
}

func TestInvalidIndexFormatReturnsError(t *testing.T) {
	root := t.TempDir()
	indexPath := filepath.Join(root, ".claude-plugin", "marketplace.json")
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexPath, []byte(`{"name":"broken","plugins":"not-an-array"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := New(root, filepath.Join(t.TempDir(), "cache.json")).Load(context.Background())
	if err == nil {
		t.Fatal("invalid index must return an error")
	}
	if !strings.Contains(err.Error(), IndexFile) {
		t.Fatalf("error %q does not name the index file", err)
	}
}

func TestMalformedJSONIsNotSmoothedOverByCache(t *testing.T) {
	root := t.TempDir()
	indexPath := writeIndex(t, root)
	cachePath := filepath.Join(t.TempDir(), "cache.json")
	if _, err := New(root, cachePath).Load(context.Background()); err != nil {
		t.Fatalf("seed cache: %v", err)
	}
	if err := os.WriteFile(indexPath, []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := New(root, cachePath).Load(context.Background())
	if err == nil {
		t.Fatal("a malformed local index must fail even when a cache exists")
	}
}

func TestRemoteOfflineFallsBackToCache(t *testing.T) {
	body := []byte(sampleIndex)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	cachePath := filepath.Join(t.TempDir(), "cache.json")
	client := New(server.URL+"/"+IndexFile, cachePath)

	first, err := client.Load(context.Background())
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	if first.Cached || len(first.Entries) != 1 {
		t.Fatalf("first load cached=%v entries=%d", first.Cached, len(first.Entries))
	}
	server.Close()

	second, err := client.Load(context.Background())
	if err != nil {
		t.Fatalf("offline load: %v", err)
	}
	if !second.Cached || !second.Offline {
		t.Fatalf("offline fallback cached=%v offline=%v", second.Cached, second.Offline)
	}
	if second.Warning == "" {
		t.Fatal("offline fallback must carry a warning")
	}
	if len(second.Entries) != 1 || second.Name != "pondera-demo" {
		t.Fatalf("offline entries = %+v", second.Entries)
	}
}

func TestRemoteETagRevalidationKeepsCacheFresh(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		_, _ = w.Write([]byte(sampleIndex))
	}))
	defer server.Close()
	cachePath := filepath.Join(t.TempDir(), "cache.json")
	client := New(server.URL+"/"+IndexFile, cachePath)
	if _, err := client.Load(context.Background()); err != nil {
		t.Fatalf("first load: %v", err)
	}
	view, err := client.Load(context.Background())
	if err != nil {
		t.Fatalf("revalidated load: %v", err)
	}
	if view.Cached || view.Offline {
		t.Fatalf("304 must be treated as live metadata, cached=%v offline=%v", view.Cached, view.Offline)
	}
	if len(view.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(view.Entries))
	}
}

func TestRemoteRelativeSourcesResolveAgainstGitHubIndex(t *testing.T) {
	resolve := resolveRemoteInstallSource("https://raw.githubusercontent.com/acme/market/main/.claude-plugin/marketplace.json")
	got, err := resolve("./gamma", "./plugins")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "https://github.com/acme/market/tree/main/plugins/gamma" {
		t.Fatalf("install source = %q", got)
	}
	if _, err := resolve("./gamma", ""); err != nil {
		t.Fatalf("resolve without pluginRoot: %v", err)
	}
	if _, err := resolveRemoteInstallSource("https://example.com/market.json")("./gamma", ""); err == nil {
		t.Fatal("a non-GitHub index must reject relative sources")
	}
	if NormalizeCategory("效率提升") != "productivity" {
		t.Fatalf("category mapping = %q", NormalizeCategory("效率提升"))
	}
}

func TestLocalReadFailureFallsBackToCache(t *testing.T) {
	root := t.TempDir()
	indexPath := writeIndex(t, root)
	cachePath := filepath.Join(t.TempDir(), "cache.json")
	client := New(root, cachePath)
	if _, err := client.Load(context.Background()); err != nil {
		t.Fatalf("seed local cache: %v", err)
	}
	if err := os.Remove(indexPath); err != nil {
		t.Fatal(err)
	}
	view, err := client.Load(context.Background())
	if err != nil {
		t.Fatalf("offline local load: %v", err)
	}
	if !view.Cached || !view.Offline || len(view.Entries) != 2 {
		t.Fatalf("local fallback cached=%v offline=%v entries=%d", view.Cached, view.Offline, len(view.Entries))
	}
	if view.SourceKind != "local-dir" {
		t.Fatalf("fallback source kind = %q, want local-dir", view.SourceKind)
	}
}

func TestSourceSettingsRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PONDERA_HOME", home)
	if err := SaveSource("  ./marketplace  "); err != nil {
		t.Fatalf("SaveSource: %v", err)
	}
	if got := LoadSource(); got != "./marketplace" {
		t.Fatalf("LoadSource = %q", got)
	}
	body, err := os.ReadFile(filepath.Join(home, "plugin-marketplace-source.json"))
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]string
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["source"] != "./marketplace" {
		t.Fatalf("stored source = %q", decoded["source"])
	}
}
