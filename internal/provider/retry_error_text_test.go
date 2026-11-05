package provider

import (
	"strings"
	"testing"
)

func TestAPIErrorErrorNeverEchoesNonJSONBody(t *testing.T) {
	html := "<html><head><title>Example Domain</title></head><body><h1>Example Domain</h1></body></html>"
	err := &APIError{Provider: "assistant", Protocol: "openai", Status: 405, Body: html}
	got := err.Error()
	if !strings.Contains(got, "status 405") {
		t.Fatalf("missing status prefix: %q", got)
	}
	if !strings.Contains(got, "响应非 JSON") {
		t.Fatalf("missing deterministic non-JSON description: %q", got)
	}
	for _, forbidden := range []string{"<", ">", "Example Domain", "html", "</"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("error text leaked response body %q: %q", forbidden, got)
		}
	}
}

func TestAPIErrorErrorUsesOnlyStructuredFields(t *testing.T) {
	err := &APIError{
		Status: 400,
		Body:   `{"error":{"type":"invalid_request_error","message":"<b>invalid model</b>","code":"model_not_found"},"request":{"echo":"private"}}`,
	}
	got := err.Error()
	if !strings.Contains(got, "invalid model") {
		t.Fatalf("structured message missing: %q", got)
	}
	for _, forbidden := range []string{"<b>", "</b>", "invalid_request_error", "model_not_found", "private", "{"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("error text leaked %q: %q", forbidden, got)
		}
	}
}

func TestAPIErrorErrorDropsCredentialBearingDetail(t *testing.T) {
	err := &APIError{
		Status: 401,
		Body:   `{"error":{"message":"invalid key sk-abcdefghijklmnop Bearer token-123"}}`,
	}
	got := err.Error()
	for _, forbidden := range []string{"sk-", "Bearer", "token-123", "abcdefghijklmnop"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("credential leaked %q: %q", forbidden, got)
		}
	}
	if !strings.Contains(got, "响应缺少错误详情") {
		t.Fatalf("credential-bearing JSON should fall back deterministically: %q", got)
	}
}

func TestAPIErrorErrorHardTruncatesLongDetail(t *testing.T) {
	long := strings.Repeat("x", maxErrorDetailRunes+80)
	err := &APIError{Provider: "assistant", Protocol: "openai", Status: 422, Body: `{"error":{"message":"` + long + `"}}`}
	got := err.Error()
	if !strings.Contains(got, "…") {
		t.Fatalf("long detail was not truncated: %q", got)
	}
	const marker = "status 422: "
	if idx := strings.Index(got, marker); idx >= 0 {
		detail := got[idx+len(marker):]
		if count := len([]rune(detail)); count > maxErrorDetailRunes+1 {
			t.Fatalf("detail kept %d runes, want <= %d", count, maxErrorDetailRunes+1)
		}
	} else {
		t.Fatalf("missing status marker: %q", got)
	}
	if strings.Contains(got, long) {
		t.Fatalf("full body text leaked: %q", got)
	}
}

func TestAPIErrorErrorEmptyBodyKeepsStatusOnly(t *testing.T) {
	err := &APIError{ProviderDisplayName: "Deepseek2", Protocol: "openai", Status: 404}
	if got := err.Error(); got != "Deepseek2 · Chat Completions: status 404" {
		t.Fatalf("empty body text = %q", got)
	}
}
