// Package assist is the facade behind the desktop assistant RPC. It owns the
// plan cache, the model client, diagnostics and the writer transaction.
package assist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"pondera/internal/assist/diagnose"
	"pondera/internal/assist/engine"
	"pondera/internal/assist/patch"
	"pondera/internal/assist/registry"
	"pondera/internal/assist/store"
	"pondera/internal/assist/writer"
	"pondera/internal/config"
	"pondera/internal/fileutil"
)

// PlanTTL bounds how long a previewed plan stays applicable.
const PlanTTL = 30 * time.Minute

// Assistant is the assistant application service.
type Assistant struct {
	Store  *store.Store
	Writer *writer.Writer
	// Client overrides the provider client (tests inject a fake).
	Client engine.Client
	Now    func() time.Time
	// OnApplied, when set, runs after a successful ApplyPlan. It carries only
	// revision/hash metadata and redacted labels; never a credential value.
	OnApplied func(AppliedEvent)

	mu    sync.Mutex
	plans map[string]cachedPlan
}

type cachedPlan struct {
	plan    *patch.Plan
	expires time.Time
}

// New returns an assistant bound to the user config and credential store.
func New() *Assistant {
	return &Assistant{Store: store.New(), Writer: writer.New(), plans: map[string]cachedPlan{}}
}

func (a *Assistant) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *Assistant) store() *store.Store {
	if a.Store != nil {
		return a.Store
	}
	a.Store = store.New()
	return a.Store
}

func (a *Assistant) writer() *writer.Writer {
	if a.Writer != nil {
		return a.Writer
	}
	a.Writer = writer.New()
	return a.Writer
}

// StatusView is the onboarding/status payload. It never contains a key.
type StatusView struct {
	Configured bool   `json:"configured"`
	Endpoint   string `json:"endpoint"`
	Model      string `json:"model"`
	Kind       string `json:"kind"`
	KeySet     bool   `json:"key_set"`
	ConfigPath string `json:"config_path"`
	HomeDir    string `json:"home_dir"`
	Note       string `json:"note"`
}

// Status reports whether the assistant has its own endpoint configured.
func (a *Assistant) Status() (StatusView, error) {
	cfg, err := a.store().Load()
	if err != nil {
		return StatusView{}, err
	}
	return StatusView{
		Configured: strings.TrimSpace(cfg.Endpoint) != "" && strings.TrimSpace(cfg.Model) != "" && store.APIKeySet(cfg),
		Endpoint:   cfg.Endpoint, Model: cfg.Model, Kind: cfg.Kind,
		KeySet:     store.APIKeySet(cfg),
		ConfigPath: config.UserConfigPath(), HomeDir: a.store().Dir,
		Note: "助手设置独立存放于 %AppData%\\pondera\\assistant，不并入主配置。",
	}, nil
}

// SaveSettings persists the assistant's own endpoint/model and, when key is
// non-empty, its API key through the credential store.
func (a *Assistant) SaveSettings(endpoint, key, model, kind string) (StatusView, error) {
	cfg := store.Config{Endpoint: endpoint, Kind: kind, Model: model, CredentialKey: store.CredentialKey}
	if strings.TrimSpace(key) != "" {
		if err := store.SetAPIKey(cfg, key); err != nil {
			return StatusView{}, err
		}
	}
	if err := a.store().Save(cfg); err != nil {
		return StatusView{}, err
	}
	return a.Status()
}

// ChatView is one assistant turn.
type ChatView struct {
	Reply     string           `json:"reply"`
	Rejection *patch.Rejection `json:"rejection,omitempty"`
	Plan      *PlanView        `json:"plan,omitempty"`
}

// PlanView is the read-only preview the renderer shows. Secret values are
// absent; only the plan id can be applied.
type PlanView struct {
	ID                   string            `json:"id"`
	Intent               string            `json:"intent"`
	Reply                string            `json:"reply"`
	Diff                 []patch.DiffEntry `json:"diff"`
	RequiresConfirmation bool              `json:"requires_confirmation"`
	CommentWarning       string            `json:"comment_warning"`
	OpCount              int               `json:"op_count"`
	ExpiresAt            string            `json:"expires_at"`
}

