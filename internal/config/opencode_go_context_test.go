package config

import (
	"path/filepath"
	"testing"

	"github.com/BurntSushi/toml"

	"pondera/internal/provider"
)

func TestNormalizeLegacyOpenCodeGoInstallsAppliesCatalogAndWindowsInOnePass(t *testing.T) {
	legacy := ProviderEntry{
		Name:          "opencode-go",
		Kind:          "openai",
		BaseURL:       "https://opencode.ai/zen/go/v1",
		Models:        append([]string(nil), legacyOpenCodeGoModels...),
		Default:       "glm-5.2",
		ContextWindow: legacyOpenCodeGoChatWindow,
		PresetID:      "opencode-go",
	}
	second := legacy
	second.Name = "opencode-go-secondary"
	c := &Config{Providers: []ProviderEntry{legacy, second}}

	if !normalizeLegacyOpenCodeGoInstalls(c) {
		t.Fatal("combined OpenCode Go migration did not report a change")
	}
	for i := range c.Providers {
		got := c.Providers[i]
		if !got.HasModel("kimi-k3") {
			t.Fatalf("provider %q did not receive the Kimi K3 catalog update", got.Name)
		}
		for model, limits := range provider.OpenCodeGoChatModels() {
			if window := got.ModelOverrides[model].ContextWindow; window != limits.Context {
				t.Fatalf("provider %q model %q window = %d, want %d", got.Name, model, window, limits.Context)
			}
		}
	}
	if normalizeLegacyOpenCodeGoInstalls(c) {
		t.Fatal("combined OpenCode Go migration was not idempotent")
	}
}

func TestNormalizeLegacyOpenCodeGoContextWindowsMigratesOnlyUntouchedPresets(t *testing.T) {
	legacy := ProviderEntry{
		Name:          "opencode-go",
		Kind:          "openai",
		BaseURL:       "https://opencode.ai/zen/go/v1",
		Models:        append([]string(nil), opencodeGoModels...),
		Default:       "glm-5.2",
		ContextWindow: 128000,
		PresetID:      "opencode-go",
		ModelOverrides: map[string]ProviderModelOverride{
			"kimi-k3": {ContextWindow: 1_048_576},
		},
	}
	customEndpoint := legacy
	customEndpoint.Name = "og-proxy"
	customEndpoint.BaseURL = "https://gateway.example/v1"
	customEndpoint.ModelOverrides = cloneModelOverrideMap(legacy.ModelOverrides)
	customWindow := legacy
	customWindow.Name = "og-custom-window"
	customWindow.ContextWindow = 777_000
	customWindow.ModelOverrides = cloneModelOverrideMap(legacy.ModelOverrides)
	customOverride := legacy
	customOverride.Name = "og-custom-override"
	customOverride.ModelOverrides = map[string]ProviderModelOverride{
		"glm-5.2": {ContextWindow: 500_000},
	}
	c := &Config{Providers: []ProviderEntry{legacy, customEndpoint, customWindow, customOverride}}
	if !normalizeLegacyOpenCodeGoContextWindows(c) {
		t.Fatal("expected official legacy preset to migrate")
	}
	if c.Providers[0].ModelOverrides["glm-5.2"].ContextWindow != 1_000_000 {
		t.Fatalf("migrated glm-5.2 = %+v", c.Providers[0].ModelOverrides["glm-5.2"])
	}
	if c.Providers[1].ModelOverrides["glm-5.2"].ContextWindow != 0 {
		t.Fatal("custom endpoint was migrated")
	}
	if c.Providers[2].ContextWindow != 777_000 {
		t.Fatal("custom provider window was overwritten")
	}
	if c.Providers[3].ModelOverrides["glm-5.2"].ContextWindow != 500_000 {
		t.Fatal("positive custom override was overwritten")
	}

	preIdentity := ProviderEntry{
		Name:          "opencode-go",
		Kind:          "openai",
		BaseURL:       "https://opencode.ai/zen/go/v1",
		Models:        append([]string(nil), opencodeGoModels...),
		Default:       "glm-5.2",
		ContextWindow: 128000,
		ModelOverrides: map[string]ProviderModelOverride{
			"kimi-k3": {ContextWindow: 1_048_576},
		},
	}
	pre := &Config{Providers: []ProviderEntry{preIdentity}}
	if !normalizeLegacyOpenCodeGoContextWindows(pre) || pre.Providers[0].ModelOverrides["glm-5.2"].ContextWindow != 1_000_000 {
		t.Fatal("pre-preset-identity OpenCode Go was not migrated")
	}
}

