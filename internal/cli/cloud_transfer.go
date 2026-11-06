package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/pflag"

	"pondera/internal/cloudsync"
	"pondera/internal/remote"
	"pondera/internal/remote/sftpfs"
)

// sshTransport adapts the project's existing supervised SSH client (with its
// TOFU host-key policy, jump hosts and reconnect loop) to the cloudsync
// Transport contract. No new credential or connection machinery is added.
type sshTransport struct {
	client *remote.Client
	fs     *sftpfs.FS
}

func (t *sshTransport) Put(ctx context.Context, p string, r io.Reader, _ int64) error {
	if err := t.fs.MkdirAll(ctx, path.Dir(p)); err != nil {
		return err
	}
	_, err := t.fs.UploadAtomic(ctx, p, r, 0o600)
	return err
}

func (t *sshTransport) Get(ctx context.Context, p string, w io.Writer) error {
	_, err := t.fs.Download(ctx, p, w)
	return err
}

func (t *sshTransport) List(ctx context.Context, dir string) ([]cloudsync.RemoteEntry, error) {
	entries, err := t.fs.List(ctx, dir)
	if err != nil {
		return nil, err
	}
	out := make([]cloudsync.RemoteEntry, 0, len(entries))
	for _, e := range entries {
		if e.IsDir {
			continue
		}
		out = append(out, cloudsync.RemoteEntry{
			Name: e.Name, Size: e.Size, ModTime: time.Unix(e.ModTime, 0),
		})
	}
	return out, nil
}

func (t *sshTransport) Exec(ctx context.Context, cmd string) (string, error) {
	res, err := t.client.Exec(ctx, cmd)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return string(res.Stdout), fmt.Errorf("remote command exited %d: %s", res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	return string(res.Stdout), nil
}

func (t *sshTransport) Close() error { return t.client.Close() }

// buildSSHTransport starts a supervised client and opens its shared SFTP
// session. The caller must Close the transport and cancel the context.
func buildSSHTransport(ctx context.Context, target string) (cloudsync.Transport, error) {
	client, cleanup, err := buildRemoteClient(target)
	if err != nil {
		return nil, err
	}
	if err := client.Start(ctx); err != nil {
		cleanup()
		_ = client.Close()
		return nil, fmt.Errorf("cloud: connect %s: %w", target, err)
	}
	fs, err := client.SFTP()
	if err != nil {
		_ = client.Close()
		cleanup()
		return nil, fmt.Errorf("cloud: open SFTP: %w", err)
	}
	return &sshTransport{client: client, fs: fs}, nil
}

// localTransport runs the whole push/pull pipeline against a local directory
// instead of an SSH host. It is useful for rehearsing a handoff and for the
// end-to-end smoke test: the "remote" pondera process is a real subprocess
// with its own PONDERA_HOME.
type localTransport struct {
	fs         *cloudsync.FilesystemTransport
	remoteHome string
}

var quotedPathRE = regexp.MustCompile(`cloud apply '([^']+)'`)

func (t *localTransport) Put(ctx context.Context, p string, r io.Reader, n int64) error {
	return t.fs.Put(ctx, p, r, n)
}
func (t *localTransport) Get(ctx context.Context, p string, w io.Writer) error {
	return t.fs.Get(ctx, p, w)
}
func (t *localTransport) List(ctx context.Context, d string) ([]cloudsync.RemoteEntry, error) {
	return t.fs.List(ctx, d)
}
func (t *localTransport) Close() error { return nil }

func (t *localTransport) Exec(ctx context.Context, cmd string) (string, error) {
	// Rewrite the remote path into the filesystem transport's local root.
	cmd = quotedPathRE.ReplaceAllStringFunc(cmd, func(m string) string {
		parts := quotedPathRE.FindStringSubmatch(m)
		resolved, err := t.fs.Resolve(parts[1])
		if err != nil {
			return m
		}
		return "cloud apply '" + resolved + "'"
	})
	c := exec.CommandContext(ctx, "sh", "-c", cmd)
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "PONDERA_HOME=") {
			env = append(env, kv)
		}
	}
	c.Env = append(env, "PONDERA_HOME="+t.remoteHome)
	out, err := c.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("local remote exec: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func buildLocalTransport(root, remoteHome string) cloudsync.Transport {
	fs := &cloudsync.FilesystemTransport{Root: root}
	return &localTransport{fs: fs, remoteHome: remoteHome}
}

