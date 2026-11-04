package patch

import (
	"fmt"
	"strings"

	"pondera/internal/assist/registry"
	"pondera/internal/config"
)

// ResolvedOp is one validated operation with the before-value captured against
// the configuration snapshot the plan was built from. Secrets carry no
// before/after value: only presence, so nothing sensitive enters a plan file.
type ResolvedOp struct {
	Op        string `json:"op"`
	Path      string `json:"path,omitempty"`
	Provider  string `json:"provider,omitempty"`
	Field     string `json:"field,omitempty"`
	APIKeyEnv string `json:"api_key_env,omitempty"`
	Value     any    `json:"value,omitempty"`
	Before    any    `json:"before,omitempty"`
	Sensitive bool   `json:"sensitive,omitempty"`
	// Risk mirrors the registry policy so the preview can badge confirm ops.
	Risk string `json:"risk,omitempty"`
	// Label is the human-readable target shown in the preview card.
	Label string `json:"label"`
}

// Validate resolves and checks every op against cfg. It is called twice: when
// the plan is previewed and again after the writer re-reads the configuration
// under the lock. A path that disappears, a provider renamed or a model ref
// that no longer resolves therefore fails closed.
func Validate(env *Envelope, cfg *config.Config) ([]ResolvedOp, error) {
	if err := ValidateShape(env); err != nil {
		return nil, err
	}
	reg := registry.Default()
	out := make([]ResolvedOp, 0, len(env.Ops))
	for i := range env.Ops {
		op := &env.Ops[i]
		resolved, err := validateOp(reg, cfg, op)
		if err != nil {
			return nil, fmt.Errorf("op %d: %w", i+1, err)
		}
		out = append(out, resolved)
	}
	return out, nil
}

func validateOp(reg *registry.Registry, cfg *config.Config, op *Op) (ResolvedOp, error) {
	switch op.Op {
	case OpSet:
		return validateSet(reg, cfg, op)
	case OpSetProviderField:
		return validateProviderField(reg, cfg, op)
	case OpSetSecret:
		return validateSecret(cfg, op)
	}
	return ResolvedOp{}, reject("unknown_op", "不支持的操作 %q", op.Op)
}

func validateSet(reg *registry.Registry, cfg *config.Config, op *Op) (ResolvedOp, error) {
	path := strings.TrimSpace(op.Path)
	field, ok := reg.Lookup(path)
	if !ok {
		if registry.IsDeniedGroup(strings.Split(path, ".")[0]) {
			return ResolvedOp{}, reject("denied_by_policy", "%s 属于安全边界配置组，助手不可写", path)
		}
		return ResolvedOp{}, reject("unknown_path", "未知配置项 %q", path)
	}
	if field.Risk == registry.RiskDeny || !field.Writable {
		return ResolvedOp{}, reject("denied_by_policy", "%s 不属于 MVP 可写范围", path)
	}
	value, err := DecodeOpValue(op.Value, field.Sensitive)
	if err != nil {
		return ResolvedOp{}, err
	}
	if err := registry.CheckValue(field, value); err != nil {
		return ResolvedOp{}, wrapCheck(err)
	}
	if err := checkCrossField(cfg, path, value); err != nil {
		return ResolvedOp{}, err
	}
	before, _ := registry.Get(cfg, path)
	return ResolvedOp{
		Op: ResolvedOpSet, Path: path, Value: value, Before: before,
		Sensitive: field.Sensitive, Risk: string(field.Risk), Label: path,
	}, nil
}

func validateProviderField(reg *registry.Registry, cfg *config.Config, op *Op) (ResolvedOp, error) {
	provider := strings.TrimSpace(op.Provider)
	field := strings.TrimSpace(op.Field)
	if registry.ProviderIndex(cfg, provider) < 0 {
		return ResolvedOp{}, reject("provider_unregistered", "provider %q 不存在", provider)
	}
	if !registry.IsProviderField(field) {
		return ResolvedOp{}, reject("denied_by_policy", "providers[].%s 不属于 MVP 可写范围", field)
	}
	desc, ok := reg.ProviderField(field)
	if !ok {
		return ResolvedOp{}, reject("unknown_path", "未知 provider 字段 %q", field)
	}
	value, err := DecodeOpValue(op.Value, desc.Sensitive)
	if err != nil {
		return ResolvedOp{}, err
	}
	if err := registry.CheckValue(desc, value); err != nil {
		return ResolvedOp{}, wrapCheck(err)
	}
	if err := checkProviderValue(cfg, provider, field, value); err != nil {
		return ResolvedOp{}, err
	}
	before, _ := registry.GetProviderField(cfg, provider, field)
	return ResolvedOp{
		Op: ResolvedOpProvider, Provider: provider, Field: field, Value: value, Before: before,
		Sensitive: desc.Sensitive, Risk: string(desc.Risk), Label: fmt.Sprintf("providers[%s].%s", provider, field),
	}, nil
}

