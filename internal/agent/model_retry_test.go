package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	wantMax := 1 + 3
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

func TestModelRetryDenominatorMatchesRealRequestBound(t *testing.T) {
	// retry_count 0/1/2 plus the config default (3): the denominator must be
	// the number of requests the loop can actually send, 1+retry_count.
	for _, retryCount := range []int{0, 1, 2, 3} {
		t.Run(fmt.Sprintf("retry-%d", retryCount), func(t *testing.T) {
			waits := withoutRetryWait(t)
			statusErr := &provider.APIError{Status: 429}
			turns := make([]testutil.Turn, retryCount+1)
			for i := range turns {
				turns[i] = testutil.Turn{StreamError: statusErr}
			}
			p := testutil.NewMock("always-down", turns...)
			sink := &recordSink{}
			a := New(p, echoRegistry(), NewSession(""), Options{RetryCount: retryCount}, sink)
			_ = a.Run(withNoClosedLoop(context.Background()), "go")

			wantMax := 1 + retryCount
			if p.CallCount() != wantMax {
				t.Fatalf("calls = %d, want %d (initial + %d retries)", p.CallCount(), wantMax, retryCount)
			}
			if len(*waits) != retryCount {
				t.Fatalf("waits = %d, want %d", len(*waits), retryCount)
			}
			retries := sink.kinds(event.Retrying)
			if len(retries) != retryCount {
				t.Fatalf("retrying events = %d, want %d", len(retries), retryCount)
			}
			for i, retry := range retries {
				if retry.RetryAttempt != i+1 {
					t.Fatalf("retry %d numerator = %d, want %d", i+1, retry.RetryAttempt, i+1)
				}
				if retry.RetryAttempt > retry.RetryMax {
					t.Fatalf("retry counter %d/%d exceeds denominator", retry.RetryAttempt, retry.RetryMax)
				}
				if retry.RetryMax != wantMax {
					t.Fatalf("retry %d denominator = %d, want %d", i+1, retry.RetryMax, wantMax)
				}
				if retry.AttemptFailure == nil || retry.AttemptFailure.Attempt != retry.RetryAttempt || retry.AttemptFailure.Max != retry.RetryMax {
					t.Fatalf("retry %d failure counters disagree: %+v", i+1, retry.AttemptFailure)
				}
			}
		})
	}
}

