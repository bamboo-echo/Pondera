package agent

import (
	"pondera/internal/completion"
	"pondera/internal/event"
)

// emitTurnShadows records end-of-turn facts without a quality decision.
func (a *Agent) emitTurnShadows(input string) {
	if a.task.ledger == nil {
		return
	}
	rep := completion.BuildFacts(a.task.ledger, a.writeWorkspaceRoot, a.scratchRoots())
	a.turn.completion = &rep
}

// CompletionReceipt returns the turn's completion record for the host to
// deliver, or nil when the turn had nothing to judge. The host renders it; the
// agent never writes the user-facing text, which is the whole point.
func (a *Agent) CompletionReceipt() *event.CompletionReceipt {
	if a == nil {
		return nil
	}
	if a.turn != nil && a.turn.completion != nil {
		return completionReceipt(*a.turn.completion)
	}
	// Error/cancellation paths can leave before the normal shadow report.
	// Preserve already-observed checks without changing execution policy.
	return completionReceipt(completion.BuildFacts(a.task.ledger, a.writeWorkspaceRoot, a.scratchRoots()))
}
