package cloudsync

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func frame(s *MirrorStream, kind string) MirrorFrame {
	return s.NewFrame(kind, json.RawMessage(`{"text":"hi"}`), "handoff-1")
}

func TestMirrorStreamSequenceAndDedup(t *testing.T) {
	w := NewMirrorStream("s1", "cloud", 0)
	r := NewMirrorStream("s1", "cloud", 0)
	f1, f2 := frame(w, "message"), frame(w, "tool")
	if err := r.Accept(f1); err != nil {
		t.Fatal(err)
	}
	if err := r.Accept(f1); err != nil {
		t.Fatalf("duplicate must be idempotent: %v", err)
	}
	if err := r.Accept(f2); err != nil {
		t.Fatal(err)
	}
	st := r.Stats()
	if st.Accepted != 2 || st.Duplicates != 1 || st.LastSeq != 2 {
		t.Fatalf("unexpected stats: %+v", st)
	}
}

func TestMirrorStreamDetectsGap(t *testing.T) {
	w := NewMirrorStream("s1", "cloud", 0)
	r := NewMirrorStream("s1", "cloud", 0)
	f1, f2, f3 := frame(w, "a"), frame(w, "b"), frame(w, "c")
	_ = r.Accept(f1)
	if err := r.Accept(f3); !errors.Is(err, ErrMirrorGap) {
		t.Fatalf("expected a gap error, got %v", err)
	}
	if r.Stats().Gaps != 1 {
		t.Fatal("gap not counted")
	}
	// Replay closes the gap.
	if err := r.Accept(f2); err != nil {
		t.Fatal(err)
	}
	if err := r.Accept(f3); err != nil {
		t.Fatal(err)
	}
	if r.Stats().LastSeq != 3 {
		t.Fatalf("replay did not catch up: %+v", r.Stats())
	}
}

func TestMirrorFrameDigestAndSessionBinding(t *testing.T) {
	w := NewMirrorStream("s1", "cloud", 0)
	r := NewMirrorStream("s1", "cloud", 0)
	f := frame(w, "a")
	f.Kind = "tampered"
	if err := r.Accept(f); !errors.Is(err, ErrMirrorBadFrame) {
		t.Fatalf("tampered frame must be rejected: %v", err)
	}
	good := frame(w, "a")
	good.SessionID = "other"
	good.Seal()
	if err := r.Accept(good); !errors.Is(err, ErrMirrorBadFrame) {
		t.Fatal("frame from another session must be rejected")
	}
}

func TestMirrorHeartbeatWANWindow(t *testing.T) {
	r := NewMirrorStream("s1", "cloud", 2*time.Minute)
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	r.SetClock(func() time.Time { return now })
	if r.Stale() {
		t.Fatal("fresh stream must not be stale")
	}
	now = now.Add(90 * time.Second)
	if r.Stale() {
		t.Fatal("90s of WAN jitter must be tolerated (local default is 30s)")
	}
	now = now.Add(90 * time.Second)
	if !r.Stale() {
		t.Fatal("stream must go stale after the heartbeat window")
	}
	r.Heartbeat()
	if r.Stale() {
		t.Fatal("heartbeat must refresh liveness")
	}
}

func TestMirrorBufferReplay(t *testing.T) {
	b := NewMirrorBuffer(3)
	w := NewMirrorStream("s", "h", 0)
	for i := 0; i < 5; i++ {
		b.Add(frame(w, "e"))
	}
	if b.Len() != 3 {
		t.Fatalf("buffer limit not enforced: %d", b.Len())
	}
	got := b.Since(4)
	if len(got) != 2 || got[0].Sequence != 4 {
		t.Fatalf("replay window wrong: %+v", got)
	}
}
