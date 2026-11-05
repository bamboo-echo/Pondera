package main

import (
	"strings"
	"testing"

	"pondera/internal/event"
	"pondera/internal/provider"
)

func TestHistoryRestoresProviderFailureInsteadOfInterruptedNotice(t *testing.T) {
	message := provider.Message{
		Role: provider.RoleTool, LocalOnly: true,
		InterruptedTurn: &provider.InterruptedTurnRecovery{
			Pending: true, TerminalStatus: "failed",
			FailureDiagnostic: &provider.FailureDiagnostic{Kind: "request", Status: 404, ProviderID: "deepseek-anthropic", ProviderDisplayName: "Deepseek2", Protocol: "openai", RequestPath: "/anthropic/v1/chat/completions"},
		},
	}
	history := historyMessages([]provider.Message{message}, func(value string) string { return value })
	if len(history) != 1 {
		t.Fatalf("history = %+v", history)
	}
	got := history[0]
	if got.Code != event.NoticeCodeProviderRequestFailed || got.Level != "warn" || !strings.Contains(got.Content, "Deepseek2 · Chat Completions") || !strings.Contains(got.Content, "HTTP 404") {
		t.Fatalf("failure history = %+v", got)
	}
	if got.Detail != "Connection ID: deepseek-anthropic\nRequest path: /anthropic/v1/chat/completions" || got.Diagnostic == nil || got.Diagnostic.ProviderID != "deepseek-anthropic" {
		t.Fatalf("failure detail = %+v", got)
	}
}

func TestHistoryKeepsLegacyAndExplicitInterruptionsCompatible(t *testing.T) {
	for _, recovery := range []*provider.InterruptedTurnRecovery{{Pending: true}, {Pending: true, TerminalStatus: "interrupted"}} {
		history := historyMessages([]provider.Message{{Role: provider.RoleTool, LocalOnly: true, InterruptedTurn: recovery}}, func(value string) string { return value })
		if len(history) != 1 || history[0].Code != event.NoticeCodeCancelledTurn {
			t.Fatalf("interrupted history = %+v", history)
		}
	}
}

// TestHistoryHydratesPersistedRetryTimeline is the restart acceptance check: a
// failed turn's stored attempts come back on the history notice unchanged, and
// an in-progress turn never grows a read-only timeline.
func TestHistoryHydratesPersistedRetryTimeline(t *testing.T) {
	attempts := []provider.AttemptFailure{
		{Attempt: 1, Max: 3, Kind: provider.FailureKindTimeout, Retryable: true, Status: 504, ElapsedMs: 1200},
		{Attempt: 2, Max: 3, Kind: provider.FailureKindOutputLimit, Retryable: false, Code: "max_tokens", ElapsedMs: 900},
		{Attempt: 3, Max: 3, Kind: provider.FailureKindProtocol, Retryable: false, ElapsedMs: 700},
	}
	recovery := &provider.InterruptedTurnRecovery{
		Pending: true, TerminalStatus: "failed", Attempts: attempts,
		FailureDiagnostic: &provider.FailureDiagnostic{Kind: "request", Status: 504, ProviderID: "deepseek"},
	}
	history := historyMessages([]provider.Message{{Role: provider.RoleTool, LocalOnly: true, InterruptedTurn: recovery}}, func(value string) string { return value })
	if len(history) != 1 || history[0].Code != event.NoticeCodeProviderRequestFailed {
		t.Fatalf("failure history = %+v", history)
	}
	got := history[0].RetryTimeline
	if len(got) != len(attempts) {
		t.Fatalf("retry timeline length = %d, want %d", len(got), len(attempts))
	}
	for i := range attempts {
		if got[i] != attempts[i] {
			t.Fatalf("attempt %d = %+v, want %+v", i+1, got[i], attempts[i])
		}
	}

	inProgress := historyMessages([]provider.Message{{Role: provider.RoleTool, LocalOnly: true, InterruptedTurn: &provider.InterruptedTurnRecovery{Pending: true}}}, func(value string) string { return value })
	if len(inProgress) != 1 || inProgress[0].Code != event.NoticeCodeCancelledTurn || inProgress[0].RetryTimeline != nil {
		t.Fatalf("in-progress history = %+v", inProgress)
	}
}
