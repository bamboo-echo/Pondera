package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"pondera/internal/cloudsync"
	"pondera/internal/config"

	"github.com/spf13/pflag"
)

// cloudCommand implements the explicit, opt-in cloud sync switch. Nothing here
// runs in the background: every transfer is a one-shot command the user types.
func cloudCommand(args []string) int {
	if len(args) == 0 {
		cloudUsage()
		return 2
	}
	switch args[0] {
	case "list", "ls":
		return cloudList()
	case "status":
		return cloudStatus(args[1:])
	case "pin":
		return cloudPin(args[1:])
	case "unpin":
		return cloudUnpin(args[1:])
	case "pack":
		return cloudPack(args[1:])
	case "apply":
		return cloudApply(args[1:])
	case "confirm":
		return cloudConfirm(args[1:])
	case "mappings":
		return cloudMappings()
	case "push":
		return cloudPush(args[1:])
	case "pull":
		return cloudPull(args[1:])
	case "watch":
		return cloudWatch(args[1:])
	case "cost":
		return cloudCost(args[1:])
	case "help", "-h", "--help":
		cloudUsage()
		return 0
	default:
		fmt.Fprintf(os.Stderr, "cloud: unknown subcommand %q\n\n", args[0])
		cloudUsage()
		return 2
	}
}

func cloudUsage() {
	fmt.Print(`pondera cloud — 手动选择式云同步（默认全部本地，选中才上云）

用法:
  pondera cloud list                          列出会话与同步状态
  pondera cloud status <session>              查看单个会话状态
  pondera cloud pin <session> [flags]         选中会话（及可选任务）上云
  pondera cloud unpin <session>               取消选中，回到纯本地
  pondera cloud pack <session> [flags]        打包一个 TaskBundle（本地 -> 云）
  pondera cloud apply <bundle> [flags]        在目标机导入 TaskBundle
  pondera cloud confirm <session> [flags]     云端确认导入成功后，本地移交所有权
  pondera cloud mappings                     查看本地<->云端身份映射
  pondera cloud push <session> --to <host>   一键：打包+上传+云端导入+移交所有权
  pondera cloud pull --from <host>           一键：下载云端 bundle 并还原到本地
  pondera cloud watch <session> [flags]      实时旁观云端任务输出（跨机镜像帧）
  pondera cloud cost [flags]                 查看/设置云端成本闸门

pin 选项:
  --title <text>        会话标题
  --workspace <id>      工作区标识
  --task <kind>:<id>    只关注某个任务，可重复（kind = goal|job|turn）

pack 选项:
  --workspace-dir <dir> 要同步的工作区目录
  --session-dir <dir>   会话数据目录（可选）
  --checkpoint <dir>    额外检查点目录，可重复
  --out <dir>           bundle 输出目录（默认 ~/.pondera/cloud/out）
  --cloud-host-id <id>  云端主机标识（默认 cloud-host）
  --cloud-session-id <id> 云端将为该任务签发的会话 ID（默认 cloud-<session>）
  --boundary-sequence N 当前持久会话序号（默认 0）
  --quiesce <mode>      check|drain|force（默认 drain：自动检测并在需要时取消回合）
  --serve-url <url>     本地 serve 地址，用于检测 running 状态
  --token <token>       serve token
  --session-root <dir>  自动发现会话目录的 sessions-v4 根
  --no-slice            禁用任务级裁剪，整体同步会话日志
  --confirm-quiesced    已弃用：等价于 --quiesce force

apply 选项:
  --dest <dir>          解包暂存目录（默认 ~/.pondera/cloud/incoming）
  --workspace-dest <dir> git 工作区还原目标目录（可选）
  --owner cloud|local   导入后的写者（上传填 cloud，拉回本地填 local）
  --pin                 首次导入时在本机记录该选择
  --have-seq N          本机已有的会话序号，用于陈旧检测
  --session-root <dir>  解包后把会话安装到该 sessions-v4 根
  --force-session       覆盖已存在的同名会话

confirm 选项:
  --bundle <path>       已上传的 bundle（读取其 manifest）
  --cloud-host-id <id>
  --cloud-session-id <id>
  --failed              标记失败（不移交所有权）

典型流程:
  # 本地：选中一个会话（不选中任何任务的会话保持纯本地，永不同步）
  pondera cloud pin sess-abc --task goal:fix-auth

  # 本地：自动检测空闲并打包（运行中会按 --quiesce 处理）
  pondera cloud pack sess-abc --workspace-dir .

  # 或者一键完成全部步骤（推荐，使用现有 SSH/SFTP 配置）
  pondera cloud push sess-abc --to my-cloud --remote-dir /srv/pondera/incoming \
      --session-root /srv/pondera/.pondera/sessions-v4

  # 手工搬运仍可用：
  scp ~/.pondera/cloud/out/task-*.tar.gz my-cloud:~/incoming/

  # 云主机：导入并成为写者
  pondera cloud apply ~/incoming/task-xxx.tar.gz --owner cloud --pin

  # 本地：确认云端导入成功，移交所有权；之后手机即可连云端继续
  pondera cloud confirm sess-abc --bundle ~/.pondera/cloud/out/task-xxx.tar.gz

  # 需要拉回本地继续开发时，在云端 pack、本地 apply --owner local 即可
`)
}

