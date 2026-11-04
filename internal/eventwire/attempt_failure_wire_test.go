package eventwire

import (
	"encoding/json"
	"strings"
	"testing"

	"pondera/internal/event"
	"pondera/internal/provider"
)

func TestRetryFailureReachesWireWithoutSecrets(t *testing.T) {
	failure := &provider.AttemptFailure{
		Attempt: 3, Max: 19, Kind: provider.FailureKindRateLimit, Phase: "headers",
		Status: 429, Code: "rate_limit_exceeded", Retryable: true, BackoffMs: 4000,
		ProviderID: "relay-1", Model: "deepseek-chat", TraceID: "req-8d2c1",
	}
	wire := ToWire(event.Event{
		Kind: event.Retrying, RetryAttempt: 3, RetryMax: 19,
		RetryScope: event.RetryScopeStream, AttemptFailure: failure,
	})
	payload, err := json.Marshal(wire)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	text := string(payload)
	if !strings.Contains(text, `"attemptFailure"`) || !strings.Contains(text, `"rate_limit"`) {
		t.Fatalf("wire payload lost the failure: %s", text)
	}
	for _, forbidden := range []string{"sk-", "Bearer", "Authorization", "response body"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("wire payload leaked %q: %s", forbidden, text)
		}
	}
	if wire.RetryAttempt != 3 || wire.RetryMax != 19 || wire.AttemptFailure.Max != 19 {
		t.Fatalf("denominator mismatch: %+v", wire)
	}
}

func TestStreamAttemptDiscardCarriesFailure(t *testing.T) {
	failure := &provider.AttemptFailure{
		Attempt: 2, Max: 19, Kind: provider.FailureKindServerError,
		Status: 502, Retryable: true, BackoffMs: 2000,
	}
	wire := ToWire(event.Event{
		Kind: event.StreamAttempt,
		StreamAttempt: event.StreamAttemptInfo{
			ID: "sa-1", Action: event.StreamAttemptDiscard, Attempt: 2, Max: 19,
			Reason: "retry", Failure: failure,
		},
	})
	if wire.StreamAttempt == nil || wire.StreamAttempt.Failure == nil {
		t.Fatalf("stream attempt lost the failure: %+v", wire.StreamAttempt)
	}
	if wire.StreamAttempt.Max != wire.StreamAttempt.Failure.Max || wire.StreamAttempt.Failure.Status != 502 {
		t.Fatalf("stream attempt failure mismatch: %+v", wire.StreamAttempt)
	}
}
