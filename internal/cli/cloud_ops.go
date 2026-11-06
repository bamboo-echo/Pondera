package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/pflag"

	"pondera/internal/cloudsync"
	"pondera/internal/costgate"
)

// cloudWatch tails a cloud serve's event stream and (optionally) relays each
// event as a sealed mirror frame into a local serve, so the workstation can
// watch a cloud-owned session without being its writer.
func cloudWatch(args []string) int {
	fs := pflag.NewFlagSet("cloud watch", pflag.ContinueOnError)
	baseURL := fs.String("url", "", "云端 serve 地址，例如 https://cloud:8787")
	token := fs.String("token", "", "云端 serve token")
	sessionID := fs.String("session", "", "会话 ID")
	relay := fs.String("relay", "", "本地 serve 地址；把帧转发到 /mirror/publish")
	relayToken := fs.String("relay-token", "", "本地 serve token")
	from := fs.Uint64("from", 0, "从该序号之后开始补帧")
	maxFrames := fs.Int("max-frames", 0, "只读取 N 帧后退出（0 = 持续）")
	heartbeat := fs.Duration("heartbeat", cloudsync.DefaultMirrorHeartbeat, "跨机心跳窗口")
	if code, ok := parseCommandFlags(fs, args); !ok {
		return code
	}
	if *baseURL == "" {
		fmt.Fprintln(os.Stderr, "cloud: watch 需要 --url <云端 serve 地址>")
		return 2
	}
	sid := *sessionID
	if sid == "" && fs.NArg() > 0 {
		sid = fs.Arg(0)
	}
	if sid == "" {
		sid = "cloud-session"
	}
	store, home, err := cloudOpenStore()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cloud:", err)
		return 1
	}
	seqPath := filepath.Join(home, "cloud", "mirror-seq-"+sanitizeFilePart(sid)+".json")
	writer := cloudsync.NewMirrorStream(sid, store.LocalHostID(), *heartbeat)
	if start := loadMirrorSequence(seqPath, *from); start > 1 {
		writer.SetExpectedSequence(start)
	}
	client := &http.Client{}
	count := 0
	backoff := time.Second
	for {
		n, err := watchOnce(client, *baseURL, *token, sid, *from, *relay, *relayToken, writer, seqPath, *maxFrames-count)
		count += n
		if err != nil {
			if errors.Is(err, errWatchDone) {
				break
			}
			fmt.Fprintf(os.Stderr, "cloud: watch 断线（%v），%s 后重连…\n", err, backoff)
		}
		if *maxFrames > 0 && count >= *maxFrames {
			break
		}
		select {
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
	fmt.Printf("\n共转发 %d 帧。会话 %s 的镜像序号已持久化到 %s\n", count, sid, seqPath)
	return 0
}

var errWatchDone = errors.New("watch complete")

func sanitizeFilePart(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() > 64 {
		return b.String()[:64]
	}
	return b.String()
}

func loadMirrorSequence(path string, from uint64) uint64 {
	if from > 0 {
		return from + 1
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 1
	}
	var st struct {
		Next uint64 `json:"next"`
	}
	if json.Unmarshal(data, &st) == nil && st.Next > 1 {
		return st.Next
	}
	return 1
}

func saveMirrorSequence(path string, next uint64) {
	data, _ := json.Marshal(map[string]any{"next": next, "updatedAt": time.Now().UTC()})
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, data, 0o600)
}

