package serve

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pondera/internal/cloudsync"
	"pondera/internal/config"
	"pondera/internal/costgate"
	"pondera/internal/webpush"
)

func testServerWithAuth() (*Server, http.Handler) {
	gate := newAuthGate(config.ServeConfig{})
	s := &Server{auth: gate}
	return s, s.Handler()
}

func TestCloudOpsRoutesRegistered(t *testing.T) {
	t.Setenv("PONDERA_HOME", t.TempDir())
	_, h := testServerWithAuth()
	paths := []string{
		"/mobile", "/settings", "/sw.js",
		"/mirror/status?session=route-test", "/mirror/replay?session=route-test",
		"/cost/status", "/cost/check?session=route-test",
		"/push/vapid", "/push/subscriptions",
	}
	for _, p := range paths {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, p, nil))
		if rr.Code == http.StatusNotFound {
			t.Errorf("route %s is not registered", p)
		}
	}
}

func TestMirrorPublishReplayAndGap(t *testing.T) {
	t.Setenv("PONDERA_HOME", t.TempDir())
	s := &Server{}
	sid := t.Name() + "-mirror"
	w := cloudsync.NewMirrorStream(sid, "cloud", 0)
	f1, f2, f3 := w.NewFrame("message", json.RawMessage(`{"n":1}`), ""),
		w.NewFrame("tool", json.RawMessage(`{"n":2}`), ""),
		w.NewFrame("message", json.RawMessage(`{"n":3}`), "")
	post := func(f cloudsync.MirrorFrame) *httptest.ResponseRecorder {
		body, _ := json.Marshal(f)
		rr := httptest.NewRecorder()
		s.mirrorPublish(rr, httptest.NewRequest(http.MethodPost, "/mirror/publish", bytes.NewReader(body)))
		return rr
	}
	if rr := post(f1); rr.Code != http.StatusOK {
		t.Fatalf("first frame rejected: %d %s", rr.Code, rr.Body.String())
	}
	if rr := post(f1); rr.Code != http.StatusOK {
		t.Fatal("duplicate frame must be idempotent")
	}
	if rr := post(f3); rr.Code != http.StatusConflict {
		t.Fatalf("gap must be a 409, got %d", rr.Code)
	}
	if rr := post(f2); rr.Code != http.StatusOK {
		t.Fatalf("replay frame rejected: %d", rr.Code)
	}
	if rr := post(f3); rr.Code != http.StatusOK {
		t.Fatalf("catch-up frame rejected: %d", rr.Code)
	}
	rr := httptest.NewRecorder()
	s.mirrorReplay(rr, httptest.NewRequest(http.MethodGet, "/mirror/replay?session="+sid+"&from=2", nil))
	var replay struct {
		Frames           []cloudsync.MirrorFrame `json:"frames"`
		ExpectedSequence uint64                  `json:"expectedSequence"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &replay); err != nil {
		t.Fatal(err)
	}
	if len(replay.Frames) != 2 || replay.ExpectedSequence != 4 {
		t.Fatalf("unexpected replay: %+v", replay)
	}
	rr = httptest.NewRecorder()
	s.mirrorStatus(rr, httptest.NewRequest(http.MethodGet, "/mirror/status?session="+sid, nil))
	var status struct {
		Stats cloudsync.MirrorStats `json:"stats"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Stats.Accepted != 3 || status.Stats.Duplicates != 1 || status.Stats.Gaps != 1 {
		t.Fatalf("unexpected mirror stats: %+v", status.Stats)
	}
}

func mirroredFrameTampered(f cloudsync.MirrorFrame) cloudsync.MirrorFrame {
	f.Kind = "tampered"
	return f
}

func TestMirrorRejectsTamperedFrame(t *testing.T) {
	t.Setenv("PONDERA_HOME", t.TempDir())
	s := &Server{}
	w := cloudsync.NewMirrorStream(t.Name(), "cloud", 0)
	f := mirroredFrameTampered(w.NewFrame("message", json.RawMessage(`{}`), ""))
	body, _ := json.Marshal(f)
	rr := httptest.NewRecorder()
	s.mirrorPublish(rr, httptest.NewRequest(http.MethodPost, "/mirror/publish", bytes.NewReader(body)))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("tampered frame must be rejected, got %d", rr.Code)
	}
}