func TestModelRetryCountersStayCoherentWithSamplingRepairs(t *testing.T) {
	// Sampling repairs consume attempts without consuming retries. Two repairs
	// before the retries would push attempt 4 past a denominator of
	// 1+retry_count; the counter must grow with the repairs instead.
	waits := withoutRetryWait(t)
	output := &provider.OutputLimitError{
		APIError:        &provider.APIError{Status: 400},
		MaxOutputTokens: 1024,
	}
	overflow := &provider.ContextLimitError{
		APIError:         &provider.APIError{Status: 400},
		WindowTokens:     10_000,
		PromptTokens:     100,
		CompletionTokens: 512,
	}
	p := testutil.NewMock("mixed",
		testutil.Turn{StreamError: output},
		testutil.Turn{StreamError: overflow},
		testutil.Turn{StreamError: &provider.APIError{Status: 503}},
		testutil.Turn{StreamError: &provider.APIError{Status: 503}},
		testutil.Turn{StreamError: &provider.APIError{Status: 503}},
	)
	sink := &recordSink{}
	a := New(p, echoRegistry(), NewSession(""), Options{RetryCount: 2, ContextWindow: 1_000_000, MaxOutputTokens: 4096}, sink)
	_ = a.Run(withNoClosedLoop(context.Background()), "go")

	const repairs = 2
	wantMax := 1 + 2 + repairs
	if p.CallCount() != wantMax {
		t.Fatalf("calls = %d, want %d (initial + %d retries + %d repairs)", p.CallCount(), wantMax, 2, repairs)
	}
	if len(*waits) != 2 {
		t.Fatalf("waits = %d, want 2 retries", len(*waits))
	}
	retries := sink.kinds(event.Retrying)
	if len(retries) != 2 {
		t.Fatalf("retrying events = %d, want 2: %+v", len(retries), retries)
	}
	for i, retry := range retries {
		if retry.RetryMax != wantMax {
			t.Fatalf("retry %d denominator = %d, want %d", i+1, retry.RetryMax, wantMax)
		}
		if retry.RetryAttempt > retry.RetryMax {
			t.Fatalf("inverted counter %d/%d", retry.RetryAttempt, retry.RetryMax)
		}
		if retry.AttemptFailure == nil || retry.AttemptFailure.Attempt != retry.RetryAttempt || retry.AttemptFailure.Max != retry.RetryMax {
			t.Fatalf("retry %d failure counters disagree: %+v", i+1, retry.AttemptFailure)
		}
	}
	if retries[0].RetryAttempt != 3 || retries[1].RetryAttempt != 4 {
		t.Fatalf("retry numerators = %d,%d, want 3,4 after two repairs", retries[0].RetryAttempt, retries[1].RetryAttempt)
	}

	// The repair-chain discards must report the same denominator base as the
	// retry chain: attempts sent so far + retries still unspent + the replay
	// being scheduled. A fixed maxSamplingAttempts here would disagree with the
	// retry events in the same turn.
	repairMax := map[string]int{}
	for _, attempt := range sink.kinds(event.StreamAttempt) {
		info := attempt.StreamAttempt
		if info.Action != event.StreamAttemptDiscard {
			continue
		}
		if info.Reason != "output_limit" && info.Reason != "context_limit" {
			continue
		}
		repairMax[info.Reason] = info.Max
		if info.Attempt > info.Max {
			t.Fatalf("%s counter %d/%d exceeds its denominator", info.Reason, info.Attempt, info.Max)
		}
		if info.Failure == nil || info.Failure.Attempt != info.Attempt || info.Failure.Max != info.Max {
			t.Fatalf("%s repair counters disagree: %+v", info.Reason, info.Failure)
		}
	}
	wantRepairMax := map[string]int{"output_limit": 1 + 2 + 1, "context_limit": 2 + 2 + 1}
	for reason, want := range wantRepairMax {
		if repairMax[reason] != want {
			t.Fatalf("%s max = %d, want %d (same base as the retry chain)", reason, repairMax[reason], want)
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

func TestModelRetryHonoursServerRetryAfterInsideCap(t *testing.T) {
	waits := withoutRetryWait(t)
	sink := &recordSink{}
	p := testutil.NewMock("limited",
		testutil.Turn{StreamError: &provider.APIError{Status: 429, RetryAfter: 30 * time.Second}},
		testutil.Turn{Text: "ok"},
	)
	a := New(p, echoRegistry(), NewSession(""), Options{RetryCount: 3}, sink)
	if err := a.Run(withNoClosedLoop(context.Background()), "go"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(*waits) != 1 || (*waits)[0] != 30*time.Second {
		t.Fatalf("waits = %v, want [30s] (the server Retry-After wins over the 2s schedule)", *waits)
	}
	retries := sink.kinds(event.Retrying)
	if len(retries) != 1 {
		t.Fatalf("retrying events = %d, want 1", len(retries))
	}
	failure := retries[0].AttemptFailure
	if failure == nil || failure.BackoffMs != 30000 || failure.RetryAfterMs != 30000 {
		t.Fatalf("failure = %+v, want backoff and retry-after both 30000ms", failure)
	}
}

func TestModelRetryRefusesServerWaitBeyondCap(t *testing.T) {
	previous := retryWait
	retryWait = func(context.Context, time.Duration) bool {
		t.Fatal("a wait beyond the cap must stop instead of sleeping")
		return false
	}
	t.Cleanup(func() { retryWait = previous })

	sink := &recordSink{}
	p := testutil.NewMock("limited",
		testutil.Turn{StreamError: &provider.APIError{Status: 429, RetryAfter: 120 * time.Second}},
		testutil.Turn{Text: "never"},
	)
	a := New(p, echoRegistry(), NewSession(""), Options{RetryCount: 15}, sink)
	err := a.Run(withNoClosedLoop(context.Background()), "go")
	if err == nil {
		t.Fatal("want a failure naming the server wait")
	}
	if !strings.Contains(err.Error(), "120") {
		t.Fatalf("err = %v, want the server wait in seconds", err)
	}
	var api *provider.APIError
	if !errors.As(err, &api) || api.Status != 429 {
		t.Fatalf("err = %v, want the original 429 preserved for classification", err)
	}
	if p.CallCount() != 1 || len(sink.kinds(event.Retrying)) != 0 {
		t.Fatalf("calls = %d, retries = %d, want 1 and 0", p.CallCount(), len(sink.kinds(event.Retrying)))
	}
}

func TestModelRetryFallsBackToScheduleWhenRetryAfterIsZero(t *testing.T) {
	// parseRetryAfter yields 0 for a missing header and for an elapsed HTTP-date,
	// so both land on the unchanged 2s schedule.
	waits := withoutRetryWait(t)
	sink := &recordSink{}
	p := testutil.NewMock("limited",
		testutil.Turn{StreamError: &provider.APIError{Status: 429}},
		testutil.Turn{Text: "ok"},
	)
	a := New(p, echoRegistry(), NewSession(""), Options{RetryCount: 3}, sink)
	if err := a.Run(withNoClosedLoop(context.Background()), "go"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(*waits) != 1 || (*waits)[0] != 2*time.Second {
		t.Fatalf("waits = %v, want [2s]", *waits)
	}
	if failure := sink.kinds(event.Retrying)[0].AttemptFailure; failure == nil || failure.RetryAfterMs != 0 || failure.BackoffMs != 2000 {
		t.Fatalf("failure = %+v, want no retry-after and the 2000ms schedule", failure)
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
