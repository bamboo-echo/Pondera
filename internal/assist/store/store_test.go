package store

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestLoadMissingUsesDefaults(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	cfg, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Endpoint != "" || cfg.Model != "" || cfg.Kind != "" {
		t.Fatalf("missing file must load empty settings, got %+v", cfg)
	}
	if cfg.CredentialKey != CredentialKey {
		t.Fatalf("CredentialKey = %q, want %q", cfg.CredentialKey, CredentialKey)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := &Store{Dir: dir}
	saved := Config{
		Endpoint: " https://api.example.com/v1/ ",
		Kind:     " OPENAI ",
		Model:    " gpt-4o ",
	}
	if err := s.Save(saved); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(s.Path()); err != nil {
		t.Fatalf("assistant.json not written: %v", err)
	}

	loaded, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Endpoint != "https://api.example.com/v1" {
		t.Errorf("Endpoint = %q, want trailing slash trimmed", loaded.Endpoint)
	}
	if loaded.Kind != "openai" {
		t.Errorf("Kind = %q, want lower-case openai", loaded.Kind)
	}
	if loaded.Model != "gpt-4o" {
		t.Errorf("Model = %q, want trimmed model", loaded.Model)
	}
	if loaded.CredentialKey != CredentialKey {
		t.Errorf("CredentialKey = %q, want %q", loaded.CredentialKey, CredentialKey)
	}
	if _, err := time.Parse(time.RFC3339, loaded.UpdatedAt); err != nil {
		t.Errorf("UpdatedAt %q is not RFC3339: %v", loaded.UpdatedAt, err)
	}
}

func TestSaveStampsUpdatedAtOnEveryWrite(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	cfg := Config{Endpoint: "https://api.example.com/v1", Model: "m"}
	if err := s.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	first, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if first.UpdatedAt == "" {
		t.Fatal("first save did not stamp UpdatedAt")
	}

	// A second save always refreshes the timestamp; sleep past the RFC3339
	// second granularity so the assertion cannot be flaky on fast machines.
	time.Sleep(1100 * time.Millisecond)
	if err := s.Save(Config{Endpoint: "https://api.example.com/v2", Model: "m2"}); err != nil {
		t.Fatalf("second Save: %v", err)
	}
	second, err := s.Load()
	if err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if second.UpdatedAt == first.UpdatedAt {
		t.Fatalf("UpdatedAt did not advance: %q", second.UpdatedAt)
	}
	if second.Endpoint != "https://api.example.com/v2" || second.Model != "m2" {
		t.Fatalf("second save did not replace settings: %+v", second)
	}
}

func TestSaveRejectsInvalidSettings(t *testing.T) {
	valid := func() Config {
		return Config{Endpoint: "https://api.example.com/v1", Kind: "openai", Model: "m"}
	}
	cases := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"non http scheme", func(c *Config) { c.Endpoint = "ftp://api.example.com" }, "必须"},
		{"missing host", func(c *Config) { c.Endpoint = "https:///v1" }, "主机名"},
		{"embedded credentials", func(c *Config) { c.Endpoint = "https://user:pass@api.example.com" }, "内嵌账号密码"},
		{"fragment", func(c *Config) { c.Endpoint = "https://api.example.com/v1#frag" }, "fragment"},
		{"unknown kind", func(c *Config) { c.Kind = "gemini" }, "不支持的接口类型"},
		{"empty model", func(c *Config) { c.Model = "   " }, "模型名不能为空"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Store{Dir: t.TempDir()}
			cfg := valid()
			tc.mutate(&cfg)
			if err := s.Save(cfg); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Save error = %v, want %q", err, tc.want)
			}
			if _, err := os.Stat(s.Path()); !os.IsNotExist(err) {
				t.Fatalf("invalid settings must not create assistant.json, stat err = %v", err)
			}
		})
	}
}

func TestLoadCorruptFileFails(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(s.Path(), []byte("{"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := s.Load(); err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("Load error = %v, want corrupt", err)
	}
}

func TestStoreUnavailableWithoutHome(t *testing.T) {
	s := &Store{}
	if s.Path() != "" {
		t.Fatalf("Path() = %q, want empty", s.Path())
	}
	if _, err := s.Load(); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("Load error = %v, want unavailable", err)
	}
	if err := s.Save(Config{Endpoint: "https://api.example.com/v1", Model: "m"}); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("Save error = %v, want unavailable", err)
	}
	var nilStore *Store
	if nilStore.Path() != "" {
		t.Fatalf("nil Store Path() = %q, want empty", nilStore.Path())
	}
}

func TestAPIKeyHelpersDoNotLeak(t *testing.T) {
	if err := SetAPIKey(Config{}, "   "); err == nil || !strings.Contains(err.Error(), "不能为空") {
		t.Fatalf("empty key error = %v", err)
	}
	if err := SetAPIKey(Config{}, "abc\ndef"); err == nil || !strings.Contains(err.Error(), "换行") {
		t.Fatalf("newline key error = %v", err)
	}

	const envName = "PONDERA_ASSIST_TEST_GET_KEY"
	cfg := Config{CredentialKey: envName}
	t.Setenv(envName, "")
	if _, err := GetAPIKey(cfg); err == nil || !strings.Contains(err.Error(), "未设置") {
		t.Fatalf("missing key error = %v", err)
	}
	t.Setenv(envName, "   ")
	if _, err := GetAPIKey(cfg); err == nil || !strings.Contains(err.Error(), "未设置") {
		t.Fatalf("blank key error = %v", err)
	}
	t.Setenv(envName, " value ")
	if got, err := GetAPIKey(cfg); err != nil || got != "value" {
		t.Fatalf("GetAPIKey = %q, %v; want trimmed value", got, err)
	}
}
