package cloudsync

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
)

const (
	// ManifestSchema is the TaskBundle format version.
	ManifestSchema = 1
	// ManifestName is the fixed entry name inside every bundle.
	ManifestName = "manifest.json"
	// MaxBundleEntries bounds a bundle against archive-bomb style inputs.
	MaxBundleEntries = 20000
	// MaxEntryBytes bounds a single file inside a bundle (512 MiB).
	MaxEntryBytes = 512 << 20
	// MaxBundleBytes bounds the extracted payload of a whole bundle (4 GiB).
	MaxBundleBytes = 4 << 30
)

// Origin describes where a bundle was captured.
type Origin struct {
	HostID            string    `json:"hostId"`
	SessionID         string    `json:"sessionId"`
	WorkspaceID       string    `json:"workspaceId,omitempty"`
	HeadTurnID        string    `json:"headTurnId,omitempty"`
	BoundarySequence  uint64    `json:"boundarySequence"`
	StorageGeneration string    `json:"storageGeneration,omitempty"`
	CapturedAt        time.Time `json:"capturedAt"`
}

// ConfigDigest carries only NON-sensitive configuration hints. API keys,
// tokens, SSH identities and .env contents are never part of a bundle.
type ConfigDigest struct {
	ModelRef          string   `json:"modelRef,omitempty"`
	PermissionPreset  string   `json:"permissionPreset,omitempty"`
	MCPNames          []string `json:"mcpNames,omitempty"`
	SkillNames        []string `json:"skillNames,omitempty"`
	PluginNames       []string `json:"pluginNames,omitempty"`
	ConfigFingerprint string   `json:"configFingerprint,omitempty"`
}

// WorkspaceInfo records how the workspace payload was produced.
type WorkspaceInfo struct {
	Root          string `json:"root"`
	IsGit         bool   `json:"isGit"`
	HeadCommit    string `json:"headCommit,omitempty"`
	HandoffCommit string `json:"handoffCommit,omitempty"`
	Dirty         bool   `json:"dirty"`
	BundlePath    string `json:"bundlePath,omitempty"`
	Strategy      string `json:"strategy"` // git-bundle | file-list | none
}