func TestLoadForEditOpenCodeGoWindowsStayInMemoryUntilSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg := Default()
	preset, ok := CuratedProviderPreset("opencode-go")
	if !ok {
		t.Fatal("missing preset")
	}
	entry := preset.Entries[0]
	entry.ContextWindow = 128000
	entry.ModelOverrides = map[string]ProviderModelOverride{"kimi-k3": {ContextWindow: 1_048_576}}
	cfg.Providers = []ProviderEntry{entry}
	if err := cfg.SaveTo(path); err != nil {
		t.Fatal(err)
	}
	loaded := LoadForEdit(path)
	got, _ := loaded.Provider("opencode-go")
	if got.ModelOverrides["glm-5.2"].ContextWindow != 1_000_000 {
		t.Fatalf("in-memory migration missing: %+v", got.ModelOverrides)
	}
	var disk Config
	if _, err := toml.DecodeFile(path, &disk); err != nil {
		t.Fatal(err)
	}
	persisted, _ := disk.Provider("opencode-go")
	if persisted.ModelOverrides["glm-5.2"].ContextWindow != 0 {
		t.Fatalf("read-only load wrote disk: %+v", persisted.ModelOverrides)
	}
}

func TestNormalizeLegacyOpenCodeGoRouteCatalogSplitsAggregateModelsAndRetargetsHistory(t *testing.T) {
	legacy := ProviderEntry{
		Name:          "opencode-go",
		Kind:          "openai",
		BaseURL:       "https://opencode.ai/zen/go/v1",
		Models:        []string{"glm-5.2", "qwen3.7-plus", "grok-4.5", "private-model"},
		Default:       "grok-4.5",
		APIKeyEnv:     "OPENCODE_GO_API_KEY",
		PresetID:      "opencode-go",
		ContextWindow: legacyOpenCodeGoChatWindow,
		Headers:       map[string]string{"X-User": "keep"},
	}
	c := &Config{
		DefaultModel: "opencode-go/grok-4.5",
		Agent: AgentConfig{
			PlannerModel:   "grok-4.5",
			VisionModel:    "opencode-go/grok-4.5",
			SubagentModel:  "opencode-go/qwen3.7-plus",
			SubagentModels: map[string]string{"review": "grok-4.5", "local": "private-model"},
		},
		Bot:       BotConfig{Model: "opencode-go/grok-4.5"},
		Desktop:   DesktopConfig{ProviderAccess: []string{"opencode-go"}},
		Providers: []ProviderEntry{legacy},
	}
	if !normalizeLegacyOpenCodeGoInstalls(c) {
		t.Fatal("aggregate OpenCode Go route migration did not report a change")
	}
	chat, ok := c.Provider("opencode-go")
	if !ok || !chat.HasModel("glm-5.2") || !chat.HasModel("private-model") || chat.HasModel("grok-4.5") {
		t.Fatalf("chat provider after split = %+v", chat)
	}
	if chat.BillingMode != "subscription_equivalent" || chat.Headers["X-User"] != "keep" {
		t.Fatalf("chat provider lost preserved metadata = %+v", chat)
	}
	responses, ok := c.Provider("opencode-go-responses")
	if !ok || responses.Kind != "responses" || responses.DefaultModel() != "grok-4.5" || !responses.HasModel("grok-4.5") || responses.HasModel("qwen3.7-plus") {
		t.Fatalf("responses provider after split = %+v", responses)
	}
	if c.DefaultModel != "opencode-go-responses/grok-4.5" || c.Agent.PlannerModel != "opencode-go-responses/grok-4.5" || c.Agent.VisionModel != "opencode-go-responses/grok-4.5" || c.Agent.SubagentModel != "opencode-go-anthropic/qwen3.7-plus" || c.Bot.Model != "opencode-go-responses/grok-4.5" {
		t.Fatalf("historical model refs = default:%q planner:%q vision:%q subagent:%q bot:%q", c.DefaultModel, c.Agent.PlannerModel, c.Agent.VisionModel, c.Agent.SubagentModel, c.Bot.Model)
	}
	if len(c.Agent.SubagentModels) != 2 || c.Agent.SubagentModels["review"] != "opencode-go-responses/grok-4.5" || c.Agent.SubagentModels["local"] != "private-model" {
		t.Fatalf("historical subagent refs = %v", c.Agent.SubagentModels)
	}
	access := desktopProviderAccessMap(c.Desktop.ProviderAccess)
	if !access["opencode-go"] || !access["opencode-go-responses"] {
		t.Fatalf("provider access after split = %v", c.Desktop.ProviderAccess)
	}
	if normalizeLegacyOpenCodeGoInstalls(c) {
		t.Fatal("aggregate OpenCode Go route migration was not idempotent")
	}
}

