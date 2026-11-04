package diagnose

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestConnectivityEndpointClassifies covers the three classification paths the
// onboarding probe relies on: auth (401), not_found (404), timeout.
func TestConnectivityEndpointClassifies(t *testing.T) {
	const secret = "sk-verify-secret-0004"
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(previous)

	cases := []struct {
		name     string
		status   int
		delay    time.Duration
		timeout  time.Duration
		category string
	}{
		{name: "401", status: http.StatusUnauthorized, category: "auth"},
		{name: "404", status: http.StatusNotFound, category: "not_found"},
		{name: "timeout", delay: 300 * time.Millisecond, timeout: 80 * time.Millisecond, category: "timeout"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.delay > 0 {
					time.Sleep(tc.delay)
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"error":{"message":"probe"}}`))
			}))
			defer server.Close()
			ctx := context.Background()
			if tc.timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, tc.timeout)
				defer cancel()
			}
			result := ConnectivityEndpoint(ctx, server.URL, "openai", "test-model", secret)
			if result.Category != tc.category {
				t.Fatalf("category = %q, want %q (message %q)", result.Category, tc.category, result.Message)
			}
			if strings.TrimSpace(result.Message) == "" {
				t.Fatalf("failure must be readable")
			}
			if strings.Contains(result.Message, secret) {
				t.Fatalf("secret leaked into the probe message")
			}
		})
	}
	if strings.Contains(logs.String(), secret) {
		t.Fatalf("secret leaked into logs: %s", logs.String())
	}
	// A port number must never be read as an HTTP status: an 80ms deadline on
	// 127.0.0.1:40400 is still a timeout, not a 404.
	if category, _ := classifyProbeError(fmt.Errorf(`request failed: Post "http://127.0.0.1:40400/chat/completions": %w`, context.DeadlineExceeded)); category != "timeout" {
		t.Fatalf("category = %q, want timeout (port must not be read as a status)", category)
	}
}
