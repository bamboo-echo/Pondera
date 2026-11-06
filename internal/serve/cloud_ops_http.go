package serve

// Cross-machine mirror, cost gate and Web Push endpoints. All state lives in
// package-level singletons keyed by Pondera home so the serve process does not
// need any changes to its constructor (which would touch the kernel).

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"pondera/internal/cloudsync"
	"pondera/internal/config"
	"pondera/internal/costgate"
	"pondera/internal/webpush"
)

//go:embed settings.html mobile-sw.js
var cloudAssets embed.FS

type mirrorSession struct {
	mu     sync.Mutex
	stream *cloudsync.MirrorStream
	buffer *cloudsync.MirrorBuffer
	subs   map[chan cloudsync.MirrorFrame]struct{}
	lastHB time.Time
}

var cloudOps = struct {
	mirrorMu    sync.Mutex
	mirrors     map[string]*mirrorSession
	pushMu      sync.Mutex
	vapidMu     sync.Mutex
	vapidByPath map[string]*webpush.VAPIDKey
	costMu      sync.Mutex
	costByPath  map[string]*costgate.Ledger
}{
	mirrors:     map[string]*mirrorSession{},
	vapidByPath: map[string]*webpush.VAPIDKey{},
	costByPath:  map[string]*costgate.Ledger{},
}

func cloudHome() string { return config.PonderaHomeDir() }

func mirrorFor(sessionID string) *mirrorSession {
	cloudOps.mirrorMu.Lock()
	defer cloudOps.mirrorMu.Unlock()
	if m, ok := cloudOps.mirrors[sessionID]; ok {
		return m
	}
	m := &mirrorSession{
		stream: cloudsync.NewMirrorStream(sessionID, "", cloudsync.DefaultMirrorHeartbeat),
		buffer: cloudsync.NewMirrorBuffer(512),
		subs:   map[chan cloudsync.MirrorFrame]struct{}{},
		lastHB: time.Now(),
	}
	cloudOps.mirrors[sessionID] = m
	return m
}

func (m *mirrorSession) broadcast(f cloudsync.MirrorFrame) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for ch := range m.subs {
		select {
		case ch <- f:
		default: // a slow spectator is dropped from this frame, not blocked
		}
	}
}

func (m *mirrorSession) subscribe() chan cloudsync.MirrorFrame {
	ch := make(chan cloudsync.MirrorFrame, 64)
	m.mu.Lock()
	m.subs[ch] = struct{}{}
	m.mu.Unlock()
	return ch
}

func (m *mirrorSession) unsubscribe(ch chan cloudsync.MirrorFrame) {
	m.mu.Lock()
	delete(m.subs, ch)
	m.mu.Unlock()
	close(ch)
}

// mirrorPublish receives a sealed frame from a cloud writer (or from
// `pondera cloud watch --relay`) and fans it out to local spectators.
func (s *Server) mirrorPublish(w http.ResponseWriter, r *http.Request) {
	var f cloudsync.MirrorFrame
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&f); err != nil {
		http.Error(w, "invalid frame", http.StatusBadRequest)
		return
	}
	m := mirrorFor(f.SessionID)
	if f.Kind == "heartbeat" {
		m.stream.Heartbeat()
		m.mu.Lock()
		m.lastHB = time.Now()
		m.mu.Unlock()
		writeJSON(w, map[string]any{"ok": true, "kind": "heartbeat", "stats": m.stream.Stats()})
		return
	}
	if err := m.stream.Accept(f); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, cloudsync.ErrMirrorGap) {
			status = http.StatusConflict
		}
		w.WriteHeader(status)
		writeJSON(w, map[string]any{"ok": false, "error": err.Error(), "expectedSequence": m.stream.ExpectedSequence()})
		return
	}
	m.buffer.Add(f)
	m.broadcast(f)
	writeJSON(w, map[string]any{"ok": true, "sequence": f.Sequence, "stats": m.stream.Stats()})
}

func (s *Server) mirrorStream(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.URL.Query().Get("session"))
	if sessionID == "" {
		sessionID = s.currentSessionID()
	}
	from, _ := strconv.ParseUint(r.URL.Query().Get("from"), 10, 64)
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	m := mirrorFor(sessionID)
	if from > 0 {
		for _, f := range m.buffer.Since(from) {
			writeSSEFrame(w, f)
		}
	}
	ch := m.subscribe()
	defer m.unsubscribe(ch)
	flusher.Flush()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case f := <-ch:
			writeSSEFrame(w, f)
			flusher.Flush()
		case <-ticker.C:
			_, _ = io.WriteString(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

func writeSSEFrame(w io.Writer, f cloudsync.MirrorFrame) {
	data, err := json.Marshal(f)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintf(w, "id: %d\ndata: %s\n\n", f.Sequence, data)
}

func (s *Server) mirrorReplay(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.URL.Query().Get("session"))
	if sessionID == "" {
		sessionID = s.currentSessionID()
	}
	from, _ := strconv.ParseUint(r.URL.Query().Get("from"), 10, 64)
	m := mirrorFor(sessionID)
	writeJSON(w, map[string]any{
		"sessionId": sessionID, "from": from,
		"frames": m.buffer.Since(from), "expectedSequence": m.stream.ExpectedSequence(),
	})
}

