package session

// Task-slice export turns an immutable, self-contained session export into a
// smaller export that contains only the durable commits belonging to the
// turns the user selected for cloud handoff.
//
// The V4 log is a strictly contiguous, checksummed transaction stream: every
// commit starts at the sequence where the previous one ended and every event
// carries its absolute sequence. Physically deleting a middle commit would
// therefore corrupt the stream. SliceExport renumbers the retained commits
// back to a dense sequence starting at 1 and recomputes each operation hash,
// which keeps the result replayable by the ordinary importer without any
// change to the storage format.
//
// Payload references are resolved by Replay and re-published by
// encodeV4Commits, so the sliced export remains fully self-contained; derived
// indexes (which describe the old log's byte length and sequence tip) are
// deliberately dropped and rebuilt on import.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SliceTurn summarises one logical turn inside an exported session.
type SliceTurn struct {
	TurnID        string   `json:"turnId"`
	FirstSequence uint64   `json:"firstSequence"`
	LastSequence  uint64   `json:"lastSequence"`
	EventCount    int      `json:"eventCount"`
	CommitCount   int      `json:"commitCount"`
	Refs          []string `json:"refs,omitempty"`
}

// SliceReport describes one completed slice operation.
type SliceReport struct {
	TotalCommits   int      `json:"totalCommits"`
	KeptCommits    int      `json:"keptCommits"`
	DroppedCommits int      `json:"droppedCommits"`
	TotalEvents    int      `json:"totalEvents"`
	KeptEvents     int      `json:"keptEvents"`
	FirstSequence  uint64   `json:"firstSequence"`
	LastSequence   uint64   `json:"lastSequence"`
	KeptTurnIDs    []string `json:"keptTurnIds,omitempty"`
	DroppedTurnIDs []string `json:"droppedTurnIds,omitempty"`
}

// InspectSliceTurns reads an exported session directory and groups its durable
// commits by turn, in log order. It never mutates the directory.
func InspectSliceTurns(dir string) ([]SliceTurn, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "." || dir == "" {
		return nil, errors.New("session: slice source directory is required")
	}
	commits, err := Replay(dir, nil)
	if err != nil {
		return nil, err
	}
	var out []SliceTurn
	byID := map[string]int{}
	for _, commit := range commits {
		id := strings.TrimSpace(commit.TurnID)
		if id == "" {
			id = "(untagged)"
		}
		i, ok := byID[id]
		if !ok {
			i = len(out)
			byID[id] = i
			out = append(out, SliceTurn{
				TurnID: id, FirstSequence: commit.FirstSequence,
				LastSequence: commit.LastSequence(),
			})
		}
		t := &out[i]
		t.CommitCount++
		t.EventCount += commit.EventCount
		if commit.FirstSequence < t.FirstSequence {
			t.FirstSequence = commit.FirstSequence
		}
		if commit.LastSequence() > t.LastSequence {
			t.LastSequence = commit.LastSequence()
		}
		for _, ev := range commit.Events {
			t.Refs = append(t.Refs, payloadRefs(ev.Payload)...)
		}
	}
	for i := range out {
		out[i].Refs = dedupStrings(out[i].Refs)
	}
	return out, nil
}

