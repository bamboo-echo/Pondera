package patch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"pondera/internal/assist/registry"
	"pondera/internal/config"
)

// ProviderSpec is the strict add_provider value. It deliberately excludes
// headers and extra_body: those can smuggle secrets past the redaction rules.
type ProviderSpec struct {
	Kind        string   `json:"kind"`
	BaseURL     string   `json:"base_url"`
	ModelsURL   string   `json:"models_url,omitempty"`
	Model       string   `json:"model,omitempty"`
	Models      []string `json:"models,omitempty"`
	Default     string   `json:"default,omitempty"`
	APIKeyEnv   string   `json:"api_key_env,omitempty"`
	DisplayName string   `json:"display_name,omitempty"`
}

const (
	ResolvedOpAddProvider    = "add_provider"
	ResolvedOpRemoveProvider = "remove_provider"
)

var providerNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// reservedProviderNames are routing identities the assistant must not create.
var reservedProviderNames = map[string]bool{
	"assistant": true, "default": true, "system": true, "auto": true,
}

var allowedProviderKinds = map[string]bool{
	"openai": true, "chat": true, "responses": true, "anthropic": true,
}

// DecodeProviderSpec strictly decodes an add_provider value. Unknown keys,
// arrays, null and non-object values are rejected.
func DecodeProviderSpec(raw json.RawMessage) (*ProviderSpec, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, reject("invalid_value", "add_provider 缺少 value")
	}
	if trimmed[0] != '{' {
		return nil, reject("invalid_value", "add_provider 的 value 必须是对象")
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.DisallowUnknownFields()
	var spec ProviderSpec
	if err := dec.Decode(&spec); err != nil {
		return nil, reject("invalid_value", "provider 描述无法解析：%v", err)
	}
	if err := checkJSONEOF(dec); err != nil {
		return nil, err
	}
	return &spec, nil
}

func checkJSONEOF(dec *json.Decoder) error {
	if _, err := dec.Token(); err != io.EOF {
		return reject("invalid_value", "provider 描述之后还有多余内容")
	}
	return nil
}

// validateAddProvider checks the name and spec, then captures the resolved op.
func validateAddProvider(cfg *config.Config, op *Op) (ResolvedOp, error) {
	name := strings.TrimSpace(op.Provider)
	if err := checkProviderName(cfg, name); err != nil {
		return ResolvedOp{}, err
	}
	spec, err := DecodeProviderSpec(op.Value)
	if err != nil {
		return ResolvedOp{}, err
	}
	if err := normalizeProviderSpec(spec); err != nil {
		return ResolvedOp{}, err
	}
	return ResolvedOp{
		Op: ResolvedOpAddProvider, Provider: name, NewProvider: spec,
		Risk: string(registry.RiskConfirm), Label: "providers[" + name + "]",
	}, nil
}

// validateRemoveProvider refuses to delete a provider that any default or
// binding still references, and reports the exact reference in the reason.
func validateRemoveProvider(cfg *config.Config, op *Op) (ResolvedOp, error) {
	name := strings.TrimSpace(op.Provider)
	index := registry.ProviderIndex(cfg, name)
	if index < 0 {
		return ResolvedOp{}, reject("provider_unregistered", "provider %q 不存在", name)
	}
	if refs := providerReferences(cfg, name); len(refs) > 0 {
		return ResolvedOp{}, reject("provider_referenced", "provider %q 仍被 %s 引用，先解除再删除", name, strings.Join(refs, "、"))
	}
	entry := cfg.Providers[index]
	before := fmt.Sprintf("kind=%s base_url=%s", entry.Kind, entry.BaseURL)
	return ResolvedOp{
		Op: ResolvedOpRemoveProvider, Provider: name, Before: before,
		Risk: string(registry.RiskConfirm), Label: "providers[" + name + "]",
	}, nil
}

func checkProviderName(cfg *config.Config, name string) error {
	if name == "" {
		return reject("invalid_provider", "provider 名称不能为空")
	}
	if !providerNamePattern.MatchString(name) {
		return reject("invalid_provider", "provider 名称只能由字母、数字、点、下划线和短横线组成，且不超过 64 字符")
	}
	if reservedProviderNames[strings.ToLower(name)] {
		return reject("reserved_provider_name", "%q 是保留名称，助手不能创建", name)
	}
	if registry.ProviderIndex(cfg, name) >= 0 {
		return reject("provider_exists", "provider %q 已存在，请改用 set_provider_field", name)
	}
	for _, entry := range cfg.Providers {
		if strings.EqualFold(strings.TrimSpace(entry.Name), name) {
			return reject("provider_exists", "provider %q 已存在（名称比较不区分大小写）", name)
		}
	}
	return nil
}

