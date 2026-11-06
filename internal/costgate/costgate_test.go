package costgate

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestLedger(t *testing.T) *Ledger {
	t.Helper()
	l, err := Open(filepath.Join(t.TempDir(), "cost-ledger.json"))
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestEstimateUSDAndPriceOverride(t *testing.T) {
	l := newTestLedger(t)
	if got := l.EstimateUSD("deepseek-chat", 1000, 1000); got < 0.001 || got > 0.002 {
		t.Fatalf("unexpected estimate: %f", got)
	}
	if err := l.SetPrice("my-model", Price{InputPer1K: 1, OutputPer1K: 2}); err != nil {
		t.Fatal(err)
	}
	if got := l.EstimateUSD("my-model", 1000, 1000); got != 3 {
		t.Fatalf("override price not used: %f", got)
	}
}

func TestRecordAndSessionLimit(t *testing.T) {
	l := newTestLedger(t)
	if err := l.SetLimits(Limits{Enabled: true, MaxUSDPerSession: 1, MaxUSDPerDay: 10, MaxUSDPerTask: 0, MaxConcurrentJobs: 0, MaxTurnSeconds: 0}); err != nil {
		t.Fatal(err)
	}
	if err := l.Record(Entry{SessionID: "s1", USD: 0.4}); err != nil {
		t.Fatal(err)
	}
	d, err := l.Check("s1", "", 0)
	if err != nil || !d.Allowed {
		t.Fatalf("should be allowed: %+v %v", d, err)
	}
	if err := l.Record(Entry{SessionID: "s1", USD: 0.7}); err != nil {
		t.Fatal(err)
	}
	d, err = l.Check("s1", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if d.Allowed {
		t.Fatal("session limit should have been enforced")
	}
	// A different session is unaffected.
	d2, _ := l.Check("s2", "", 0)
	if !d2.Allowed {
		t.Fatal("limit must be per-session")
	}
}

func TestDailyAndTaskLimits(t *testing.T) {
	l := newTestLedger(t)
	l.SetClock(func() time.Time { return time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC) })
	lim := DefaultLimits()
	lim.MaxUSDPerDay = 5
	lim.MaxUSDPerTask = 1
	lim.MaxUSDPerSession = 100
	if err := l.SetLimits(lim); err != nil {
		t.Fatal(err)
	}
	if err := l.Record(Entry{SessionID: "s", TaskID: "t1", USD: 1.2}); err != nil {
		t.Fatal(err)
	}
	d, _ := l.Check("s", "t1", 0)
	if d.Allowed {
		t.Fatal("task limit should be enforced")
	}
	// Next day: daily usage resets but the task limit does not.
	l.SetClock(func() time.Time { return time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC) })
	d2, _ := l.Check("s", "t2", 0)
	if !d2.Allowed || d2.DayUSD != 0 {
		t.Fatalf("daily rollover failed: %+v", d2)
	}
}

func TestDailyLimitSumsAcrossSessions(t *testing.T) {
	l := newTestLedger(t)
	l.SetClock(func() time.Time { return time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC) })
	lim := DefaultLimits()
	lim.MaxUSDPerSession = 100
	lim.MaxUSDPerDay = 1
	if err := l.SetLimits(lim); err != nil {
		t.Fatal(err)
	}
	for _, e := range []Entry{
		{SessionID: "s1", USD: 0.6},
		{SessionID: "s2", USD: 0.6},
	} {
		if err := l.Record(e); err != nil {
			t.Fatal(err)
		}
	}
	d, err := l.Check("s1", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if d.SessionUSD < 0.59 || d.SessionUSD > 0.61 {
		t.Fatalf("session spend must stay scoped to the session: %+v", d)
	}
	if d.DayUSD < 1.19 || d.DayUSD > 1.21 || d.Allowed {
		t.Fatalf("daily spend must aggregate every session (%.2f) and block: %+v", d.DayUSD, d)
	}
}

func TestConcurrencyAndTurnLimits(t *testing.T) {
	l := newTestLedger(t)
	lim := DefaultLimits()
	lim.MaxConcurrentJobs = 2
	lim.MaxTurnSeconds = 60
	if err := l.SetLimits(lim); err != nil {
		t.Fatal(err)
	}
	l.SetActiveJobs(2)
	d, _ := l.Check("s", "", 0)
	if d.Allowed || d.Reason == "" {
		t.Fatalf("concurrency gate failed: %+v", d)
	}
	l.SetActiveJobs(0)
	d2, _ := l.Check("s", "", 61)
	if d2.Allowed {
		t.Fatal("turn time gate failed")
	}
}

func TestPersistenceAndReset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.SetLimits(Limits{Enabled: true, MaxUSDPerSession: 3}); err != nil {
		t.Fatal(err)
	}
	if err := l.Record(Entry{SessionID: "s", TokensIn: 1000, TokensOut: 1000, Model: "gpt-4o"}); err != nil {
		t.Fatal(err)
	}
	l2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if l2.Status().Entries != 1 || l2.Limits().MaxUSDPerSession != 3 {
		t.Fatalf("state not persisted: %+v", l2.Status())
	}
	if err := l2.Reset(); err != nil {
		t.Fatal(err)
	}
	if l2.Status().Entries != 0 || l2.Limits().MaxUSDPerSession != 3 {
		t.Fatal("reset should clear usage but keep limits")
	}
}

func TestDisabledGateAlwaysAllows(t *testing.T) {
	l := newTestLedger(t)
	if err := l.SetLimits(Limits{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if err := l.Record(Entry{SessionID: "s", USD: 1000}); err != nil {
		t.Fatal(err)
	}
	d, _ := l.Check("s", "", 99999)
	if !d.Allowed {
		t.Fatal("disabled gate must allow")
	}
}

func TestCorruptLedgerRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := writeBad(path); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("a corrupt ledger must be refused")
	}
}

func writeBad(path string) error { return os.WriteFile(path, []byte("{not-json"), 0o600) }
