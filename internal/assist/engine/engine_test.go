package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"pondera/internal/assist/registry"
	"pondera/internal/config"
)

const redactedMarker = "[已脱敏]"

func TestRedactCredentialShapes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"sk token", "key sk-abcdefghijklmnop1234 end", "key " + redactedMarker + " end"},
		{"bearer header", "Authorization: Bearer abcdefghijklmnop.qrst end", "Authorization: " + redactedMarker + " end"},
		{"github token", "token ghp_abcdefghijklmnopqrst end", "token " + redactedMarker + " end"},
		{"plain text", "no credentials here", "no credentials here"},
		{"short sk prefix", "sk-short", "sk-short"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Redact(tc.in, nil); got != tc.want {
				t.Fatalf("Redact(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRedactConfiguredEnvValue(t *testing.T) {
	const envName = "PONDERA_ASSIST_TEST_KEY"
	t.Setenv(envName, "super-secret-value-123")
	cfg := &config.Config{Providers: []config.ProviderEntry{{APIKeyEnv: envName}}}

	got := Redact("the key is super-secret-value-123", cfg)
	if !strings.Contains(got, redactedMarker) {
		t.Fatalf("configured key value was not redacted: %q", got)
	}
	if strings.Contains(got, "super-secret-value-123") {
		t.Fatalf("configured key value leaked: %q", got)
	}

	// Values shorter than 8 bytes are not treated as credentials, so common
	// words such as "short" are not mangled.
	t.Setenv(envName, "short")
	if got := Redact("a short note", cfg); got != "a short note" {
		t.Fatalf("short env value must stay untouched, got %q", got)
	}
}

func TestSystemPromptContract(t *testing.T) {
	prompt := SystemPrompt()
	for _, want := range []string{
		`{"version":1`,
		`"op":"set"`,
		`"op":"set_provider_field"`,
		`"op":"set_secret"`,
		"硬规则",
		"serve/bot/network/sandbox/permissions/secrets/plugins/statusline",
		strings.Join(registry.ProviderWritableFields, ", "),
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("system prompt missing %q", want)
		}
	}
	if got := strings.Count(prompt, "providers[]."); got != 1 {
		t.Fatalf("system prompt lists %d providers[] paths, want exactly the <field> summary", got)
	}
	writable := registry.Default().Writable()
	for _, f := range writable {
		if strings.HasPrefix(f.Path, "providers[].") {
			continue
		}
		if !strings.Contains(prompt, f.Path) {
			t.Fatalf("system prompt missing writable path %q", f.Path)
		}
	}
}

func TestUserPromptCarriesSnapshotAndRequest(t *testing.T) {
	snap := registry.Snapshot{
		DefaultModel: "gpt-x",
		Language:     "zh",
		Providers: []registry.ProviderView{{
			Name:       "main",
			Kind:       "openai",
			Model:      "gpt-x",
			APIKeyEnv:  "PONDERA_MAIN_KEY",
			Credential: "已设置",
		}},
	}
	got := UserPrompt(snap, "把默认模型改成 gpt-x")
	for _, want := range []string{
		"当前配置快照",
		`"default_model": "gpt-x"`,
		`"api_key_env": "PONDERA_MAIN_KEY"`,
		`"credential": "已设置"`,
		"把默认模型改成 gpt-x",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("user prompt missing %q:\n%s", want, got)
		}
	}
}

func TestProviderClientCompleteRejectsIncompleteConfig(t *testing.T) {
	cases := []struct {
		name     string
		client   ProviderClient
		wantText string
	}{
		{"missing endpoint", ProviderClient{Model: "m", APIKey: "k"}, "端点或模型未配置"},
		{"missing model", ProviderClient{Endpoint: "http://x", APIKey: "k"}, "端点或模型未配置"},
		{"blank fields", ProviderClient{Endpoint: "  ", Model: "  ", APIKey: "  "}, "端点或模型未配置"},
		{"missing key", ProviderClient{Endpoint: "http://x", Model: "m"}, "API key 未设置"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tc.client.Complete(context.Background(), "s", "u"); err == nil || !strings.Contains(err.Error(), tc.wantText) {
				t.Fatalf("Complete error = %v, want %q", err, tc.wantText)
			}
		})
	}
}

func TestCompleteFuncDelegates(t *testing.T) {
	sentinel := errors.New("stop")
	var gotSystem, gotUser string
	client := CompleteFunc(func(_ context.Context, system, user string) (string, error) {
		gotSystem, gotUser = system, user
		return "", sentinel
	})
	if _, err := client.Complete(context.Background(), "sys", "usr"); !errors.Is(err, sentinel) {
		t.Fatalf("Complete error = %v, want sentinel", err)
	}
	if gotSystem != "sys" || gotUser != "usr" {
		t.Fatalf("delegate saw system=%q user=%q", gotSystem, gotUser)
	}
}
