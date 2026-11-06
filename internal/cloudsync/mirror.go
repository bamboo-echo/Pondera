package cloudsync

// Cross-machine mirror frames. The in-repo single-writer handoff protocol
// (external/frames) is explicitly same-host: it relies on shared paths and a
// local file lock. A cloud writer cannot use it. MirrorStream provides the
// transport-independent part of the cross-machine variant: monotonic sequence
// validation, deduplication, gap detection, integrity digests and a heartbeat
// window tuned for WAN jitter (default 3 minutes instead of the local 30s).

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	ErrMirrorGap      = errors.New("cloudsync: mirror frame gap")
	ErrMirrorStale    = errors.New("cloudsync: mirror stream is stale")
	ErrMirrorBadFrame = errors.New("cloudsync: invalid mirror frame")
)

const DefaultMirrorHeartbeat = 3 * time.Minute

type MirrorFrame struct {
	Sequence  uint64          `json:"sequence"`
	SessionID string          `json:"sessionId"`
	HostID    string          `json:"hostId"`
	Kind      string          `json:"kind"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	At        time.Time       `json:"at"`
	HandoffID string          `json:"handoffId,omitempty"`
	Digest    string          `json:"digest,omitempty"`
}

// FrameDigest seals the semantic fields of a frame (everything except Digest).
func FrameDigest(f MirrorFrame) string {
	canonical := struct {
		Sequence  uint64 `json:"sequence"`
		SessionID string `json:"sessionId"`
		HostID    string `json:"hostId"`
		Kind      string `json:"kind"`
		Payload   string `json:"payload"`
		At        int64  `json:"at"`
		HandoffID string `json:"handoffId"`
	}{f.Sequence, f.SessionID, f.HostID, f.Kind, string(f.Payload), f.At.UnixNano(), f.HandoffID}
	b, _ := json.Marshal(canonical)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (f *MirrorFrame) Seal() { f.Digest = FrameDigest(*f) }

func (f MirrorFrame) Verify() error {
	if f.Sequence == 0 || f.SessionID == "" || f.Kind == "" {
		return fmt.Errorf("%w: missing sequence/session/kind", ErrMirrorBadFrame)
	}
	if f.Digest == "" || f.Digest != FrameDigest(f) {
		return fmt.Errorf("%w: digest mismatch", ErrMirrorBadFrame)
	}
	return nil
}

type MirrorStats struct {
	Accepted   uint64 `json:"accepted"`
	Duplicates uint64 `json:"duplicates"`
	Gaps       uint64 `json:"gaps"`
	Rejected   uint64 `json:"rejected"`
	LastSeq    uint64 `json:"lastSequence"`
	LastAt     string `json:"lastAt,omitempty"`
	Stale      bool   `json:"stale"`
}

type MirrorStream struct {
	mu        sync.Mutex
	sessionID string
	hostID    string
	next      uint64
	lastAt    time.Time
	heartbeat time.Duration
	now       func() time.Time
	stats     MirrorStats
	seen      map[uint64]bool
}

func NewMirrorStream(sessionID, hostID string, heartbeat time.Duration) *MirrorStream {
	if heartbeat <= 0 {
		heartbeat = DefaultMirrorHeartbeat
	}
	return &MirrorStream{
		sessionID: sessionID, hostID: hostID, next: 1,
		heartbeat: heartbeat, now: time.Now, seen: map[uint64]bool{},
		lastAt: time.Now(),
	}
}

func (s *MirrorStream) SetClock(now func() time.Time) {
	if now != nil {
		s.mu.Lock()
		s.now = now
		s.lastAt = now().UTC()
		s.mu.Unlock()
	}
}

// Accept validates and applies one inbound frame. Duplicate frames are
// idempotent (a WAN retry is normal); a gap is a hard error that tells the
// subscriber to replay from LastSeq+1 rather than silently skipping events.
func (s *MirrorStream) Accept(f MirrorFrame) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := f.Verify(); err != nil {
		s.stats.Rejected++
		return err
	}
	if s.sessionID != "" && f.SessionID != s.sessionID {
		s.stats.Rejected++
		return fmt.Errorf("%w: frame belongs to %q", ErrMirrorBadFrame, f.SessionID)
	}
	if f.Sequence < s.next {
		if s.seen[f.Sequence] {
			s.stats.Duplicates++
			return nil
		}
		s.stats.Rejected++
		return fmt.Errorf("%w: out-of-order frame %d (next %d)", ErrMirrorBadFrame, f.Sequence, s.next)
	}
	if f.Sequence > s.next {
		s.stats.Gaps++
		return fmt.Errorf("%w: expected %d, got %d (replay from %d)", ErrMirrorGap, s.next, f.Sequence, s.next)
	}
	s.seen[f.Sequence] = true
	// Keep the dedup window bounded; anything below the window can no longer
	// arrive in order anyway.
	if len(s.seen) > 4096 {
		cut := s.next - 2048
		for k := range s.seen {
			if k < cut {
				delete(s.seen, k)
			}
		}
	}
	s.next++
	s.stats.Accepted++
	s.stats.LastSeq = f.Sequence
	s.lastAt = s.now().UTC()
	s.stats.LastAt = s.lastAt.Format(time.RFC3339Nano)
	return nil
}

// Heartbeat refreshes liveness without consuming a sequence number.
func (s *MirrorStream) Heartbeat() {
	s.mu.Lock()
	s.lastAt = s.now().UTC()
	s.mu.Unlock()
}

func (s *MirrorStream) Stale() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now().Sub(s.lastAt) > s.heartbeat
}

func (s *MirrorStream) Stats() MirrorStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.stats
	out.Stale = s.now().Sub(s.lastAt) > s.heartbeat
	return out
}

// SetExpectedSequence resumes a writer after a restart from its persisted
// sequence cursor so a relay never sees a repeated or regressing frame.
func (s *MirrorStream) SetExpectedSequence(next uint64) {
	s.mu.Lock()
	if next > 0 {
		s.next = next
	}
	s.mu.Unlock()
}

func (s *MirrorStream) ExpectedSequence() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.next
}

// NewFrame builds a sealed outbound frame at the next sequence of a writer.
// Writers own their own stream; spectators validate with Accept.
func (s *MirrorStream) NewFrame(kind string, payload json.RawMessage, handoffID string) MirrorFrame {
	s.mu.Lock()
	seq := s.next
	s.next++
	host, sid := s.hostID, s.sessionID
	s.mu.Unlock()
	f := MirrorFrame{
		Sequence: seq, SessionID: sid, HostID: host, Kind: kind,
		Payload: payload, At: s.now().UTC(), HandoffID: handoffID,
	}
	f.Seal()
	return f
}

// MirrorBuffer is a bounded ring of recent frames used to answer replay
// requests after a detected gap or an SSE reconnect.
type MirrorBuffer struct {
	mu     sync.Mutex
	limit  int
	frames []MirrorFrame
}

func NewMirrorBuffer(limit int) *MirrorBuffer {
	if limit <= 0 {
		limit = 512
	}
	return &MirrorBuffer{limit: limit}
}

func (b *MirrorBuffer) Add(f MirrorFrame) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.frames = append(b.frames, f)
	if len(b.frames) > b.limit {
		b.frames = append([]MirrorFrame(nil), b.frames[len(b.frames)-b.limit:]...)
	}
}

func (b *MirrorBuffer) Since(seq uint64) []MirrorFrame {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]MirrorFrame, 0, len(b.frames))
	for _, f := range b.frames {
		if f.Sequence >= seq {
			out = append(out, f)
		}
	}
	return out
}

func (b *MirrorBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.frames)
}
