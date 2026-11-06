package cloudsync

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// BuildOptions describes one explicit, user-initiated bundle capture.
type BuildOptions struct {
	// OutPath is the destination .tar.gz path.
	OutPath string
	// HandoffID overrides the generated transfer id (used by Planner so the
	// plan and the sealed manifest share one identity).
	HandoffID string
	// Direction is upload (local->cloud) or download (cloud->local).
	Direction Direction
	// Selection must be pinned; an unpinned session can never be bundled.
	Selection Selection
	Origin    Origin
	// SessionDir / CheckpointDirs / WorkspaceDir are optional source roots.
	// Their contents are copied under session/, checkpoints/ and workspace/
	// respectively inside the archive.
	SessionDir     string
	CheckpointDirs []string
	WorkspaceDir   string
	Workspace      WorkspaceInfo
	CheckpointIDs  []string
	ConfigDigest   ConfigDigest
	// TaskWindow records how the session log was scoped for selected tasks.
	TaskWindow *TaskWindow
	// Excludes overrides DefaultExcludes when non-nil.
	Excludes []string
	// Now is injectable for deterministic tests.
	Now func() time.Time
}

// BuildResult is returned by Build.
type BuildResult struct {
	Manifest   *Manifest
	BundlePath string
	Bytes      int64
}

// Build captures a TaskBundle. It never mutates any source file and never
// reads anything outside the declared roots.
func Build(opts BuildOptions) (*BuildResult, error) {
	if strings.TrimSpace(opts.OutPath) == "" {
		return nil, errors.New("cloudsync: empty output path")
	}
	if !opts.Selection.IsPinned() {
		return nil, errors.New("cloudsync: refusing to bundle a local-only session")
	}
	if opts.Direction != DirectionUpload && opts.Direction != DirectionDownload {
		return nil, fmt.Errorf("cloudsync: invalid direction %q", opts.Direction)
	}
	handoffID := strings.TrimSpace(opts.HandoffID)
	if handoffID == "" {
		generated, genErr := NewHandoffID()
		if genErr != nil {
			return nil, genErr
		}
		handoffID = generated
	}
	now := time.Now().UTC()
	if opts.Now != nil {
		now = opts.Now().UTC()
	}
	excludes := opts.Excludes
	if excludes == nil {
		excludes = DefaultExcludes
	}
	origin := opts.Origin
	origin.CapturedAt = now

	// Stage every source file first so the manifest is complete and sealed
	// before a single byte of the archive is written.
	type staged struct {
		abs, entry string
		size       int64
		sha        string
	}
	var files []staged
	addDir := func(root, prefix string) error {
		if strings.TrimSpace(root) == "" {
			return nil
		}
		info, err := os.Stat(root)
		if err != nil {
			return fmt.Errorf("cloudsync: stat %s: %w", root, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("cloudsync: %s is not a directory", root)
		}
		return filepath.WalkDir(root, func(p string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				return nil
			}
			if !d.Type().IsRegular() {
				return nil // symlinks / devices are never bundled
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			entry := path.Join(prefix, filepath.ToSlash(rel))
			if ShouldExclude(entry, excludes) {
				return nil
			}
			if err := ValidateEntryPath(entry); err != nil {
				// A sensitive file is skipped, not fatal: the rule exists to
				// keep credentials out, and the caller can inspect Excludes.
				if strings.Contains(err.Error(), "sensitive") {
					return nil
				}
				return err
			}
			st, err := d.Info()
			if err != nil {
				return err
			}
			if st.Size() > MaxEntryBytes {
				return fmt.Errorf("cloudsync: %s exceeds per-file limit", entry)
			}
			sha, err := fileSHA256(p)
			if err != nil {
				return err
			}
			files = append(files, staged{abs: p, entry: entry, size: st.Size(), sha: sha})
			return nil
		})
	}
	if err := addDir(opts.SessionDir, "session"); err != nil {
		return nil, err
	}
	for i, dir := range opts.CheckpointDirs {
		prefix := path.Join("checkpoints", fmt.Sprintf("%02d", i))
		if err := addDir(dir, prefix); err != nil {
			return nil, err
		}
	}
	if err := addDir(opts.WorkspaceDir, "workspace"); err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].entry < files[j].entry })
	if len(files) > MaxBundleEntries {
		return nil, fmt.Errorf("cloudsync: too many files (%d)", len(files))
	}

	entries := make([]FileEntry, 0, len(files))
	for _, f := range files {
		entries = append(entries, FileEntry{Path: f.entry, SHA256: f.sha, Size: f.size})
	}
	m := &Manifest{
		Schema:        ManifestSchema,
		HandoffID:     handoffID,
		CreatedAt:     now,
		Direction:     opts.Direction,
		Origin:        origin,
		Selection:     opts.Selection,
		CheckpointIDs: append([]string(nil), opts.CheckpointIDs...),
		ConfigDigest:  opts.ConfigDigest,
		Workspace:     opts.Workspace,
		TaskWindow:    opts.TaskWindow,
		Excludes:      append([]string(nil), excludes...),
		Files:         entries,
	}
	if err := m.Seal(); err != nil {
		return nil, err
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(opts.OutPath), 0o700); err != nil {
		return nil, err
	}
	out, err := os.Create(opts.OutPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = out.Close() }()
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)

	manifestBytes, err := marshalManifest(m)
	if err != nil {
		return nil, err
	}
	if err := writeTarEntry(tw, ManifestName, manifestBytes, now); err != nil {
		return nil, err
	}
	for _, f := range files {
		if err := writeTarFile(tw, f.entry, f.abs, f.size, now); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	if err := out.Sync(); err != nil {
		return nil, err
	}
	st, err := out.Stat()
	if err != nil {
		return nil, err
	}
	return &BuildResult{Manifest: m, BundlePath: opts.OutPath, Bytes: st.Size()}, nil
}