func TestNormalizeLegacyOpenCodeGoRouteCatalogRepairsWrongSingleRouteInPlace(t *testing.T) {
	c := &Config{Providers: []ProviderEntry{{
		Name: "opencode-go", Kind: "anthropic", BaseURL: "https://opencode.ai/zen/go",
		Models: []string{"grok-4.5"}, Default: "grok-4.5", APIKeyEnv: "OPENCODE_GO_API_KEY",
	}}}
	if !normalizeLegacyOpenCodeGoRouteCatalog(c) {
		t.Fatal("wrong single-route OpenCode Go config was not migrated")
	}
	entry := c.Providers[0]
	if entry.Kind != "responses" || entry.BaseURL != "https://opencode.ai/zen/go/v1" || entry.DefaultModel() != "grok-4.5" || entry.PresetID != "opencode-go-responses" || entry.PresetVersion != ProviderPresetVersion || entry.BillingMode != "" {
		t.Fatalf("wrong single-route migration = %+v", entry)
	}
}

func TestNormalizeLegacyOpenCodeGoRouteCatalogLeavesCustomRequestURLUntouched(t *testing.T) {
	original := ProviderEntry{
		Name: "opencode-go", Kind: "anthropic", BaseURL: "https://opencode.ai/zen/go",
		RequestURL: "https://proxy.example/messages", Models: []string{"grok-4.5"}, Default: "grok-4.5",
	}
	c := &Config{Providers: []ProviderEntry{original}}
	if normalizeLegacyOpenCodeGoRouteCatalog(c) {
		t.Fatal("custom request_url provider was migrated")
	}
	if got := c.Providers[0]; got.Kind != original.Kind || got.BaseURL != original.BaseURL || got.RequestURL != original.RequestURL || !stringSlicesEqual(got.Models, original.Models) {
		t.Fatalf("custom request_url provider changed = %+v", got)
	}
}

func TestOpenCodeGoShippedChatPresetMovesMiniMaxM27ToAnthropicSibling(t *testing.T) {
	for _, shipped := range shippedMiniMaxChatOpenCodeGoModels {
		installed := ProviderEntry{
			Name: "opencode-go", Kind: "openai", BaseURL: "https://opencode.ai/zen/go/v1",
			Models: append([]string(nil), shipped...), Default: "minimax-m2.7",
			APIKeyEnv: "OPENCODE_GO_API_KEY", PresetID: "opencode-go",
		}
		c := &Config{ConfigVersion: openCodeGoUpgradeVersion, DefaultModel: "opencode-go/minimax-m2.7", Providers: []ProviderEntry{installed}}
		if !normalizeLegacyOpenCodeGoInstalls(c) {
			t.Fatal("shipped chat preset kept minimax-m2.7 on the Chat Completions route")
		}
		chat, _ := c.Provider("opencode-go")
		anthropic, ok := c.Provider("opencode-go-anthropic")
		if !ok || anthropic.Kind != "anthropic" || anthropic.BaseURL != "https://opencode.ai/zen/go" || !stringSlicesEqual(anthropic.ModelList(), []string{"minimax-m2.7", "qwen3.6-plus", "qwen3.7-max", "qwen3.7-plus", "qwen3.8-max"}) {
			t.Fatalf("anthropic sibling = %+v, %t", anthropic, ok)
		}
		for _, id := range []string{"qwen3.6-plus", "qwen3.7-max", "qwen3.7-plus", "qwen3.8-max"} {
			if !anthropic.HasModel(id) {
				t.Errorf("%s left the Anthropic connection", id)
			}
		}
		if !chat.HasModel("glm-5.3") {
			t.Error("glm-5.3 left the Chat Completions connection")
		}
		if chat.HasModel("minimax-m2.7") || c.DefaultModel != "opencode-go-anthropic/minimax-m2.7" {
			t.Fatalf("chat=%v default=%q", chat.ModelList(), c.DefaultModel)
		}
	}
	if preset, _ := CuratedProviderPreset("opencode-go"); preset.Entries[0].HasModel("minimax-m2.7") {
		t.Fatal("Chat Completions preset ships minimax-m2.7")
	}
}