func cloudOpenStore() (*cloudsync.Store, string, error) {
	home := config.PonderaHomeDir()
	store, err := cloudsync.OpenStore(home)
	if err != nil {
		return nil, "", err
	}
	if err := cloudsync.EnsureLocalHostID(store, home); err != nil {
		return nil, "", err
	}
	return store, home, nil
}

func cloudList() int {
	store, _, err := cloudOpenStore()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cloud:", err)
		return 1
	}
	sels := store.List()
	if len(sels) == 0 {
		fmt.Println("没有记录任何会话的同步策略；所有会话均为纯本地（local-only）。")
		fmt.Println("用 `pondera cloud pin <session>` 显式选中需要上云的会话。")
		return 0
	}
	fmt.Printf("%-24s %-14s %-8s %-6s %s\n", "SESSION", "SCOPE", "OWNER", "TASKS", "TITLE")
	for _, s := range sels {
		fmt.Printf("%-24s %-14s %-8s %-6d %s\n",
			s.SessionID, s.Scope, s.Owner, len(s.Tasks), s.Title)
	}
	fmt.Printf("\n共 %d 个会话，其中 %d 个已选中上云。未列出的会话永不上传。\n",
		len(sels), len(store.Pinned()))
	return 0
}

func cloudStatus(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "用法: pondera cloud status <session>")
		return 2
	}
	store, _, err := cloudOpenStore()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cloud:", err)
		return 1
	}
	sel, ok := store.Get(args[0])
	if !ok {
		fmt.Printf("会话 %q 没有同步策略 → 纯本地（local-only），永不上传。\n", args[0])
		return 0
	}
	enc, _ := json.MarshalIndent(sel, "", "  ")
	fmt.Println(string(enc))
	if m, ok := store.Resolve(store.LocalHostID(), sel.SessionID); ok {
		fmt.Printf("\n云端对应身份: %s:%s (上次 handoff %s)\n",
			m.CloudHostID, m.CloudSessionID, m.LastHandoffID)
	}
	return 0
}

func parseTaskRefs(raw []string) ([]cloudsync.TaskRef, error) {
	out := make([]cloudsync.TaskRef, 0, len(raw))
	for _, item := range raw {
		parts := strings.SplitN(item, ":", 2)
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return nil, fmt.Errorf("任务格式应为 <kind>:<id>，得到 %q", item)
		}
		out = append(out, cloudsync.TaskRef{Kind: parts[0], ID: parts[1]})
	}
	return out, nil
}

func cloudPin(args []string) int {
	fs := pflag.NewFlagSet("cloud pin", pflag.ContinueOnError)
	title := fs.String("title", "", "会话标题")
	workspace := fs.String("workspace", "", "工作区标识")
	tasks := fs.StringArray("task", nil, "只关注的任务 <kind>:<id>，可重复")
	if code, ok := parseCommandFlags(fs, args); !ok {
		return code
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "用法: pondera cloud pin <session> [--title ...] [--task goal:id]")
		return 2
	}
	refs, err := parseTaskRefs(*tasks)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cloud:", err)
		return 2
	}
	store, _, err := cloudOpenStore()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cloud:", err)
		return 1
	}
	if err := store.Pin(fs.Arg(0), *title, *workspace, refs); err != nil {
		fmt.Fprintln(os.Stderr, "cloud:", err)
		return 1
	}
	fmt.Printf("已选中会话 %q 上云（%s）。注意：仅标记，尚未上传任何数据。\n",
		fs.Arg(0), cloudsync.TaskScopeNote(cloudsync.Selection{Tasks: refs}))
	return 0
}

func cloudUnpin(args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "用法: pondera cloud unpin <session>")
		return 2
	}
	store, _, err := cloudOpenStore()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cloud:", err)
		return 1
	}
	if err := store.Unpin(args[0]); err != nil {
		fmt.Fprintln(os.Stderr, "cloud:", err)
		return 1
	}
	fmt.Printf("会话 %q 已回到纯本地。云端副本不会被自动删除。\n", args[0])
	return 0
}

