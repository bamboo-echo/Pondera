package agent

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"pondera/internal/event"
	"pondera/internal/provider"
)

const (
	maxModelRetryCount = 1000
	firstRetryDelay    = 2 * time.Second
	secondRetryDelay   = 4 * time.Second
	laterRetryDelay    = 8 * time.Second
	// maxServerRetryAfter is the longest wait Pondera will honour from a
	// Retry-After header. Beyond it the retry chain stops: replaying early only
	// re-earns the same 429, and the retry budget cannot shorten a window the
	// server has already fixed.
	maxServerRetryAfter = 60 * time.Second
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

// transportAttemptLimit is the real upper bound of the transport replay chain
// for one model round: the initial request plus one replay per configured
// retry. maxSamplingAttempts bounds a separate repair chain (output/context/
// protocol); adding the two produced a denominator (6 for retry_count=2) that
// the loop could never reach because only 1+retry_count requests are sent.
func (a *Agent) transportAttemptLimit() int {
	return 1 + a.retryCount
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

// retryAfterRefusal ends the retry chain when the server asks for longer than
// maxServerRetryAfter. It wraps the transport error so classification (and
// errors.As) still points at the original 429/5xx, while the message tells the
// reader how long the server wanted.
type retryAfterRefusal struct {
	err  error
	wait time.Duration
}

func (e *retryAfterRefusal) Error() string {
	return fmt.Sprintf("%s (server asked to wait %d seconds before retrying; Pondera stopped instead of retrying early)", e.err.Error(), int(e.wait/time.Second))
}

func (e *retryAfterRefusal) Unwrap() error { return e.err }

// retryModelRequest waits out the backoff and replays the frozen request. It
// returns false when the budget is spent, the failure is not retryable, the
// attempt already produced visible output, or the user cancelled.
func (a *Agent) retryModelRequest(ctx context.Context, state *samplingRecoveryState, result *streamedTurn, sink *deferredStreamSink, attempt int, id string) bool {
	if a.retryCount <= 0 || state.retries >= a.retryCount || ctx.Err() != nil {
		return false
	}
	if !retryableModelFailure(result.err) || !isEmptyStreamResult(result.text, result.reasoning, result.calls, result.responsesItems, result.serverSearch) {
		return false
	}
	// The server's own Retry-After is authoritative inside the cap: waiting the
	// schedule instead just earns another 429. Outside the cap, refuse before
	// spending a retry so the failure names the wait the server demanded.
	serverWait := provider.ClassifyRecovery(result.err).RetryAfter
	if serverWait > maxServerRetryAfter {
		result.err = &retryAfterRefusal{err: result.err, wait: serverWait}
		return false
	}
	state.retries++
	delay := modelRetryDelay(state.retries)
	if serverWait > delay {
		delay = serverWait
	}
	// Denominator = every request this round can still send at most: attempts
	// already made (sampling repairs included), the remaining retries, and the
	// final attempt that ends the chain. Repairs do not consume the retry
	// budget but do consume attempts, so the nominal 1+retry_count grows with
	// them and the counter can never invert (attempt <= max always holds).
	retryMax := a.maxRequestsThisRound(attempt, state.retries)
	failure := a.buildAttemptFailure(result.err, attempt, retryMax, delay, state.attemptStartedAt)
	a.svc.sink.Emit(event.Event{
		Kind:           event.Retrying,
		RetryAttempt:   attempt,
		RetryMax:       retryMax,
		RetryScope:     event.RetryScopeStream,
		AttemptFailure: failure,
	})
	sink.Discard()
	a.emitStreamAttempt(id, event.StreamAttemptDiscard, attempt, "retry", result.err, failure)
	return retryWait(ctx, delay)
}

// buildAttemptFailure fills the timing and identity fields the provider layer
// cannot know. It never includes error text. max is the attempt budget of the
// chain that produced the failure, so the numerator and denominator share one
// counting scheme.
func (a *Agent) buildAttemptFailure(err error, attempt, max int, backoff time.Duration, started time.Time) *provider.AttemptFailure {
	if err == nil {
		return nil
	}
	failure := provider.ExplainFailure(err)
	failure.Attempt = attempt
	failure.Max = max
	if backoff > 0 {
		failure.BackoffMs = int(backoff / time.Millisecond)
	}
	if wait := provider.ClassifyRecovery(err).RetryAfter; wait > 0 {
		failure.RetryAfterMs = int(wait / time.Millisecond)
	}
	if !started.IsZero() {
		failure.ElapsedMs = time.Since(started).Milliseconds()
	}
	failure.At = time.Now().UnixMilli()
	if failure.ProviderID == "" && a.svc.prov != nil {
		failure.ProviderID = a.svc.prov.Name()
	}
	failure.Model = a.modelRef
	return &failure
}

func (a *Agent) protocolAttemptFailure(attempt int) *provider.AttemptFailure {
	return &provider.AttemptFailure{
		Attempt: attempt,
		// The protocol repair chain has one replay; emitProtocolRetry reports
		// the same one-attempt budget, so the counters share a base.
		Max:        1,
		Kind:       provider.FailureKindProtocol,
		Phase:      "protocol",
		Retryable:  true,
		At:         time.Now().UnixMilli(),
		ProviderID: a.svc.prov.Name(),
		Model:      a.modelRef,
	}
}

// logAttemptFailure writes one structured line per discarded attempt. It shares
// the exact object sent to the UI, so there is a single source of truth.
func (a *Agent) logAttemptFailure(id string, attempt int, reason string, failure *provider.AttemptFailure) {
	if failure == nil {
		return
	}
	slog.Warn("model request attempt failed",
		"message_id", id,
		"attempt", attempt,
		"max", failure.Max,
		"scope", "stream",
		"reason", reason,
		"kind", string(failure.Kind),
		"phase", failure.Phase,
		"status", failure.Status,
		"code", failure.Code,
		"retryable", failure.Retryable,
		"backoff_ms", failure.BackoffMs,
		"retry_after_ms", failure.RetryAfterMs,
		"elapsed_ms", failure.ElapsedMs,
		"provider_id", failure.ProviderID,
		"model", failure.Model,
		"trace_id", failure.TraceID,
	)
}
