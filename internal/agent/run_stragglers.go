package agent

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"
)

// ErrPreviousTurnStragglers refuses a new turn while tool calls from the
// previous one are still inside the agent. Starting it anyway would let those
// goroutines observe the new turn's state.
var ErrPreviousTurnStragglers = errors.New("previous turn tool calls are still running; wait for them to finish and retry")

func errPreviousTurnStragglers(live int64) error {
	return fmt.Errorf("%w (%d still running)", ErrPreviousTurnStragglers, live)
}

// runStragglers counts parallel tool goroutines that outlived their batch's
// grace period. The batch already reported their result as unknown, but they
// can still be inside the agent when the next turn resets per-turn state.
type runStragglers struct{ live atomic.Int64 }

func (s *runStragglers) enter() { s.live.Add(1) }
func (s *runStragglers) leave() { s.live.Add(-1) }

// drain waits up to limit for abandoned goroutines to leave the agent and
// reports whether every one of them is gone. The caller decides what to do
// when false; a goroutine that ignores its cancelled context past the limit is
// left to the batch's unknown outcome, exactly as before.
func (s *runStragglers) drain(ctx context.Context, limit time.Duration) bool {
	if s.live.Load() == 0 {
		return true
	}
	deadline := time.Now().Add(limit)
	for s.live.Load() > 0 && time.Now().Before(deadline) && ctx.Err() == nil {
		time.Sleep(10 * time.Millisecond)
	}
	return s.live.Load() == 0
}
