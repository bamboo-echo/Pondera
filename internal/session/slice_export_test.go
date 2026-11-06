package session

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// makeSliceFixture writes a minimal but genuine self-contained V4 export.
func makeSliceFixture(t *testing.T, turns []string) string {
	t.Helper()
	dir := t.TempDir()
	commits := make([]Commit, 0, len(turns))
	next := uint64(1)
	for i, turn := range turns {
		events := []Event{
			{ID: "e" + turn + "a", Sequence: next, Kind: "message/complete", Optional: true,
				Payload: json.RawMessage(`{"taskId":"task-` + turn + `","text":"hello"}`)},
			{ID: "e" + turn + "b", Sequence: next + 1, Kind: "message/complete", Optional: true,
				Payload: json.RawMessage(`{"text":"world"}`)},
		}
		commits = append(commits, Commit{
			SchemaVersion: V4SchemaVersion, Codec: Codec, RecordType: "commit",
			ID: "commit-" + turn, OperationID: "op-" + turn, OperationHash: "hash-" + turn,
			FirstSequence: next, EventCount: len(events), TurnID: turn,
			WriterGeneration: 1, CreatedAt: time.Now().UTC(), Events: events,
		})
		_ = i
		next += uint64(len(events))
	}
	if err := os.MkdirAll(filepath.Join(dir, ".content-v1"), 0o700); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	if _, err := encodeV4Commits(context.Background(), &log, contentStoreForSessionDir(dir), commits); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, currentLogName), log.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	m := Manifest{
		SchemaVersion: SchemaVersion, Codec: Codec, StorageRevision: StorageRevision,
		ContentRoot: ".content-v1", SessionID: "slice-test",
		CreatedAt: time.Now().UTC(), WriterGeneration: 1,
	}
	if err := writeManifestFile(filepath.Join(dir, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
	replayed, err := Replay(dir, nil)
	if err != nil {
		t.Fatalf("fixture replay: %v", err)
	}
	if len(replayed) != len(commits) {
		t.Fatalf("fixture replay got %d commits, want %d", len(replayed), len(commits))
	}
	// A derived index must never survive a slice.
	if err := os.WriteFile(filepath.Join(dir, "events.offset-index.json"), []byte(`{"stale":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".query-cache", "slice-test"), 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestInspectSliceTurns(t *testing.T) {
	dir := makeSliceFixture(t, []string{"turn-a", "turn-b", "turn-c"})
	turns, err := InspectSliceTurns(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 3 {
		t.Fatalf("got %d turns, want 3", len(turns))
	}
	for i, want := range []string{"turn-a", "turn-b", "turn-c"} {
		if turns[i].TurnID != want {
			t.Errorf("turn %d = %q, want %q", i, turns[i].TurnID, want)
		}
		if turns[i].EventCount != 2 {
			t.Errorf("turn %s events = %d, want 2", want, turns[i].EventCount)
		}
		if turns[i].FirstSequence != uint64(1+2*i) {
			t.Errorf("turn %s first = %d", want, turns[i].FirstSequence)
		}
	}
}

func TestSliceExportRenumbersAndReplays(t *testing.T) {
	dir := makeSliceFixture(t, []string{"turn-a", "turn-b", "turn-c"})
	dest := filepath.Join(t.TempDir(), "sliced")
	report, err := SliceExport(context.Background(), dir, dest, func(s SliceTurn) bool {
		return s.TurnID == "turn-a" || s.TurnID == "turn-c"
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.TotalCommits != 3 || report.KeptCommits != 2 || report.DroppedCommits != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if report.LastSequence != 4 {
		t.Fatalf("last sequence = %d, want 4 (two turns x two events)", report.LastSequence)
	}
	if len(report.KeptTurnIDs) != 2 || report.KeptTurnIDs[0] != "turn-a" || report.KeptTurnIDs[1] != "turn-c" {
		t.Fatalf("kept turns = %v", report.KeptTurnIDs)
	}
	replayed, err := Replay(dest, nil)
	if err != nil {
		t.Fatalf("sliced export does not replay: %v", err)
	}
	if len(replayed) != 2 {
		t.Fatalf("sliced replay got %d commits, want 2", len(replayed))
	}
	if replayed[0].FirstSequence != 1 || replayed[1].FirstSequence != 3 {
		t.Fatalf("sequences not dense: %d, %d", replayed[0].FirstSequence, replayed[1].FirstSequence)
	}
	if replayed[0].TurnID != "turn-a" || replayed[1].TurnID != "turn-c" {
		t.Fatalf("wrong turns retained: %q, %q", replayed[0].TurnID, replayed[1].TurnID)
	}
	for _, c := range replayed {
		for i, ev := range c.Events {
			if ev.Sequence != c.FirstSequence+uint64(i) {
				t.Fatalf("event sequence %d in commit %d is not contiguous", ev.Sequence, c.FirstSequence)
			}
		}
	}
	// Derived projections describing the old log must be gone.
	if _, err := os.Stat(filepath.Join(dest, "events.offset-index.json")); !os.IsNotExist(err) {
		t.Fatal("stale offset index survived the slice")
	}
	if _, err := os.Stat(filepath.Join(dest, ".query-cache")); !os.IsNotExist(err) {
		t.Fatal("stale query cache survived the slice")
	}
	// The sliced directory must still be a valid import source: exportDirectory
	// (used by Service.Import) validates it with Replay.
	staged := filepath.Join(t.TempDir(), "staged")
	if err := exportDirectory(context.Background(), dest, staged); err != nil {
		t.Fatalf("sliced export cannot be imported: %v", err)
	}
}

func TestSliceExportRefusesEmptySelection(t *testing.T) {
	dir := makeSliceFixture(t, []string{"turn-a"})
	_, err := SliceExport(context.Background(), dir, filepath.Join(t.TempDir(), "out"), func(SliceTurn) bool { return false })
	if err == nil {
		t.Fatal("expected an error for an empty task selection")
	}
}

func TestSliceExportRejectsNonSelfContained(t *testing.T) {
	dir := makeSliceFixture(t, []string{"turn-a"})
	m, err := readManifest(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	m.ContentRoot = "../.content-v1"
	if err := writeManifestFile(filepath.Join(dir, "manifest.json"), m); err != nil {
		t.Fatal(err)
	}
	_, err = SliceExport(context.Background(), dir, filepath.Join(t.TempDir(), "out"), func(SliceTurn) bool { return true })
	if err == nil {
		t.Fatal("expected a non-self-contained source to be rejected")
	}
}

func TestMatchTurnRefs(t *testing.T) {
	turns := []SliceTurn{
		{TurnID: "turn-a", Refs: []string{"task-turn-a", "fix-auth"}},
		{TurnID: "turn-b", Refs: []string{"task-turn-b"}},
	}
	got := MatchTurnRefs(turns, []string{"fix-auth", "turn-b"})
	if !got["turn-a"] || !got["turn-b"] {
		t.Fatalf("match failed: %v", got)
	}
	if len(MatchTurnRefs(turns, []string{"not-there"})) != 0 {
		t.Fatal("unexpected match for an unknown task")
	}
}
