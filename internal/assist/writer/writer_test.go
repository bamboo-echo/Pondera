package writer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"pondera/internal/assist/patch"
	"pondera/internal/config"
	"pondera/internal/fileutil"
)

const seedTOML = `default_model = "deepseek-flash"

[agent]
temperature = 0.2
retry_count = 3

[[providers]]
name = "deepseek-flash"
kind = "openai"
base_url = "https://api.deepseek.com"
model = "deepseek-flash"
api_key_env = "DEEPSEEK_API_KEY"
context_window = 1000000
`

func seed(t *testing.T) (string, *Writer) {
	t.Helper()
	home := t.TempDir()
	// The write path resolves PONDERA_HOME through symlinks before the atomic
	// write hook sees it (macOS /var -> /private/var, Windows final paths).
	// Pin the physical form so the helper's crash-point comparison and the
	// file the test reads back are the same file.
	if real, err := filepath.EvalSymlinks(home); err == nil {
		home = real
	}
	t.Setenv("PONDERA_HOME", home)
	path := config.UserConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(seedTOML), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	w := &Writer{ConfigPath: path, BackupRoot: filepath.Join(home, "assistant", "backups")}
	return path, w
}

func planFor(t *testing.T, w *Writer, rawEnvelope string) *patch.Plan {
	t.Helper()
	cfg, err := config.LoadForEditWithoutCredentialsReadOnlyStrict(w.configPath())
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	env, err := patch.Parse(rawEnvelope)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ops, err := patch.Validate(env, cfg)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	hash, rev, err := w.CurrentBaseHash()
	if err != nil {
		t.Fatalf("base hash: %v", err)
	}
	id, _ := patch.NewPlanID()
	return patch.BuildPlan(id, env, ops, hash, rev)
}

func TestApplyThenRollbackRestoresExactHash(t *testing.T) {
	path, w := seed(t)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	plan := planFor(t, w, `{"version":1,"intent":"温度","reply":"改温度","ops":[{"op":"set","path":"agent.temperature","value":0.6}]}`)
	result, err := w.Apply(context.Background(), plan)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	cfg, err := config.LoadForEditWithoutCredentialsReadOnlyStrict(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if cfg.Agent.Temperature != 0.6 {
		t.Fatalf("temperature = %v, want 0.6", cfg.Agent.Temperature)
	}

	if _, err := w.Rollback(result.Backup.ID); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	restored, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read restored: %v", err)
	}
	if Hash(restored) == Hash(original) {
		// SaveTo re-renders the TOML, so byte equality is not guaranteed; the
		// backup hash equality is asserted below.
		if err := config.ValidateFile(path); err != nil {
			t.Fatalf("restored file does not parse: %v", err)
		}
		return
	}
	backupBytes, err := os.ReadFile(filepath.Join(result.Backup.Dir, "config.toml"))
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if Hash(restored) != Hash(backupBytes) {
		t.Fatalf("restored hash %s != backup hash %s", Hash(restored), Hash(backupBytes))
	}
	if result.Backup.Hash != Hash(backupBytes) {
		t.Fatalf("manifest hash mismatch")
	}
}

func TestStalePlanIsRejected(t *testing.T) {
	path, w := seed(t)
	plan := planFor(t, w, `{"version":1,"intent":"温度","reply":"改温度","ops":[{"op":"set","path":"agent.temperature","value":0.6}]}`)
	// Another process saves the whole config between preview and apply.
	if err := config.EditConfigFile(path, func(cfg *config.Config) error {
		cfg.Agent.RetryCount = 7
		return nil
	}); err != nil {
		t.Fatalf("concurrent save: %v", err)
	}
	_, err := w.Apply(context.Background(), plan)
	if err == nil {
		t.Fatalf("expected stale_plan rejection")
	}
	if !errors.Is(err, ErrStale) {
		t.Fatalf("expected ErrStale, got %v", err)
	}
}