// payloadRefs extracts the small set of identifiers a task selection may refer
// to. It is intentionally conservative: only explicit id-ish fields are used,
// never free-form prose, so a task can never match unrelated conversation text.
func payloadRefs(payload []byte) []string {
	if len(payload) == 0 || len(payload) > 1<<20 {
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal(payload, &obj); err != nil {
		return nil
	}
	keys := []string{"taskId", "task_id", "goalId", "goal_id", "jobId", "job_id", "id", "turnId", "turn", "label", "title"}
	var out []string
	for _, k := range keys {
		if v, ok := obj[k]; ok {
			switch x := v.(type) {
			case string:
				if s := strings.TrimSpace(x); s != "" && len(s) <= 200 {
					out = append(out, s)
				}
			case float64:
				out = append(out, fmt.Sprintf("%v", x))
			}
		}
	}
	return out
}

func dedupStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// SliceExport writes a new self-contained export at destination containing
// only the turns for which keep returns true. keep is evaluated once per turn,
// in log order. The source is never modified.
func SliceExport(ctx context.Context, source, destination string, keep func(SliceTurn) bool) (*SliceReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	source = filepath.Clean(strings.TrimSpace(source))
	destination = filepath.Clean(strings.TrimSpace(destination))
	if source == "" || source == "." || destination == "" || destination == "." {
		return nil, errors.New("session: slice requires explicit source and destination")
	}
	if destination == source || strings.HasPrefix(destination+string(os.PathSeparator), source+string(os.PathSeparator)) {
		return nil, fmt.Errorf("session: invalid slice destination %q", destination)
	}
	if _, err := os.Stat(destination); err == nil {
		return nil, fmt.Errorf("%w: slice destination", ErrSessionExists)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	commits, err := Replay(source, nil)
	if err != nil {
		return nil, fmt.Errorf("session: read slice source: %w", err)
	}
	turns, err := InspectSliceTurns(source)
	if err != nil {
		return nil, err
	}
	if keep == nil {
		return nil, errors.New("session: slice keep predicate is required")
	}
	keptTurn := map[string]bool{}
	for _, t := range turns {
		if keep(t) {
			keptTurn[t.TurnID] = true
		}
	}
	if len(keptTurn) == 0 {
		return nil, errors.New("session: task selection matched no durable turn; refusing to produce an empty slice")
	}

	manifest, err := readManifest(filepath.Join(source, "manifest.json"))
	if err != nil {
		return nil, fmt.Errorf("session: read slice manifest: %w", err)
	}
	if manifest.ContentRoot != ".content-v1" {
		return nil, errors.New("session: slice source is not a self-contained export")
	}

	report := &SliceReport{TotalCommits: len(commits)}
	kept := make([]Commit, 0, len(commits))
	next := uint64(1)
	for _, original := range commits {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		turnID := strings.TrimSpace(original.TurnID)
		if turnID == "" {
			turnID = "(untagged)"
		}
		report.TotalEvents += original.EventCount
		if !keptTurn[turnID] {
			report.DroppedCommits++
			continue
		}
		commit := cloneCommit(original)
		commit.Codec = Codec
		commit.ID = deterministicID("slice\x00" + manifest.SessionID + "\x00" + original.ID)
		commit.OperationID = "slice:" + original.ID
		commit.FirstSequence = next
		commit.WriterGeneration = manifest.WriterGeneration
		for i := range commit.Events {
			commit.Events[i].Sequence = next + uint64(i)
		}
		hash, hashErr := hashOperation(manifest.SessionID, commit.TurnID, commit.Events)
		if hashErr != nil {
			return nil, hashErr
		}
		commit.OperationHash = hash
		kept = append(kept, commit)
		report.KeptCommits++
		report.KeptEvents += commit.EventCount
		next += uint64(commit.EventCount)
	}
	if len(kept) == 0 {
		return nil, errors.New("session: slice retained no commits")
	}
	report.FirstSequence = 1
	report.LastSequence = next - 1
	for _, t := range turns {
		if keptTurn[t.TurnID] {
			report.KeptTurnIDs = append(report.KeptTurnIDs, t.TurnID)
		} else {
			report.DroppedTurnIDs = append(report.DroppedTurnIDs, t.TurnID)
		}
	}

	parent := filepath.Dir(destination)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(parent, ".session-slice-")
	if err != nil {
		return nil, err
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(tmp)
		}
	}()
	if err := copySliceSupport(ctx, source, tmp); err != nil {
		return nil, err
	}
	// encodeV4Commits may publish large payloads into the destination content
	// store; create it before encoding so Verify can resolve references.
	if err := os.MkdirAll(filepath.Join(tmp, ".content-v1"), 0o700); err != nil {
		return nil, err
	}
	var log bytes.Buffer
	if _, err := encodeV4Commits(ctx, &log, contentStoreForSessionDir(tmp), kept); err != nil {
		return nil, fmt.Errorf("session: encode slice log: %w", err)
	}
	manifest.InheritedEvents = 0
	manifest.ContentRoot = ".content-v1"
	manifest.Source = &Source{
		Path: source, Size: int64(log.Len()), Version: Codec,
		SHA256: fmt.Sprintf("%x", sha256Sum(log.Bytes())),
	}
	if err := writeManifestFile(filepath.Join(tmp, "manifest.json"), manifest); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(tmp, currentLogName), log.Bytes(), 0o600); err != nil {
		return nil, err
	}
	replayed, err := Replay(tmp, nil)
	if err != nil {
		return nil, fmt.Errorf("session: validate slice log: %w", err)
	}
	if len(replayed) != len(kept) {
		return nil, fmt.Errorf("session: slice replay retained %d of %d commits", len(replayed), len(kept))
	}
	if err := os.Rename(tmp, destination); err != nil {
		return nil, err
	}
	published = true
	return report, nil
}