// FileEntry is one verified file in the bundle.
type FileEntry struct {
	Path   string `json:"path"` // slash-separated, relative to bundle root
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Manifest is the authoritative description of a TaskBundle. Digest covers
// every field except Digest itself, so tampering with the file list, origin,
// selection or version vector is detectable.
type Manifest struct {
	Schema        int           `json:"schema"`
	HandoffID     string        `json:"handoffId"`
	CreatedAt     time.Time     `json:"createdAt"`
	Direction     Direction     `json:"direction"`
	Origin        Origin        `json:"origin"`
	Selection     Selection     `json:"selection"`
	CheckpointIDs []string      `json:"checkpointIds,omitempty"`
	ConfigDigest  ConfigDigest  `json:"configDigest"`
	Workspace     WorkspaceInfo `json:"workspace"`
	// TaskWindow records the task-level slice applied to the session log.
	// Nil on legacy bundles (whole session).
	TaskWindow *TaskWindow `json:"taskWindow,omitempty"`
	Excludes   []string    `json:"excludes,omitempty"`
	Files      []FileEntry `json:"files"`
	Digest     string      `json:"digest,omitempty"`
}

// DefaultExcludes are never synchronised. Credentials must stay on the host
// that owns them; build artefacts are reproducible from the source.
var DefaultExcludes = []string{
	"**/node_modules/**",
	"**/target/**",
	"**/dist/**",
	"**/build/**",
	"**/.venv/**",
	"**/venv/**",
	"**/__pycache__/**",
	"**/.next/**",
	"**/.cache/**",
	"**/*.key",
	"**/*.pem",
	"**/id_rsa*",
	"**/id_ed25519*",
	"**/.env",
	"**/.env.*",
	"**/*.p12",
	"**/*.pfx",
	"**/.pondera/cloud/**",
}

// sensitiveExact lists basenames that are rejected outright even if a caller
// overrides the exclude list. This is a deliberate second line of defence:
// a mistyped exclude rule must never leak a private key into a bundle.
var sensitiveExact = map[string]bool{
	".env": true, "id_rsa": true, "id_ed25519": true,
	"credentials.json": true, "service-account.json": true,
}

var safeEntryPath = regexp.MustCompile(`^[A-Za-z0-9._/@+-]+(/[A-Za-z0-9._/@+-]+)*$`)

// ValidateEntryPath rejects absolute paths, parent traversal, backslashes,
// drive letters and control characters. Every archive entry passes through
// here before it is written to disk.
func ValidateEntryPath(name string) error {
	if name == "" || !safeEntryPath.MatchString(name) {
		return fmt.Errorf("cloudsync: unsafe entry path %q", name)
	}
	clean := path.Clean(name)
	if clean != name || strings.HasPrefix(name, "/") || strings.Contains(name, "..") {
		return fmt.Errorf("cloudsync: unsafe entry path %q", name)
	}
	for _, seg := range strings.Split(name, "/") {
		if sensitiveExact[seg] {
			return fmt.Errorf("cloudsync: refusing sensitive file %q", name)
		}
	}
	return nil
}

// Validate checks the manifest's internal consistency.
func (m *Manifest) Validate() error {
	if m.Schema != ManifestSchema {
		return fmt.Errorf("cloudsync: unsupported bundle schema %d", m.Schema)
	}
	if strings.TrimSpace(m.HandoffID) == "" {
		return errors.New("cloudsync: missing handoffId")
	}
	if m.Direction != DirectionUpload && m.Direction != DirectionDownload {
		return fmt.Errorf("cloudsync: invalid direction %q", m.Direction)
	}
	if strings.TrimSpace(m.Origin.HostID) == "" || strings.TrimSpace(m.Origin.SessionID) == "" {
		return errors.New("cloudsync: incomplete origin")
	}
	if m.Selection.SessionID == "" {
		return errors.New("cloudsync: missing selection.sessionId")
	}
	if !m.Selection.IsPinned() {
		return errors.New("cloudsync: bundle selection is not pinned")
	}
	if len(m.Files) > MaxBundleEntries {
		return fmt.Errorf("cloudsync: too many entries (%d)", len(m.Files))
	}
	var total int64
	seen := map[string]bool{}
	for _, f := range m.Files {
		if err := ValidateEntryPath(f.Path); err != nil {
			return err
		}
		if seen[f.Path] {
			return fmt.Errorf("cloudsync: duplicate entry %q", f.Path)
		}
		seen[f.Path] = true
		if f.Size < 0 || f.Size > MaxEntryBytes {
			return fmt.Errorf("cloudsync: entry %q size %d out of range", f.Path, f.Size)
		}
		if len(f.SHA256) != 64 {
			return fmt.Errorf("cloudsync: entry %q has invalid sha256", f.Path)
		}
		total += f.Size
		if total > MaxBundleBytes {
			return errors.New("cloudsync: bundle exceeds size limit")
		}
	}
	return nil
}

// ComputeDigest returns the hex SHA-256 of the canonical manifest with the
// Digest field cleared.
func (m *Manifest) ComputeDigest() (string, error) {
	copyOf := *m
	copyOf.Digest = ""
	copyOf.Files = append([]FileEntry(nil), m.Files...)
	copyOf.Excludes = append([]string(nil), m.Excludes...)
	copyOf.CheckpointIDs = append([]string(nil), m.CheckpointIDs...)
	data, err := json.Marshal(copyOf)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// Seal computes and stores the manifest digest.
func (m *Manifest) Seal() error {
	d, err := m.ComputeDigest()
	if err != nil {
		return err
	}
	m.Digest = d
	return nil
}

// Verify recomputes the digest and compares it in constant time.
func (m *Manifest) Verify() error {
	if m.Digest == "" {
		return errors.New("cloudsync: manifest is not sealed")
	}
	want, err := m.ComputeDigest()
	if err != nil {
		return err
	}
	if !constantTimeEqual(want, m.Digest) {
		return errors.New("cloudsync: manifest digest mismatch")
	}
	return m.Validate()
}

func constantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

// ParseManifest decodes and verifies a manifest from raw bytes.
func ParseManifest(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("cloudsync: decode manifest: %w", err)
	}
	if err := m.Verify(); err != nil {
		return nil, err
	}
	return &m, nil
}
