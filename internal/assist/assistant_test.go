package assist

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pondera/internal/assist/engine"
	"pondera/internal/assist/store"
	"pondera/internal/assist/writer"
	"pondera/internal/config"
)

const assistSeedTOML = `default_model = "deepseek-flash"

[agent]
temperature = 0.2

[[providers]]
name = "deepseek-flash"
kind = "openai"
base_url = "https://api.deepseek.com"
model = "deepseek-flash"
api_key_env = "DEEPSEEK_API_KEY"
context_window = 1000000
`

func assistSeed(t *testing.T) (string, *Assistant) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("PONDERA_HOME", home)
	t.Setenv("DEEPSEEK_API_KEY", "sk-live-secret-value-1234")
	path := config.UserConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(assistSeedTOML), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a := New()
	a.Store = &store.Store{Dir: filepath.Join(home, "assistant")}
	a.Writer = &writer.Writer{ConfigPath: path, BackupRoot: filepath.Join(home, "assistant", "backups")}
	return path, a
}

// TestChatRedactsSecretsFromModelRequest is the redaction acceptance check:
// neither a known credential value nor a newly supplied one may reach the
// model request body, plan preview or history.
func TestChatRedactsSecretsFromModelRequest(t *testing.T) {
	_, a := assistSeed(t)
	var capturedUser string
	a.Client = engine.CompleteFunc(func(ctx context.Context, system, user string) (string, error) {
		capturedUser = user
		return `{"version":1,"intent":"设置密钥","reply":"已准备","ops":[{"op":"set_secret","api_key_env":"DEEPSEEK_API_KEY","value":"sk-new-secret-0002"}]}`, nil
	})

	view, err := a.Chat(context.Background(), "我的旧 key 是 sk-live-secret-value-1234，帮我换一个")
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if strings.Contains(capturedUser, "sk-live-secret-value-1234") {
		t.Fatalf("existing secret leaked into model request")
	}
	if view.Plan == nil {
		t.Fatalf("expected a plan, got %+v", view)
	}
	for _, row := range view.Plan.Diff {
		if strings.Contains(row.After, "sk-new-secret-0002") || strings.Contains(row.Before, "sk-new-secret-0002") {
			t.Fatalf("new secret leaked into preview: %+v", row)
		}
	}

	result, err := a.ApplyPlan(context.Background(), view.Plan.ID)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if result.AppliedOps != 1 {
		t.Fatalf("applied ops = %d", result.AppliedOps)
	}
	if got := os.Getenv("DEEPSEEK_API_KEY"); got != "sk-new-secret-0002" {
		t.Fatalf("credential not written through SetCredential: %q", got)
	}
	cfg, err := config.LoadForEditWithoutCredentialsReadOnlyStrict(config.UserConfigPath())
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if cfg.Providers[0].APIKeyEnv != "DEEPSEEK_API_KEY" {
		t.Fatalf("api_key_env changed unexpectedly")
	}
	rawConfig, _ := os.ReadFile(config.UserConfigPath())
	if strings.Contains(string(rawConfig), "sk-new-secret-0002") {
		t.Fatalf("config.toml must never contain a credential value")
	}
	records, err := a.History()
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(records) == 0 {
		t.Fatalf("expected a history record")
	}
	for _, rec := range records {
		for _, row := range rec.Diff {
			if strings.Contains(row.After, "sk-new-secret-0002") || strings.Contains(row.Before, "sk-new-secret-0002") {
				t.Fatalf("secret leaked into history")
			}
		}
	}
}

func TestChatRejectionIsReadable(t *testing.T) {
	_, a := assistSeed(t)
	a.Client = engine.CompleteFunc(func(ctx context.Context, system, user string) (string, error) {
		return `{"version":1,"intent":"改端口","reply":"试试","ops":[{"op":"set","path":"serve.port","value":8080}]}`, nil
	})
	view, err := a.Chat(context.Background(), "把端口改成 8080")
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if view.Rejection == nil {
		t.Fatalf("expected a rejection")
	}
	if view.Rejection.Reason != "denied_by_policy" {
		t.Fatalf("reason = %q", view.Rejection.Reason)
	}
	if strings.TrimSpace(view.Rejection.Message) == "" {
		t.Fatalf("rejection message must be readable")
	}
}

func TestExplanationWithoutOps(t *testing.T) {
	_, a := assistSeed(t)
	a.Client = engine.CompleteFunc(func(ctx context.Context, system, user string) (string, error) {
		return `{"version":1,"intent":"解释","reply":"context_window 决定何时压缩上下文。","ops":[]}`, nil
	})
	view, err := a.Chat(context.Background(), "context_window 是什么")
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if view.Plan != nil {
		t.Fatalf("explanation must not produce a plan")
	}
	if !strings.Contains(view.Reply, "context_window") {
		t.Fatalf("reply = %q", view.Reply)
	}
}

func TestApplyRequiresLivePlan(t *testing.T) {
	_, a := assistSeed(t)
	if _, err := a.ApplyPlan(context.Background(), "not-a-plan"); err == nil {
		t.Fatalf("expected plan_expired error")
	} else if !strings.Contains(err.Error(), "plan_expired") {
		t.Fatalf("error = %v", err)
	}
}

func TestStalePlanPropagatesToFacade(t *testing.T) {
	path, a := assistSeed(t)
	a.Client = engine.CompleteFunc(func(ctx context.Context, system, user string) (string, error) {
		return `{"version":1,"intent":"温度","reply":"改温度","ops":[{"op":"set","path":"agent.temperature","value":0.6}]}`, nil
	})
	view, err := a.Chat(context.Background(), "把温度改成 0.6")
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if err := config.EditConfigFile(path, func(cfg *config.Config) error {
		cfg.Agent.RetryCount = 9
		return nil
	}); err != nil {
		t.Fatalf("concurrent save: %v", err)
	}
	_, err = a.ApplyPlan(context.Background(), view.Plan.ID)
	if !errors.Is(err, writer.ErrStale) {
		t.Fatalf("expected stale plan, got %v", err)
	}
}