func normalizeProviderSpec(spec *ProviderSpec) error {
	if spec == nil {
		return reject("invalid_value", "provider 描述为空")
	}
	spec.Kind = strings.TrimSpace(spec.Kind)
	if !allowedProviderKinds[spec.Kind] {
		return reject("invalid_provider", "kind 必须是 openai/chat/responses/anthropic，收到 %q", spec.Kind)
	}
	spec.BaseURL = strings.TrimSpace(spec.BaseURL)
	if err := checkHTTPURL(spec.BaseURL); err != nil {
		return err
	}
	if spec.ModelsURL != "" {
		spec.ModelsURL = strings.TrimSpace(spec.ModelsURL)
		if err := checkHTTPURL(spec.ModelsURL); err != nil {
			return err
		}
	}
	spec.Model = strings.TrimSpace(spec.Model)
	spec.Default = strings.TrimSpace(spec.Default)
	models := make([]string, 0, len(spec.Models))
	for _, model := range spec.Models {
		model = strings.TrimSpace(model)
		if model == "" {
			return reject("invalid_value", "models 不能包含空模型名")
		}
		if utf8.RuneCountInString(model) > 200 || strings.ContainsAny(model, "\r\n\x00") {
			return reject("invalid_value", "模型名过长或包含控制字符")
		}
		models = append(models, model)
	}
	spec.Models = models
	if spec.Model == "" && len(spec.Models) == 0 {
		return reject("invalid_value", "至少需要 model 或 models 之一")
	}
	if len(spec.Models) > 50 {
		return reject("invalid_value", "models 最多 50 个")
	}
	if spec.Default != "" && len(spec.Models) > 0 && !containsString(spec.Models, spec.Default) {
		return reject("invalid_value", "default %q 不在 models 列表中", spec.Default)
	}
	if spec.APIKeyEnv != "" {
		spec.APIKeyEnv = strings.TrimSpace(spec.APIKeyEnv)
		if !credentialKeyPattern.MatchString(spec.APIKeyEnv) {
			return reject("invalid_credential_key", "api_key_env 必须形如 [A-Z][A-Z0-9_]*")
		}
	}
	spec.DisplayName = strings.TrimSpace(spec.DisplayName)
	if utf8.RuneCountInString(spec.DisplayName) > 80 || strings.ContainsAny(spec.DisplayName, "\r\n\x00") {
		return reject("invalid_value", "display_name 过长或包含控制字符")
	}
	return nil
}

func checkHTTPURL(raw string) error {
	if raw == "" {
		return reject("invalid_value", "base_url 不能为空")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return reject("invalid_value", "base_url 必须是 http(s) URL，收到 %q", raw)
	}
	if u.User != nil {
		return reject("invalid_value", "base_url 不能携带用户名或密码")
	}
	return nil
}

func containsString(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// providerReferences lists the defaults/bindings that still point at name.
func providerReferences(cfg *config.Config, name string) []string {
	var refs []string
	index := providerIndexFold(cfg, name)
	check := func(label, ref string) {
		if refBindsProvider(cfg, index, ref) {
			refs = append(refs, label+"="+strings.TrimSpace(ref))
		}
	}
	check("default_model", cfg.DefaultModel)
	check("agent.planner_model", cfg.Agent.PlannerModel)
	check("agent.subagent_model", cfg.Agent.SubagentModel)
	check("agent.vision_model", cfg.Agent.VisionModel)
	check("agent.web_search_model", cfg.Agent.WebSearchModel)
	check("agent.guardian_model", cfg.Agent.GuardianModel)
	check("agent.recovery_model", cfg.Agent.RecoveryModel)
	for skill, ref := range cfg.Agent.SubagentModels {
		check("agent.subagent_models."+skill, ref)
	}
	for _, allowed := range cfg.Desktop.ProviderAccess {
		if strings.EqualFold(strings.TrimSpace(allowed), name) {
			refs = append(refs, "desktop.provider_access="+strings.TrimSpace(allowed))
			break
		}
	}
	return refs
}

// refBindsProvider matches both "provider/model" references and bare model
// names that only this provider serves. The "auto" sentinel never binds.
func refBindsProvider(cfg *config.Config, index int, ref string) bool {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.EqualFold(ref, "auto") || index < 0 {
		return false
	}
	entry := cfg.Providers[index]
	if slash := strings.Index(ref, "/"); slash > 0 {
		return strings.EqualFold(strings.TrimSpace(ref[:slash]), strings.TrimSpace(entry.Name))
	}
	if strings.EqualFold(ref, entry.Name) {
		return true
	}
	for _, model := range entry.ModelList() {
		if strings.EqualFold(strings.TrimSpace(model), ref) {
			return true
		}
	}
	return false
}

// providerEntryOf converts a validated spec into a config entry.
func providerEntryOf(name string, spec *ProviderSpec) config.ProviderEntry {
	return config.ProviderEntry{
		Name: name, DisplayName: spec.DisplayName, Kind: spec.Kind,
		BaseURL: spec.BaseURL, ModelsURL: spec.ModelsURL,
		Model: spec.Model, Models: append([]string(nil), spec.Models...),
		Default: spec.Default, APIKeyEnv: spec.APIKeyEnv,
	}
}

func providerIndexFold(cfg *config.Config, name string) int {
	for i, entry := range cfg.Providers {
		if strings.EqualFold(strings.TrimSpace(entry.Name), name) {
			return i
		}
	}
	return -1
}

func removeProvider(cfg *config.Config, name string) {
	index := providerIndexFold(cfg, name)
	if index < 0 {
		return
	}
	cfg.Providers = append(cfg.Providers[:index], cfg.Providers[index+1:]...)
}