func (s *Server) mirrorStatus(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.URL.Query().Get("session"))
	if sessionID == "" {
		sessionID = s.currentSessionID()
	}
	m := mirrorFor(sessionID)
	writeJSON(w, map[string]any{"sessionId": sessionID, "stats": m.stream.Stats(), "buffered": m.buffer.Len()})
}

// ---- cost gate -----------------------------------------------------------

func costLedger() (*costgate.Ledger, error) {
	path := filepath.Join(cloudHome(), "cloud", "cost-ledger.json")
	cloudOps.costMu.Lock()
	defer cloudOps.costMu.Unlock()
	if l, ok := cloudOps.costByPath[path]; ok {
		return l, nil
	}
	l, err := costgate.Open(path)
	if err != nil {
		return nil, err
	}
	cloudOps.costByPath[path] = l
	return l, nil
}

func (s *Server) costStatus(w http.ResponseWriter, _ *http.Request) {
	l, err := costLedger()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.syncActiveJobs(l)
	writeJSON(w, l.Status())
}

// activeJobCount is the server's own view of in-flight work. The client cannot
// raise or lower it: a desktop turn (and any background jobs it owns) is what
// counts, which is why POST /cost/active is gone.
func (s *Server) activeJobCount() int {
	ctrl := s.ctl()
	if ctrl == nil {
		return 0
	}
	n := ctrl.RuntimeStatus().BackgroundJobs
	if ctrl.Running() {
		n++
	}
	return n
}

func (s *Server) syncActiveJobs(l *costgate.Ledger) {
	l.SetActiveJobs(s.activeJobCount())
}

func (s *Server) costCheck(w http.ResponseWriter, r *http.Request) {
	l, err := costLedger()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.syncActiveJobs(l)
	elapsed, _ := strconv.Atoi(r.URL.Query().Get("elapsed"))
	d, err := l.Check(r.URL.Query().Get("session"), r.URL.Query().Get("task"), elapsed)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, d)
}

func (s *Server) costRecord(w http.ResponseWriter, r *http.Request) {
	var e costgate.Entry
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&e); err != nil {
		http.Error(w, "invalid entry", http.StatusBadRequest)
		return
	}
	l, err := costLedger()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := l.Record(e); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "status": l.Status()})
}

func (s *Server) costLimits(w http.ResponseWriter, r *http.Request) {
	var lim costgate.Limits
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&lim); err != nil {
		http.Error(w, "invalid limits", http.StatusBadRequest)
		return
	}
	l, err := costLedger()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := l.SetLimits(lim); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "limits": l.Limits()})
}

// costActive used to let a client publish its own concurrency count. That made
// the "hard" concurrency gate trivially bypassable (or falsely triggered), so
// the count now comes from activeJobCount() and this route is retired.
func (s *Server) costActive(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusGone)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":     false,
		"error":  "active jobs are maintained by the server",
		"active": s.activeJobCount(),
	})
}

func (s *Server) costReset(w http.ResponseWriter, _ *http.Request) {
	l, err := costLedger()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := l.Reset(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// costGatedSubmit is a fail-closed pre-submit check. It deliberately does not
// consume the request body: the foreground mutation fence buffers it later.
func (s *Server) costGatedSubmit(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l, err := costLedger()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		sessionID := strings.TrimSpace(r.URL.Query().Get("session"))
		if sessionID == "" {
			sessionID = s.currentSessionID()
		}
		s.syncActiveJobs(l)
		// The turn-time cap is only as real as the elapsed the caller reports.
		// Desktop does not report it, so 0 keeps this limit advisory there.
		elapsed, _ := strconv.Atoi(r.URL.Query().Get("elapsed"))
		d, err := l.Check(sessionID, strings.TrimSpace(r.URL.Query().Get("task")), elapsed)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if !d.Allowed {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": "cost gate", "reason": d.Reason, "decision": d,
			})
			return
		}
		next(w, r)
	}
}

// ---- Web Push ------------------------------------------------------------

func pushPaths() (vapidPath, subsPath string) {
	base := filepath.Join(cloudHome(), "cloud")
	return filepath.Join(base, "push-vapid.json"), filepath.Join(base, "push-subscriptions.json")
}