func jsonRequest(method, path string, body any) *http.Request {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestCostEndpointsAndGate(t *testing.T) {
	t.Setenv("PONDERA_HOME", t.TempDir())
	s := &Server{}
	// Record a charge, then set a limit below it.
	rr := httptest.NewRecorder()
	s.costRecord(rr, jsonRequest(http.MethodPost, "/cost/record", costgate.Entry{SessionID: "s1", USD: 2.5}))
	if rr.Code != http.StatusOK {
		t.Fatalf("record failed: %d %s", rr.Code, rr.Body.String())
	}
	lim := costgate.DefaultLimits()
	lim.MaxUSDPerSession = 2
	rr = httptest.NewRecorder()
	s.costLimits(rr, jsonRequest(http.MethodPost, "/cost/limits", lim))
	if rr.Code != http.StatusOK {
		t.Fatalf("limits failed: %d %s", rr.Code, rr.Body.String())
	}
	rr = httptest.NewRecorder()
	s.costCheck(rr, httptest.NewRequest(http.MethodGet, "/cost/check?session=s1", nil))
	var d costgate.Decision
	if err := json.Unmarshal(rr.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if d.Allowed {
		t.Fatal("expected the session limit to be enforced")
	}
	// The submit gate must refuse before reaching the controller.
	called := false
	gate := s.costGatedSubmit(func(http.ResponseWriter, *http.Request) { called = true })
	rr = httptest.NewRecorder()
	gate(rr, httptest.NewRequest(http.MethodPost, "/submit?session=s1", strings.NewReader(`{}`)))
	if rr.Code != http.StatusTooManyRequests || called {
		t.Fatalf("submit gate did not block: %d called=%v", rr.Code, called)
	}
	// A different session is unaffected.
	rr = httptest.NewRecorder()
	gate(rr, httptest.NewRequest(http.MethodPost, "/submit?session=s2", strings.NewReader(`{}`)))
	if !called {
		t.Fatal("submit gate blocked an unrelated session")
	}
	// The turn-time cap uses the elapsed the caller reports instead of a
	// hard-coded 0.
	lim.MaxUSDPerSession = 0
	lim.MaxTurnSeconds = 60
	rr = httptest.NewRecorder()
	s.costLimits(rr, jsonRequest(http.MethodPost, "/cost/limits", lim))
	called = false
	rr = httptest.NewRecorder()
	gate(rr, httptest.NewRequest(http.MethodPost, "/submit?session=s2&elapsed=61", strings.NewReader(`{}`)))
	if rr.Code != http.StatusTooManyRequests || called {
		t.Fatalf("elapsed turn-time gate did not block: %d called=%v", rr.Code, called)
	}
	// Clients can no longer publish their own concurrency count.
	rr = httptest.NewRecorder()
	s.costActive(rr, jsonRequest(http.MethodPost, "/cost/active", map[string]int{"active": 99}))
	if rr.Code != http.StatusGone {
		t.Fatalf("cost/active must be retired, got %d", rr.Code)
	}
}

func TestPushEndpoints(t *testing.T) {
	t.Setenv("PONDERA_HOME", t.TempDir())
	s := &Server{}
	rr := httptest.NewRecorder()
	s.pushVAPID(rr, httptest.NewRequest(http.MethodGet, "/push/vapid", nil))
	var v struct {
		PublicKey string `json:"publicKey"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &v); err != nil || v.PublicKey == "" {
		t.Fatalf("vapid key missing: %s %v", rr.Body.String(), err)
	}
	clientPriv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatal(err)
	}
	pushSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Encoding") != "aes128gcm" {
			t.Errorf("push request missing aes128gcm encoding")
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer pushSrv.Close()
	sub := webpush.Subscription{
		Endpoint: pushSrv.URL + "/push/abc",
		P256dh:   base64.RawURLEncoding.EncodeToString(clientPriv.PublicKey().Bytes()),
		Auth:     base64.RawURLEncoding.EncodeToString(auth),
		Label:    "test-device",
	}
	rr = httptest.NewRecorder()
	s.pushSubscribe(rr, jsonRequest(http.MethodPost, "/push/subscribe", sub))
	if rr.Code != http.StatusOK {
		t.Fatalf("subscribe failed: %d %s", rr.Code, rr.Body.String())
	}
	rr = httptest.NewRecorder()
	s.pushSubscriptions(rr, httptest.NewRequest(http.MethodGet, "/push/subscriptions", nil))
	if !strings.Contains(rr.Body.String(), `"count":1`) {
		t.Fatalf("subscription not listed: %s", rr.Body.String())
	}
	rr = httptest.NewRecorder()
	s.pushNotify(rr, jsonRequest(http.MethodPost, "/push/notify", pushNotifyBody{Title: "任务完成", Body: "测试"}))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"sent":1`) {
		t.Fatalf("notify failed: %d %s", rr.Code, rr.Body.String())
	}
	rr = httptest.NewRecorder()
	s.pushUnsubscribe(rr, jsonRequest(http.MethodPost, "/push/unsubscribe", map[string]string{"endpoint": sub.Endpoint}))
	if rr.Code != http.StatusOK {
		t.Fatalf("unsubscribe failed: %d", rr.Code)
	}
	rr = httptest.NewRecorder()
	s.pushSubscriptions(rr, httptest.NewRequest(http.MethodGet, "/push/subscriptions", nil))
	if !strings.Contains(rr.Body.String(), `"count":0`) {
		t.Fatalf("subscription not removed: %s", rr.Body.String())
	}
}

func TestSettingsAndServiceWorkerAreEmbedded(t *testing.T) {
	s := &Server{}
	rr := httptest.NewRecorder()
	s.settingsIndex(rr, httptest.NewRequest(http.MethodGet, "/settings", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "成本闸门") {
		t.Fatal("settings page not served")
	}
	rr = httptest.NewRecorder()
	s.serviceWorker(rr, httptest.NewRequest(http.MethodGet, "/sw.js", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "pondera-outbox") {
		t.Fatal("service worker not served")
	}
}
