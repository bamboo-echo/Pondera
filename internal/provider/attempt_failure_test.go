package provider

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestExplainFailureClassifiesRetryReasons(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		kind      FailureKind
		status    int
		retryable bool
	}{
		{"rate limit", &APIError{Status: 429}, FailureKindRateLimit, 429, true},
		{"server error", &APIError{Status: 502}, FailureKindServerError, 502, true},
		{"auth", &AuthError{Status: 401}, FailureKindAuth, 401, false},
		{"quota", &QuotaError{Status: 402}, FailureKindQuota, 402, false},
		{"context limit", &ContextLimitError{APIError: &APIError{Status: 400}}, FailureKindContextLimit, 400, true},
		{"output limit", &OutputLimitError{APIError: &APIError{Status: 400}}, FailureKindOutputLimit, 400, true},
		{"stream idle", &StreamInterruptedError{Reason: StreamInterruptIdleTimeout}, FailureKindTimeout, 0, true},
		{"stream eof", &StreamInterruptedError{Reason: StreamInterruptPrematureEOF}, FailureKindStreamCut, 0, true},
		{"connection reset", &StreamInterruptedError{Reason: StreamInterruptConnectionReset}, FailureKindNetwork, 0, true},
		{"empty response", ErrEmptyResponse, FailureKindEmptyResponse, 0, true},
		{"deadline stays unknown", context.DeadlineExceeded, FailureKindUnknown, 0, false},
		{"permanent 400", &APIError{Status: 400}, FailureKindUnknown, 400, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ExplainFailure(tc.err)
			if got.Kind != tc.kind || got.Status != tc.status || got.Retryable != tc.retryable {
				t.Fatalf("ExplainFailure(%v) = kind=%s status=%d retryable=%v, want kind=%s status=%d retryable=%v",
					tc.err, got.Kind, got.Status, got.Retryable, tc.kind, tc.status, tc.retryable)
			}
		})
	}
}

func TestExplainFailureNeverCarriesSecretsOrBodyText(t *testing.T) {
	err := &APIError{
		Status: 429,
		Body:   `{"error":{"code":"rate_limit_exceeded","message":"sk-abcdefghijklmnop Bearer token-123"}}`,
	}
	got := ExplainFailure(err)
	if got.Code != "rate_limit_exceeded" {
		t.Fatalf("code = %q, want rate_limit_exceeded", got.Code)
	}
	payload, marshalErr := json.Marshal(got)
	if marshalErr != nil {
		t.Fatalf("marshal: %v", marshalErr)
	}
	text := string(payload)
	for _, forbidden := range []string{"sk-", "Bearer", "token-123", "abcdefghijklmnop"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("attempt failure leaked %q: %s", forbidden, text)
		}
	}
	if strings.Contains(got.Code, "sk-") {
		t.Fatalf("code leaked a key: %q", got.Code)
	}
}

func TestExplainFailureRejectsUnsafeCodeTokens(t *testing.T) {
	err := &APIError{Status: 429, Body: `{"error":{"code":"sk-secret value with spaces"}}`}
	got := ExplainFailure(err)
	if strings.Contains(got.Code, "sk-") || strings.Contains(got.Code, " ") {
		t.Fatalf("unsafe code token = %q", got.Code)
	}
}
