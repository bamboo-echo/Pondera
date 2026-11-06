package cloudsync

// Task-level session slicing and automatic session-directory discovery.
//
// A pinned Selection may name specific goals/jobs/turns. Previously that was
// only metadata: the whole event log travelled to the cloud. This file wires
// session.ExportColdDirectory + session.SliceExport into the bundle pipeline so
// that only the durable commits belonging to the selected turns are packed,
// with the resulting log renumbered back into a valid replayable stream.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"pondera/internal/session"
)

// TaskWindow records how the session log was scoped for one handoff.
type TaskWindow struct {
	TaskIDs      []string `json:"taskIds,omitempty"`
	KeptTurnIDs  []string `json:"keptTurnIds,omitempty"`
	TotalCommits int      `json:"totalCommits,omitempty"`
	KeptCommits  int      `json:"keptCommits,omitempty"`
	TotalEvents  int      `json:"totalEvents,omitempty"`
	KeptEvents   int      `json:"keptEvents,omitempty"`
	FirstSeq     uint64   `json:"firstSequence,omitempty"`
	LastSeq      uint64   `json:"lastSequence,omitempty"`
	Sliced       bool     `json:"sliced"`
	Reason       string   `json:"reason,omitempty"`
}

// DiscoverSessionDir resolves the on-disk session directory from Pondera's
// data home. It verifies the directory is a replayable session store rather
// than trusting a path, so a mistyped id can never pack an unrelated folder.
func DiscoverSessionDir(home, sessionID string) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || strings.ContainsAny(sessionID, `/\`) {
		return "", errors.New("cloudsync: invalid session id for directory discovery")
	}
	roots := []string{
		os.Getenv("PONDERA_SESSION_ROOT"),
		filepath.Join(home, "sessions-v4", sessionID),
		filepath.Join(home, "sessions", sessionID),
		filepath.Join(home, "data", "sessions-v4", sessionID),
		filepath.Join(home, ".pondera", "sessions-v4", sessionID),
	}
	for _, root := range roots {
		if strings.TrimSpace(root) == "" {
			continue
		}
		candidate := filepath.Clean(root)
		if filepath.Base(candidate) != sessionID {
			candidate = filepath.Join(candidate, sessionID)
		}
		if st, err := os.Stat(filepath.Join(candidate, "manifest.json")); err != nil || st.IsDir() {
			continue
		}
		if _, err := session.ReplayExport(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("cloudsync: no replayable session directory found for %q (set --session-dir or PONDERA_SESSION_ROOT)", sessionID)
}

// PrepareTaskSlice stages a session directory into destDir. When taskIDs match
// durable turns, only those turns are retained; otherwise the whole session is
// carried and the fallback is recorded explicitly (never silently drop data).
// The returned TaskWindow is sealed into the bundle manifest.
func PrepareTaskSlice(ctx context.Context, sourceDir, destDir string, taskIDs []string) (*TaskWindow, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	sourceDir = filepath.Clean(strings.TrimSpace(sourceDir))
	destDir = filepath.Clean(strings.TrimSpace(destDir))
	if sourceDir == "" || sourceDir == "." || destDir == "" || destDir == "." {
		return nil, errors.New("cloudsync: task slice requires source and destination")
	}
	if _, err := os.Stat(destDir); err == nil {
		return nil, fmt.Errorf("cloudsync: task slice destination already exists: %s", destDir)
	}
	if err := os.MkdirAll(filepath.Dir(destDir), 0o700); err != nil {
		return nil, err
	}

	// Normalise to a self-contained export first: a live on-disk session uses a
	// shared content root and is not a valid bundle payload on its own.
	selfContained := sourceDir
	cleanupTemp := func() {}
	if m, err := readSessionManifest(sourceDir); err == nil && m != ".content-v1" {
		tmp := destDir + ".cold"
		_ = os.RemoveAll(tmp)
		if err := session.ExportColdDirectory(ctx, sourceDir, tmp); err != nil {
			return nil, fmt.Errorf("cloudsync: snapshot session: %w", err)
		}
		selfContained = tmp
		cleanupTemp = func() { _ = os.RemoveAll(tmp) }
	}
	defer cleanupTemp()

	window := &TaskWindow{TaskIDs: append([]string(nil), taskIDs...)}
	if len(taskIDs) == 0 {
		if err := copySessionTree(selfContained, destDir); err != nil {
			return nil, err
		}
		window.Reason = "whole-session"
		if err := fillWindowCounts(destDir, window); err != nil {
			return nil, err
		}
		return window, nil
	}
	turns, err := session.InspectSliceTurns(selfContained)
	if err != nil {
		return nil, fmt.Errorf("cloudsync: inspect session turns: %w", err)
	}
	matched := session.MatchTurnRefs(turns, taskIDs)
	if len(matched) == 0 {
		// Fail open on data, fail closed on claims: carry the whole session and
		// state plainly that no task-level filtering happened.
		if err := copySessionTree(selfContained, destDir); err != nil {
			return nil, err
		}
		window.Sliced = false
		window.Reason = "no-task-match-fallback-whole-session"
		if err := fillWindowCounts(destDir, window); err != nil {
			return nil, err
		}
		return window, nil
	}
	keep := map[string]bool(matched)
	report, err := session.SliceExport(ctx, selfContained, destDir, func(t session.SliceTurn) bool { return keep[t.TurnID] })
	if err != nil {
		return nil, fmt.Errorf("cloudsync: slice session: %w", err)
	}
	window.Sliced = true
	window.Reason = "task-matched-turns"
	window.KeptTurnIDs = append([]string(nil), report.KeptTurnIDs...)
	window.TotalCommits = report.TotalCommits
	window.KeptCommits = report.KeptCommits
	window.TotalEvents = report.TotalEvents
	window.KeptEvents = report.KeptEvents
	window.FirstSeq = report.FirstSequence
	window.LastSeq = report.LastSequence
	sort.Strings(window.KeptTurnIDs)
	return window, nil
}

func fillWindowCounts(dir string, w *TaskWindow) error {
	commits, err := session.ReplayExport(dir)
	if err != nil {
		return fmt.Errorf("cloudsync: validate staged session: %w", err)
	}
	w.TotalCommits = len(commits)
	w.KeptCommits = len(commits)
	for _, c := range commits {
		w.TotalEvents += c.EventCount
		w.KeptEvents += c.EventCount
		if c.LastSequence() > w.LastSeq {
			w.LastSeq = c.LastSequence()
		}
	}
	if len(commits) > 0 {
		w.FirstSeq = commits[0].FirstSequence
	}
	return nil
}

// copySessionTree copies a session export without transient writer artifacts.
// A quiesce check may have created an empty writer.lock; it is not part of the
// session and must never travel to another host.
func copySessionTree(src, dst string) error {
	if err := copyTree(src, dst); err != nil {
		return err
	}
	return filepath.WalkDir(dst, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		base := filepath.Base(p)
		if base == "writer.lock" || strings.HasSuffix(base, ".tmp") || strings.HasSuffix(base, ".uploading") {
			return os.Remove(p)
		}
		return nil
	})
}

func readSessionManifest(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return "", err
	}
	var m struct {
		ContentRoot string `json:"contentRoot"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return "", err
	}
	return m.ContentRoot, nil
}