func cloudPack(args []string) int {
	fs := pflag.NewFlagSet("cloud pack", pflag.ContinueOnError)
	workspaceDir := fs.String("workspace-dir", "", "工作区目录")
	sessionDir := fs.String("session-dir", "", "会话数据目录")
	outDir := fs.String("out", "", "输出目录")
	cloudHostID := fs.String("cloud-host-id", "cloud-host", "云端主机标识")
	cloudSessionID := fs.String("cloud-session-id", "", "云端会话 ID")
	boundary := fs.Uint64("boundary-sequence", 0, "持久会话序号")
	legacyQuiesced := fs.Bool("confirm-quiesced", false, "已弃用：等价于 --quiesce force")
	quiesceMode := fs.String("quiesce", "drain", "check|drain|force")
	serveURL := fs.String("serve-url", "", "本地 serve 地址")
	token := fs.String("token", "", "serve token")
	sessionRoot := fs.String("session-root", "", "sessions-v4 根目录")
	noSlice := fs.Bool("no-slice", false, "禁用任务级裁剪")
	checkpoints := fs.StringArray("checkpoint", nil, "额外检查点目录，可重复")
	if code, ok := parseCommandFlags(fs, args); !ok {
		return code
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "用法: pondera cloud pack <session> --workspace-dir <dir> --confirm-quiesced")
		return 2
	}
	sessionID := fs.Arg(0)
	mode := *quiesceMode
	if *legacyQuiesced {
		mode = "force"
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
	qdir := *sessionDir
	if qdir == "" {
		if found, ferr := cloudsync.DiscoverSessionDir(home, sessionID); ferr == nil {
			qdir = found
		}
	}
	qopts, qerr := cloudQuiesceOptions(mode, *serveURL, *token, sessionID, qdir)
	if qerr != nil {
		fmt.Fprintln(os.Stderr, "cloud:", qerr)
		return 2
	}
	report, qerr := cloudsync.CheckQuiesced(context.Background(), *qopts)
	if qerr != nil {
		fmt.Fprintln(os.Stderr, "cloud: 会话未确认空闲:", qerr)
		return 1
	}
	sliceEnabled := !*noSlice
	planner := &cloudsync.Planner{Store: store, Home: home}
	plan, err := planner.PrepareUpload(cloudsync.PrepareUploadOptions{
		SessionID:        sessionID,
		WorkspaceDir:     *workspaceDir,
		SessionDir:       *sessionDir,
		Checkpoints:      *checkpoints,
		OutDir:           out,
		CloudHostID:      *cloudHostID,
		CloudSessionID:   csid,
		BoundarySequence: *boundary,
		SessionRoot:      *sessionRoot,
		SliceTasks:       &sliceEnabled,
	}, true)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cloud:", err)
		return 1
	}
	fmt.Printf("TaskBundle 已生成（尚未上传，也尚未移交所有权）:\n")
	fmt.Printf("  handoff-id : %s\n", plan.HandoffID)
	fmt.Printf("  bundle     : %s\n", plan.BundlePath)
	fmt.Printf("  文件数     : %d\n", len(plan.Manifest.Files))
	fmt.Printf("  digest     : %s\n", plan.Manifest.Digest)
	fmt.Printf("  工作区策略 : %s\n", plan.Manifest.Workspace.Strategy)
	fmt.Printf("  任务范围   : %s\n", cloudsync.WindowNote(plan.TaskWindow))
	fmt.Printf("  空闲检测   : %s (%s)\n", report.Source, strings.Join(report.Checks, "; "))
	fmt.Printf("\n下一步（推荐一键）：\n")
	fmt.Printf("  pondera cloud push %s --to <cloud-host> --remote-dir /srv/pondera/incoming\n", sessionID)
	fmt.Printf("手工搬运仍可用：scp %s <cloud>:~/incoming/ 后执行 cloud apply / cloud confirm\n", plan.BundlePath)
	return 0
}

