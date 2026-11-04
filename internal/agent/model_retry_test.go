package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"pondera/internal/agent/testutil"
	"pondera/internal/event"
	"pondera/internal/provider"
)

func withoutRetryWait(t *testing.T) *[]time.Duration {
	t.Helper()
	previous := retryWait
	waits := &[]time.Duration{}
	retryWait = func(_ context.Context, d time.Duration) bool {
		*waits = append(*waits, d)
		return true
	}
	t.Cleanup(func() { retryWait = previous })
	return waits
}

func TestModelRetryReplaysTransientFailuresWithConfiguredBackoff(t *testing.T) {
	waits := withoutRetryWait(t)
	sink := &recordSink{}
	p := testutil.NewMock("flaky", testutil.Turn{StreamError: &provider.APIError{Status: 503}}, testutil.Turn{StreamError: &provider.APIError{Status: 503}}, testutil.Turn{Text: "ok"})
	a := New(p, echoRegistry(), NewSession(""), Options{RetryCount: 3}, sink)

	if err := a.Run(withNoClosedLoop(context.Background()), "go"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if p.CallCount() != 3 {
		t.Fatalf("calls = %d, want 3 (initial + 2 retries)", p.CallCount())
	}
	if len(*waits) != 2 || (*waits)[0] != 2*time.Second || (*waits)[1] != 4*time.Second {
		t.Fatalf("waits = %v, want [2s 4s]", *waits)
	}
	retries := sink.kinds(event.Retrying)
	wantMax := maxSamplingAttempts + 3
	if len(retries) != 2 || retries[0].RetryAttempt != 1 || retries[1].RetryAttempt != 2 || retries[0].RetryMax != wantMax {
		t.Fatalf("retrying events = %+v", retries)
	}
	wantKinds := []provider.FailureKind{provider.FailureKindServerError, provider.FailureKindServerError}
	wantBackoff := []int{2000, 4000}
	for i, retry := range retries {
		failure := retry.AttemptFailure
		if failure == nil {
			t.Fatalf("retry %d has no attempt failure", i+1)
		}
		if failure.Kind != wantKinds[i] || failure.Status != 503 || !failure.Retryable || failure.BackoffMs != wantBackoff[i] {
			t.Fatalf("retry %d failure = %+v", i+1, failure)
		}
		if failure.Attempt != retry.RetryAttempt || failure.Max != retry.RetryMax {
			t.Fatalf("retry %d denominators disagree: retry=%d/%d failure=%d/%d", i+1, retry.RetryAttempt, retry.RetryMax, failure.Attempt, failure.Max)
		}
	}
	for _, attempt := range sink.kinds(event.StreamAttempt) {
		if attempt.StreamAttempt.Action != event.StreamAttemptDiscard {
			continue
		}
		if attempt.StreamAttempt.Max != wantMax {
			t.Fatalf("stream attempt max = %d, retry max = %d, want both %d", attempt.StreamAttempt.Max, wantMax, wantMax)
		}
		if attempt.StreamAttempt.Failure == nil || attempt.StreamAttempt.Failure.Status != 503 {
			t.Fatalf("discard failure = %+v", attempt.StreamAttempt.Failure)
		}
	}
}

func TestModelRetryStopsAtConfiguredBudget(t *testing.T) {
	waits := withoutRetryWait(t)
	statusErr := &provider.APIError{Status: 429}
	p := testutil.NewMock("always-down", testutil.Turn{StreamError: statusErr}, testutil.Turn{StreamError: statusErr}, testutil.Turn{StreamError: statusErr})
	sink := &recordSink{}
	a := New(p, echoRegistry(), NewSession(""), Options{RetryCount: 2}, sink)

	err := a.Run(withNoClosedLoop(context.Background()), "go")
	var api *provider.APIError
	if !errors.As(err, &api) || api.Status != 429 {
		t.Fatalf("err = %v, want the last 429", err)
	}
	if p.CallCount() != 3 {
		t.Fatalf("calls = %d, want 3 (initial + 2 retries)", p.CallCount())
	}
	if len(*waits) != 2 {
		t.Fatalf("waits = %v, want two delays", *waits)
	}
}

func TestModelRetryDisabledAndPermanentFailuresStayFailFast(t *testing.T) {
	previous := retryWait
	retryWait = func(context.Context, time.Duration) bool {
		t.Fatal("a disabled or permanent retry must not wait")
		return false
	}
	t.Cleanup(func() { retryWait = previous })

	for _, tc := range []struct {
		name  string
		opts  Options
		turns []testutil.Turn
	}{
		{"disabled", Options{RetryCount: 0}, []testutil.Turn{{StreamError: &provider.APIError{Status: 503}}, {Text: "never"}}},
		{"permanent", Options{RetryCount: 3}, []testutil.Turn{{StreamError: &provider.APIError{Status: 400}}, {Text: "never"}}},
		{"auth", Options{RetryCount: 3}, []testutil.Turn{{StreamError: &provider.AuthError{Status: 401}}, {Text: "never"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := testutil.NewMock("permanent", tc.turns...)
			sink := &recordSink{}
			a := New(p, echoRegistry(), NewSession(""), tc.opts, sink)
			_ = a.Run(withNoClosedLoop(context.Background()), "go")
			if p.CallCount() != 1 || len(sink.kinds(event.Retrying)) != 0 {
				t.Fatalf("calls = %d, retries = %d", p.CallCount(), len(sink.kinds(event.Retrying)))
			}
		})
	}
}

func TestModelRetrySkipsAttemptsThatAlreadyProducedOutput(t *testing.T) {
	previous := retryWait
	retryWait = func(context.Context, time.Duration) bool {
		t.Fatal("a visible partial attempt must not be replayed")
		return false
	}
	t.Cleanup(func() { retryWait = previous })

	p := testutil.NewMock("partial", testutil.Turn{Text: "partial", ChunkError: errors.New("stream reset")}, testutil.Turn{Text: "never"})
	sink := &recordSink{}
	a := New(p, echoRegistry(), NewSession(""), Options{RetryCount: 3}, sink)
	_ = a.Run(withNoClosedLoop(context.Background()), "go")
	if p.CallCount() != 1 {
		t.Fatalf("calls = %d, want 1", p.CallCount())
	}
}

func TestModelRetryDelayScheduleAndClamp(t *testing.T) {
	for retry, want := range map[int]time.Duration{1: 2 * time.Second, 2: 4 * time.Second, 3: 8 * time.Second, 1000: 8 * time.Second} {
		if got := modelRetryDelay(retry); got != want {
			t.Errorf("retry %d delay = %s, want %s", retry, got, want)
		}
	}
	if got := normalizeRetryCount(1001); got != 1000 {
		t.Errorf("normalize(1001) = %d, want 1000", got)
	}
	if got := normalizeRetryCount(-5); got != 0 {
		t.Errorf("normalize(-5) = %d, want 0", got)
	}
}