func TestOpenCodeGoManualConnectionFollowsRecommendedRoutes(t *testing.T) {
	cases := []struct {
		models      []string
		wantKinds   []string
		wantModels  [][]string
		wantDefault string
	}{
		{
			models:      []string{"glm-5.3", "qwen3.7-plus"},
			wantKinds:   []string{"openai", "anthropic"},
			wantModels:  [][]string{{"glm-5.3"}, {"qwen3.7-plus"}},
			wantDefault: "custom-opencode-ai-anthropic/qwen3.7-plus",
		},
		{
			models:      []string{"qwen3.7-plus"},
			wantKinds:   []string{"anthropic"},
			wantModels:  [][]string{{"qwen3.7-plus"}},
			wantDefault: "custom-opencode-ai/qwen3.7-plus",
		},
		{
			models:      []string{"glm-5.3", "minimax-m2.7"},
			wantKinds:   []string{"openai"},
			wantModels:  [][]string{{"glm-5.3", "minimax-m2.7"}},
			wantDefault: "custom-opencode-ai/minimax-m2.7",
		},
		{
			models:      []string{"minimax-m2.7"},
			wantKinds:   []string{"openai"},
			wantModels:  [][]string{{"minimax-m2.7"}},
			wantDefault: "custom-opencode-ai/minimax-m2.7",
		},
	}
	for _, tc := range cases {
		manual := ProviderEntry{
			Name: "custom-opencode-ai", Kind: "openai", BaseURL: "https://opencode.ai/zen/go/v1",
			Models: append([]string(nil), tc.models...), Default: tc.models[len(tc.models)-1],
		}
		ref := "custom-opencode-ai/" + manual.Default
		c := &Config{ConfigVersion: openCodeGoUpgradeVersion, DefaultModel: ref, Providers: []ProviderEntry{manual}}
		normalizeLegacyOpenCodeGoInstalls(c)
		if len(c.Providers) != len(tc.wantKinds) {
			t.Fatalf("%v: providers=%d, want %d", tc.models, len(c.Providers), len(tc.wantKinds))
		}
		for i, kind := range tc.wantKinds {
			got := c.Providers[i]
			if got.Kind != kind || !stringSlicesEqual(got.ModelList(), tc.wantModels[i]) {
				t.Errorf("%v: provider[%d] kind=%q models=%v, want %q %v", tc.models, i, got.Kind, got.ModelList(), kind, tc.wantModels[i])
			}
		}
		if c.DefaultModel != tc.wantDefault {
			t.Errorf("%v: default=%q, want %q", tc.models, c.DefaultModel, tc.wantDefault)
		}

		legacy := &Config{ConfigVersion: openCodeGoUpgradeVersion - 1, DefaultModel: ref, Providers: []ProviderEntry{manual}}
		planOpenCodeGoUpgrade(legacy)
		present := map[string]bool{}
		for _, p := range legacy.Providers {
			for _, m := range p.ModelList() {
				present[m] = true
			}
		}
		for _, m := range tc.models {
			if !present[m] {
				t.Errorf("%v: journaled upgrade dropped the manual model %q", tc.models, m)
			}
		}
	}
}
