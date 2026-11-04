package registry

import (
	"strings"

	"pondera/internal/config"
)

// ProviderView is the redacted provider projection sent to the model.
type ProviderView struct {
	Name            string   `json:"name"`
	Kind            string   `json:"kind"`
	Model           string   `json:"model"`
	Default         string   `json:"default"`
	Models          []string `json:"models"`
	ContextWindow   int      `json:"context_window"`
	MaxOutputTokens int      `json:"max_output_tokens"`
	Effort          string   `json:"effort"`
	APIKeyEnv       string   `json:"api_key_env"`
	Credential      string   `json:"credential"` // 已设置 / 未设置 only
}

// AgentView is the redacted agent projection sent to the model.
type AgentView struct {
	Temperature    float64 `json:"temperature"`
	CompactRatio   float64 `json:"compact_ratio"`
	RetryCount     int     `json:"retry_count"`
	PlannerModel   string  `json:"planner_model"`
	SubagentModel  string  `json:"subagent_model"`
	VisionModel    string  `json:"vision_model"`
	WebSearchModel string  `json:"web_search_model"`
}

// Snapshot is the whitelisted configuration context. It contains no secret
// values: credentials appear only as presence strings.
type Snapshot struct {
	DefaultModel string         `json:"default_model"`
	Language     string         `json:"language"`
	Providers    []ProviderView `json:"providers"`
	Agent        AgentView      `json:"agent"`
}

// SnapshotOf builds the redacted model context.
func SnapshotOf(cfg *config.Config) Snapshot {
	if cfg == nil {
		return Snapshot{}
	}
	snap := Snapshot{DefaultModel: cfg.DefaultModel, Language: cfg.Language}
	snap.Agent = AgentView{
		Temperature: cfg.Agent.Temperature, CompactRatio: cfg.Agent.CompactRatio,
		RetryCount: cfg.Agent.RetryCount, PlannerModel: cfg.Agent.PlannerModel,
		SubagentModel: cfg.Agent.SubagentModel, VisionModel: cfg.Agent.VisionModel,
		WebSearchModel: cfg.Agent.WebSearchModel,
	}
	for i := range cfg.Providers {
		p := &cfg.Providers[i]
		view := ProviderView{
			Name: p.Name, Kind: p.Kind, Model: p.Model, Default: p.Default,
			Models: p.ModelList(), ContextWindow: p.ContextWindow,
			MaxOutputTokens: p.MaxOutputTokens, Effort: p.Effort,
			APIKeyEnv: p.APIKeyEnv, Credential: "未设置",
		}
		if strings.TrimSpace(p.APIKeyEnv) != "" && config.CredentialIsSet(p.APIKeyEnv) {
			view.Credential = "已设置"
		}
		if view.Models == nil {
			view.Models = []string{}
		}
		snap.Providers = append(snap.Providers, view)
	}
	if snap.Providers == nil {
		snap.Providers = []ProviderView{}
	}
	return snap
}
