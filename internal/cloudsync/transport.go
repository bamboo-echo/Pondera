package cloudsync

// One-shot cross-machine transport for TaskBundles. The Planner itself stays
// network-free; Push/Pull compose explicit prepare -> transfer -> apply ->
// commit steps over a small Transport interface. Nothing runs in the
// background: each command is a single user-initiated operation.

import (
	"context"
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

type RemoteEntry struct {
	Name    string
	Size    int64
	ModTime time.Time
}

// Transport is the minimal contract a push/pull needs. The SSH/SFTP adapter
// lives in internal/cli (it owns the supervised remote.Client); a filesystem
// adapter below makes the full pipeline testable without a network.
type Transport interface {
	Put(ctx context.Context, remotePath string, r io.Reader, size int64) error
	Get(ctx context.Context, remotePath string, w io.Writer) error
	List(ctx context.Context, dir string) ([]RemoteEntry, error)
	Exec(ctx context.Context, cmd string) (string, error)
	Close() error
}

// FilesystemTransport maps remote paths onto a local root. Exec is injectable
// so an end-to-end test can emulate the destination's `cloud apply` step.
type FilesystemTransport struct {
	Root     string
	ExecFunc func(ctx context.Context, cmd string) (string, error)
}

func (t *FilesystemTransport) resolve(p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", errors.New("cloudsync: empty remote path")
	}
	raw := strings.TrimSpace(p)
	for _, seg := range strings.Split(strings.ReplaceAll(raw, `\`, "/"), "/") {
		if seg == ".." {
			return "", fmt.Errorf("cloudsync: remote path escapes root: %s", p)
		}
	}
	clean := path.Clean("/" + raw)
	rel := strings.TrimPrefix(clean, "/")
	if rel == "" {
		return filepath.Clean(t.Root), nil
	}
	if strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("cloudsync: remote path escapes root: %s", p)
	}
	return filepath.Join(t.Root, filepath.FromSlash(rel)), nil
}

// Resolve exposes the safe root-relative mapping for adapters that need to
// hand a local path to a subprocess (e.g. the local rehearsal transport).
func (t *FilesystemTransport) Resolve(p string) (string, error) { return t.resolve(p) }

func (t *FilesystemTransport) Put(_ context.Context, remotePath string, r io.Reader, _ int64) error {
	dst, err := t.resolve(remotePath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	tmp := dst + ".uploading"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dst)
}

func (t *FilesystemTransport) Get(_ context.Context, remotePath string, w io.Writer) error {
	src, err := t.resolve(remotePath)
	if err != nil {
		return err
	}
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(w, f)
	return err
}

func (t *FilesystemTransport) List(_ context.Context, dir string) ([]RemoteEntry, error) {
	root, err := t.resolve(dir)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	out := make([]RemoteEntry, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, statErr := e.Info()
		if statErr != nil {
			continue
		}
		out = append(out, RemoteEntry{Name: e.Name(), Size: info.Size(), ModTime: info.ModTime()})
	}
	return out, nil
}

func (t *FilesystemTransport) Exec(ctx context.Context, cmd string) (string, error) {
	if t.ExecFunc == nil {
		return "", errors.New("cloudsync: filesystem transport has no remote exec; inject ExecFunc")
	}
	return t.ExecFunc(ctx, cmd)
}

func (t *FilesystemTransport) Close() error { return nil }

// ShellQuote wraps a single argument for a POSIX shell.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

type PushOptions struct {
	Planner       *Planner
	Prepare       PrepareUploadOptions
	Transport     Transport
	RemoteDir     string
	CloudBin      string
	RemoteHome    string
	SessionRoot   string
	ForceSession  bool
	Quiesce       *QuiesceReport
	QuiesceOpts   *QuiesceOptions
	SkipRemoteCmd bool
}

type PushResult struct {
	Plan        *UploadPlan
	Quiesce     *QuiesceReport
	RemotePath  string
	RemoteApply string
}

// Push performs the whole local->cloud handoff in one command. Ownership only
// flips after the destination reports a successful import; any earlier failure
// leaves the local machine as the writer and keeps the bundle for a retry.
func Push(ctx context.Context, opts PushOptions) (*PushResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if opts.Planner == nil || opts.Transport == nil {
		return nil, errors.New("cloudsync: push requires a planner and a transport")
	}
	result := &PushResult{}
	if opts.Quiesce != nil {
		result.Quiesce = opts.Quiesce
	} else if opts.QuiesceOpts != nil {
		q, err := CheckQuiesced(ctx, *opts.QuiesceOpts)
		if err != nil {
			return nil, err
		}
		result.Quiesce = q
	} else {
		q, err := CheckQuiesced(ctx, QuiesceOptions{
			SessionID: opts.Prepare.SessionID, SessionDir: opts.Prepare.SessionDir,
			Mode: QuiesceForce,
		})
		if err != nil {
			return nil, err
		}
		result.Quiesce = q
	}
	plan, err := opts.Planner.PrepareUpload(opts.Prepare, true)
	if err != nil {
		return nil, err
	}
	result.Plan = plan
	remoteDir := opts.RemoteDir
	if remoteDir == "" {
		remoteDir = "~/pondera/incoming"
	}
	remotePath := path.Join(remoteDir, filepath.Base(plan.BundlePath))
	f, err := os.Open(plan.BundlePath)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := opts.Transport.Put(ctx, remotePath, f, st.Size()); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("cloudsync: upload bundle: %w", err)
	}
	_ = f.Close()
	result.RemotePath = remotePath

	if !opts.SkipRemoteCmd {
		bin := opts.CloudBin
		if bin == "" {
			bin = "pondera"
		}
		cmd := ShellQuote(bin) + " cloud apply " + ShellQuote(remotePath) +
			" --owner cloud --pin --dest " + ShellQuote(path.Join(remoteHomeFor(opts), "cloud", "incoming"))
		if opts.SessionRoot != "" {
			cmd += " --session-root " + ShellQuote(opts.SessionRoot)
		}
		if opts.ForceSession {
			cmd += " --force-session"
		}
		out, execErr := opts.Transport.Exec(ctx, cmd)
		if execErr != nil {
			return nil, fmt.Errorf("cloudsync: destination import failed (local ownership unchanged): %w", execErr)
		}
		result.RemoteApply = strings.TrimSpace(out)
	}
	if err := opts.Planner.CommitUpload(plan, opts.Prepare.CloudHostID, opts.Prepare.CloudSessionID, true, "push: "+remotePath); err != nil {
		return nil, err
	}
	return result, nil
}

func remoteHomeFor(opts PushOptions) string {
	if strings.TrimSpace(opts.RemoteHome) != "" {
		return opts.RemoteHome
	}
	return "~/.pondera"
}

type PullOptions struct {
	Planner       *Planner
	Transport     Transport
	RemoteDir     string
	RemoteBundle  string
	LocalDir      string
	ExtractRoot   string
	SessionRoot   string
	ForceSession  bool
	HaveSeq       uint64
	SkipRemoteCmd bool
}

type PullResult struct {
	LocalBundle string
	Apply       *ApplyResult
}

// Pull brings a cloud-side bundle back to the workstation. It never touches
// the cloud's ownership state: the source host must confirm the handoff
// separately (or run push in the other direction).
func Pull(ctx context.Context, opts PullOptions) (*PullResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if opts.Planner == nil || opts.Transport == nil {
		return nil, errors.New("cloudsync: pull requires a planner and a transport")
	}
	remote := strings.TrimSpace(opts.RemoteBundle)
	if remote == "" {
		dir := opts.RemoteDir
		if dir == "" {
			dir = "~/pondera/out"
		}
		entries, err := opts.Transport.List(ctx, dir)
		if err != nil {
			return nil, fmt.Errorf("cloudsync: list remote bundles: %w", err)
		}
		var candidates []RemoteEntry
		for _, e := range entries {
			if strings.HasPrefix(e.Name, "task-") && strings.HasSuffix(e.Name, ".tar.gz") {
				candidates = append(candidates, e)
			}
		}
		if len(candidates) == 0 {
			return nil, errors.New("cloudsync: no task bundle found on the remote host")
		}
		sort.Slice(candidates, func(i, j int) bool { return candidates[i].ModTime.After(candidates[j].ModTime) })
		remote = path.Join(dir, candidates[0].Name)
	}
	localDir := opts.LocalDir
	if localDir == "" {
		return nil, errors.New("cloudsync: pull requires a local directory")
	}
	if err := os.MkdirAll(localDir, 0o700); err != nil {
		return nil, err
	}
	local := filepath.Join(localDir, path.Base(remote))
	out, err := os.Create(local)
	if err != nil {
		return nil, err
	}
	if err := opts.Transport.Get(ctx, remote, out); err != nil {
		_ = out.Close()
		_ = os.Remove(local)
		return nil, fmt.Errorf("cloudsync: download bundle: %w", err)
	}
	if err := out.Close(); err != nil {
		return nil, err
	}
	extractRoot := opts.ExtractRoot
	if extractRoot == "" {
		extractRoot = filepath.Join(localDir, "incoming")
	}
	apply, err := opts.Planner.ApplyIncoming(ApplyOptions{
		BundlePath: local, ExtractRoot: extractRoot, NewOwner: OwnerLocal,
		PinOnApply: false, SessionRoot: opts.SessionRoot,
		ForceSession: opts.ForceSession, Ctx: ctx,
		DestinationVector: VersionVector{BoundarySequence: opts.HaveSeq},
	})
	if err != nil {
		return nil, err
	}
	return &PullResult{LocalBundle: local, Apply: apply}, nil
}