func viewOf(plan *patch.Plan, expires time.Time) *PlanView {
	return &PlanView{
		ID: plan.ID, Intent: plan.Intent, Reply: plan.Reply, Diff: plan.Diff,
		RequiresConfirmation: plan.RequiresConfirmation, CommentWarning: plan.CommentWarning,
		OpCount: len(plan.Ops), ExpiresAt: expires.UTC().Format(time.RFC3339),
	}
}

// Chat sends one redacted question to the user's endpoint and returns either
// an explanation or a previewed plan.
func (a *Assistant) Chat(ctx context.Context, message string) (ChatView, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return ChatView{}, fmt.Errorf("问题不能为空")
	}
	cfg, err := loadUserConfig()
	if err != nil {
		return ChatView{}, fmt.Errorf("读取配置失败: %w", err)
	}
	client, err := a.client()
	if err != nil {
		return ChatView{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	raw, err := client.Complete(ctx, engine.SystemPrompt(), engine.UserPrompt(registry.SnapshotOf(cfg), engine.Redact(message, cfg)))
	if err != nil {
		return ChatView{}, fmt.Errorf("助手请求失败: %w", err)
	}
	env, err := patch.Parse(stripCodeFence(raw))
	if err != nil {
		var rejection *patch.Rejection
		if errors.As(err, &rejection) {
			return ChatView{Reply: "模型输出不符合受控协议，已拒绝。", Rejection: rejection}, nil
		}
		return ChatView{}, err
	}
	ops, err := patch.Validate(env, cfg)
	if err != nil {
		var rejection *patch.Rejection
		if errors.As(err, &rejection) {
			return ChatView{Reply: env.Reply, Rejection: rejection}, nil
		}
		return ChatView{}, err
	}
	if len(ops) == 0 {
		return ChatView{Reply: env.Reply}, nil
	}
	baseHash, revision, err := a.writer().CurrentBaseHash()
	if err != nil {
		return ChatView{}, err
	}
	id, err := patch.NewPlanID()
	if err != nil {
		return ChatView{}, err
	}
	plan := patch.BuildPlan(id, env, ops, baseHash, revision)
	expires := a.now().Add(PlanTTL)
	a.cachePlan(plan, expires)
	return ChatView{Reply: env.Reply, Plan: viewOf(plan, expires)}, nil
}

func (a *Assistant) cachePlan(plan *patch.Plan, expires time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.plans == nil {
		a.plans = map[string]cachedPlan{}
	}
	for id, cached := range a.plans {
		if a.now().After(cached.expires) {
			delete(a.plans, id)
		}
	}
	a.plans[plan.ID] = cachedPlan{plan: plan, expires: expires}
}

func (a *Assistant) takePlan(id string) (cachedPlan, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	cached, ok := a.plans[id]
	if !ok {
		return cachedPlan{}, fmt.Errorf("plan_expired: 预览已过期或宿主已重启，请重新提问")
	}
	if a.now().After(cached.expires) {
		delete(a.plans, id)
		return cachedPlan{}, fmt.Errorf("plan_expired: 预览已过期，请重新提问")
	}
	return cached, nil
}

// ChangeSummary describes one changed field without its value. Sensitive rows
// are identified only by their label and a redaction flag.
type ChangeSummary struct {
	Label     string `json:"label"`
	Risk      string `json:"risk"`
	Sensitive bool   `json:"sensitive"`
}

// AppliedEvent is the post-commit broadcast payload. It contains revision hashes
// and redacted labels only; secret values are never present.
type AppliedEvent struct {
	Revision           string          `json:"revision"`
	CredentialRevision string          `json:"credential_revision"`
	BackupID           string          `json:"backup_id"`
	Summary            []ChangeSummary `json:"summary"`
}

// ApplyView reports a committed change.
type ApplyView struct {
	BackupID           string `json:"backup_id"`
	BackupDir          string `json:"backup_dir"`
	AppliedOps         int    `json:"applied_ops"`
	Message            string `json:"message"`
	Revision           string `json:"revision"`
	CredentialRevision string `json:"credential_revision"`
}

// ApplyPlan commits one cached plan through the writer transaction.
func (a *Assistant) ApplyPlan(ctx context.Context, planID string) (ApplyView, error) {
	cached, err := a.takePlan(planID)
	if err != nil {
		return ApplyView{}, err
	}
	result, err := a.writer().Apply(ctx, cached.plan)
	if err != nil {
		return ApplyView{}, err
	}
	a.mu.Lock()
	delete(a.plans, planID)
	a.mu.Unlock()
	record := HistoryRecord{
		ID: result.Backup.ID, Kind: "applied", CreatedAt: a.now().UTC().Format(time.RFC3339),
		Intent: cached.plan.Intent, Diff: cached.plan.Diff, BackupID: result.Backup.ID,
		BackupDir: result.Backup.Dir, Status: "ok",
	}
	_ = a.appendHistory(record)
	if a.OnApplied != nil {
		a.OnApplied(AppliedEvent{
			Revision:           result.Revision,
			CredentialRevision: result.CredentialRevision,
			BackupID:           result.Backup.ID,
			Summary:            summarizeChanges(cached.plan.Diff),
		})
	}
	return ApplyView{
		BackupID: result.Backup.ID, BackupDir: result.Backup.Dir, AppliedOps: result.AppliedOps,
		Message: "配置已保存", Revision: result.Revision, CredentialRevision: result.CredentialRevision,
	}, nil
}

// summarizeChanges keeps labels and risk only, so the broadcast can never
// leak a before/after value even for non-secret fields.
func summarizeChanges(diff []patch.DiffEntry) []ChangeSummary {
	out := make([]ChangeSummary, 0, len(diff))
	for _, row := range diff {
		out = append(out, ChangeSummary{Label: row.Label, Risk: row.Risk, Sensitive: row.Sensitive})
	}
	return out
}

// DiagnoseView wraps the diagnostics report.
type DiagnoseView struct {
	Report diagnose.Report `json:"report"`
}

// Diagnose runs the read-only checks for the given workspace root.
func (a *Assistant) Diagnose(root string) DiagnoseView {
	return DiagnoseView{Report: diagnose.Run(root)}
}

// ConnectivityView is the result of one bounded probe.
type ConnectivityView struct {
	Result diagnose.ConnectivityResult `json:"result"`
}

// TestConnectivity probes a provider/model; a draft key stays request-local.
func (a *Assistant) TestConnectivity(ctx context.Context, root, provider, model, draftKey string) ConnectivityView {
	return ConnectivityView{Result: diagnose.Connectivity(ctx, root, provider, model, draftKey)}
}

// TestEndpoint probes the assistant's own unsaved endpoint. The draft key never
// leaves this call; when it is empty the saved assistant key is reused.
func (a *Assistant) TestEndpoint(ctx context.Context, endpoint, kind, model, draftKey string) ConnectivityView {
	key := strings.TrimSpace(draftKey)
	if key == "" {
		if cfg, err := a.store().Load(); err == nil {
			if saved, keyErr := store.GetAPIKey(cfg); keyErr == nil {
				key = saved
			}
		}
	}
	return ConnectivityView{Result: diagnose.ConnectivityEndpoint(ctx, endpoint, kind, model, key)}
}

// HistoryRecord is one applied or rolled-back change.
type HistoryRecord struct {
	ID        string            `json:"id"`
	Kind      string            `json:"kind"`
	CreatedAt string            `json:"created_at"`
	Intent    string            `json:"intent"`
	Diff      []patch.DiffEntry `json:"diff"`
	BackupID  string            `json:"backup_id"`
	BackupDir string            `json:"backup_dir"`
	Status    string            `json:"status"`
}

// History returns recent change records, newest first.
func (a *Assistant) History() ([]HistoryRecord, error) {
	path := a.historyPath()
	if path == "" {
		return nil, fmt.Errorf("assistant home unavailable")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []HistoryRecord{}, nil
		}
		return nil, err
	}
	var records []HistoryRecord
	if err := json.Unmarshal(raw, &records); err != nil {
		return nil, fmt.Errorf("历史记录损坏: %w", err)
	}
	if records == nil {
		records = []HistoryRecord{}
	}
	return records, nil
}