// InstallSessionExport validates an unpacked session payload and publishes it
// under a sessions-v4 root so the destination serve can open it. It refuses to
// overwrite an existing identity unless overwrite is explicitly requested.
func InstallSessionExport(ctx context.Context, sourceDir, root, sessionID string, overwrite bool) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(sessionID) == "" || strings.ContainsAny(sessionID, `/\`) {
		return "", errors.New("cloudsync: invalid session id for install")
	}
	if _, err := session.ReplayExport(sourceDir); err != nil {
		return "", fmt.Errorf("cloudsync: refusing to install an invalid session export: %w", err)
	}
	target := filepath.Join(root, sessionID)
	if _, err := os.Lstat(target); err == nil {
		if !overwrite {
			return "", fmt.Errorf("cloudsync: session %q already exists at %s (use --force-session to replace)", sessionID, target)
		}
		if err := os.RemoveAll(target); err != nil {
			return "", err
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	tmp := target + ".install-" + fmt.Sprintf("%d", os.Getpid())
	_ = os.RemoveAll(tmp)
	if err := copySessionTree(sourceDir, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return "", err
	}
	if _, err := session.ReplayExport(tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return "", fmt.Errorf("cloudsync: installed session failed validation: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.RemoveAll(tmp)
		return "", err
	}
	return target, nil
}