func validateSecret(cfg *config.Config, op *Op) (ResolvedOp, error) {
	key := strings.TrimSpace(op.APIKeyEnv)
	if !credentialKeyPattern.MatchString(key) {
		return ResolvedOp{}, reject("invalid_credential_key", "api_key_env 必须形如 [A-Z][A-Z0-9_]*，收到 %q", key)
	}
	referenced := false
	for _, entry := range cfg.Providers {
		if strings.TrimSpace(entry.APIKeyEnv) == key {
			referenced = true
			break
		}
	}
	if !referenced {
		return ResolvedOp{}, reject("credential_unreferenced", "没有 provider 引用 %s，拒绝写入孤立密钥", key)
	}
	value, err := DecodeOpValue(op.Value, true)
	if err != nil {
		return ResolvedOp{}, err
	}
	s, ok := value.(string)
	if !ok {
		return ResolvedOp{}, reject("type_mismatch", "set_secret 的值必须是字符串")
	}
	if strings.TrimSpace(s) == "" {
		return ResolvedOp{}, reject("invalid_value", "密钥不能为空；如需清除请直接在设置页操作")
	}
	return ResolvedOp{
		Op: ResolvedOpSecret, APIKeyEnv: key, Value: s, Sensitive: true,
		Before: presence(config.CredentialIsSet(key)), Risk: string(registry.RiskConfirm),
		Label: key,
	}, nil
}

const (
	ResolvedOpSet      = "set"
	ResolvedOpProvider = "set_provider_field"
	ResolvedOpSecret   = "set_secret"
)

func presence(set bool) string {
	if set {
		return "已设置"
	}
	return "未设置"
}

// wrapCheck converts registry validation errors into the patch rejection type so
// the UI sees a stable reason code instead of a free-form string.
func wrapCheck(err error) error {
	msg := err.Error()
	for _, reason := range []string{"type_mismatch", "out_of_range", "enum_mismatch"} {
		prefix := reason + ": "
		if strings.HasPrefix(msg, prefix) {
			return reject(reason, "%s", strings.TrimPrefix(msg, prefix))
		}
	}
	return err
}

func checkCrossField(cfg *config.Config, path string, value any) error {
	text, _ := value.(string)
	switch path {
	case "default_model":
		if !registry.ModelRefResolves(cfg, text, false) {
			return reject("model_unresolved", "default_model %q 解析不到已配置的 provider/model", text)
		}
	case "agent.planner_model", "agent.subagent_model":
		if !registry.ModelRefResolves(cfg, text, false) {
			return reject("model_unresolved", "%s %q 解析不到已配置的 provider/model", path, text)
		}
	case "agent.vision_model", "agent.web_search_model":
		if !registry.ModelRefResolves(cfg, text, true) {
			return reject("model_unresolved", "%s %q 解析不到已配置的 provider/model", path, text)
		}
	}
	return nil
}

func checkProviderValue(cfg *config.Config, provider, field string, value any) error {
	text, _ := value.(string)
	if field == "model" || field == "default" {
		if strings.TrimSpace(text) == "" {
			return reject("invalid_value", "%s 不能为空", field)
		}
	}
	i := registry.ProviderIndex(cfg, provider)
	if i < 0 {
		return reject("provider_unregistered", "provider %q 不存在", provider)
	}
	models := cfg.Providers[i].ModelList()
	if len(models) == 0 {
		return nil
	}
	if field == "model" || field == "default" {
		found := false
		for _, m := range models {
			if m == text {
				found = true
				break
			}
		}
		if !found {
			return reject("model_unresolved", "%s 不在 provider %s 的模型列表 %v 中", text, provider, models)
		}
	}
	return nil
}

