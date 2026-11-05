package agent

import (
	"errors"
	"testing"

	"pondera/internal/provider"
)

// TestFailedTurnPersistsRetryTimeline proves the retry timeline is attached to
// a failed turn's recovery record (and therefore survives a restart) while an
// interrupted or pending turn never claims one.
func TestFailedTurnPersistsRetryTimeline(t *testing.T) {
	a := &Agent{}
	a.recordAttemptFailure(&provider.AttemptFailure{Attempt: 1, Max: 3, Kind: provider.FailureKindTimeout, Retryable: true})
	// A repair-only attempt replaces the same attempt slot instead of growing.
	a.recordAttemptFailure(&provider.AttemptFailure{Attempt: 1, Max: 3, Kind: provider.FailureKindOutputLimit, Retryable: false})
	a.recordAttemptFailure(&provider.AttemptFailure{Attempt: 2, Max: 3, Kind: provider.FailureKindContextLimit, Retryable: false})
	if len(a.turnAttempts) != 2 {
		t.Fatalf("attempt timeline = %+v", a.turnAttempts)
	}

	failed := interruptedDisplayRecord("", "", "", nil, true, errors.New("provider failed"))
	a.attachAttemptTimeline(&failed)
	if failed.InterruptedTurn == nil || failed.InterruptedTurn.TerminalStatus != "failed" {
		t.Fatalf("failed record = %+v", failed.InterruptedTurn)
	}
	if len(failed.InterruptedTurn.Attempts) != 2 {
		t.Fatalf("persisted attempts = %+v", failed.InterruptedTurn.Attempts)
	}
	if failed.InterruptedTurn.Attempts[0].Kind != provider.FailureKindOutputLimit || failed.InterruptedTurn.Attempts[1].Kind != provider.FailureKindContextLimit {
		t.Fatalf("persisted attempts = %+v", failed.InterruptedTurn.Attempts)
	}

	pending := interruptedDisplayRecord("", "", "", nil, true, nil)
	a.attachAttemptTimeline(&pending)
	if pending.InterruptedTurn == nil || pending.InterruptedTurn.TerminalStatus != "interrupted" || len(pending.InterruptedTurn.Attempts) != 0 {
		t.Fatalf("in-progress record must not hydrate attempts: %+v", pending.InterruptedTurn)
	}
}
