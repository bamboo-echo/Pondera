// Package cloudsync implements opt-in, per-session / per-task synchronisation
// between a local Pondera workspace and a user-owned cloud host.
//
// The governing rule is explicit selection: nothing is ever uploaded in the
// background. A session stays strictly local until the user pins it (optionally
// with a subset of its tasks). Upload and download are one-shot, user-initiated
// operations built on an immutable TaskBundle; there is no daemon, no polling,
// and no implicit merge.
//
// This package deliberately has no dependency on internal/session or
// internal/control. It treats session files as opaque durable bytes and only
// rewrites the identity metadata in the manifest. That keeps the sync layer
// independently testable and avoids coupling a rebranded fork's core to a new
// feature.
package cloudsync

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Scope is the sync policy of one session.
type Scope string

const (
	// ScopeLocalOnly is the default. The session never leaves this machine.
	ScopeLocalOnly Scope = "local-only"
	// ScopePinned means the user explicitly selected this session for cloud
	// work. It is still never uploaded automatically.
	ScopePinned Scope = "cloud-pinned"
)

// Owner identifies the single writer of a session (see docs/DEVLOG.md).
type Owner string

const (
	OwnerLocal Owner = "local"
	OwnerCloud Owner = "cloud"
)

// Direction is the direction of a transfer relative to the local machine.
type Direction string

const (
	DirectionUpload   Direction = "upload"
	DirectionDownload Direction = "download"
)

// TaskRef is one selectable unit of work inside a session (a goal, a
// background job, or a turn range). An empty Tasks list on a Selection means
// "the whole session".
type TaskRef struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"` // goal | job | turn
	Label string `json:"label,omitempty"`
}

// TransferRecord is the audit trail of one completed handoff.
type TransferRecord struct {
	HandoffID    string    `json:"handoffId"`
	Direction    Direction `json:"direction"`
	At           time.Time `json:"at"`
	BundleDigest string    `json:"bundleDigest,omitempty"`
	Status       string    `json:"status"` // ok | failed
	Detail       string    `json:"detail,omitempty"`
}

// Selection is the per-session sync policy record.
type Selection struct {
	SessionID    string          `json:"sessionId"`
	WorkspaceID  string          `json:"workspaceId,omitempty"`
	Title        string          `json:"title,omitempty"`
	Scope        Scope           `json:"scope"`
	Tasks        []TaskRef       `json:"tasks,omitempty"`
	Owner        Owner           `json:"owner,omitempty"`
	UpdatedAt    time.Time       `json:"updatedAt"`
	LastUpload   *TransferRecord `json:"lastUpload,omitempty"`
	LastDownload *TransferRecord `json:"lastDownload,omitempty"`
}

// IsPinned reports whether the user selected this session for cloud work.
func (s Selection) IsPinned() bool { return s.Scope == ScopePinned }

// Clone returns a deep copy so callers cannot mutate store state.
func (s Selection) Clone() Selection {
	out := s
	out.Tasks = append([]TaskRef(nil), s.Tasks...)
	if s.LastUpload != nil {
		v := *s.LastUpload
		out.LastUpload = &v
	}
	if s.LastDownload != nil {
		v := *s.LastDownload
		out.LastDownload = &v
	}
	return out
}

// State is the on-disk sync state. It lives under the user's Pondera home,
// never inside a project workspace, so a cloned repository cannot influence
// which sessions are pinned.
type State struct {
	Version     int                      `json:"version"`
	LocalHostID string                   `json:"localHostId"`
	Selections  map[string]*Selection    `json:"selections"`
	Origins     map[string]OriginMapping `json:"origins"`
}

const stateVersion = 1

// Store owns the sync state file. All mutations are serialized and written
// atomically (temp file + fsync + rename), matching the project's existing
// atomic-JSON convention.
type Store struct {
	path string
	mu   sync.Mutex
	st   State
}

// StateFileName is the fixed basename of the sync state file.
const StateFileName = "sync-state.json"