func marshalManifest(m *Manifest) ([]byte, error) {
	return json.MarshalIndent(m, "", "  ")
}

func writeTarEntry(tw *tar.Writer, name string, data []byte, now time.Time) error {
	hdr := &tar.Header{
		Name: name, Mode: 0o600, Size: int64(len(data)), ModTime: now,
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}

func writeTarFile(tw *tar.Writer, name, abs string, size int64, now time.Time) error {
	f, err := os.Open(abs)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	hdr := &tar.Header{
		Name: name, Mode: 0o600, Size: size, ModTime: now, Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	n, err := io.Copy(tw, io.LimitReader(f, size))
	if err != nil {
		return err
	}
	if n != size {
		return fmt.Errorf("cloudsync: short read of %s (%d != %d)", abs, n, size)
	}
	return nil
}

// Opened is a verified, opened bundle ready for extraction.
type Opened struct {
	Path     string
	Manifest *Manifest
}

// OpenBundle reads and verifies a bundle's manifest without extracting any
// payload. Call Extract afterwards.
func OpenBundle(bundlePath string) (*Opened, error) {
	f, err := os.Open(bundlePath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("cloudsync: open gzip: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("cloudsync: bundle has no manifest")
		}
		if err != nil {
			return nil, fmt.Errorf("cloudsync: read archive: %w", err)
		}
		if hdr.Name != ManifestName {
			continue
		}
		if hdr.Size > 8<<20 {
			return nil, errors.New("cloudsync: manifest too large")
		}
		data, err := io.ReadAll(io.LimitReader(tr, hdr.Size))
		if err != nil {
			return nil, err
		}
		m, err := ParseManifest(data)
		if err != nil {
			return nil, err
		}
		return &Opened{Path: bundlePath, Manifest: m}, nil
	}
}

// Extract verifies every entry against the manifest and writes it under dest.
// It is fail-closed: any mismatch aborts the extraction.
func (o *Opened) Extract(dest string) error {
	if strings.TrimSpace(dest) == "" {
		return errors.New("cloudsync: empty extraction destination")
	}
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return err
	}
	expected := map[string]FileEntry{}
	for _, e := range o.Manifest.Files {
		expected[e.Path] = e
	}
	f, err := os.Open(o.Path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	seen := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("cloudsync: read archive: %w", err)
		}
		if hdr.Name == ManifestName {
			continue
		}
		if err := ValidateEntryPath(hdr.Name); err != nil {
			return err
		}
		want, ok := expected[hdr.Name]
		if !ok {
			return fmt.Errorf("cloudsync: unexpected entry %q", hdr.Name)
		}
		if hdr.Size != want.Size || hdr.Size > MaxEntryBytes {
			return fmt.Errorf("cloudsync: entry %q size mismatch", hdr.Name)
		}
		if hdr.Typeflag != tar.TypeReg {
			return fmt.Errorf("cloudsync: entry %q is not a regular file", hdr.Name)
		}
		target := filepath.Join(dest, filepath.FromSlash(hdr.Name))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		h := sha256.New()
		n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(tr, hdr.Size))
		closeErr := out.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if n != hdr.Size {
			return fmt.Errorf("cloudsync: short entry %q", hdr.Name)
		}
		if got := hex.EncodeToString(h.Sum(nil)); got != want.SHA256 {
			return fmt.Errorf("cloudsync: checksum mismatch for %q", hdr.Name)
		}
		seen[hdr.Name] = true
	}
	for name := range expected {
		if !seen[name] {
			return fmt.Errorf("cloudsync: missing entry %q", name)
		}
	}
	return nil
}

func fileSHA256(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ShouldExclude matches a slash-separated relative path against glob rules.
// "**" matches any number of path segments; "*" matches within one segment.
func ShouldExclude(name string, rules []string) bool {
	name = strings.TrimPrefix(path.Clean(name), "/")
	for _, rule := range rules {
		rule = strings.TrimPrefix(path.Clean(strings.TrimSpace(rule)), "/")
		if rule == "" {
			continue
		}
		if globMatch(rule, name) {
			return true
		}
		// A directory rule also excludes everything beneath it.
		base := strings.TrimSuffix(rule, "/**")
		if base != rule && (name == base || strings.HasPrefix(name, base+"/")) {
			return true
		}
	}
	return false
}

func globMatch(pattern, name string) bool {
	pSeg := strings.Split(pattern, "/")
	nSeg := strings.Split(name, "/")
	var walk func(pi, ni int) bool
	walk = func(pi, ni int) bool {
		if pi == len(pSeg) {
			return ni == len(nSeg)
		}
		if pSeg[pi] == "**" {
			for k := ni; k <= len(nSeg); k++ {
				if walk(pi+1, k) {
					return true
				}
			}
			return false
		}
		if ni >= len(nSeg) {
			return false
		}
		ok, err := path.Match(pSeg[pi], nSeg[ni])
		if err != nil || !ok {
			return false
		}
		return walk(pi+1, ni+1)
	}
	return walk(0, 0)
}
