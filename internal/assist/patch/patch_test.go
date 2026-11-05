package patch

import (
	"strings"
	"testing"

	"pondera/internal/config"
)

func testConfig() *config.Config {
	return config.Default()
}

func envelope(ops ...string) string {
	return `{"version":1,"intent":"测试","reply":"测试","ops":[` + strings.Join(ops, ",") + `]}`
}

// TestIllegalPatchesAreRejected covers the twelve rejection classes required by
// the MVP acceptance criteria. Each case asserts the machine-readable reason so
// the UI can show a readable explanation.
func TestIllegalPatchesAreRejected(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		reason string
	}{
		{
			name:   "unknown path",
			raw:    envelope(`{"op":"set","path":"agent.unknown_knob","value":1}`),
			reason: "unknown_path",
		},
		{
			name:   "denied security group",
			raw:    envelope(`{"op":"set","path":"serve.token","value":"x"}`),
			reason: "denied_by_policy",
		},
		{
			name:   "type mismatch",
			raw:    envelope(`{"op":"set","path":"agent.retry_count","value":"3"}`),
			reason: "type_mismatch",
		},
		{
			name:   "retry out of range",
			raw:    envelope(`{"op":"set","path":"agent.retry_count","value":5000}`),
			reason: "out_of_range",
		},
		{
			name:   "compact ratio out of range",
			raw:    envelope(`{"op":"set","path":"agent.compact_ratio","value":0.3}`),
			reason: "out_of_range",
		},
		{
			name:   "unresolvable default model",
			raw:    envelope(`{"op":"set","path":"default_model","value":"nope/nope"}`),
			reason: "model_unresolved",
		},
		{
			name:   "invalid credential key",
			raw:    envelope(`{"op":"set_secret","api_key_env":"deepseek_api_key","value":"sk-abc"}`),
			reason: "invalid_credential_key",
		},
		{
			name:   "unreferenced credential",
			raw:    envelope(`{"op":"set_secret","api_key_env":"OPENAI_API_KEY","value":"sk-abc"}`),
			reason: "credential_unreferenced",
		},
		{
			name:   "unknown op rename_provider",
			raw:    envelope(`{"op":"rename_provider","provider":"x","value":"y"}`),
			reason: "unknown_op",
		},
		{
			name:   "add provider scalar value",
			raw:    envelope(`{"op":"add_provider","provider":"x","value":"y"}`),
			reason: "invalid_value",
		},
		{
			name:   "extra envelope field",
			raw:    `{"version":1,"intent":"x","reply":"y","ops":[{"op":"set","path":"default_model","value":"deepseek-flash"}],"shell":"rm -rf /"}`,
			reason: "invalid_envelope",
		},
		{
			name:   "extra op field",
			raw:    envelope(`{"op":"set","path":"default_model","value":"deepseek-flash","command":"rm -rf /"}`),
			reason: "invalid_envelope",
		},
		{
			name:   "whole TOML value",
			raw:    envelope(`{"op":"set","path":"default_model","value":"default_model = \"x\""}`),
			reason: "invalid_value",
		},
		{
			name:   "multiline value",
			raw:    envelope(`{"op":"set","path":"default_model","value":"a\nb"}`),
			reason: "invalid_value",
		},
		{
			name:   "too many ops",
			raw:    `{"version":1,"intent":"x","reply":"y","ops":[` + strings.Repeat(`{"op":"set","path":"agent.temperature","value":0.5},`, 11) + `{"op":"set","path":"agent.temperature","value":0.5}]}`,
			reason: "invalid_envelope",
		},
		{
			name:   "duplicate target",
			raw:    envelope(`{"op":"set","path":"agent.temperature","value":0.5}`, `{"op":"set","path":"agent.temperature","value":0.7}`),
			reason: "duplicate_op",
		},
		{
			name:   "provider field denied",
			raw:    envelope(`{"op":"set_provider_field","provider":"deepseek-flash","field":"api_key_env","value":"X"}`),
			reason: "denied_by_policy",
		},
		{
			name:   "provider missing",
			raw:    envelope(`{"op":"set_provider_field","provider":"ghost","field":"model","value":"m"}`),
			reason: "provider_unregistered",
		},
		{
			name:   "value in providers is a table",
			raw:    envelope(`{"op":"set","path":"default_model","value":{"a":1}}`),
			reason: "invalid_value",
		},
	}

	cfg := testConfig()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.raw)
			if err == nil {
				_, err = Validate(mustParse(t, tc.raw), cfg)
			}
			if err == nil {
				t.Fatalf("expected rejection %s, got nil", tc.reason)
			}
			var rejection *Rejection
			if !asRejection(err, &rejection) {
				t.Fatalf("expected *Rejection, got %T: %v", err, err)
			}
			if rejection.Reason != tc.reason {
				t.Fatalf("reason = %q, want %q (message: %s)", rejection.Reason, tc.reason, rejection.Message)
			}
			if strings.TrimSpace(rejection.Message) == "" {
				t.Fatalf("rejection message must be readable")
			}
		})
	}
}

func mustParse(t *testing.T, raw string) *Envelope {
	t.Helper()
	env, err := Parse(raw)
	if err != nil {
		return nil
	}
	return env
}

func asRejection(err error, target **Rejection) bool {
	for err != nil {
		if r, ok := err.(*Rejection); ok {
			*target = r
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func TestValidEnvelopeResolvesAndRedactsSecrets(t *testing.T) {
	cfg := testConfig()
	env, err := Parse(envelope(
		`{"op":"set","path":"agent.temperature","value":0.4}`,
		`{"op":"set_provider_field","provider":"deepseek-flash","field":"context_window","value":1000000}`,
		`{"op":"set_secret","api_key_env":"DEEPSEEK_API_KEY","value":"sk-super-secret-value-0001"}`,
	))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ops, err := Validate(env, cfg)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(ops) != 3 {
		t.Fatalf("ops = %d, want 3", len(ops))
	}
	diff := RenderDiff(ops)
	if len(diff) != 3 {
		t.Fatalf("diff rows = %d, want 3", len(diff))
	}
	for _, row := range diff {
		if strings.Contains(row.After, "sk-super-secret-value-0001") || strings.Contains(row.Before, "sk-super-secret-value-0001") {
			t.Fatalf("secret leaked into diff row: %+v", row)
		}
	}
	if !diff[2].Sensitive {
		t.Fatalf("secret diff row must be marked sensitive")
	}
	if err := Apply(cfg, ops); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if cfg.Agent.Temperature != 0.4 {
		t.Fatalf("temperature = %v, want 0.4", cfg.Agent.Temperature)
	}
	if cfg.Providers[0].ContextWindow != 1000000 {
		t.Fatalf("context window not applied")
	}
}

func TestEmptyOpsAllowedForExplanation(t *testing.T) {
	env, err := Parse(`{"version":1,"intent":"解释","reply":"context_window 是什么","ops":[]}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, err := Validate(env, testConfig()); err != nil {
		t.Fatalf("validate: %v", err)
	}
}

func TestEmptyReplyWithNoOpsRejected(t *testing.T) {
	_, err := Parse(`{"version":1,"intent":"x","reply":"","ops":[]}`)
	if err == nil {
		t.Fatalf("expected rejection")
	}
}
