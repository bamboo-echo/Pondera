// Package costgate implements a fail-closed spending and concurrency gate for
// cloud-side agent runs. It is deliberately transport-independent: the CLI,
// the serve endpoints and the mobile UI all share one persisted ledger.
package costgate

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type Price struct {
	InputPer1K  float64 `json:"inputPer1K"`
	OutputPer1K float64 `json:"outputPer1K"`
}

// DefaultPrices are conservative list prices in USD per 1K tokens. Operators
// can override any model via `pondera cloud cost set-price`.
var DefaultPrices = map[string]Price{
	"default":           {InputPer1K: 0.01, OutputPer1K: 0.03},
	"deepseek-chat":     {InputPer1K: 0.00027, OutputPer1K: 0.0011},
	"deepseek-reasoner": {InputPer1K: 0.00055, OutputPer1K: 0.00219},
	"claude-sonnet-4":   {InputPer1K: 0.003, OutputPer1K: 0.015},
	"claude-opus-4":     {InputPer1K: 0.015, OutputPer1K: 0.075},
	"gpt-4o":            {InputPer1K: 0.0025, OutputPer1K: 0.01},
	"gpt-4.1":           {InputPer1K: 0.002, OutputPer1K: 0.008},
	"gemini-2.5-pro":    {InputPer1K: 0.00125, OutputPer1K: 0.01},
}

type Limits struct {
	Enabled           bool             `json:"enabled"`
	MaxUSDPerSession  float64          `json:"maxUsdPerSession"`
	MaxUSDPerDay      float64          `json:"maxUsdPerDay"`
	MaxUSDPerTask     float64          `json:"maxUsdPerTask"`
	MaxConcurrentJobs int              `json:"maxConcurrentJobs"`
	MaxTurnSeconds    int              `json:"maxTurnSeconds"`
	Prices            map[string]Price `json:"prices,omitempty"`
}

func DefaultLimits() Limits {
	return Limits{
		Enabled: true, MaxUSDPerSession: 5, MaxUSDPerDay: 20,
		MaxUSDPerTask: 2, MaxConcurrentJobs: 2, MaxTurnSeconds: 900,
	}
}

type Entry struct {
	At        time.Time `json:"at"`
	SessionID string    `json:"sessionId"`
	TaskID    string    `json:"taskId,omitempty"`
	Model     string    `json:"model,omitempty"`
	Label     string    `json:"label,omitempty"`
	TokensIn  int64     `json:"tokensIn"`
	TokensOut int64     `json:"tokensOut"`
	USD       float64   `json:"usd"`
}

type state struct {
	Limits  Limits  `json:"limits"`
	Entries []Entry `json:"entries"`
}

type Ledger struct {
	path       string
	mu         sync.Mutex
	st         state
	now        func() time.Time
	activeJobs int
}

// Open loads (or creates) the ledger at path. Missing files start from the
// default limits; corrupt files are refused rather than silently reset.
func Open(path string) (*Ledger, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("costgate: empty ledger path")
	}
	l := &Ledger{path: path, now: time.Now, st: state{Limits: DefaultLimits(), Entries: []Entry{}}}
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &l.st); err != nil {
			return nil, fmt.Errorf("costgate: ledger is corrupt: %w", err)
		}
		if l.st.Limits.Prices == nil {
			l.st.Limits.Prices = map[string]Price{}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return l, nil
}

func (l *Ledger) SetClock(now func() time.Time) {
	if now != nil {
		l.mu.Lock()
		l.now = now
		l.mu.Unlock()
	}
}

func (l *Ledger) flushLocked() error {
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(l.st, "", "  ")
	if err != nil {
		return err
	}
	tmp := l.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, l.path)
}

func (l *Ledger) Limits() Limits {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.st.Limits
}

func (l *Ledger) SetLimits(lim Limits) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if lim.MaxUSDPerSession < 0 || lim.MaxUSDPerDay < 0 || lim.MaxUSDPerTask < 0 ||
		lim.MaxConcurrentJobs < 0 || lim.MaxTurnSeconds < 0 {
		return errors.New("costgate: limits must not be negative")
	}
	if lim.Prices == nil {
		lim.Prices = l.st.Limits.Prices
	}
	l.st.Limits = lim
	return l.flushLocked()
}

func (l *Ledger) SetPrice(model string, p Price) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if strings.TrimSpace(model) == "" || p.InputPer1K < 0 || p.OutputPer1K < 0 {
		return errors.New("costgate: invalid price")
	}
	if l.st.Limits.Prices == nil {
		l.st.Limits.Prices = map[string]Price{}
	}
	l.st.Limits.Prices[strings.ToLower(model)] = p
	return l.flushLocked()
}

// EstimateUSD converts token counts using the configured (or default) price.
func (l *Ledger) EstimateUSD(model string, tokensIn, tokensOut int64) float64 {
	l.mu.Lock()
	p := l.priceLocked(model)
	l.mu.Unlock()
	return float64(tokensIn)/1000*p.InputPer1K + float64(tokensOut)/1000*p.OutputPer1K
}

func (l *Ledger) priceLocked(model string) Price {
	key := strings.ToLower(strings.TrimSpace(model))
	p, ok := l.st.Limits.Prices[key]
	if !ok {
		p = DefaultPrices[key]
	}
	if p.InputPer1K == 0 && p.OutputPer1K == 0 {
		p = DefaultPrices["default"]
	}
	return p
}

