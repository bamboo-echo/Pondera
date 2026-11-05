package agent

import (
	"context"
	"errors"
	"pondera/internal/event"
	"pondera/internal/provider"
	"strings"
)

// maxPersistedAttemptTimeline bounds the retry timeline stored with a failed
// turn so a hostile provider cannot grow the recovery record without limit.
const maxPersistedAttemptTimeline = 32

// recordAttemptFailure keeps the latest failure per attempt. Repair-only
// attempts (output_limit / context_limit / reasoning_replay) reach this path
// too, so reopened history explains them even without a generic retrying row.
func (a *Agent) recordAttemptFailure(failure *provider.AttemptFailure) {
	if failure == nil {
		return
	}
	for i := range a.turnAttempts {
		if a.turnAttempts[i].Attempt == failure.Attempt {
			a.turnAttempts[i] = *failure
			return
		}
	}
	if len(a.turnAttempts) >= maxPersistedAttemptTimeline {
		return
	}
	a.turnAttempts = append(a.turnAttempts, *failure)
}

func (a *Agent) attachAttemptTimeline(record *provider.Message) {
	if record == nil || record.InterruptedTurn == nil || record.InterruptedTurn.TerminalStatus != "failed" {
		return
	}
	if len(a.turnAttempts) == 0 {
		return
	}
	record.InterruptedTurn.Attempts = append([]provider.AttemptFailure(nil), a.turnAttempts...)
}

func (a *Agent) recordInterruptedDisplay(text, reasoning string, calls []provider.ToolCall, pending bool, terminalErr error, workDurationMs int64, messageIDs ...string) {
	var messageID string
	if len(messageIDs) > 0 {
		messageID = messageIDs[0]
	}
	record := interruptedDisplayRecord(messageID, text, reasoning, calls, pending, terminalErr)
	record.WorkDurationMs = workDurationMs
	a.attachAttemptTimeline(&record)
	err := a.appendCommittedMessages(context.Background(), "interrupted-attempt", record)
	if err != nil {
		a.svc.sink.Emit(event.Event{Kind: event.Notice, Level: event.LevelWarn, Code: "transcript_save_failed", Text: "Interrupted output could not be saved; displayed output is retained."})
	} else if messageID != "" {
		a.emitStreamAttempt(messageID, event.StreamAttemptCommit, 0, "interrupted", nil, nil)
	}
}

// InterruptedStreamRecord is the record a cancelled stream leaves, built for a
// stream that is still open so a host can hold it durably until the stream
// either commits its message or never returns.
func InterruptedStreamRecord(messageID, text, reasoning string) provider.Message {
	return interruptedDisplayRecord(messageID, text, reasoning, nil, true, nil)
}

func interruptedDisplayRecord(messageID, text, reasoning string, calls []provider.ToolCall, pending bool, terminalErr error) provider.Message {
	displayCalls := make([]provider.ToolCall, 0, len(calls))
	interrupted := make([]string, 0, len(calls))
	notStarted := make([]provider.InterruptedToolSummary, 0, len(calls))
	seen := make(map[string]struct{}, len(calls))
	for _, call := range calls {
		name := strings.TrimSpace(call.Name)
		key := call.ID + "\x00" + name
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		displayCalls = append(displayCalls, provider.ToolCall{ID: call.ID, Name: name})
		if name != "" {
			interrupted = append(interrupted, name)
			notStarted = append(notStarted, provider.InterruptedToolSummary{ID: call.ID, Name: name})
		}
	}
	terminalStatus := "interrupted"
	var failureDiagnostic *provider.FailureDiagnostic
	if terminalErr != nil && !errors.Is(terminalErr, context.Canceled) {
		terminalStatus = "failed"
		failureDiagnostic = provider.DiagnoseFailure(terminalErr)
	}
	return provider.Message{
		ID:               messageID,
		Role:             provider.RoleTool,
		Content:          text,
		ReasoningContent: reasoning,
		ToolCalls:        displayCalls,
		ToolCallID:       provider.LocalOnlyToolID,
		Name:             provider.LocalOnlyToolName,
		LocalOnly:        true,
		InterruptedTurn: &provider.InterruptedTurnRecovery{
			TerminalStatus:          terminalStatus,
			FailureDiagnostic:       failureDiagnostic,
			Pending:                 pending,
			InterruptedTools:        interrupted,
			NotStartedTools:         notStarted,
			DroppedPartialText:      strings.TrimSpace(text) != "",
			DroppedPartialReasoning: strings.TrimSpace(reasoning) != "",
		},
	}
}