func sha256Sum(b []byte) [32]byte { return sha256.Sum256(b) }

// copySliceSupport copies everything except the event log and derived indexes.
// Derived projections describe the original log's size/sequence tip and must
// be rebuilt from the sliced log on import.
func copySliceSupport(ctx context.Context, source, dest string) error {
	return filepath.WalkDir(source, func(p string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(source, p)
		if err != nil || rel == "." {
			return err
		}
		if skipSliceEntry(rel) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dest, rel)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("session: slice refuses symlink %s", rel)
		}
		if entry.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("session: slice refuses non-regular file %s", rel)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	})
}

func skipSliceEntry(rel string) bool {
	base := filepath.Base(rel)
	switch base {
	case currentLogName, legacyLogName, "writer.lock", "events.offset-index.json",
		"catalog-metadata.json", "history-locator-v2.sqlite", "manifest.json":
		return true
	}
	if rel == ".query-cache" || strings.HasPrefix(rel, ".query-cache"+string(os.PathSeparator)) {
		return true
	}
	if strings.Contains(base, "index") || strings.Contains(base, "projection") {
		return true
	}
	return strings.HasSuffix(base, ".sqlite") || strings.HasSuffix(base, ".db") ||
		strings.HasSuffix(base, ".tail") || strings.HasSuffix(base, ".tmp")
}

// ExportColdDirectory snapshots a live (or cold) session directory into a
// self-contained, replayable export without attaching a runtime. It is the
// non-runtime counterpart of Service.TryExportCold and is safe to call while
// no writer holds the session's lease.
func ExportColdDirectory(ctx context.Context, source, destination string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := Replay(source, nil); err != nil {
		return fmt.Errorf("session: cold export source is not replayable: %w", err)
	}
	return exportDirectory(ctx, source, destination)
}

// WriteEmptyExport creates a valid, empty, self-contained session export.
// Cloud handoffs use it as a skeleton when the destination has no session yet,
// and importers use it as a replayable identity that carries zero turns.
func WriteEmptyExport(dir, sessionID string) error {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" || dir == "." || strings.TrimSpace(sessionID) == "" {
		return errors.New("session: empty export requires a directory and session id")
	}
	if err := os.MkdirAll(filepath.Join(dir, ".content-v1"), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, currentLogName), nil, 0o600); err != nil {
		return err
	}
	return writeManifestFile(filepath.Join(dir, "manifest.json"), Manifest{
		SchemaVersion: SchemaVersion, Codec: Codec, StorageRevision: StorageRevision,
		ContentRoot: ".content-v1", SessionID: sessionID,
		CreatedAt: time.Now().UTC(), WriterGeneration: 1,
	})
}

// ReplayExport validates a self-contained export and returns its durable
// commits. It is the read-only integrity check used by bundle importers.
func ReplayExport(dir string) ([]Commit, error) {
	return Replay(dir, nil)
}

// MatchTurnRefs reports which turn IDs are referenced by the given task ids.
// It is deliberately strict: an exact match on a turn id or on one of the
// identifier-like payload fields. A caller that gets no match must fall back
// to the whole session rather than silently shipping an empty conversation.
func MatchTurnRefs(turns []SliceTurn, taskIDs []string) map[string]bool {
	out := map[string]bool{}
	want := make([]string, 0, len(taskIDs))
	for _, id := range taskIDs {
		if s := strings.TrimSpace(id); s != "" {
			want = append(want, s)
		}
	}
	for _, t := range turns {
		for _, w := range want {
			if w == t.TurnID || strings.HasSuffix(t.TurnID, ":"+w) || strings.HasSuffix(t.TurnID, "/"+w) {
				out[t.TurnID] = true
			}
			for _, ref := range t.Refs {
				if ref == w {
					out[t.TurnID] = true
				}
			}
		}
	}
	return out
}
