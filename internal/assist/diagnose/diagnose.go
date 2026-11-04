// Package diagnose performs read-only checks over the effective Pondera
// configuration plus one bounded connectivity probe.
package diagnose

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"pondera/internal/assist/registry"
	"pondera/internal/boot"
	"pondera/internal/config"
	"pondera/internal/netclient"
	"pondera/internal/provider"
)

// Severity levels surfaced by the UI.
const (
	SeverityError = "error"
	SeverityWarn  = "warn"
	SeverityInfo  = "info"
)

// Finding is one diagnostic result.
type Finding struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	Provider string `json:"provider,omitempty"`
	Fix      string `json:"fix,omitempty"`
}

// Report is the full diagnostics result.
type Report struct {
	Findings []Finding `json:"findings"`
	Checked  int       `json:"checked"`
}

// Run executes the ten MVP checks against the user configuration layered with
// the project file, when a root is supplied.
func Run(root string) Report {
	report := Report{Findings: []Finding{}}
	cfg, err := config.LoadForRootWithoutCredentialsReadOnly(root)
	if err != nil {
		report.Findings = append(report.Findings, Finding{
			Code: "config_parse_error", Severity: SeverityError, Title: "配置无法解析",
			Detail: err.Error(), Fix: "修复损坏的 config.toml，或从备份恢复",
		})
		return report
	}

	// 2 credential_missing
	for i := range cfg.Providers {
		p := &cfg.Providers[i]
		report.Checked++
		key := strings.TrimSpace(p.APIKeyEnv)
		if key != "" && !config.CredentialIsSet(key) {
			report.Findings = append(report.Findings, Finding{
				Code: "credential_missing", Severity: SeverityError, Provider: p.Name,
				Title:  "缺少 API 密钥",
				Detail: fmt.Sprintf("provider %s 引用 %s，但凭据库与环境中都没有值", p.Name, key),
				Fix:    "在设置页或助手 onboarding 中填写该密钥",
			})
		}
	}

	// 3 credential_unused
	referenced := map[string]bool{}
	for i := range cfg.Providers {
		referenced[strings.TrimSpace(cfg.Providers[i].APIKeyEnv)] = true
	}
	for _, key := range credentialKeyNames() {
		report.Checked++
		if !referenced[key] {
			report.Findings = append(report.Findings, Finding{
				Code: "credential_unused", Severity: SeverityWarn,
				Title:  "存在未被引用的密钥",
				Detail: fmt.Sprintf("%s 已写入凭据库，但没有 provider 引用它", key),
				Fix:    "确认是否拼错 api_key_env，或清理闲置密钥",
			})
		}
	}

	// 4 provider_unregistered
	for _, ref := range modelRefs(cfg) {
		report.Checked++
		if registry.ModelRefResolves(cfg, ref.Value, ref.AllowAuto) {
			continue
		}
		report.Findings = append(report.Findings, Finding{
			Code: "provider_unregistered", Severity: SeverityError,
			Title:  "模型引用无法解析",
			Detail: fmt.Sprintf("%s = %q 找不到对应 provider/model", ref.Field, ref.Value),
			Fix:    "改成 provider/model 形式，或先注册该 provider",
		})
	}

	// 5 model_missing + 7 duplicate_provider + 6 endpoint_mismatch
	seen := map[string]int{}
	for i := range cfg.Providers {
		p := &cfg.Providers[i]
		report.Checked++
		seen[p.Name]++
		if seen[p.Name] == 2 {
			report.Findings = append(report.Findings, Finding{
				Code: "duplicate_provider", Severity: SeverityWarn, Provider: p.Name,
				Title: "provider 名称重复", Detail: fmt.Sprintf("%s 出现多次，只有最后一个生效", p.Name),
				Fix: "删除或重命名重复条目",
			})
		}
		models := p.ModelList()
		if strings.TrimSpace(p.Model) == "" && len(models) == 0 && strings.TrimSpace(p.Default) == "" {
			report.Findings = append(report.Findings, Finding{
				Code: "model_missing", Severity: SeverityError, Provider: p.Name,
				Title: "provider 没有配置模型", Detail: fmt.Sprintf("%s 既没有 model 也没有 models", p.Name),
				Fix: "补上 model 或 models 列表",
			})
		}
		if d := strings.TrimSpace(p.Default); d != "" && len(models) > 0 {
			found := false
			for _, m := range models {
				if m == d {
					found = true
					break
				}
			}
			if !found {
				report.Findings = append(report.Findings, Finding{
					Code: "model_missing", Severity: SeverityWarn, Provider: p.Name,
					Title: "默认模型不在模型列表中", Detail: fmt.Sprintf("%s default=%s 不在 models 里", p.Name, d),
					Fix: "把 default 改成列表中的模型",
				})
			}
		}
		if err := config.ValidateProviderEndpoint(p); err != nil {
			report.Findings = append(report.Findings, Finding{
				Code: "endpoint_mismatch", Severity: SeverityError, Provider: p.Name,
				Title: "端点与协议不匹配", Detail: err.Error(), Fix: "检查 base_url/chat_url 的路径后缀",
			})
		}
	}

	// 8 project_shadow
	if root = strings.TrimSpace(root); root != "" {
		report.Checked++
		project := filepath.Join(root, "pondera.toml")
		if _, err := os.Stat(project); err == nil {
			report.Findings = append(report.Findings, Finding{
				Code: "project_shadow", Severity: SeverityInfo,
				Title:  "项目配置会覆盖用户配置",
				Detail: "当前工作区存在 pondera.toml，其中同名键的优先级高于用户配置",
				Fix:    "确认覆盖关系后再修改用户配置",
			})
		}
	}

	// 9 compact_disabled
	for i := range cfg.Providers {
		p := &cfg.Providers[i]
		report.Checked++
		if p.ContextWindow <= 0 {
			report.Findings = append(report.Findings, Finding{
				Code: "compact_disabled", Severity: SeverityInfo, Provider: p.Name,
				Title:  "未设置上下文窗口",
				Detail: fmt.Sprintf("%s 的 context_window 为 0，压缩不会触发，可能更早撞到上限", p.Name),
				Fix:    "设置 context_window（≥1000）",
			})
		}
	}

	// 10 retry_out_of_range
	report.Checked++
	if cfg.Agent.RetryCount < 0 || cfg.Agent.RetryCount > 1000 {
		report.Findings = append(report.Findings, Finding{
			Code: "retry_out_of_range", Severity: SeverityWarn,
			Title:  "重试次数超出范围",
			Detail: fmt.Sprintf("agent.retry_count = %d，合理范围 0–1000", cfg.Agent.RetryCount),
			Fix:    "改成 0–1000 之间的值",
		})
	}
	sort.SliceStable(report.Findings, func(i, j int) bool {
		return severityRank(report.Findings[i].Severity) < severityRank(report.Findings[j].Severity)
	})
	return report
}