func vapidKey() (*webpush.VAPIDKey, error) {
	path, _ := pushPaths()
	cloudOps.vapidMu.Lock()
	defer cloudOps.vapidMu.Unlock()
	if k, ok := cloudOps.vapidByPath[path]; ok {
		return k, nil
	}
	if data, err := os.ReadFile(path); err == nil {
		var k webpush.VAPIDKey
		if json.Unmarshal(data, &k) == nil && k.PrivatePEM != "" {
			cloudOps.vapidByPath[path] = &k
			return &k, nil
		}
	}
	k, err := webpush.GenerateVAPID("mailto:pondera@localhost")
	if err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(k, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return nil, err
	}
	cloudOps.vapidByPath[path] = k
	return k, nil
}

func loadSubs() []webpush.Subscription {
	cloudOps.pushMu.Lock()
	defer cloudOps.pushMu.Unlock()
	_, path := pushPaths()
	data, err := os.ReadFile(path)
	if err != nil {
		return []webpush.Subscription{}
	}
	var subs []webpush.Subscription
	if json.Unmarshal(data, &subs) != nil {
		return []webpush.Subscription{}
	}
	return subs
}

func saveSubs(subs []webpush.Subscription) error {
	cloudOps.pushMu.Lock()
	defer cloudOps.pushMu.Unlock()
	_, path := pushPaths()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(subs, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Server) pushVAPID(w http.ResponseWriter, _ *http.Request) {
	k, err := vapidKey()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"publicKey": k.PublicKey, "subject": k.Subject})
}

func (s *Server) pushSubscribe(w http.ResponseWriter, r *http.Request) {
	var sub webpush.Subscription
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&sub); err != nil {
		http.Error(w, "invalid subscription", http.StatusBadRequest)
		return
	}
	if sub.Endpoint == "" || sub.P256dh == "" || sub.Auth == "" {
		http.Error(w, "incomplete subscription", http.StatusBadRequest)
		return
	}
	subs := loadSubs()
	found := false
	for i := range subs {
		if subs[i].Endpoint == sub.Endpoint {
			subs[i] = sub
			found = true
			break
		}
	}
	if !found {
		subs = append(subs, sub)
	}
	if err := saveSubs(subs); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "subscriptions": len(subs)})
}

func (s *Server) pushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	out := make([]webpush.Subscription, 0)
	for _, sub := range loadSubs() {
		if sub.Endpoint != body.Endpoint {
			out = append(out, sub)
		}
	}
	if err := saveSubs(out); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "subscriptions": len(out)})
}

func (s *Server) pushSubscriptions(w http.ResponseWriter, _ *http.Request) {
	subs := loadSubs()
	out := make([]map[string]any, 0, len(subs))
	for _, sub := range subs {
		u, _ := parseEndpointHost(sub.Endpoint)
		out = append(out, map[string]any{"endpointHost": u, "label": sub.Label, "registered": true})
	}
	writeJSON(w, map[string]any{"subscriptions": out, "count": len(out)})
}

func parseEndpointHost(endpoint string) (string, error) {
	if i := strings.Index(endpoint, "://"); i >= 0 {
		rest := endpoint[i+3:]
		if j := strings.IndexByte(rest, '/'); j >= 0 {
			rest = rest[:j]
		}
		return rest, nil
	}
	return "", errors.New("invalid endpoint")
}

type pushNotifyBody struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Tag   string `json:"tag"`
	URL   string `json:"url"`
}

func (s *Server) pushNotify(w http.ResponseWriter, r *http.Request) {
	var b pushNotifyBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&b); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(b.Title) == "" {
		b.Title = "Pondera"
	}
	if b.Tag == "" {
		b.Tag = "pondera-task"
	}
	payload, _ := json.Marshal(map[string]any{
		"title": b.Title, "body": b.Body, "tag": b.Tag, "url": b.URL,
		"icon": "/mobile/manifest.webmanifest",
	})
	k, err := vapidKey()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	subs := loadSubs()
	client := &http.Client{Timeout: 15 * time.Second}
	results := make([]map[string]any, 0, len(subs))
	alive := make([]webpush.Subscription, 0, len(subs))
	for _, sub := range subs {
		res, sendErr := webpush.Send(client, k, sub, payload, 24*time.Hour)
		if sendErr != nil {
			results = append(results, map[string]any{"error": sendErr.Error()})
			alive = append(alive, sub) // transient failure: keep it
			continue
		}
		results = append(results, map[string]any{"status": res.StatusCode, "endpoint": res.Endpoint})
		if res.StatusCode == 404 || res.StatusCode == 410 {
			continue // expired subscription
		}
		alive = append(alive, sub)
	}
	if len(alive) != len(subs) {
		_ = saveSubs(alive)
	}
	writeJSON(w, map[string]any{"ok": true, "sent": len(subs), "results": results, "retained": len(alive)})
}

// ---- settings page / service worker --------------------------------------

func (s *Server) settingsIndex(w http.ResponseWriter, _ *http.Request) {
	data, err := cloudAssets.ReadFile("settings.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}

func (s *Server) serviceWorker(w http.ResponseWriter, _ *http.Request) {
	data, err := cloudAssets.ReadFile("mobile-sw.js")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Service-Worker-Allowed", "/")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}