// Revalidate re-checks a resolved operation list against a newly loaded
// configuration snapshot and refreshes before-values. The writer uses it after
// taking the write lock so a plan cannot be applied to a file that changed
// since the preview.
func Revalidate(ops []ResolvedOp, cfg *config.Config) ([]ResolvedOp, error) {
	reg := registry.Default()
	out := make([]ResolvedOp, 0, len(ops))
	for i, op := range ops {
		refreshed := op
		switch op.Op {
		case ResolvedOpSet:
			field, ok := reg.Lookup(op.Path)
			if !ok || field.Risk == registry.RiskDeny || !field.Writable {
				return nil, fmt.Errorf("op %d: %w", i+1, reject("denied_by_policy", "%s 不属于 MVP 可写范围", op.Path))
			}
			if err := registry.CheckValue(field, op.Value); err != nil {
				return nil, fmt.Errorf("op %d: %w", i+1, wrapCheck(err))
			}
			if err := checkCrossField(cfg, op.Path, op.Value); err != nil {
				return nil, fmt.Errorf("op %d: %w", i+1, err)
			}
			refreshed.Before, _ = registry.Get(cfg, op.Path)
		case ResolvedOpProvider:
			if registry.ProviderIndex(cfg, op.Provider) < 0 {
				return nil, fmt.Errorf("op %d: %w", i+1, reject("provider_unregistered", "provider %q 不存在", op.Provider))
			}
			if !registry.IsProviderField(op.Field) {
				return nil, fmt.Errorf("op %d: %w", i+1, reject("denied_by_policy", "providers[].%s 不属于 MVP 可写范围", op.Field))
			}
			desc, ok := reg.ProviderField(op.Field)
			if !ok {
				return nil, fmt.Errorf("op %d: %w", i+1, reject("unknown_path", "未知 provider 字段 %q", op.Field))
			}
			if err := registry.CheckValue(desc, op.Value); err != nil {
				return nil, fmt.Errorf("op %d: %w", i+1, wrapCheck(err))
			}
			if err := checkProviderValue(cfg, op.Provider, op.Field, op.Value); err != nil {
				return nil, fmt.Errorf("op %d: %w", i+1, err)
			}
			refreshed.Before, _ = registry.GetProviderField(cfg, op.Provider, op.Field)
		case ResolvedOpSecret:
			key := strings.TrimSpace(op.APIKeyEnv)
			if !credentialKeyPattern.MatchString(key) {
				return nil, fmt.Errorf("op %d: %w", i+1, reject("invalid_credential_key", "api_key_env 非法"))
			}
			referenced := false
			for _, entry := range cfg.Providers {
				if strings.TrimSpace(entry.APIKeyEnv) == key {
					referenced = true
					break
				}
			}
			if !referenced {
				return nil, fmt.Errorf("op %d: %w", i+1, reject("credential_unreferenced", "没有 provider 引用 %s", key))
			}
			if s, ok := op.Value.(string); !ok || strings.TrimSpace(s) == "" {
				return nil, fmt.Errorf("op %d: %w", i+1, reject("invalid_value", "密钥不能为空"))
			}
			refreshed.Before = presence(config.CredentialIsSet(key))
		default:
			return nil, fmt.Errorf("op %d: %w", i+1, reject("unknown_op", "不支持的操作 %q", op.Op))
		}
		out = append(out, refreshed)
	}
	return out, nil
}

// Apply mutates cfg with the resolved non-secret ops. Credentials are written
// separately by the writer through config.SetCredential.
func Apply(cfg *config.Config, ops []ResolvedOp) error {
	for _, op := range ops {
		switch op.Op {
		case ResolvedOpSet:
			if err := registry.Set(cfg, op.Path, op.Value); err != nil {
				return err
			}
		case ResolvedOpProvider:
			if err := registry.SetProviderField(cfg, op.Provider, op.Field, op.Value); err != nil {
				return err
			}
		case ResolvedOpSecret:
			// handled by the credential write; never touches config.toml
		default:
			return reject("unknown_op", "不支持的操作 %q", op.Op)
		}
	}
	return nil
}

// AssertApplied verifies that every non-secret target now holds its planned
// value. The writer calls it after re-loading the saved file.
func AssertApplied(cfg *config.Config, ops []ResolvedOp) error {
	for _, op := range ops {
		switch op.Op {
		case ResolvedOpSet:
			got, ok := registry.Get(cfg, op.Path)
			if !ok {
				return fmt.Errorf("verify: %s missing after save", op.Path)
			}
			if !equalScalar(got, op.Value) {
				return fmt.Errorf("verify: %s = %v, want %v", op.Path, got, op.Value)
			}
		case ResolvedOpProvider:
			got, ok := registry.GetProviderField(cfg, op.Provider, op.Field)
			if !ok {
				return fmt.Errorf("verify: providers[%s].%s missing after save", op.Provider, op.Field)
			}
			if !equalScalar(got, op.Value) {
				return fmt.Errorf("verify: providers[%s].%s = %v, want %v", op.Provider, op.Field, got, op.Value)
			}
		}
	}
	return nil
}

func equalScalar(a, b any) bool {
	switch av := a.(type) {
	case int64:
		bv, ok := b.(int64)
		return ok && av == bv
	case float64:
		bv, ok := b.(float64)
		return ok && av == bv
	}
	return a == b
}

// Secrets returns the credential ops in order.
func Secrets(ops []ResolvedOp) []ResolvedOp {
	out := make([]ResolvedOp, 0)
	for _, op := range ops {
		if op.Op == ResolvedOpSecret {
			out = append(out, op)
		}
	}
	return out
}
