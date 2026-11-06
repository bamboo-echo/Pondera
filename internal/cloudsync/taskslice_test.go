package cloudsync

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"pondera/internal/session"
)

func makeSessionSkeleton(t *testing.T, dir, id string) {
	t.Helper()
	if err := session.WriteEmptyExport(dir, id); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverSessionDir(t *testing.T) {
	home := t.TempDir()
	id := "sess-discover"
	makeSessionSkeleton(t, filepath.Join(home, "sessions-v4", id), id)
	got, err := DiscoverSessionDir(home, id)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != id {
		t.Fatalf("discovered %q", got)
	}
	if _, err := DiscoverSessionDir(home, "missing-session"); err == nil {
		t.Fatal("expected discovery to fail for a missing session")
	}
	if _, err := DiscoverSessionDir(home, "../escape"); err == nil {
		t.Fatal("expected path traversal to be rejected")
	}
}

func TestPrepareTaskSliceFallbackCarriesWholeSession(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "session-src")
	dst := filepath.Join(base, "session-slice")
	makeSessionSkeleton(t, src, "sess-fallback")
	w, err := PrepareTaskSlice(context.Background(), src, dst, []string{"goal:does-not-exist"})
	if err != nil {
		t.Fatal(err)
	}
	if w.Sliced {
		t.Fatal("an unmatched task selection must not claim a slice")
	}
	if w.Reason != "no-task-match-fallback-whole-session" {
		t.Fatalf("reason = %q", w.Reason)
	}
	if _, err := session.ReplayExport(dst); err != nil {
		t.Fatalf("fallback export is not replayable: %v", err)
	}
}

func TestPrepareTaskSliceWholeSessionWithoutTasks(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "src")
	dst := filepath.Join(base, "dst")
	makeSessionSkeleton(t, src, "sess-whole")
	w, err := PrepareTaskSlice(context.Background(), src, dst, nil)
	if err != nil {
		t.Fatal(err)
	}
	if w.Reason != "whole-session" || w.Sliced {
		t.Fatalf("unexpected window: %+v", w)
	}
}

func TestInstallSessionExportRefusesOverwrite(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "src")
	root := filepath.Join(base, "sessions-v4")
	makeSessionSkeleton(t, src, "sess-install")
	got, err := InstallSessionExport(context.Background(), src, root, "sess-install", false)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(root, "sess-install") {
		t.Fatalf("installed at %q", got)
	}
	if _, err := InstallSessionExport(context.Background(), src, root, "sess-install", false); err == nil {
		t.Fatal("expected overwrite to be refused")
	}
	if _, err := InstallSessionExport(context.Background(), src, root, "sess-install", true); err != nil {
		t.Fatalf("forced install failed: %v", err)
	}
	bad := filepath.Join(base, "bad")
	if err := os.MkdirAll(bad, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallSessionExport(context.Background(), bad, root, "sess-bad", false); err == nil {
		t.Fatal("expected an invalid export to be rejected")
	}
}

func TestPrepareUploadAutoDiscoveryAndManifestWindow(t *testing.T) {
	store, home := newTestStore(t)
	id := "sess-auto"
	if err := store.Pin(id, "自动发现", "ws-1", nil); err != nil {
		t.Fatal(err)
	}
	makeSessionSkeleton(t, filepath.Join(home, "sessions-v4", id), id)
	planner := &Planner{Store: store, Home: home}
	plan, err := planner.PrepareUpload(PrepareUploadOptions{
		SessionID: id, OutDir: filepath.Join(home, "out"),
		CloudHostID: "cloud-host", CloudSessionID: "cloud-" + id,
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if plan.TaskWindow == nil {
		t.Fatal("automatic session discovery did not produce a task window")
	}
	if plan.Manifest.TaskWindow == nil {
		t.Fatal("task window was not sealed into the manifest")
	}
	opened, err := OpenBundle(plan.BundlePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := opened.Manifest.Verify(); err != nil {
		t.Fatalf("sealed manifest did not verify: %v", err)
	}
	if opened.Manifest.TaskWindow == nil || opened.Manifest.TaskWindow.Reason != "whole-session" {
		t.Fatalf("task window did not survive the bundle round-trip: %+v", opened.Manifest.TaskWindow)
	}
	// The destination installs the unpacked session into its own root.
	root := filepath.Join(t.TempDir(), "sessions-v4")
	res, err := planner.ApplyIncoming(ApplyOptions{
		BundlePath: plan.BundlePath, ExtractRoot: filepath.Join(home, "incoming"),
		NewOwner: OwnerCloud, PinOnApply: true, SessionRoot: root,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.InstalledSessionDir != filepath.Join(root, id) {
		t.Fatalf("session installed at %q", res.InstalledSessionDir)
	}
	if _, err := session.ReplayExport(res.InstalledSessionDir); err != nil {
		t.Fatalf("installed session is not replayable: %v", err)
	}
}