// OpenStore loads (or initialises) the sync state under home. home is normally
// config.PonderaHomeDir(); the state is written to <home>/cloud/.
func OpenStore(home string) (*Store, error) {
	if strings.TrimSpace(home) == "" {
		return nil, errors.New("cloudsync: empty home directory")
	}
	dir := filepath.Join(home, "cloud")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("cloudsync: create state dir: %w", err)
	}
	s := &Store{path: filepath.Join(dir, StateFileName)}
	data, err := os.ReadFile(s.path)
	switch {
	case err == nil:
		var st State
		if err := json.Unmarshal(data, &st); err != nil {
			return nil, fmt.Errorf("cloudsync: decode %s: %w", s.path, err)
		}
		if st.Version != stateVersion {
			return nil, fmt.Errorf("cloudsync: unsupported state version %d", st.Version)
		}
		if st.Selections == nil {
			st.Selections = map[string]*Selection{}
		}
		if st.Origins == nil {
			st.Origins = map[string]OriginMapping{}
		}
		s.st = st
	case errors.Is(err, os.ErrNotExist):
		s.st = State{
			Version:     stateVersion,
			Selections:  map[string]*Selection{},
			Origins:     map[string]OriginMapping{},
			LocalHostID: "", // set lazily by SetLocalHostID
		}
	default:
		return nil, fmt.Errorf("cloudsync: read %s: %w", s.path, err)
	}
	return s, nil
}

// Path returns the absolute path of the state file (for diagnostics).
func (s *Store) Path() string { return s.path }

func (s *Store) flushLocked() error {
	data, err := json.MarshalIndent(s.st, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".sync-state-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, s.path)
}

// SetLocalHostID records this machine's stable session host identity.
func (s *Store) SetLocalHostID(hostID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if strings.TrimSpace(hostID) == "" {
		return errors.New("cloudsync: empty host id")
	}
	if s.st.LocalHostID == hostID {
		return nil
	}
	if s.st.LocalHostID != "" {
		return fmt.Errorf("cloudsync: local host id already set to %q", s.st.LocalHostID)
	}
	s.st.LocalHostID = hostID
	return s.flushLocked()
}

// LocalHostID returns the recorded local host identity.
func (s *Store) LocalHostID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.LocalHostID
}

// Get returns a copy of one selection.
func (s *Store) Get(sessionID string) (Selection, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sel, ok := s.st.Selections[sessionID]
	if !ok {
		return Selection{}, false
	}
	return sel.Clone(), true
}

// List returns all selections sorted by session id. Sessions that were never
// pinned may be absent entirely; that absence is equivalent to local-only.
func (s *Store) List() []Selection {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Selection, 0, len(s.st.Selections))
	for _, sel := range s.st.Selections {
		out = append(out, sel.Clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SessionID < out[j].SessionID })
	return out
}

// Pinned returns only the sessions the user explicitly selected.
func (s *Store) Pinned() []Selection {
	all := s.List()
	out := make([]Selection, 0, len(all))
	for _, sel := range all {
		if sel.IsPinned() {
			out = append(out, sel)
		}
	}
	return out
}

// Pin marks a session (and optionally a subset of its tasks) as selected for
// cloud work. It does NOT upload anything.
func (s *Store) Pin(sessionID, title, workspaceID string, tasks []TaskRef) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return errors.New("cloudsync: empty session id")
	}
	if err := validateTasks(tasks); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sel, ok := s.st.Selections[sessionID]
	if !ok {
		sel = &Selection{SessionID: sessionID, Owner: OwnerLocal}
		s.st.Selections[sessionID] = sel
	}
	sel.Scope = ScopePinned
	sel.Title = strings.TrimSpace(title)
	sel.WorkspaceID = strings.TrimSpace(workspaceID)
	sel.Tasks = append([]TaskRef(nil), tasks...)
	if sel.Owner == "" {
		sel.Owner = OwnerLocal
	}
	sel.UpdatedAt = time.Now().UTC()
	return s.flushLocked()
}

