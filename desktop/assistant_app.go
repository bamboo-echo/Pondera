package main

import (
	"sync"

	"pondera/internal/assist"
	"pondera/internal/assist/patch"
)

// assistantMu guards creation of each App's own assistant service.
var (
	assistantInstances sync.Map // map[*App]*assist.Assistant
)

const assistantConfigChangedEvent = "assistant:config-changed"

// assistantConfigChangedPayload carries hashes and redacted labels only. No
// credential value or config value is included.
type assistantConfigChangedPayload struct {
	Revision           string                 `json:"revision"`
	CredentialRevision string                 `json:"credential_revision"`
	BackupID           string                 `json:"backup_id"`
	Summary            []assist.ChangeSummary `json:"summary"`
}

// assistant returns this App's assistant service and binds its post-commit
// outlet to that App's runtime bridge. Each App owns its own instance so two
// Apps in one process (tests, embedded hosts) never share a broadcast target.
func (a *App) assistant() *assist.Assistant {
	if cached, ok := assistantInstances.Load(a); ok {
		return cached.(*assist.Assistant)
	}
	service := assist.New()
	service.OnApplied = func(event assist.AppliedEvent) {
		ctx := a.ctx
		if ctx == nil {
			ctx = a.reqCtx()
		}
		a.runtimeEvents.Emit(ctx, assistantConfigChangedEvent, assistantConfigChangedPayload{
			Revision:           event.Revision,
			CredentialRevision: event.CredentialRevision,
			BackupID:           event.BackupID,
			Summary:            event.Summary,
		})
	}
	actual, _ := assistantInstances.LoadOrStore(a, service)
	return actual.(*assist.Assistant)
}

// AssistantStatusView is the onboarding payload. It never contains a key.
type AssistantStatusView struct {
	Configured bool   `json:"configured"`
	Endpoint   string `json:"endpoint"`
	Model      string `json:"model"`
	Kind       string `json:"kind"`
	KeySet     bool   `json:"key_set"`
	ConfigPath string `json:"config_path"`
	HomeDir    string `json:"home_dir"`
	Note       string `json:"note"`
}

// AssistantRejection explains why a model patch was refused.
type AssistantRejection struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// AssistantPlanRow is one redacted diff row.
type AssistantPlanRow struct {
	Label     string `json:"label"`
	Before    string `json:"before"`
	After     string `json:"after"`
	Sensitive bool   `json:"sensitive"`
	Risk      string `json:"risk"`
}

// AssistantPlanView is the preview card shown before an explicit confirmation.
type AssistantPlanView struct {
	ID                   string             `json:"id"`
	Intent               string             `json:"intent"`
	Reply                string             `json:"reply"`
	Diff                 []AssistantPlanRow `json:"diff"`
	RequiresConfirmation bool               `json:"requires_confirmation"`
	CommentWarning       string             `json:"comment_warning"`
	OpCount              int                `json:"op_count"`
	ExpiresAt            string             `json:"expires_at"`
}

// AssistantChatResult is one assistant turn.
type AssistantChatResult struct {
	Reply     string              `json:"reply"`
	Rejection *AssistantRejection `json:"rejection,omitempty"`
	Plan      *AssistantPlanView  `json:"plan,omitempty"`
}

// AssistantApplyResult reports a committed change.
type AssistantApplyResult struct {
	BackupID           string `json:"backup_id"`
	BackupDir          string `json:"backup_dir"`
	AppliedOps         int    `json:"applied_ops"`
	Message            string `json:"message"`
	Revision           string `json:"revision"`
	CredentialRevision string `json:"credential_revision"`
}

// AssistantFinding is one diagnostic result.
type AssistantFinding struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	Provider string `json:"provider,omitempty"`
	Fix      string `json:"fix,omitempty"`
}

// AssistantDiagnoseResult wraps the diagnostics report.
type AssistantDiagnoseResult struct {
	Findings []AssistantFinding `json:"findings"`
	Checked  int                `json:"checked"`
}

