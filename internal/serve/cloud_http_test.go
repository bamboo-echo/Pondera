package serve

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"pondera/internal/config"
	"pondera/internal/messageops"
)

func TestMobileIndexServesSelfContainedClient(t *testing.T) {
	s := &Server{}
	rr := httptest.NewRecorder()
	s.mobileIndex(rr, httptest.NewRequest(http.MethodGet, "/mobile", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Fatalf("content type = %q", ct)
	}
	body := rr.Body.String()
	for _, want := range []string{
		"/messages/retract", "/messages/edit", "/message-targets",
		"/cloud/selections", "/submit", "/approve", "/events",
		"viewport-fit=cover", "manifest.webmanifest",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("mobile client missing %q", want)
		}
	}
}

func TestMobileManifest(t *testing.T) {
	s := &Server{}
	rr := httptest.NewRecorder()
	s.mobileManifest(rr, httptest.NewRequest(http.MethodGet, "/mobile/manifest.webmanifest", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var m map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if m["display"] != "standalone" || m["start_url"] != "/mobile" {
		t.Fatalf("unexpected manifest: %+v", m)
	}
	if !strings.HasPrefix(m["icons"].([]any)[0].(map[string]any)["src"].(string), "data:image/svg+xml,") {
		t.Fatal("icon is not an embedded data URI")
	}
}

func TestURLEscape(t *testing.T) {
	got := urlEscape("a b<>#")
	if got != "a%20b%3C%3E%23" {
		t.Fatalf("urlEscape = %q", got)
	}
}

func TestWriteMessageOpErrorStatusMapping(t *testing.T) {
	cases := []struct {
		code messageops.Code
		want int
	}{
		{messageops.CodeInvalid, http.StatusBadRequest},
		{messageops.CodeNotFound, http.StatusNotFound},
		{messageops.CodeBusy, http.StatusConflict},
		{messageops.CodeReadOnly, http.StatusConflict},
		{messageops.CodeStaleSource, http.StatusConflict},
		{messageops.CodeCompacted, http.StatusConflict},
	}
	for _, c := range cases {
		rr := httptest.NewRecorder()
		writeMessageOpError(rr, &messageops.Error{Code: c.code, Message: "x"})
		if rr.Code != c.want {
			t.Errorf("code %s -> %d, want %d", c.code, rr.Code, c.want)
		}
		var body messageOpError
		if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil || body.Code != c.code {
			t.Errorf("body not structured: %s %v", rr.Body.String(), err)
		}
	}
}

func TestMessageOpIdempotencyCache(t *testing.T) {
	op := "op-" + strings.Repeat("a", 24)
	if _, ok := cachedMessageOp(op); ok {
		t.Fatal("cache hit before remember")
	}
	rememberMessageOp(op, http.StatusOK, messageOpResponse{OK: true, MessageID: "m1"})
	cached, ok := cachedMessageOp(op)
	if !ok || cached.status != http.StatusOK {
		t.Fatalf("cache miss: %+v %v", cached, ok)
	}
	body, ok := cached.body.(messageOpResponse)
	if !ok || !body.OK || body.MessageID != "m1" {
		t.Fatalf("cached body wrong: %+v", cached.body)
	}
	// Empty operation ids must never be cached or replayed.
	rememberMessageOp("", http.StatusOK, "x")
	if _, ok := cachedMessageOp(""); ok {
		t.Fatal("empty operation id was cached")
	}
}

func TestCloudRoutesRegistered(t *testing.T) {
	// authNone still generates a launch token; use it so POST routes reach
	// their handlers instead of being rejected by the mutation gate.
	gate := newAuthGate(config.ServeConfig{})
	s := &Server{auth: gate}
	h := s.Handler()
	for _, p := range []string{"/mobile", "/mobile/manifest.webmanifest"} {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, p, nil)
		h.ServeHTTP(rr, req)
		if rr.Code == http.StatusNotFound {
			t.Errorf("route %s is not registered", p)
		}
	}
	// Malformed JSON is rejected by the handler's decoder before any handler
	// touches the controller, so a non-404 proves the POST route is mounted
	// without needing a live controller.
	for _, p := range []string{
		"/cloud/pin", "/cloud/unpin", "/messages/retract", "/messages/edit",
	} {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, p, strings.NewReader("{not json"))
		req.Header.Set("Content-Type", "application/json")
		func() {
			defer func() { _ = recover() }()
			h.ServeHTTP(rr, req)
		}()
		if rr.Code == http.StatusNotFound {
			t.Errorf("route %s is not registered", p)
		}
	}
}