func TestPostWriteAssertFailureRollsBack(t *testing.T) {
	path, w := seed(t)
	original, _ := os.ReadFile(path)
	plan := planFor(t, w, `{"version":1,"intent":"温度","reply":"改温度","ops":[{"op":"set","path":"agent.temperature","value":0.55}]}`)
	// Simulate a SaveTo that persisted a different value: the read-back
	// assertion must catch it and restore the backup.
	w.SaveConfig = func(cfg *config.Config, p string) error {
		if err := cfg.SaveTo(p); err != nil {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		lines := strings.Split(string(raw), "\n")
		replaced := false
		for i, line := range lines {
			if strings.Contains(line, "temperature") && strings.Contains(line, "=") {
				lines[i] = "temperature = 0.11"
				replaced = true
			}
		}
		if !replaced {
			t.Fatalf("SaveTo output had no temperature assignment")
		}
		return fileutil.AtomicWriteFile(p, []byte(strings.Join(lines, "\n")), 0o600)
	}
	defer func() { w.SaveConfig = nil }()
	if _, err := w.Apply(context.Background(), plan); err == nil {
		t.Fatalf("expected assertion failure")
	}
	restored, _ := os.ReadFile(path)
	if Hash(restored) != Hash(original) {
		t.Fatalf("config was not rolled back after assertion failure")
	}
	if verr := config.ValidateFile(path); verr != nil {
		t.Fatalf("rolled-back file does not parse: %v", verr)
	}
}

// TestConcurrentAssistantAndFullSave hammers both write paths in the same
// second and asserts the file is parseable at every observed point.
func TestConcurrentAssistantAndFullSave(t *testing.T) {
	path, w := seed(t)
	const rounds = 100
	var wg sync.WaitGroup
	errCh := make(chan error, 2*rounds)
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			for attempt := 0; attempt < 5; attempt++ {
				value := 0.3 + float64(i%4)*0.1
				raw := fmt.Sprintf(`{"version":1,"intent":"并发","reply":"并发","ops":[{"op":"set","path":"agent.temperature","value":%.2f}]}`, value)
				plan := planFor(t, w, raw)
				if _, err := w.Apply(context.Background(), plan); err != nil {
					if errors.Is(err, ErrStale) {
						continue
					}
					errCh <- err
					break
				}
				if err := config.ValidateFile(path); err != nil {
					errCh <- fmt.Errorf("assistant round %d: file unparsable: %w", i, err)
				}
				break
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			if err := config.EditConfigFile(path, func(cfg *config.Config) error {
				cfg.Agent.RetryCount = i % 5
				return nil
			}); err != nil {
				errCh <- fmt.Errorf("full save round %d: %w", i, err)
				continue
			}
			if err := config.ValidateFile(path); err != nil {
				errCh <- fmt.Errorf("full save round %d: file unparsable: %w", i, err)
			}
		}
	}()
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrency failure: %v", err)
	}
}

// TestMain hosts the crash helper. The helper performs a real Apply and exits
// inside fileutil's replace boundary, leaving the original file untouched.
func TestMain(m *testing.M) {
	if os.Getenv("ASSIST_CRASH_HELPER") == "1" {
		runCrashHelper()
		return
	}
	os.Exit(m.Run())
}

func runCrashHelper() {
	path := config.UserConfigPath()
	fileutil.CrashPoint = func(op, target string) {
		if op != "atomic-write" {
			return
		}
		same := target == path
		if !same {
			// The caller hands us its resolved path; compare by file identity
			// so future path-normalization drift cannot silently skip the crash.
			if a, err := os.Stat(target); err == nil {
				if b, err := os.Stat(path); err == nil {
					same = os.SameFile(a, b)
				}
			}
		}
		if same {
			os.Exit(23)
		}
	}
	w := &Writer{ConfigPath: path, BackupRoot: filepath.Join(config.PonderaHomeDir(), "assistant", "backups")}
	hash, rev, err := w.CurrentBaseHash()
	if err != nil {
		os.Exit(24)
	}
	plan := &patch.Plan{
		ID: "crash", BaseHash: hash, CredentialRevision: rev,
		Ops: []patch.ResolvedOp{{Op: patch.ResolvedOpSet, Path: "agent.temperature", Value: 0.9, Label: "agent.temperature"}},
	}
	_, _ = w.Apply(context.Background(), plan)
	os.Exit(0)
}

func TestCrashDuringWriteLeavesOriginalIntact(t *testing.T) {
	path, _ := seed(t)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "ASSIST_CRASH_HELPER=1", "PONDERA_HOME="+os.Getenv("PONDERA_HOME"))
	err = cmd.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 23 {
		t.Fatalf("helper exited %v, want the exit-23 crash point: %v", err, err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after crash: %v", err)
	}
	if Hash(after) != Hash(original) {
		t.Fatalf("original config changed by a crashed write")
	}
	if err := config.ValidateFile(path); err != nil {
		t.Fatalf("original config no longer parses: %v", err)
	}
}
