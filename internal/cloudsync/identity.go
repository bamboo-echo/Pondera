package cloudsync

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// OriginMapping records that a local session identity and a cloud session
// identity are two physical incarnations of the same logical task.
//
// Pondera's SessionRef is {HostID, SessionID} and session.Service.validate()
// refuses any ref whose HostID differs from the local service host. A bundle
// therefore cannot be imported under its original identity: the cloud host
// must issue a fresh {cloudHostID, cloudSessionID} and remember the mapping.
// The same applies in reverse when the task is pulled back down.
type OriginMapping struct {
	LocalHostID    string    `json:"localHostId"`
	LocalSessionID string    `json:"localSessionId"`
	CloudHostID    string    `json:"cloudHostId"`
	CloudSessionID string    `json:"cloudSessionId"`
	CreatedAt      time.Time `json:"createdAt"`
	LastHandoffID  string    `json:"lastHandoffId,omitempty"`
}

// originKey is the stable lookup key for one physical identity.
func originKey(hostID, sessionID string) string {
	return hostID + ":" + sessionID
}

// ErrIdentityConflict means two different logical tasks claim the same
// physical identity. We refuse rather than silently merge.
var ErrIdentityConflict = errors.New("cloudsync: identity conflict")

// Bind records a local<->cloud identity pair. It is idempotent for an
// identical pair and rejects any attempt to reuse an identity for a different
// task.
func (s *Store) Bind(m OriginMapping) error {
	if strings.TrimSpace(m.LocalHostID) == "" || strings.TrimSpace(m.LocalSessionID) == "" ||
		strings.TrimSpace(m.CloudHostID) == "" || strings.TrimSpace(m.CloudSessionID) == "" {
		return errors.New("cloudsync: incomplete origin mapping")
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now().UTC()
	}
	lKey := originKey(m.LocalHostID, m.LocalSessionID)
	cKey := originKey(m.CloudHostID, m.CloudSessionID)

	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.st.Origins[lKey]; ok {
		if existing.CloudHostID != m.CloudHostID || existing.CloudSessionID != m.CloudSessionID {
			return fmt.Errorf("%w: local %s already bound to cloud %s:%s",
				ErrIdentityConflict, lKey, existing.CloudHostID, existing.CloudSessionID)
		}
		if m.LastHandoffID != "" {
			existing.LastHandoffID = m.LastHandoffID
		}
		s.st.Origins[lKey] = existing
		return s.flushLocked()
	}
	if existing, ok := s.st.Origins[cKey]; ok {
		return fmt.Errorf("%w: cloud %s already bound to local %s:%s",
			ErrIdentityConflict, cKey, existing.LocalHostID, existing.LocalSessionID)
	}
	s.st.Origins[lKey] = m
	// Both physical identities point at the same logical mapping. Keeping one
	// canonical view (local = origin host, cloud = destination host) means a
	// lookup from either side answers the same question: "which task is this?".
	s.st.Origins[cKey] = m
	return s.flushLocked()
}

// Resolve returns the logical mapping for a physical identity on either host.
func (s *Store) Resolve(hostID, sessionID string) (OriginMapping, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.st.Origins[originKey(hostID, sessionID)]
	return m, ok
}

// Mappings returns the de-duplicated set of logical identity pairs.
func (s *Store) Mappings() []OriginMapping {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]bool{}
	out := make([]OriginMapping, 0, len(s.st.Origins)/2+1)
	for _, m := range s.st.Origins {
		key := m.LocalHostID + ":" + m.LocalSessionID + "->" + m.CloudHostID + ":" + m.CloudSessionID
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.LocalHostID != b.LocalHostID {
			return a.LocalHostID < b.LocalHostID
		}
		return a.LocalSessionID < b.LocalSessionID
	})
	return out
}

// VersionVector is the monotonic position of a task on one host. A handoff is
// only accepted when the incoming vector is not older than the local one.
type VersionVector struct {
	HostID            string `json:"hostId"`
	SessionID         string `json:"sessionId"`
	BoundarySequence  uint64 `json:"boundarySequence"`
	StorageGeneration string `json:"storageGeneration,omitempty"`
}

// Compare returns -1, 0 or +1 comparing a to b by boundary sequence. Two
// vectors from different hosts are never numerically comparable; callers must
// check HostID/SessionID first and treat a divergence as a hard conflict.
func (a VersionVector) Compare(b VersionVector) int {
	switch {
	case a.BoundarySequence < b.BoundarySequence:
		return -1
	case a.BoundarySequence > b.BoundarySequence:
		return 1
	default:
		return 0
	}
}

// CheckHandoff enforces the single-writer rule before any ownership change:
// the incoming bundle must not be older than what the destination already has,
// and a destination that has advanced beyond the last handoff is a conflict.
func CheckHandoff(incoming, destination VersionVector) error {
	if incoming.HostID == "" || incoming.SessionID == "" {
		return errors.New("cloudsync: incomplete version vector")
	}
	fresh := destination.HostID == "" && destination.SessionID == "" &&
		destination.BoundarySequence == 0 && destination.StorageGeneration == ""
	if fresh {
		return nil // destination has never seen this task
	}
	if destination.BoundarySequence > incoming.BoundarySequence {
		return fmt.Errorf("cloudsync: stale handoff (destination seq %d > incoming seq %d); refusing to overwrite",
			destination.BoundarySequence, incoming.BoundarySequence)
	}
	return nil
}
