package agent

import (
	"context"
	"testing"
	"time"

	"pondera/internal/provider"
)

func TestBuildAttemptFailureKindsAndBackoff(t *testing.T) {
	a := new(Agent)
	a.retryCount = 15
	a.modelRef = "deepseek-chat"
	tests := []struct {
		name      string
		err       error
		attempt   int
		backoff   time.Duration
		kind      provider.FailureKind
		status    int
		retryable bool
	}{
		{"429", &provider.APIError{Status: 429}, 3, 2 * time.Second, provider.FailureKindRateLimit, 429, true},
		{"502", &provider.APIError{Status: 502}, 5, 8 * time.Second, provider.FailureKindServerError, 502, true},
		{"sse cutoff", &provider.StreamInterruptedError{Reason: provider.StreamInterruptPrematureEOF}, 7, 4 * time.Second, provider.FailureKindStreamCut, 0, true},
		{"context overflow", &provider.ContextLimitError{APIError: &provider.APIError{Status: 400}}, 4, 0, provider.FailureKindContextLimit, 400, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			failure := a.buildAttemptFailure(tc.err, tc.attempt, tc.backoff, time.Now())
			if failure == nil {
				t.Fatal("nil failure")
			}
			if failure.Kind != tc.kind || failure.Status != tc.status || failure.Retryable != tc.retryable {
				t.Fatalf("failure = %+v", failure)
			}
			if failure.BackoffMs != int(tc.backoff/time.Millisecond) {
				t.Fatalf("backoff = %d, want %d", failure.BackoffMs, int(tc.backoff/time.Millisecond))
			}
			if failure.Attempt != tc.attempt || failure.Max != maxSamplingAttempts+15 {
				t.Fatalf("denominator = %d/%d, want %d/%d", failure.Attempt, failure.Max, tc.attempt, maxSamplingAttempts+15)
			}
			if failure.Model != "deepseek-chat" || failure.At == 0 {
				t.Fatalf("identity missing: %+v", failure)
			}
		})
	}
}

func TestBuildAttemptFailureIgnoresContextDeadline(t *testing.T) {
	a := new(Agent)
	a.retryCount = 3
	failure := a.buildAttemptFailure(context.DeadlineExceeded, 1, 0, time.Time{})
	if failure == nil || failure.Kind != provider.FailureKindUnknown || failure.Retryable {
		t.Fatalf("deadline classified as %+v, want unknown and non-retryable", failure)
	}
}