func (l *Ledger) Record(e Entry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if strings.TrimSpace(e.SessionID) == "" {
		return errors.New("costgate: entry requires a session id")
	}
	if e.At.IsZero() {
		e.At = l.now().UTC()
	}
	if e.USD == 0 && (e.TokensIn > 0 || e.TokensOut > 0) {
		p := l.priceLocked(e.Model)
		e.USD = float64(e.TokensIn)/1000*p.InputPer1K + float64(e.TokensOut)/1000*p.OutputPer1K
	}
	if e.USD < 0 || math.IsNaN(e.USD) || math.IsInf(e.USD, 0) {
		return errors.New("costgate: invalid cost")
	}
	l.st.Entries = append(l.st.Entries, e)
	// Keep the ledger bounded: retain the most recent 20k entries.
	if len(l.st.Entries) > 20000 {
		l.st.Entries = append([]Entry(nil), l.st.Entries[len(l.st.Entries)-20000:]...)
	}
	return l.flushLocked()
}

func (l *Ledger) SetActiveJobs(n int) {
	l.mu.Lock()
	if n < 0 {
		n = 0
	}
	l.activeJobs = n
	l.mu.Unlock()
}

type Decision struct {
	Allowed      bool    `json:"allowed"`
	Reason       string  `json:"reason,omitempty"`
	SessionUSD   float64 `json:"sessionUsd"`
	DayUSD       float64 `json:"dayUsd"`
	TaskUSD      float64 `json:"taskUsd"`
	RemainingUSD float64 `json:"remainingUsd"`
	ActiveJobs   int     `json:"activeJobs"`
}

// Check evaluates the gate before a new turn is submitted. It never mutates
// state, so it is safe to call on every request.
func (l *Ledger) Check(sessionID, taskID string, turnSeconds int) (*Decision, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	lim := l.st.Limits
	day := l.now().UTC().Format("2006-01-02")
	d := &Decision{Allowed: true, ActiveJobs: l.activeJobs}
	if !lim.Enabled {
		d.Reason = "gate disabled"
		return d, nil
	}
	for _, e := range l.st.Entries {
		if e.SessionID == sessionID {
			d.SessionUSD += e.USD
			if taskID != "" && e.TaskID == taskID {
				d.TaskUSD += e.USD
			}
		}
		// The daily budget is global: every recorded session contributes,
		// otherwise a client could escape it by spreading work across sessions.
		if e.At.UTC().Format("2006-01-02") == day {
			d.DayUSD += e.USD
		}
	}
	remaining := math.Inf(1)
	check := func(value, limit float64, label string) {
		if limit > 0 && value >= limit {
			d.Allowed = false
			d.Reason = fmt.Sprintf("%s limit reached ($%.4f / $%.2f)", label, value, limit)
		}
		if limit > 0 && limit-value < remaining {
			remaining = limit - value
		}
	}
	check(d.SessionUSD, lim.MaxUSDPerSession, "session")
	check(d.DayUSD, lim.MaxUSDPerDay, "daily")
	if taskID != "" {
		check(d.TaskUSD, lim.MaxUSDPerTask, "task")
	}
	if remaining > 0 && !math.IsInf(remaining, 1) {
		d.RemainingUSD = remaining
	}
	if lim.MaxConcurrentJobs > 0 && l.activeJobs >= lim.MaxConcurrentJobs {
		d.Allowed = false
		d.Reason = fmt.Sprintf("concurrency limit reached (%d / %d)", l.activeJobs, lim.MaxConcurrentJobs)
	}
	if lim.MaxTurnSeconds > 0 && turnSeconds > lim.MaxTurnSeconds {
		d.Allowed = false
		d.Reason = fmt.Sprintf("turn time limit exceeded (%ds / %ds)", turnSeconds, lim.MaxTurnSeconds)
	}
	if d.Allowed && d.Reason == "" {
		d.Reason = "within limits"
	}
	return d, nil
}

type Status struct {
	Limits      Limits             `json:"limits"`
	TodayUSD    float64            `json:"todayUsd"`
	WeekUSD     float64            `json:"weekUsd"`
	TotalUSD    float64            `json:"totalUsd"`
	Entries     int                `json:"entries"`
	BySession   map[string]float64 `json:"bySession"`
	ActiveJobs  int                `json:"activeJobs"`
	LastEntries []Entry            `json:"lastEntries,omitempty"`
}

func (l *Ledger) Status() Status {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now().UTC()
	day := now.Format("2006-01-02")
	weekAgo := now.AddDate(0, 0, -7)
	st := Status{Limits: l.st.Limits, BySession: map[string]float64{}, ActiveJobs: l.activeJobs, Entries: len(l.st.Entries)}
	for _, e := range l.st.Entries {
		st.TotalUSD += e.USD
		st.BySession[e.SessionID] += e.USD
		if e.At.UTC().Format("2006-01-02") == day {
			st.TodayUSD += e.USD
		}
		if e.At.UTC().After(weekAgo) {
			st.WeekUSD += e.USD
		}
	}
	start := len(l.st.Entries) - 5
	if start < 0 {
		start = 0
	}
	st.LastEntries = append([]Entry(nil), l.st.Entries[start:]...)
	sort.Slice(st.LastEntries, func(i, j int) bool { return st.LastEntries[i].At.After(st.LastEntries[j].At) })
	return st
}

// Reset clears usage entries but keeps the configured limits.
func (l *Ledger) Reset() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.st.Entries = []Entry{}
	return l.flushLocked()
}

func (l *Ledger) Path() string { return l.path }