func severityRank(s string) int {
	switch s {
	case SeverityError:
		return 0
	case SeverityWarn:
		return 1
	}
	return 2
}

type modelRef struct {
	Field     string
	Value     string
	AllowAuto bool
}

func modelRefs(cfg *config.Config) []modelRef {
	refs := []modelRef{
		{Field: "default_model", Value: strings.TrimSpace(cfg.DefaultModel)},
		{Field: "agent.planner_model", Value: strings.TrimSpace(cfg.Agent.PlannerModel)},
		{Field: "agent.subagent_model", Value: strings.TrimSpace(cfg.Agent.SubagentModel)},
		{Field: "agent.vision_model", Value: strings.TrimSpace(cfg.Agent.VisionModel), AllowAuto: true},
		{Field: "agent.web_search_model", Value: strings.TrimSpace(cfg.Agent.WebSearchModel), AllowAuto: true},
	}
	out := make([]modelRef, 0, len(refs))
	for _, ref := range refs {
		if ref.Value != "" {
			out = append(out, ref)
		}
	}
	return out
}

// credentialKeyNames reads only the key names from the credential store; values
// are never parsed or returned.
func credentialKeyNames() []string {
	path := config.UserCredentialsPath()
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	names := []string{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, _, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(strings.TrimPrefix(key, "export "))
		if key != "" {
			names = append(names, key)
		}
	}
	sort.Strings(names)
	return names
}

