package cloudsync

// Automatic quiesce detection replaces the old "--confirm-quiesced" human
// attestation. It asks a live serve for its running state, falls back to the
// session writer lease, and can optionally drain (cancel) the running turn
// before waiting for the session to become idle.
//
// The rule is fail-closed: when neither a status probe nor a lease check can
// prove the session is idle, the handoff is refused unless the caller
// explicitly chooses QuiesceForce.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"pondera/internal/filelock"
)

type QuiesceMode string

const (
	// QuiesceCheck probes and refuses if a turn is running.
	QuiesceCheck QuiesceMode = "check"
	// QuiesceDrain cancels a running turn, then waits for it to go idle.
	QuiesceDrain QuiesceMode = "drain"
	// QuiesceForce skips verification entirely (explicit operator override).
	QuiesceForce QuiesceMode = "force"
)

type QuiesceOptions struct {
	ServeURL   string
	Token      string
	SessionID  string
	SessionDir string
	Mode       QuiesceMode
	Timeout    time.Duration
	PollEvery  time.Duration
	Client     *http.Client
}

type QuiesceReport struct {
	Quiesced  bool        `json:"quiesced"`
	Mode      QuiesceMode `json:"mode"`
	Source    string      `json:"source"`
	Detail    string      `json:"detail,omitempty"`
	Probed    bool        `json:"probed"`
	Running   bool        `json:"running"`
	LeaseHeld bool        `json:"leaseHeld"`
	Cancelled bool        `json:"cancelled"`
	Checks    []string    `json:"checks,omitempty"`
}

func (q *QuiesceReport) check(s string) { q.Checks = append(q.Checks, s) }

// CheckQuiesced proves (or disproves) that a session has no running turn.
func CheckQuiesced(ctx context.Context, opts QuiesceOptions) (*QuiesceReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	mode := opts.Mode
	if mode == "" {
		mode = QuiesceCheck
	}
	report := &QuiesceReport{Mode: mode, Source: "unverified"}
	if mode == QuiesceForce {
		report.Quiesced = true
		report.Source = "forced"
		report.Detail = "operator explicitly overrode quiesce verification"
		report.check("force override accepted")
		return report, nil
	}
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	probe := func() (bool, bool, error) {
		if strings.TrimSpace(opts.ServeURL) == "" {
			return false, false, errors.New("no serve URL configured")
		}
		u, err := url.Parse(strings.TrimRight(opts.ServeURL, "/") + "/status")
		if err != nil {
			return false, false, err
		}
		q := u.Query()
		if strings.TrimSpace(opts.SessionID) != "" {
			q.Set("session", opts.SessionID)
		}
		if strings.TrimSpace(opts.Token) != "" {
			q.Set("token", opts.Token)
		}
		u.RawQuery = q.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return false, false, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return false, false, err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return false, false, fmt.Errorf("status endpoint returned %d", resp.StatusCode)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return false, false, err
		}
		var view map[string]any
		if err := json.Unmarshal(body, &view); err != nil {
			return false, false, err
		}
		running := truthy(view["running"]) || truthy(view["pendingPrompt"])
		if state, ok := view["runtimeState"].(string); ok {
			switch strings.ToLower(state) {
			case "running", "busy", "thinking", "tool":
				running = true
			}
		}
		return true, running, nil
	}

	probed, running, probeErr := probe()
	if probeErr != nil {
		report.check("serve status probe unavailable: " + probeErr.Error())
	} else {
		report.Probed = true
		report.Running = running
		report.check("serve /status probed")
	}

	if probed && running {
		if mode != QuiesceDrain {
			report.Detail = "a turn is still running; stop it or use --quiesce drain"
			return report, errors.New("cloudsync: " + report.Detail)
		}
		if err := cancelRemoteTurn(ctx, client, opts); err != nil {
			report.Detail = "could not cancel the running turn: " + err.Error()
			return report, errors.New("cloudsync: " + report.Detail)
		}
		report.Cancelled = true
		report.check("POST /cancel-session accepted")
		deadline := time.Now().Add(opts.Timeout)
		if opts.Timeout <= 0 {
			deadline = time.Now().Add(30 * time.Second)
		}
		every := opts.PollEvery
		if every <= 0 {
			every = 500 * time.Millisecond
		}
		for {
			_, stillRunning, err := probe()
			if err == nil && !stillRunning {
				report.Running = false
				report.Quiesced = true
				report.Source = "drained"
				report.check("session went idle after cancel")
				return report, nil
			}
			if time.Now().After(deadline) {
				report.Detail = "timed out waiting for the turn to drain"
				return report, errors.New("cloudsync: " + report.Detail)
			}
			select {
			case <-ctx.Done():
				return report, ctx.Err()
			case <-time.After(every):
			}
		}
	}
	if probed && !running {
		report.Quiesced = true
		report.Source = "serve-status"
		report.check("running=false")
		return report, nil
	}

	// No live serve: fall back to the on-disk writer lease. Acquiring it proves
	// no other runtime currently owns the session directory.
	if strings.TrimSpace(opts.SessionDir) != "" {
		lockPath := filepath.Join(opts.SessionDir, "writer.lock")
		release, lockErr := filelock.TryAcquire(lockPath)
		if lockErr == nil {
			release()
			report.LeaseHeld = false
			report.Quiesced = true
			report.Source = "writer-lease-free"
			report.check("writer.lock acquired and released immediately")
			return report, nil
		}
		report.LeaseHeld = true
		report.check("writer.lock is held by another runtime: " + lockErr.Error())
	}
	report.Detail = "cannot prove the session is idle (no reachable serve, lease unavailable or held); use --quiesce force only if you are certain"
	return report, errors.New("cloudsync: " + report.Detail)
}

func truthy(v any) bool {
	b, ok := v.(bool)
	return ok && b
}

func cancelRemoteTurn(ctx context.Context, client *http.Client, opts QuiesceOptions) error {
	u, err := url.Parse(strings.TrimRight(opts.ServeURL, "/") + "/cancel-session")
	if err != nil {
		return err
	}
	q := u.Query()
	if strings.TrimSpace(opts.Token) != "" {
		q.Set("token", opts.Token)
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader([]byte(`{}`)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("cancel returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}
