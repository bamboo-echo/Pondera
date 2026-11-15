package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"pondera/internal/config"
	"pondera/internal/remote"
)

// Live SSH smoke test. Skipped unless PONDERA_LIVE_SSH_TARGET is set to a full
// "user@host:port" target and PONDERA_LIVE_SSH_KEY names the private key.
// Every field is written into config.toml explicitly: the desktop TestMain
// pins HOME to a temp dir, so ~/.ssh/config is unreadable here and ssh_config
// layering cannot be exercised from a test binary. The test binary has no TOFU
// dialog either, so newClient is overridden with an auto-accept host-key
// policy; the fingerprint is logged for the reader.
func TestLiveRemoteSSHWorkspace(t *testing.T) {
	target := os.Getenv("PONDERA_LIVE_SSH_TARGET")
	key := os.Getenv("PONDERA_LIVE_SSH_KEY")
	if target == "" || key == "" {
		t.Skip("set PONDERA_LIVE_SSH_TARGET=user@host:port and PONDERA_LIVE_SSH_KEY to run the live SSH check")
	}
	isolateDesktopUserDirs(t)

	// Split user@host:port without assuming a default user.
	user, rest := "", target
	for i := 0; i < len(target); i++ {
		if target[i] == '@' {
			user, rest = target[:i], target[i+1:]
			break
		}
	}
	host, portText := rest, ""
	for i := len(rest) - 1; i >= 0; i-- {
		if rest[i] == ':' {
			host, portText = rest[:i], rest[i+1:]
			break
		}
	}
	port := 22
	if portText != "" {
		if v, err := strconv.Atoi(portText); err == nil && v > 0 && v <= 65535 {
			port = v
		}
	}

	cfgPath := config.UserConfigPath()
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.LoadForEdit(cfgPath)
	if err := cfg.UpsertRemoteHost(config.RemoteHostEntry{
		Name: "livetest", Host: host, Port: port, User: user,
		IdentityFile: key, ServeInstall: "auto",
	}); err != nil {
		t.Fatalf("UpsertRemoteHost: %v", err)
	}
	// Upsert only mutates the in-memory table; Connect re-reads the file.
	if err := cfg.SaveTo(cfgPath); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
	t.Logf("target=%s@%s:%d key=%s", user, host, port, key)

	app := NewApp()
	app.ctx = t.Context()
	t.Cleanup(func() {
		app.stopRemoteRuntime()
		app.closeSessionServices()
	})

	rt, err := app.remoteRT()
	if err != nil {
		t.Fatalf("remoteRT: %v", err)
	}
	mgr := rt.(*desktopRemoteManager)
	realNewClient := mgr.newClient
	mgr.newClient = func(opts remote.Options) (desktopSSHClient, error) {
		opts.HostKeys = &remote.HostKeyPolicy{
			Prompt: func(ctx context.Context, q remote.HostKeyQuestion) (bool, error) {
				t.Logf("TOFU accept %s %s %s", q.Host, q.KeyType, q.Fingerprint)
				return true, nil
			},
		}
		return realNewClient(opts)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()

	if err := rt.Connect("livetest"); err != nil {
		t.Fatalf("connect: %v", err)
	}

	deadline := time.Now().Add(120 * time.Second)
	state := ""
	for time.Now().Before(deadline) {
		mh := mgr.managed("livetest")
		if mh != nil {
			state = mh.status.State
			if state == "connected" || state == "degraded" {
				break
			}
			if state == "stopped" && mh.status.Error != "" {
				t.Fatalf("connection stopped: %s", mh.status.Error)
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if state != "connected" && state != "degraded" {
		t.Fatalf("connection never reached connected (last state %q)", state)
	}
	t.Logf("connected (state=%s)", state)

	if err := rt.CheckPlatform(ctx, "livetest"); err != nil {
		t.Fatalf("CheckPlatform: %v", err)
	}
	t.Log("platform ok (linux/darwin)")

	view, _, err := rt.EnsureServer(ctx, "livetest", "~")
	if err != nil {
		t.Fatalf("EnsureServer: %v", err)
	}
	t.Logf("remote serve state=%s url=%q", view.State, view.LocalURL)

	entries, err := rt.ListDir(ctx, "livetest", "~")
	if err != nil {
		t.Fatalf("ListDir(~): %v", err)
	}
	dirs := 0
	for _, e := range entries {
		if e.IsDir {
			dirs++
		}
	}
	t.Logf("ListDir(~) -> %d entries, %d dirs", len(entries), dirs)
	for i, e := range entries {
		if i >= 10 {
			break
		}
		t.Logf("  %s dir=%v", e.Path, e.IsDir)
	}
	if len(entries) == 0 {
		t.Fatal("remote home listed no entries")
	}

	// The picker's finish step: register the workspace and open the in-app
	// remote tab exactly like RemoteWorkspacePicker.confirm does.
	proj, err := app.AddRemoteProject("livetest", "/root")
	if err != nil {
		t.Fatalf("AddRemoteProject: %v", err)
	}
	t.Logf("project registered workspace=%q merged=%v", proj.Workspace, proj.Merged)
	tab, err := app.OpenRemoteProjectTab("livetest", proj.Workspace, RemoteTabOpenOptions{NewSession: true})
	if err != nil {
		t.Fatalf("OpenRemoteProjectTab: %v", err)
	}
	if tab.Remote == nil || tab.Remote.HostID != "livetest" {
		t.Fatalf("tab remote ref = %+v, want livetest", tab.Remote)
	}
	t.Logf("in-app remote tab opened id=%s state=%q title=%q", tab.ID, tab.RemoteState, tab.Label)
	for i := 0; i < 60; i++ {
		snap, serr := app.RemoteTabStatus(tab.ID)
		if serr == nil && snap != nil {
			t.Logf("tab status: %v", string(snap))
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
}