// RollbackView reports a restored backup.
type RollbackView struct {
	BackupID string `json:"backup_id"`
	Message  string `json:"message"`
}

// Rollback restores one backup and records it in the history.
func (a *Assistant) Rollback(backupID string) (RollbackView, error) {
	ref, err := a.writer().Rollback(backupID)
	if err != nil {
		return RollbackView{}, err
	}
	record := HistoryRecord{
		ID: ref.ID + "-rollback", Kind: "rolled_back", CreatedAt: a.now().UTC().Format(time.RFC3339),
		Intent: "还原备份 " + ref.ID, BackupID: ref.ID, BackupDir: ref.Dir, Status: "ok",
	}
	_ = a.appendHistory(record)
	revision, credentialRevision, hashErr := a.writer().CurrentBaseHash()
	if hashErr != nil {
		revision, credentialRevision = "", ""
	}
	if a.OnApplied != nil {
		a.OnApplied(AppliedEvent{
			Revision:           revision,
			CredentialRevision: credentialRevision,
			BackupID:           ref.ID,
			Summary:            []ChangeSummary{{Label: "rollback:" + ref.ID, Risk: "confirm"}},
		})
	}
	return RollbackView{BackupID: ref.ID, Message: "已还原到备份状态"}, nil
}

func (a *Assistant) historyPath() string {
	dir := a.store().Dir
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "history.json")
}