// SetTasks replaces the selected task subset for a pinned session.
func (s *Store) SetTasks(sessionID string, tasks []TaskRef) error {
	if err := validateTasks(tasks); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sel, ok := s.st.Selections[sessionID]
	if !ok || !sel.IsPinned() {
		return fmt.Errorf("cloudsync: session %q is not pinned", sessionID)
	}
	sel.Tasks = append([]TaskRef(nil), tasks...)
	sel.UpdatedAt = time.Now().UTC()
	return s.flushLocked()
}

// Unpin returns a session to strictly local. It does not delete any cloud copy;
// use a separate purge command for that (deliberate: no surprise data loss).
func (s *Store) Unpin(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sel, ok := s.st.Selections[sessionID]
	if !ok {
		return nil
	}
	sel.Scope = ScopeLocalOnly
	sel.Tasks = nil
	sel.UpdatedAt = time.Now().UTC()
	return s.flushLocked()
}

// SetOwner changes the single-writer owner. Only a successful, verified
// handoff may call this.
func (s *Store) SetOwner(sessionID string, owner Owner) error {
	if owner != OwnerLocal && owner != OwnerCloud {
		return fmt.Errorf("cloudsync: invalid owner %q", owner)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sel, ok := s.st.Selections[sessionID]
	if !ok {
		return fmt.Errorf("cloudsync: unknown session %q", sessionID)
	}
	if !sel.IsPinned() {
		return fmt.Errorf("cloudsync: session %q is local-only", sessionID)
	}
	sel.Owner = owner
	sel.UpdatedAt = time.Now().UTC()
	return s.flushLocked()
}

// RecordTransfer appends an audit record for a completed (or failed) handoff.
func (s *Store) RecordTransfer(sessionID string, rec TransferRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sel, ok := s.st.Selections[sessionID]
	if !ok {
		return fmt.Errorf("cloudsync: unknown session %q", sessionID)
	}
	if rec.HandoffID == "" {
		return errors.New("cloudsync: empty handoff id")
	}
	if rec.At.IsZero() {
		rec.At = time.Now().UTC()
	}
	v := rec
	switch rec.Direction {
	case DirectionUpload:
		sel.LastUpload = &v
	case DirectionDownload:
		sel.LastDownload = &v
	default:
		return fmt.Errorf("cloudsync: invalid direction %q", rec.Direction)
	}
	sel.UpdatedAt = time.Now().UTC()
	return s.flushLocked()
}

func validateTasks(tasks []TaskRef) error {
	seen := map[string]bool{}
	for _, t := range tasks {
		id := strings.TrimSpace(t.ID)
		if id == "" {
			return errors.New("cloudsync: task id is empty")
		}
		key := t.Kind + ":" + id
		if t.Kind != "goal" && t.Kind != "job" && t.Kind != "turn" {
			return fmt.Errorf("cloudsync: invalid task kind %q", t.Kind)
		}
		if seen[key] {
			return fmt.Errorf("cloudsync: duplicate task %q", key)
		}
		seen[key] = true
	}
	return nil
}

// EnsureLocalHostID gives a machine a stable, opaque identity, creating one on
// first use. It lives outside the project tree deliberately: a cloned repo
// must never be able to influence which host a session is pinned to.
func EnsureLocalHostID(store *Store, home string) error {
	if store.LocalHostID() != "" {
		return nil
	}
	path := filepath.Join(home, "cloud", "host-id")
	if data, err := os.ReadFile(path); err == nil {
		if id := strings.TrimSpace(string(data)); id != "" {
			return store.SetLocalHostID(id)
		}
	}
	id, err := NewHandoffID()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(id+"\n"), 0o600); err != nil {
		return err
	}
	return store.SetLocalHostID(id)
}

// NewHandoffID returns an opaque, unguessable transfer identifier.
func NewHandoffID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("cloudsync: generate handoff id: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}