// ConnectivityResult is one probe outcome.
type ConnectivityResult struct {
	OK        bool   `json:"ok"`
	Category  string `json:"category"`
	Message   string `json:"message"`
	LatencyMS int64  `json:"latency_ms"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
}

// Connectivity sends one minimal, tool-free request through the configured
// adapter. A draft key may be supplied; it is request-local.
func Connectivity(ctx context.Context, root, provider, model, draftKey string) ConnectivityResult {
	result := ConnectivityResult{Provider: provider, Model: model}
	cfg, err := config.LoadForRootWithoutCredentialsReadOnly(root)
	if err != nil {
		result.Category = "config_parse_error"
		result.Message = err.Error()
		return result
	}
	var entry *config.ProviderEntry
	if strings.TrimSpace(model) != "" {
		entry, _ = cfg.ResolveModel(provider + "/" + strings.TrimSpace(model))
	}
	if entry == nil {
		i := registry.ProviderIndex(cfg, provider)
		if i < 0 {
			result.Category = "provider_unregistered"
			result.Message = fmt.Sprintf("provider %q 不存在", provider)
			return result
		}
		copied := cfg.Providers[i]
		if strings.TrimSpace(model) != "" {
			copied.Model = strings.TrimSpace(model)
		}
		entry = &copied
	}
	entry.ResolveAPIKeyForRoot(root)
	if strings.TrimSpace(draftKey) != "" {
		copied := entry.WithAPIKeyForProbe(draftKey)
		entry = &copied
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	started := time.Now()
	err = boot.ProbeProviderConnection(ctx, *entry, draftKey, cfg.NetworkProxySpec())
	result.LatencyMS = time.Since(started).Milliseconds()
	if err == nil {
		result.OK = true
		result.Category = "ok"
		result.Message = "连接成功，密钥与模型可用"
		return result
	}
	result.Category, result.Message = classifyProbeError(err)
	return result
}

// ConnectivityEndpoint probes the assistant's own (unsaved) endpoint. It never
// reads or writes the main config provider, and a draft key stays request-local.
func ConnectivityEndpoint(ctx context.Context, endpoint, kind, model, draftKey string) ConnectivityResult {
	model = strings.TrimSpace(model)
	result := ConnectivityResult{Provider: "assistant", Model: model}
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if endpoint == "" || model == "" {
		result.Category = "unknown"
		result.Message = "请先填写端点与模型，再测试连接"
		return result
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		kind = "openai"
	}
	entry := config.ProviderEntry{
		Name: "assistant", Kind: kind, BaseURL: endpoint, Model: model, MaxOutputTokens: 16,
	}
	if strings.TrimSpace(draftKey) != "" {
		entry = entry.WithAPIKeyForProbe(draftKey)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	started := time.Now()
	err := boot.ProbeProviderConnection(ctx, entry, draftKey, netclient.ProxySpec{Mode: netclient.ModeAuto})
	result.LatencyMS = time.Since(started).Milliseconds()
	if err == nil {
		result.OK = true
		result.Category = "ok"
		result.Message = "连接成功，端点、模型与密钥可用"
		return result
	}
	result.Category, result.Message = classifyProbeError(err)
	return result
}

// probeURLPattern strips request URLs before text classification: a random
// local port ("...:40123/...") must never be read as an HTTP status.
var probeURLPattern = regexp.MustCompile(`(?i)\bhttps?://[^\s"']+`)

func classifyProbeError(err error) (string, string) {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout", "请求超时：检查网络或代理"
	}
	switch probeStatus(err) {
	case http.StatusUnauthorized:
		return "auth", "认证失败（401）：检查 API key 是否正确、是否过期"
	case http.StatusForbidden:
		return "forbidden", "权限不足（403）：该 key 无权访问该模型"
	case http.StatusNotFound:
		return "not_found", "端点或模型不存在（404）：检查 base_url 与模型名"
	case http.StatusTooManyRequests:
		return "rate_limit", "触发限流（429）：稍后重试或降低频率"
	}
	lower := strings.ToLower(probeURLPattern.ReplaceAllString(err.Error(), ""))
	switch {
	case strings.Contains(lower, "401") || strings.Contains(lower, "unauthorized") || strings.Contains(lower, "invalid api key"):
		return "auth", "认证失败（401）：检查 API key 是否正确、是否过期"
	case strings.Contains(lower, "403") || strings.Contains(lower, "forbidden"):
		return "forbidden", "权限不足（403）：该 key 无权访问该模型"
	case strings.Contains(lower, "404") || strings.Contains(lower, "not found"):
		return "not_found", "端点或模型不存在（404）：检查 base_url 与模型名"
	case strings.Contains(lower, "429") || strings.Contains(lower, "rate limit"):
		return "rate_limit", "触发限流（429）：稍后重试或降低频率"
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline"):
		return "timeout", "请求超时：检查网络或代理"
	case strings.Contains(lower, "no such host") || strings.Contains(lower, "dns"):
		return "dns", "域名解析失败：检查拼写与 DNS"
	case strings.Contains(lower, "tls") || strings.Contains(lower, "certificate"):
		return "tls", "TLS 证书错误：检查代理或系统证书"
	}
	return "unknown", err.Error()
}

// probeStatus extracts an HTTP status from structured provider errors, so
// classification never guesses from free-form text.
func probeStatus(err error) int {
	var api *provider.APIError
	if errors.As(err, &api) && api.Status != 0 {
		return api.Status
	}
	var auth *provider.AuthError
	if errors.As(err, &auth) && auth.Status != 0 {
		return auth.Status
	}
	if quota := provider.AsQuotaError(err); quota != nil && quota.Status != 0 {
		return quota.Status
	}
	return 0
}
