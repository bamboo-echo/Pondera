package control

import (
	"strings"
	"testing"

	"pondera/internal/event"
	"pondera/internal/provider"
	"pondera/internal/session"
)

func TestRetryingPersistsSkippableDiagnosticEvent(t *testing.T) {
	controller := &Controller{}
	failure := &provider.AttemptFailure{
		Attempt: 3, Max: 19, Kind: provider.FailureKindRateLimit, Phase: "headers",
		Status: 429, Code: "rate_limit_exceeded", Retryable: true, BackoffMs: 4000,
		ProviderID: "relay-1", Model: "deepseek-chat", TraceID: "req-8d2c1",
	}
	events, err := controller.v3EventsFor(event.Event{
		Kind: event.Retrying, RetryScope: event.RetryScopeStream,
		RetryAttempt: 3, RetryMax: 19, AttemptFailure: failure,
	}, session.Projection{})
	if err != nil {
		t.Fatalf("v3EventsFor: %v", err)
	}
	var persisted *session.Event
	for i := range events {
		if events[i].Kind == "diagnostic/retry" {
			persisted = &events[i]
		}
	}
	if persisted == nil || !persisted.Optional {
		t.Fatalf("retrying was not persisted as an optional diagnostic event: %+v", events)
	}
	payload := string(persisted.Payload)
	for _, want := range []string{`"attempt":3`, `"max":19`, `"rate_limit"`, `"status":429`} {
		if !strings.Contains(payload, want) {
			t.Fatalf("payload %s missing %s", payload, want)
		}
	}
	for _, forbidden := range []string{"sk-", "Bearer", "response body"} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("payload leaked %q: %s", forbidden, payload)
		}
	}

	// Protocol-scope repairs are internal and must not pollute the session log.
	protocol, err := controller.v3EventsFor(event.Event{
		Kind: event.Retrying, RetryScope: event.RetryScopeProtocol,
		RetryAttempt: 1, RetryMax: 2, AttemptFailure: failure,
	}, session.Projection{})
	if err != nil {
		t.Fatalf("v3EventsFor protocol: %v", err)
	}
	for _, e := range protocol {
		if e.Kind == "diagnostic/retry" {
			t.Fatalf("protocol retry should not be persisted: %+v", e)
		}
	}
}
