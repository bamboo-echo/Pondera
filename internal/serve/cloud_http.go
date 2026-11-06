package serve

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"pondera/internal/agent"
	"pondera/internal/cloudsync"
	"pondera/internal/config"
	"pondera/internal/control"
)

// cloudStoreCache keeps one Store per Pondera home. Opening a Store on every
// request made concurrent pin/unpin race on the host-id bootstrap and paid an
// extra file read; the Store is internally locked, so sharing it is safe.
var cloudStoreCache = struct {
	mu     sync.Mutex
	byPath map[string]*cloudsync.Store
}{byPath: map[string]*cloudsync.Store{}}

//go:embed mobile.html
var mobileHTML []byte

// mobileIndex serves the phone-first PWA client. It is intentionally a single
// self-contained file so it works offline after first load and needs no build
// step, and so the desktop/Electron frontend stays untouched.
func (s *Server) mobileIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(mobileHTML)
}

func (s *Server) mobileManifest(w http.ResponseWriter, _ *http.Request) {
	icon := "data:image/svg+xml," + urlEscape(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><rect width="64" height="64" rx="14" fill="#5b9cff"/><text x="32" y="43" font-size="34" text-anchor="middle" fill="white" font-family="sans-serif">P</text></svg>`)
	w.Header().Set("Content-Type", "application/manifest+json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"name":             "Pondera Mobile",
		"short_name":       "Pondera",
		"description":      "在手机上给 Pondera 发送指令并查看任务进度",
		"start_url":        "/mobile",
		"scope":            "/",
		"display":          "standalone",
		"background_color": "#0f1115",
		"theme_color":      "#0f1115",
		"icons": []map[string]string{
			{"src": icon, "sizes": "any", "type": "image/svg+xml", "purpose": "any maskable"},
		},
	})
}

func urlEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("-_.~", r):
			b.WriteRune(r)
		default:
			const hex = "0123456789ABCDEF"
			b.WriteByte('%')
			b.WriteByte(hex[(r>>4)&0xF])
			b.WriteByte(hex[r&0xF])
		}
	}
	return b.String()
}

func (s *Server) cloudStore() (*cloudsync.Store, error) {
	home := config.PonderaHomeDir()
	cloudStoreCache.mu.Lock()
	defer cloudStoreCache.mu.Unlock()
	if st, ok := cloudStoreCache.byPath[home]; ok {
		return st, nil
	}
	st, err := cloudsync.OpenStore(home)
	if err != nil {
		return nil, err
	}
	// Bootstrap under the same lock: two requests racing to create the
	// host-id would otherwise persist two different identities.
	if err := cloudsync.EnsureLocalHostID(st, home); err != nil {
		return nil, err
	}
	cloudStoreCache.byPath[home] = st
	return st, nil
}

func (s *Server) currentSessionID() string {
	ctrl := s.ctl()
	if identity, ok := ctrl.(control.IdentityLifecycle); ok {
		if ref, bound := identity.SessionRef(); bound && ref.SessionID != "" {
			return ref.SessionID
		}
	}
	return agent.BranchID(ctrl.SessionPath())
}

// cloudSelections is read-only: a phone can inspect which sessions are pinned
// without ever being able to trigger a bundle upload.
func (s *Server) cloudSelections(w http.ResponseWriter, _ *http.Request) {
	st, err := s.cloudStore()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"selections":       st.List(),
		"localHostId":      st.LocalHostID(),
		"currentSessionId": s.currentSessionID(),
	})
}

func (s *Server) cloudPin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID   string              `json:"sessionId"`
		Title       string              `json:"title"`
		WorkspaceID string              `json:"workspaceId"`
		Tasks       []cloudsync.TaskRef `json:"tasks"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	st, err := s.cloudStore()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := st.Pin(body.SessionID, body.Title, body.WorkspaceID, body.Tasks); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sel, _ := st.Get(body.SessionID)
	writeJSON(w, map[string]any{"ok": true, "selection": sel,
		"note": "仅标记，尚未上传任何数据；请用 pondera cloud pack 显式打包。"})
}

func (s *Server) cloudUnpin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	st, err := s.cloudStore()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := st.Unpin(body.SessionID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "note": "云端副本不会被自动删除。"})
}