func (a *Assistant) appendHistory(record HistoryRecord) error {
	records, err := a.History()
	if err != nil {
		records = []HistoryRecord{}
	}
	records = append([]HistoryRecord{record}, records...)
	if len(records) > 50 {
		records = records[:50]
	}
	path := a.historyPath()
	if path == "" {
		return fmt.Errorf("assistant home unavailable")
	}
	raw, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	return fileutil.AtomicWriteFile(path, append(raw, '\n'), 0o600)
}

func (a *Assistant) client() (engine.Client, error) {
	if a.Client != nil {
		return a.Client, nil
	}
	cfg, err := a.store().Load()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.Endpoint) == "" || strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("助手尚未配置：请先填写 endpoint + key + model")
	}
	key, err := store.GetAPIKey(cfg)
	if err != nil {
		return nil, fmt.Errorf("助手尚未配置：请先填写 endpoint + key + model")
	}
	return &engine.ProviderClient{Endpoint: cfg.Endpoint, Kind: cfg.Kind, Model: cfg.Model, APIKey: key}, nil
}

func loadUserConfig() (*config.Config, error) {
	path := config.UserConfigPath()
	if path == "" {
		return nil, fmt.Errorf("user config path unavailable")
	}
	return config.LoadForEditWithoutCredentialsReadOnlyStrict(path)
}

// stripCodeFence tolerates models that wrap the envelope in a markdown fence.
// Nothing else is stripped: any prose around the JSON still fails parsing.
func stripCodeFence(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "```") {
		trimmed = strings.TrimPrefix(trimmed, "```")
		if idx := strings.IndexByte(trimmed, '\n'); idx >= 0 {
			trimmed = trimmed[idx+1:]
		}
		trimmed = strings.TrimSuffix(strings.TrimSpace(trimmed), "```")
	}
	return strings.TrimSpace(trimmed)
}