// AssistantConnectivity is one probe outcome.
type AssistantConnectivity struct {
	OK        bool   `json:"ok"`
	Category  string `json:"category"`
	Message   string `json:"message"`
	LatencyMS int64  `json:"latency_ms"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
}

// AssistantHistoryRecord is one applied or rolled-back change.
type AssistantHistoryRecord struct {
	ID        string             `json:"id"`
	Kind      string             `json:"kind"`
	CreatedAt string             `json:"created_at"`
	Intent    string             `json:"intent"`
	Diff      []AssistantPlanRow `json:"diff"`
	BackupID  string             `json:"backup_id"`
	BackupDir string             `json:"backup_dir"`
	Status    string             `json:"status"`
}

// AssistantRollbackResult reports a restored backup.
type AssistantRollbackResult struct {
	BackupID string `json:"backup_id"`
	Message  string `json:"message"`
}

// AssistantStatus reports whether the assistant has its own endpoint configured.
func (a *App) AssistantStatus() (AssistantStatusView, error) {
	status, err := a.assistant().Status()
	if err != nil {
		return AssistantStatusView{}, err
	}
	return AssistantStatusView(status), nil
}

// AssistantSaveSettings stores the assistant's own endpoint/model/key.
func (a *App) AssistantSaveSettings(endpoint, key, model, kind string) (AssistantStatusView, error) {
	status, err := a.assistant().SaveSettings(endpoint, key, model, kind)
	if err != nil {
		return AssistantStatusView{}, err
	}
	return AssistantStatusView(status), nil
}

// AssistantChat asks the configured model for an explanation or a patch plan.
func (a *App) AssistantChat(message string) (AssistantChatResult, error) {
	view, err := a.assistant().Chat(a.reqCtx(), message)
	if err != nil {
		return AssistantChatResult{}, err
	}
	result := AssistantChatResult{Reply: view.Reply}
	if view.Rejection != nil {
		result.Rejection = &AssistantRejection{Reason: view.Rejection.Reason, Message: view.Rejection.Message}
	}
	if view.Plan != nil {
		result.Plan = &AssistantPlanView{
			ID: view.Plan.ID, Intent: view.Plan.Intent, Reply: view.Plan.Reply,
			Diff: rowsOf(view.Plan.Diff), RequiresConfirmation: view.Plan.RequiresConfirmation,
			CommentWarning: view.Plan.CommentWarning, OpCount: view.Plan.OpCount, ExpiresAt: view.Plan.ExpiresAt,
		}
	}
	return result, nil
}

// AssistantApplyPlan commits a previewed plan after explicit confirmation.
func (a *App) AssistantApplyPlan(planID string) (AssistantApplyResult, error) {
	view, err := a.assistant().ApplyPlan(a.reqCtx(), planID)
	if err != nil {
		return AssistantApplyResult{}, err
	}
	return AssistantApplyResult(view), nil
}

// AssistantDiagnose runs the read-only configuration checks.
func (a *App) AssistantDiagnose() (AssistantDiagnoseResult, error) {
	report := a.assistant().Diagnose(a.activeWorkspaceRoot()).Report
	out := AssistantDiagnoseResult{Checked: report.Checked, Findings: []AssistantFinding{}}
	for _, finding := range report.Findings {
		out.Findings = append(out.Findings, AssistantFinding(finding))
	}
	return out, nil
}

// AssistantTestConnectivity probes one provider/model with a bounded request.
func (a *App) AssistantTestConnectivity(provider, model, draftKey string) (AssistantConnectivity, error) {
	view := a.assistant().TestConnectivity(a.reqCtx(), a.activeWorkspaceRoot(), provider, model, draftKey)
	return AssistantConnectivity(view.Result), nil
}

// AssistantTestEndpoint probes the assistant's own unsaved endpoint; the draft
// key stays inside this call and is never logged or emitted.
func (a *App) AssistantTestEndpoint(endpoint, kind, model, draftKey string) (AssistantConnectivity, error) {
	view := a.assistant().TestEndpoint(a.reqCtx(), endpoint, kind, model, draftKey)
	return AssistantConnectivity(view.Result), nil
}

// AssistantHistory returns recent applied/rolled-back changes.
func (a *App) AssistantHistory() ([]AssistantHistoryRecord, error) {
	records, err := a.assistant().History()
	if err != nil {
		return nil, err
	}
	out := make([]AssistantHistoryRecord, 0, len(records))
	for _, record := range records {
		out = append(out, AssistantHistoryRecord{
			ID: record.ID, Kind: record.Kind, CreatedAt: record.CreatedAt, Intent: record.Intent,
			Diff: rowsOf(record.Diff), BackupID: record.BackupID, BackupDir: record.BackupDir, Status: record.Status,
		})
	}
	return out, nil
}

// AssistantRollback restores one backup by id.
func (a *App) AssistantRollback(backupID string) (AssistantRollbackResult, error) {
	view, err := a.assistant().Rollback(backupID)
	if err != nil {
		return AssistantRollbackResult{}, err
	}
	return AssistantRollbackResult(view), nil
}

func rowsOf(rows []patch.DiffEntry) []AssistantPlanRow {
	out := make([]AssistantPlanRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, AssistantPlanRow(row))
	}
	return out
}