func parseQuiesceMode(s string) (cloudsync.QuiesceMode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "check":
		return cloudsync.QuiesceCheck, nil
	case "drain":
		return cloudsync.QuiesceDrain, nil
	case "force":
		return cloudsync.QuiesceForce, nil
	default:
		return "", fmt.Errorf("invalid --quiesce %q (check|drain|force)", s)
	}
}

func cloudQuiesceOptions(mode, serveURL, token, sessionID, sessionDir string) (*cloudsync.QuiesceOptions, error) {
	m, err := parseQuiesceMode(mode)
	if err != nil {
		return nil, err
	}
	return &cloudsync.QuiesceOptions{
		Mode: m, ServeURL: serveURL, Token: token,
		SessionID: sessionID, SessionDir: sessionDir,
	}, nil
}

// cloudPush implements `pondera cloud push <session>`: one command replaces
// the old pack + manual scp + apply + confirm sequence.
func cloudPush(args []string) int {
	fs := pflag.NewFlagSet("cloud push", pflag.ContinueOnError)
	target := fs.String("to", "", "remote host name from [remote.hosts] (SSH/SFTP)")
	localRoot := fs.String("local-transport", "", "本地演练：用目录模拟远端（不接 SSH）")
	remoteDir := fs.String("remote-dir", "", "远端 bundle 目录（SSH 时须为绝对路径）")
	remoteHome := fs.String("remote-home", "", "远端 PONDERA_HOME（本地演练时必填）")
	cloudBin := fs.String("cloud-bin", "pondera", "远端 pondera 可执行文件")
	sessionRoot := fs.String("session-root", "", "远端 sessions-v4 根目录")
	forceSession := fs.Bool("force-session", false, "远端已有同名会话时覆盖")
	workspaceDir := fs.String("workspace-dir", "", "工作区目录")
	sessionDir := fs.String("session-dir", "", "会话数据目录（留空自动发现）")
	outDir := fs.String("out", "", "本地 bundle 输出目录")
	quiesce := fs.String("quiesce", "drain", "check|drain|force")
	serveURL := fs.String("serve-url", "", "本地 serve 地址（用于检测运行中任务）")
	token := fs.String("token", "", "serve token")
	cloudHostID := fs.String("cloud-host-id", "cloud-host", "")
	cloudSessionID := fs.String("cloud-session-id", "", "")
	boundary := fs.Uint64("boundary-sequence", 0, "")
	noSlice := fs.Bool("no-slice", false, "禁用任务级裁剪，整体同步会话日志")
	skipApply := fs.Bool("skip-remote-apply", false, "仅上传，不触发远端导入（调试用）")
	if code, ok := parseCommandFlags(fs, args); !ok {
		return code
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "用法: pondera cloud push <session> --to <host> [flags]")
		return 2
	}
	sessionID := fs.Arg(0)
	if *target == "" && *localRoot == "" {
		fmt.Fprintln(os.Stderr, "cloud: 需要 --to <host> 或 --local-transport <dir>")
		return 2
	}
	store, home, err := cloudOpenStore()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cloud:", err)
		return 1
	}
	out := *outDir
	if out == "" {
		out = filepath.Join(home, "cloud", "out")
	}
	csid := *cloudSessionID
	if csid == "" {
		csid = "cloud-" + sessionID
	}
	qopts, err := cloudQuiesceOptions(*quiesce, *serveURL, *token, sessionID, *sessionDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cloud:", err)
		return 2
	}
	rd := *remoteDir
	if rd == "" {
		if *localRoot != "" {
			rd = "incoming"
		} else {
			rd = "~/pondera/incoming"
		}
	}
	var tr cloudsync.Transport
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if *localRoot != "" {
		if *remoteHome == "" {
			fmt.Fprintln(os.Stderr, "cloud: --local-transport 需要 --remote-home（远端独立的 PONDERA_HOME）")
			return 2
		}
		tr = buildLocalTransport(*localRoot, *remoteHome)
	} else {
		tr, err = buildSSHTransport(ctx, *target)
		if err != nil {
			fmt.Fprintln(os.Stderr, "cloud:", err)
			return 1
		}
	}
	defer func() { _ = tr.Close() }()

	sliceEnabled := !*noSlice
	res, err := cloudsync.Push(ctx, cloudsync.PushOptions{
		Planner:   &cloudsync.Planner{Store: store, Home: home},
		Transport: tr, RemoteDir: rd, CloudBin: *cloudBin,
		RemoteHome: *remoteHome, SessionRoot: *sessionRoot,
		ForceSession: *forceSession, QuiesceOpts: qopts, SkipRemoteCmd: *skipApply,
		Prepare: cloudsync.PrepareUploadOptions{
			SessionID: sessionID, WorkspaceDir: *workspaceDir, SessionDir: *sessionDir,
			OutDir: out, CloudHostID: *cloudHostID, CloudSessionID: csid,
			BoundarySequence: *boundary, SliceTasks: &sliceEnabled,
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "cloud: push 失败（本地所有权未改变）:", err)
		return 1
	}
	fmt.Printf("push 完成:\n  handoff   : %s\n  bundle    : %s\n  远端路径  : %s\n  quiesce   : %s\n  文件数    : %d\n  任务范围  : %s\n",
		res.Plan.HandoffID, res.Plan.BundlePath, res.RemotePath, res.Quiesce.Source,
		len(res.Plan.Manifest.Files), cloudsync.WindowNote(res.Plan.TaskWindow))
	if res.RemoteApply != "" {
		fmt.Printf("  远端导入  : %s\n", strings.TrimSpace(res.RemoteApply))
	}
	fmt.Println("  所有权    : 已移交云端；手机现在可以连云端 serve 继续任务")
	return 0
}

