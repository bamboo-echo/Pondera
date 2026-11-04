package registry

import (
	"fmt"
	"sort"
	"strings"

	"pondera/internal/config"
)

// WritablePaths returns the curated top-level (non-provider) writable paths.
func WritablePaths() []string {
	out := make([]string, 0)
	for path, f := range curatedFields {
		if f.Writable && !strings.HasPrefix(path, "providers[].") {
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out
}

// Get reads a curated top-level path from cfg as its wire value.
func Get(cfg *config.Config, path string) (any, bool) {
	if cfg == nil {
		return nil, false
	}
	switch path {
	case "default_model":
		return cfg.DefaultModel, true
	case "language":
		return cfg.Language, true
	case "agent.temperature":
		return cfg.Agent.Temperature, true
	case "agent.compact_ratio":
		return cfg.Agent.CompactRatio, true
	case "agent.retry_count":
		return int64(cfg.Agent.RetryCount), true
	case "agent.planner_model":
		return cfg.Agent.PlannerModel, true
	case "agent.subagent_model":
		return cfg.Agent.SubagentModel, true
	case "agent.vision_model":
		return cfg.Agent.VisionModel, true
	case "agent.web_search_model":
		return cfg.Agent.WebSearchModel, true
	}
	return nil, false
}

// Set writes a curated top-level path into cfg. The caller validates the value
// first; this only performs the typed assignment.
func Set(cfg *config.Config, path string, value any) error {
	if cfg == nil {
		return fmt.Errorf("set: nil config")
	}
	switch path {
	case "default_model":
		cfg.DefaultModel = value.(string)
	case "language":
		cfg.Language = value.(string)
	case "agent.temperature":
		cfg.Agent.Temperature = value.(float64)
	case "agent.compact_ratio":
		cfg.Agent.CompactRatio = value.(float64)
	case "agent.retry_count":
		cfg.Agent.RetryCount = int(value.(int64))
	case "agent.planner_model":
		cfg.Agent.PlannerModel = value.(string)
	case "agent.subagent_model":
		cfg.Agent.SubagentModel = value.(string)
	case "agent.vision_model":
		cfg.Agent.VisionModel = value.(string)
	case "agent.web_search_model":
		cfg.Agent.WebSearchModel = value.(string)
	default:
		return fmt.Errorf("unknown_path: %s", path)
	}
	return nil
}

// ProviderIndex returns the index of the named provider, or -1.
func ProviderIndex(cfg *config.Config, name string) int {
	if cfg == nil {
		return -1
	}
	name = strings.TrimSpace(name)
	for i := range cfg.Providers {
		if cfg.Providers[i].Name == name {
			return i
		}
	}
	return -1
}

// ProviderNames returns configured provider names in file order.
func ProviderNames(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	out := make([]string, 0, len(cfg.Providers))
	for _, p := range cfg.Providers {
		out = append(out, p.Name)
	}
	return out
}

// GetProviderField reads a writable provider field.
func GetProviderField(cfg *config.Config, provider, field string) (any, bool) {
	i := ProviderIndex(cfg, provider)
	if i < 0 {
		return nil, false
	}
	p := &cfg.Providers[i]
	switch field {
	case "model":
		return p.Model, true
	case "default":
		return p.Default, true
	case "context_window":
		return int64(p.ContextWindow), true
	case "max_output_tokens":
		return int64(p.MaxOutputTokens), true
	case "effort":
		return p.Effort, true
	}
	return nil, false
}

// SetProviderField writes a writable provider field.
func SetProviderField(cfg *config.Config, provider, field string, value any) error {
	i := ProviderIndex(cfg, provider)
	if i < 0 {
		return fmt.Errorf("provider_unregistered: %s", provider)
	}
	p := &cfg.Providers[i]
	switch field {
	case "model":
		p.Model = value.(string)
	case "default":
		p.Default = value.(string)
	case "context_window":
		p.ContextWindow = int(value.(int64))
	case "max_output_tokens":
		p.MaxOutputTokens = int(value.(int64))
	case "effort":
		p.Effort = value.(string)
	default:
		return fmt.Errorf("unknown_path: providers[].%s", field)
	}
	return nil
}

// ProviderModelNames returns the model ids a provider exposes.
func ProviderModelNames(cfg *config.Config, provider string) []string {
	i := ProviderIndex(cfg, provider)
	if i < 0 {
		return nil
	}
	return cfg.Providers[i].ModelList()
}

// ModelRefResolves reports whether ref is either empty/auto or resolves to a
// configured provider/model.
func ModelRefResolves(cfg *config.Config, ref string, allowAuto bool) bool {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return true
	}
	if allowAuto && ref == "auto" {
		return true
	}
	_, ok := cfg.ResolveModel(ref)
	return ok
}
