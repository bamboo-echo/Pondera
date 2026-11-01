package agent

import (
	"context"
	"time"

	"pondera/internal/event"
	"pondera/internal/provider"
)

const (
	maxModelRetryCount = 1000
	firstRetryDelay    = 2 * time.Second
	secondRetryDelay   = 4 * time.Second
	laterRetryDelay    = 8 * time.Second
)

// retryWait owns the backoff sleep so tests can replace it. Production waits
// interruptibly: cancellation returns false without another attempt.
var retryWait = func(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func normalizeRetryCount(n int) int {
	if n < 0 {
		return 0
	}
	if n > maxModelRetryCount {
		return maxModelRetryCount
	}
	return n
}

// modelRetryDelay returns the wait before retry number `retry` (1-based):
// 2s, 4s, then 8s for every later attempt.
func modelRetryDelay(retry int) time.Duration {
	switch retry {
	case 1:
		return firstRetryDelay
	case 2:
		return secondRetryDelay
	default:
		return laterRetryDelay
	}
}

// retryableModelFailure reports whether a failure with no visible output may be
// replayed. Limit, protocol, auth and quota failures keep their dedicated
// recovery paths; empty responses keep the finalizer retry.
func retryableModelFailure(err error) bool {
	failure := provider.ClassifyRecovery(err)
	if !failure.Retryable {
		return false
	}
	switch failure.Phase {
	case "headers", "connect", "stream":
		return true
	default:
		return false
	}
}

// retryModelRequest waits out the backoff and replays the frozen request. It
// returns false when the budget is spent, the failure is not retryable, the
// attempt already produced visible output, or the user cancelled.
func (a *Agent) retryModelRequest(ctx context.Context, state *samplingRecoveryState, result streamedTurn, sink *deferredStreamSink, attempt int, id string) bool {
	if a.retryCount <= 0 || state.retries >= a.retryCount || ctx.Err() != nil {
		return false
	}
	if !retryableModelFailure(result.err) || !isEmptyStreamResult(result.text, result.reasoning, result.calls, result.responsesItems, result.serverSearch) {
		return false
	}
	state.retries++
	delay := modelRetryDelay(state.retries)
	a.svc.sink.Emit(event.Event{
		Kind:         event.Retrying,
		RetryAttempt: state.retries,
		RetryMax:     a.retryCount,
		RetryScope:   event.RetryScopeStream,
	})
	sink.Discard()
	a.emitStreamAttempt(id, event.StreamAttemptDiscard, attempt, "retry", result.err)
	return retryWait(ctx, delay)
}