// cloudPull implements `pondera cloud pull`: bring a cloud-side bundle back.
func cloudPull(args []string) int {
	fs := pflag.NewFlagSet("cloud pull", pflag.ContinueOnError)
	target := fs.String("from", "", "remote host name from [remote.hosts]")
	localRoot := fs.String("local-transport", "", "本地演练：用目录模拟远端")
	remoteDir := fs.String("remote-dir", "", "远端 bundle 目录")
	remoteBundle := fs.String("remote-bundle", "", "指定远端 bundle 路径（否则取最新）")
	localDir := fs.String("local-dir", "", "本地下载目录")
	sessionRoot := fs.String("session-root", "", "本地 sessions-v4 根目录")
	forceSession := fs.Bool("force-session", false, "覆盖已存在的同名会话")
	haveSeq := fs.Uint64("have-seq", 0, "本地已有会话序号")
	if code, ok := parseCommandFlags(fs, args); !ok {
		return code
	}
	if *target == "" && *localRoot == "" {
		fmt.Fprintln(os.Stderr, "cloud: 需要 --from <host> 或 --local-transport <dir>")
		return 2
	}
	store, home, err := cloudOpenStore()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cloud:", err)
		return 1
	}
	ld := *localDir
	if ld == "" {
		ld = filepath.Join(home, "cloud", "downloads")
	}
	rd := *remoteDir
	if rd == "" {
		if *localRoot != "" {
			rd = "out"
		} else {
			rd = "~/pondera/out"
		}
	}
	sr := *sessionRoot
	if sr == "" {
		sr = filepath.Join(home, "sessions-v4")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var tr cloudsync.Transport
	if *localRoot != "" {
		tr = buildLocalTransport(*localRoot, home)
	} else {
		tr, err = buildSSHTransport(ctx, *target)
		if err != nil {
			fmt.Fprintln(os.Stderr, "cloud:", err)
			return 1
		}
	}
	defer func() { _ = tr.Close() }()
	res, err := cloudsync.Pull(ctx, cloudsync.PullOptions{
		Planner: &cloudsync.Planner{Store: store, Home: home}, Transport: tr,
		RemoteDir: rd, RemoteBundle: *remoteBundle, LocalDir: ld,
		SessionRoot: sr, ForceSession: *forceSession, HaveSeq: *haveSeq,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "cloud: pull 失败:", err)
		return 1
	}
	fmt.Printf("pull 完成:\n  bundle    : %s\n  解包目录  : %s\n  会话安装  : %s\n  写者      : local\n",
		res.LocalBundle, res.Apply.ExtractDir, res.Apply.InstalledSessionDir)
	return 0
}