func cloudApply(args []string) int {
	fs := pflag.NewFlagSet("cloud apply", pflag.ContinueOnError)
	dest := fs.String("dest", "", "解包暂存目录")
	workspaceDest := fs.String("workspace-dest", "", "git 工作区还原目录")
	owner := fs.String("owner", "cloud", "导入后的写者 cloud|local")
	pin := fs.Bool("pin", false, "首次导入时记录选择")
	haveSeq := fs.Uint64("have-seq", 0, "本机已有会话序号")
	sessionRoot := fs.String("session-root", "", "解包后安装会话的 sessions-v4 根")
	forceSession := fs.Bool("force-session", false, "覆盖已存在的同名会话")
	if code, ok := parseCommandFlags(fs, args); !ok {
		return code
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "用法: pondera cloud apply <bundle> --owner cloud|local [--pin]")
		return 2
	}
	store, home, err := cloudOpenStore()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cloud:", err)
		return 1
	}
	extractRoot := *dest
	if extractRoot == "" {
		extractRoot = filepath.Join(home, "cloud", "incoming")
	}
	newOwner := cloudsync.Owner(*owner)
	planner := &cloudsync.Planner{Store: store, Home: home}
	res, err := planner.ApplyIncoming(cloudsync.ApplyOptions{
		BundlePath:    fs.Arg(0),
		ExtractRoot:   extractRoot,
		WorkspaceDest: *workspaceDest,
		NewOwner:      newOwner,
		PinOnApply:    *pin,
		SessionRoot:   *sessionRoot,
		ForceSession:  *forceSession,
		DestinationVector: cloudsync.VersionVector{
			HostID: store.LocalHostID(), BoundarySequence: *haveSeq,
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "cloud: 导入失败（本地状态未改变）:", err)
		return 1
	}
	fmt.Printf("导入成功:\n  handoff-id : %s\n  解包目录   : %s\n  会话       : %s\n  写者       : %s\n",
		res.HandoffID, res.ExtractDir, res.Manifest.Selection.SessionID, newOwner)
	if res.Manifest.Workspace.IsGit && *workspaceDest != "" {
		fmt.Printf("  工作区     : %s (git bundle 已还原)\n", *workspaceDest)
	}
	if res.InstalledSessionDir != "" {
		fmt.Printf("  会话安装   : %s\n", res.InstalledSessionDir)
	}
	return 0
}

func cloudConfirm(args []string) int {
	fs := pflag.NewFlagSet("cloud confirm", pflag.ContinueOnError)
	bundle := fs.String("bundle", "", "已上传的 bundle 路径")
	cloudHostID := fs.String("cloud-host-id", "cloud-host", "云端主机标识")
	cloudSessionID := fs.String("cloud-session-id", "", "云端会话 ID")
	failed := fs.Bool("failed", false, "标记导入失败")
	if code, ok := parseCommandFlags(fs, args); !ok {
		return code
	}
	if fs.NArg() != 1 || *bundle == "" {
		fmt.Fprintln(os.Stderr, "用法: pondera cloud confirm <session> --bundle <path> [--cloud-session-id ...]")
		return 2
	}
	sessionID := fs.Arg(0)
	opened, err := cloudsync.OpenBundle(*bundle)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cloud:", err)
		return 1
	}
	csid := *cloudSessionID
	if csid == "" {
		csid = "cloud-" + sessionID
	}
	store, home, err := cloudOpenStore()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cloud:", err)
		return 1
	}
	planner := &cloudsync.Planner{Store: store, Home: home}
	plan := &cloudsync.UploadPlan{
		HandoffID:  opened.Manifest.HandoffID,
		BundlePath: *bundle,
		Manifest:   opened.Manifest,
		Selection:  opened.Manifest.Selection,
	}
	ok := !*failed
	detail := ""
	if !ok {
		detail = "destination import reported as failed"
	}
	if err := planner.CommitUpload(plan, *cloudHostID, csid, ok, detail); err != nil {
		fmt.Fprintln(os.Stderr, "cloud:", err)
		return 1
	}
	if ok {
		fmt.Printf("所有权已移交给云端 (%s:%s)。本地现为只读旁观；取回请在云端 pack、本地 apply --owner local。\n",
			*cloudHostID, csid)
	} else {
		fmt.Println("已记录失败；本地仍持有写者权，bundle 保留可重试。")
	}
	return 0
}

func cloudMappings() int {
	store, _, err := cloudOpenStore()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cloud:", err)
		return 1
	}
	ms := store.Mappings()
	if len(ms) == 0 {
		fmt.Println("尚无本地<->云端身份映射。")
		return 0
	}
	fmt.Printf("%-20s %-20s %-20s %-20s\n", "LOCAL HOST", "LOCAL SESSION", "CLOUD HOST", "CLOUD SESSION")
	for _, m := range ms {
		fmt.Printf("%-20s %-20s %-20s %-20s\n",
			m.LocalHostID, m.LocalSessionID, m.CloudHostID, m.CloudSessionID)
	}
	return 0
}
