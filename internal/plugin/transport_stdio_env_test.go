package plugin

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"pondera/internal/sandbox"
	"pondera/internal/secrets"
)

func TestStdioShellPATHProbeFiltersEnvWhenEnabled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell probe")
	}
	secrets.SetFilterSubprocessEnv(true)
	t.Cleanup(func() { secrets.SetFilterSubprocessEnv(false) })
	t.Setenv("PONDERA_TEST_SECRET_TOKEN", "ghp_abcdefghijklmnopqrstuvwxyz")

	out := runShellPATHCommand(context.Background(), "/bin/sh", []string{"-c", `printf 'tok=%s' "${PONDERA_TEST_SECRET_TOKEN:-none}"`})
	if !strings.Contains(string(out), "tok=none") {
		t.Fatalf("stdio shell PATH probe leaked filtered env: %q", out)
	}
}

// TestStdioEnvFiltersCredentialsByDefault pins the MCP before/after boundary:
// with filter_subprocess_env at its default (off), the legacy merge over
// ProcessEnv still leaked credential-shaped variables; stdioEnv — now used by
// the stdio launch, launcher resolution and launch identity — always removes
// them while keeping ordinary host variables.
func TestStdioEnvFiltersCredentialsByDefault(t *testing.T) {
	previous := secrets.FilterSubprocessEnv()
	secrets.SetFilterSubprocessEnv(false)
	t.Cleanup(func() { secrets.SetFilterSubprocessEnv(previous) })

	t.Setenv("PLUGIN_TEST_HOST_API_KEY", "sk-host-secret")
	t.Setenv("PLUGIN_TEST_HOST_TOKEN", "host-token")
	t.Setenv("PLUGIN_TEST_AUTHORIZATION", "Bearer host-token")
	t.Setenv("PONDERA_TEST_HOST_STATE", "host-state")
	t.Setenv("PLUGIN_TEST_ORDINARY", "visible")

	legacy := strings.Join(mergeEnv(secrets.ProcessEnv(), nil), "\n")
	for _, key := range []string{"PLUGIN_TEST_HOST_API_KEY", "PLUGIN_TEST_HOST_TOKEN", "PLUGIN_TEST_AUTHORIZATION", "PONDERA_TEST_HOST_STATE"} {
		if !strings.Contains(legacy, key+"=") {
			t.Fatalf("legacy MCP env unexpectedly dropped %s; the before-state changed", key)
		}
	}

	env := strings.Join(stdioEnv(Spec{}), "\n")
	for _, key := range []string{"PLUGIN_TEST_HOST_API_KEY", "PLUGIN_TEST_HOST_TOKEN", "PLUGIN_TEST_AUTHORIZATION", "PONDERA_TEST_HOST_STATE"} {
		if strings.Contains(env, key+"=") {
			t.Fatalf("stdioEnv leaked %s:\n%s", key, env)
		}
	}
	if !strings.Contains(env, "PLUGIN_TEST_ORDINARY=visible") {
		t.Fatalf("stdioEnv dropped an ordinary variable:\n%s", env)
	}
	if !strings.Contains(env, "PATH=") {
		t.Fatalf("stdioEnv dropped PATH:\n%s", env)
	}
}

// TestStdioEnvExplicitDeclarationWins pins the compatibility escape hatch:
// a server's config-declared env is layered after filtering, so it can restore
// a value the host boundary removed (and plugin-package PONDERA_* vars still
// arrive through the same declaration path).
func TestStdioEnvExplicitDeclarationWins(t *testing.T) {
	t.Setenv("PLUGIN_TEST_HOST_API_KEY", "host-secret")
	spec := Spec{Env: map[string]string{
		"PLUGIN_TEST_HOST_API_KEY": "declared-value",
		"PONDERA_PLUGIN_ROOT":      "/plugin-root",
	}}

	if value, ok := envValue(stdioEnv(spec), "PLUGIN_TEST_HOST_API_KEY"); !ok || value != "declared-value" {
		t.Fatalf("declared credential = (%q, %v), want declared-value", value, ok)
	}
	if value, ok := envValue(stdioEnv(spec), "PONDERA_PLUGIN_ROOT"); !ok || value != "/plugin-root" {
		t.Fatalf("declared PONDERA_PLUGIN_ROOT = (%q, %v), want /plugin-root", value, ok)
	}
}

func TestPrepareMCPPrivateStateWindowsPreservesHostTemp(t *testing.T) {
	root := filepath.Join(t.TempDir(), "mcp-state", "0123456789abcdef", "matlab")
	hostTemp := `C:\Users\user\AppData\Local\Temp`
	env := []string{"TMP=" + hostTemp, "TEMP=" + hostTemp, "TMPDIR=" + hostTemp}

	_, got, err := prepareMCPPrivateStateForOS(Spec{StateDir: root}, sandbox.Spec{}, env, "windows")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"TMP", "TEMP", "TMPDIR"} {
		if value, ok := envValue(got, key); !ok || value != hostTemp {
			t.Fatalf("%s = %q, %v; want inherited host temp %q", key, value, ok, hostTemp)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "tmp")); !os.IsNotExist(err) {
		t.Fatalf("private Windows temp directory exists or stat failed: %v", err)
	}
	for key, want := range map[string]string{
		"XDG_CACHE_HOME": filepath.Join(root, "cache"),
		"XDG_STATE_HOME": filepath.Join(root, "state"),
	} {
		if value, ok := envValue(got, key); !ok || value != want {
			t.Fatalf("%s = %q, %v; want %q", key, value, ok, want)
		}
	}
}

func TestPrepareMCPPrivateStateUnixIsolatesTemp(t *testing.T) {
	root := filepath.Join(t.TempDir(), "mcp-state", "matlab")
	hostTemp := "/tmp/host"
	env := []string{"TMP=" + hostTemp, "TEMP=" + hostTemp, "TMPDIR=" + hostTemp}

	_, got, err := prepareMCPPrivateStateForOS(Spec{StateDir: root}, sandbox.Spec{}, env, "linux")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "tmp")
	for _, key := range []string{"TMP", "TEMP", "TMPDIR"} {
		if value, ok := envValue(got, key); !ok || value != want {
			t.Fatalf("%s = %q, %v; want private temp %q", key, value, ok, want)
		}
	}
	if info, err := os.Stat(want); err != nil || !info.IsDir() {
		t.Fatalf("private Unix temp directory = (%v, %v), want directory", info, err)
	}
}
