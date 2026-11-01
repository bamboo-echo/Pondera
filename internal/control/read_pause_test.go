package control

import (
	"fmt"
	"testing"

	"pondera/internal/agent"
	"pondera/internal/event"
)

func TestReadPauseKeepsItsTerminalOutcome(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", &agent.IncompleteReadError{Reason: "page budget"})
	if turnOutcome(err) != event.TurnOutcomeIncompleteRead {
		t.Fatal("read pause lost its outcome")
	}
}
