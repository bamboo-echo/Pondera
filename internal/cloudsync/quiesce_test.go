package cloudsync

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCheckQuiescedServeStatus(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"running":false,"runtimeState":"idle"}`))
	}))
	defer srv.Close()
	q, err := CheckQuiesced(context.Background(), QuiesceOptions{ServeURL: srv.URL, SessionID: "s1", Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if !q.Quiesced || q.Source != "serve-status" || !q.Probed {
		t.Fatalf("unexpected report: %+v", q)
	}
	if hits.Load() != 1 {
		t.Fatalf("expected one probe, got %d", hits.Load())
	}
}

func TestCheckQuiescedRefusesRunningTurn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"running":true}`))
	}))
	defer srv.Close()
	q, err := CheckQuiesced(context.Background(), QuiesceOptions{ServeURL: srv.URL, Mode: QuiesceCheck})
	if err == nil {
		t.Fatal("expected a running turn to be refused")
	}
	if q.Quiesced || !q.Running {
		t.Fatalf("unexpected report: %+v", q)
	}
}

func TestCheckQuiescedDrain(t *testing.T) {
	var running atomic.Bool
	running.Store(true)
	var cancelled atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cancel-session" {
			if r.Header.Get("Content-Type") != "application/json" {
				t.Errorf("cancel must send JSON content type")
			}
			cancelled.Store(true)
			running.Store(false)
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if running.Load() {
			_, _ = w.Write([]byte(`{"running":true}`))
		} else {
			_, _ = w.Write([]byte(`{"running":false}`))
		}
	}))
	defer srv.Close()
	q, err := CheckQuiesced(context.Background(), QuiesceOptions{
		ServeURL: srv.URL, Mode: QuiesceDrain, Timeout: 3 * time.Second, PollEvery: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !q.Quiesced || q.Source != "drained" || !q.Cancelled || !cancelled.Load() {
		t.Fatalf("unexpected drain report: %+v", q)
	}
}

func TestCheckQuiescedLeaseFallbackAndForce(t *testing.T) {
	dir := t.TempDir()
	q, err := CheckQuiesced(context.Background(), QuiesceOptions{SessionDir: dir, Mode: QuiesceCheck})
	if err != nil {
		t.Fatal(err)
	}
	if !q.Quiesced || q.Source != "writer-lease-free" {
		t.Fatalf("unexpected lease report: %+v", q)
	}
	if _, err := CheckQuiesced(context.Background(), QuiesceOptions{}); err == nil {
		t.Fatal("an unverifiable session must be refused")
	}
	q2, err := CheckQuiesced(context.Background(), QuiesceOptions{Mode: QuiesceForce})
	if err != nil || !q2.Quiesced || q2.Source != "forced" {
		t.Fatalf("force override failed: %+v %v", q2, err)
	}
}

func TestCheckQuiescedRejectsBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusUnauthorized)
	}))
	defer srv.Close()
	_, err := CheckQuiesced(context.Background(), QuiesceOptions{ServeURL: srv.URL})
	if err == nil || !strings.Contains(err.Error(), "cannot prove") {
		t.Fatalf("expected fail-closed on unavailable status, got %v", err)
	}
}