func watchOnce(client *http.Client, baseURL, token, sessionID string, from uint64,
	relay, relayToken string, writer *cloudsync.MirrorStream, seqPath string, limit int) (int, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/") + "/events")
	if err != nil {
		return 0, err
	}
	q := u.Query()
	if token != "" {
		q.Set("token", token)
	}
	if from > 0 {
		q.Set("since", strconv.FormatUint(from, 10))
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("events returned %d", resp.StatusCode)
	}
	reader := bufio.NewReader(resp.Body)
	count := 0
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			if readErr == io.EOF && count > 0 {
				return count, nil
			}
			return count, readErr
		}
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		raw := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if raw == "" || raw == "[DONE]" {
			continue
		}
		var payload map[string]any
		if json.Unmarshal([]byte(raw), &payload) != nil {
			continue
		}
		kind, _ := payload["kind"].(string)
		if kind == "" {
			kind = "event"
		}
		frame := writer.NewFrame(kind, json.RawMessage(raw), "")
		fmt.Printf("[%d] %-22s %s\n", frame.Sequence, kind, truncate(raw, 110))
		saveMirrorSequence(seqPath, writer.ExpectedSequence())
		if relay != "" {
			if err := publishMirrorFrame(client, relay, relayToken, frame); err != nil {
				fmt.Fprintf(os.Stderr, "cloud: 转发失败: %v\n", err)
			}
		}
		count++
		if limit > 0 && count >= limit {
			return count, errWatchDone
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func publishMirrorFrame(client *http.Client, relay, token string, f cloudsync.MirrorFrame) error {
	u, err := url.Parse(strings.TrimRight(relay, "/") + "/mirror/publish")
	if err != nil {
		return err
	}
	q := u.Query()
	if token != "" {
		q.Set("token", token)
	}
	u.RawQuery = q.Encode()
	body, err := json.Marshal(f)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("relay %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return nil
}

// cloudCost exposes the shared cost gate ledger from the CLI.
func cloudCost(args []string) int {
	if len(args) == 0 {
		args = []string{"status"}
	}
	sub := args[0]
	rest := args[1:]
	_, home, err := cloudOpenStore()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cloud:", err)
		return 1
	}
	l, err := costgate.Open(filepath.Join(home, "cloud", "cost-ledger.json"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "cloud:", err)
		return 1
	}
	fs := pflag.NewFlagSet("cloud cost "+sub, pflag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "输出 JSON")
	enabled := fs.Bool("enabled", true, "")
	sessionUSD := fs.Float64("session-usd", 0, "")
	dayUSD := fs.Float64("day-usd", 0, "")
	taskUSD := fs.Float64("task-usd", 0, "")
	jobs := fs.Int("jobs", 0, "")
	turnSeconds := fs.Int("turn-seconds", 0, "")
	sessionID := fs.String("session", "", "")
	taskID := fs.String("task", "", "")
	model := fs.String("model", "", "")
	tokensIn := fs.Int64("tokens-in", 0, "")
	tokensOut := fs.Int64("tokens-out", 0, "")
	usd := fs.Float64("usd", 0, "")
	active := fs.Int("active", 0, "")
	if code, ok := parseCommandFlags(fs, rest); !ok {
		return code
	}
	dump := func(v any) {
		if *jsonOut {
			_ = json.NewEncoder(os.Stdout).Encode(v)
			return
		}
		b, _ := json.MarshalIndent(v, "", "  ")
		fmt.Println(string(b))
	}
	switch sub {
	case "status":
		dump(l.Status())
	case "check":
		sid := *sessionID
		if sid == "" && fs.NArg() > 0 {
			sid = fs.Arg(0)
		}
		d, err := l.Check(sid, *taskID, *turnSeconds)
		if err != nil {
			fmt.Fprintln(os.Stderr, "cloud:", err)
			return 1
		}
		dump(d)
		if !d.Allowed {
			return 3
		}
	case "set":
		lim := l.Limits()
		lim.Enabled = *enabled
		if fs.Changed("session-usd") {
			lim.MaxUSDPerSession = *sessionUSD
		}
		if fs.Changed("day-usd") {
			lim.MaxUSDPerDay = *dayUSD
		}
		if fs.Changed("task-usd") {
			lim.MaxUSDPerTask = *taskUSD
		}
		if fs.Changed("jobs") {
			lim.MaxConcurrentJobs = *jobs
		}
		if fs.Changed("turn-seconds") {
			lim.MaxTurnSeconds = *turnSeconds
		}
		if err := l.SetLimits(lim); err != nil {
			fmt.Fprintln(os.Stderr, "cloud:", err)
			return 1
		}
		dump(l.Limits())
	case "record":
		sid := *sessionID
		if sid == "" && fs.NArg() > 0 {
			sid = fs.Arg(0)
		}
		if sid == "" {
			fmt.Fprintln(os.Stderr, "cloud: cost record 需要 --session")
			return 2
		}
		if err := l.Record(costgate.Entry{
			SessionID: sid, TaskID: *taskID, Model: *model,
			TokensIn: *tokensIn, TokensOut: *tokensOut, USD: *usd,
		}); err != nil {
			fmt.Fprintln(os.Stderr, "cloud:", err)
			return 1
		}
		dump(l.Status())
	case "active":
		l.SetActiveJobs(*active)
		dump(map[string]any{"ok": true, "active": *active})
	case "reset":
		if err := l.Reset(); err != nil {
			fmt.Fprintln(os.Stderr, "cloud:", err)
			return 1
		}
		dump(map[string]any{"ok": true, "note": "花费记录已清空，限额保留"})
	default:
		fmt.Fprintf(os.Stderr, "cloud cost: 未知子命令 %q（status|check|set|record|active|reset）\n", sub)
		return 2
	}
	return 0
}
