package cloudsync

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	home := t.TempDir()
	s, err := OpenStore(home)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	if err := s.SetLocalHostID("host-local-1"); err != nil {
		t.Fatalf("SetLocalHostID: %v", err)
	}
	return s, home
}

func TestPinUnpinAndPersistence(t *testing.T) {
	s, home := newTestStore(t)

	if got := s.Pinned(); len(got) != 0 {
		t.Fatalf("new store has pinned sessions: %v", got)
	}
	tasks := []TaskRef{{ID: "goal-1", Kind: "goal", Label: "fix auth"}}
	if err := s.Pin("sess-A", "Auth refactor", "ws-1", tasks); err != nil {
		t.Fatalf("Pin: %v", err)
	}
	sel, ok := s.Get("sess-A")
	if !ok || !sel.IsPinned() || sel.Owner != OwnerLocal || len(sel.Tasks) != 1 {
		t.Fatalf("unexpected selection: %+v", sel)
	}

	// Reload from disk: the switch must be durable.
	s2, err := OpenStore(home)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := s2.Pinned(); len(got) != 1 || got[0].SessionID != "sess-A" {
		t.Fatalf("pin not persisted: %+v", got)
	}
	if s2.LocalHostID() != "host-local-1" {
		t.Fatalf("host id not persisted: %q", s2.LocalHostID())
	}

	if err := s2.Unpin("sess-A"); err != nil {
		t.Fatalf("Unpin: %v", err)
	}
	sel, _ = s2.Get("sess-A")
	if sel.IsPinned() || len(sel.Tasks) != 0 {
		t.Fatalf("unpin left selection dirty: %+v", sel)
	}
}

func TestPinValidation(t *testing.T) {
	s, _ := newTestStore(t)
	if err := s.Pin("", "", "", nil); err == nil {
		t.Fatal("expected empty session id to fail")
	}
	if err := s.Pin("s1", "", "", []TaskRef{{ID: "x", Kind: "unknown"}}); err == nil {
		t.Fatal("expected invalid task kind to fail")
	}
	if err := s.Pin("s1", "", "", []TaskRef{
		{ID: "x", Kind: "goal"}, {ID: "x", Kind: "goal"},
	}); err == nil {
		t.Fatal("expected duplicate task to fail")
	}
	if err := s.SetTasks("missing", nil); err == nil {
		t.Fatal("expected SetTasks on unpinned session to fail")
	}
}

func TestIdentityBinding(t *testing.T) {
	s, _ := newTestStore(t)
	m := OriginMapping{
		LocalHostID: "L", LocalSessionID: "s1",
		CloudHostID: "C", CloudSessionID: "s9",
	}
	if err := s.Bind(m); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if err := s.Bind(m); err != nil { // idempotent
		t.Fatalf("Bind idempotent: %v", err)
	}
	got, ok := s.Resolve("C", "s9") // reverse lookup
	if !ok || got.LocalSessionID != "s1" {
		t.Fatalf("reverse mapping missing: %+v %v", got, ok)
	}
	conflict := m
	conflict.CloudSessionID = "other"
	if err := s.Bind(conflict); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("expected identity conflict, got %v", err)
	}
}

func TestVersionVectorHandoff(t *testing.T) {
	fresh := VersionVector{}
	in := VersionVector{HostID: "L", SessionID: "s1", BoundarySequence: 42}
	if err := CheckHandoff(in, fresh); err != nil {
		t.Fatalf("fresh destination: %v", err)
	}
	if err := CheckHandoff(in, VersionVector{BoundarySequence: 42}); err != nil {
		t.Fatalf("equal vector: %v", err)
	}
	if err := CheckHandoff(in, VersionVector{BoundarySequence: 43}); err == nil {
		t.Fatal("expected stale handoff to be refused")
	}
	if err := CheckHandoff(VersionVector{}, fresh); err == nil {
		t.Fatal("expected incomplete vector to fail")
	}
}

func TestManifestSealAndTamper(t *testing.T) {
	m := &Manifest{
		Schema: ManifestSchema, HandoffID: "h1", Direction: DirectionUpload,
		Origin:    Origin{HostID: "L", SessionID: "s1"},
		Selection: Selection{SessionID: "s1", Scope: ScopePinned},
		Files:     []FileEntry{{Path: "session/a.json", SHA256: strings.Repeat("a", 64), Size: 10}},
	}
	if err := m.Seal(); err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if err := m.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	m.Files[0].SHA256 = strings.Repeat("b", 64)
	if err := m.Verify(); err == nil {
		t.Fatal("expected tampered file list to fail verification")
	}

	data, _ := json.Marshal(m)
	if _, err := ParseManifest(data); err == nil {
		t.Fatal("expected ParseManifest to reject tampered manifest")
	}
}

func TestValidateEntryPath(t *testing.T) {
	good := []string{"session/a.json", "workspace/src/main.go", "checkpoints/00/x"}
	for _, p := range good {
		if err := ValidateEntryPath(p); err != nil {
			t.Errorf("rejected valid path %q: %v", p, err)
		}
	}
	bad := []string{
		"../escape", "/absolute", "a/../../b", "a\\b", "",
		".env", "config/.env", "keys/id_rsa", "a/../b",
	}
	for _, p := range bad {
		if err := ValidateEntryPath(p); err == nil {
			t.Errorf("accepted unsafe path %q", p)
		}
	}
}

func TestShouldExclude(t *testing.T) {
	rules := DefaultExcludes
	cases := map[string]bool{
		"workspace/node_modules/pkg/index.js": true,
		"workspace/src/main.go":               false,
		"workspace/.env":                      true,
		"workspace/target/debug/app":          true,
		"session/messages.jsonl":              false,
	}
	for p, want := range cases {
		if got := ShouldExclude(p, rules); got != want {
			t.Errorf("ShouldExclude(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestBuildOpenExtractRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sessionDir := filepath.Join(dir, "session")
	wsDir := filepath.Join(dir, "ws")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(wsDir, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	content := []byte("package main\n// sync me\n")
	if err := os.WriteFile(filepath.Join(wsDir, "src", "main.go"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wsDir, ".env"), []byte("SECRET=do-not-ship"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "log.jsonl"), []byte(`{"kind":"message/complete"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(dir, "bundle.tar.gz")
	res, err := Build(BuildOptions{
		OutPath:      out,
		Direction:    DirectionUpload,
		Selection:    Selection{SessionID: "s1", Scope: ScopePinned},
		Origin:       Origin{HostID: "L", SessionID: "s1"},
		SessionDir:   sessionDir,
		WorkspaceDir: wsDir,
		Workspace:    WorkspaceInfo{Strategy: "file-list"},
		Now:          func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if res.Bytes == 0 {
		t.Fatal("empty bundle")
	}
	for _, f := range res.Manifest.Files {
		if strings.Contains(f.Path, ".env") {
			t.Fatalf("credential leaked into bundle: %q", f.Path)
		}
	}
	opened, err := OpenBundle(out)
	if err != nil {
		t.Fatalf("OpenBundle: %v", err)
	}
	if opened.Manifest.Digest != res.Manifest.Digest {
		t.Fatal("digest changed across round trip")
	}
	dest := filepath.Join(dir, "extracted")
	if err := opened.Extract(dest); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "workspace", "src", "main.go"))
	if err != nil || string(got) != string(content) {
		t.Fatalf("extracted content mismatch: %q %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(dest, "workspace", ".env")); !os.IsNotExist(err) {
		t.Fatal("sensitive file was extracted")
	}
}

func TestBuildRejectsLocalOnly(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := Build(BuildOptions{
		OutPath:   filepath.Join(dir, "x.tar.gz"),
		Direction: DirectionUpload,
		Selection: Selection{SessionID: "s1", Scope: ScopeLocalOnly},
		Origin:    Origin{HostID: "L", SessionID: "s1"},
	})
	if err == nil {
		t.Fatal("expected local-only session to be refused")
	}
}

func TestPlannerEndToEnd(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	srcStore, err := OpenStore(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := srcStore.SetLocalHostID("local-host"); err != nil {
		t.Fatal(err)
	}
	if err := srcStore.Pin("sess-1", "My task", "ws-1", []TaskRef{{ID: "goal-1", Kind: "goal"}}); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(t.TempDir(), "work")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("hello cloud"), 0o600); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(t.TempDir(), "bundles")

	planner := &Planner{Store: srcStore, Home: home}
	if _, err := planner.PrepareUpload(PrepareUploadOptions{
		SessionID: "sess-1", WorkspaceDir: work, OutDir: outDir,
		CloudHostID: "cloud-host", CloudSessionID: "cloud-sess-1",
		BoundarySequence: 12,
	}, false); err == nil {
		t.Fatal("expected unquiesced upload to be refused")
	}
	plan, err := planner.PrepareUpload(PrepareUploadOptions{
		SessionID: "sess-1", WorkspaceDir: work, OutDir: outDir,
		CloudHostID: "cloud-host", CloudSessionID: "cloud-sess-1",
		BoundarySequence: 12,
	}, true)
	if err != nil {
		t.Fatalf("PrepareUpload: %v", err)
	}
	if plan.HandoffID != plan.Manifest.HandoffID {
		t.Fatal("plan and manifest handoff ids diverge")
	}
	// Ownership must not change until the destination confirms.
	if sel, _ := srcStore.Get("sess-1"); sel.Owner != OwnerLocal {
		t.Fatal("ownership flipped before confirmation")
	}

	// Destination host: fresh store, import the bundle.
	dstHome := filepath.Join(t.TempDir(), "cloudhome")
	dstStore, err := OpenStore(dstHome)
	if err != nil {
		t.Fatal(err)
	}
	if err := dstStore.SetLocalHostID("cloud-host"); err != nil {
		t.Fatal(err)
	}
	dstPlanner := &Planner{Store: dstStore, Home: dstHome}
	applied, err := dstPlanner.ApplyIncoming(ApplyOptions{
		BundlePath:  plan.BundlePath,
		ExtractRoot: filepath.Join(dstHome, "incoming"),
		NewOwner:    OwnerCloud,
		PinOnApply:  true,
	})
	if err != nil {
		t.Fatalf("ApplyIncoming: %v", err)
	}
	if applied.HandoffID != plan.HandoffID {
		t.Fatal("handoff id mismatch")
	}
	got, err := os.ReadFile(filepath.Join(applied.ExtractDir, "workspace", "README.md"))
	if err != nil || string(got) != "hello cloud" {
		t.Fatalf("workspace not transferred: %q %v", got, err)
	}
	if sel, _ := dstStore.Get("sess-1"); !sel.IsPinned() || sel.Owner != OwnerCloud {
		t.Fatalf("destination state wrong: %+v", sel)
	}

	// Source confirms and flips to cloud-owned.
	if err := planner.CommitUpload(plan, "cloud-host", "cloud-sess-1", true, ""); err != nil {
		t.Fatalf("CommitUpload: %v", err)
	}
	if sel, _ := srcStore.Get("sess-1"); sel.Owner != OwnerCloud || sel.LastUpload == nil || sel.LastUpload.Status != "ok" {
		t.Fatalf("source state wrong: %+v", sel)
	}
	if m, ok := srcStore.Resolve("local-host", "sess-1"); !ok || m.CloudSessionID != "cloud-sess-1" {
		t.Fatalf("origin mapping missing: %+v %v", m, ok)
	}
	// A second upload while cloud-owned is refused.
	if _, err := planner.PrepareUpload(PrepareUploadOptions{
		SessionID: "sess-1", WorkspaceDir: work, OutDir: outDir,
		CloudHostID: "cloud-host", CloudSessionID: "cloud-sess-1",
	}, true); err == nil {
		t.Fatal("expected second upload while cloud-owned to be refused")
	}
}

func TestApplyIncomingRejectsStaleAndTampered(t *testing.T) {
	home := t.TempDir()
	s, err := OpenStore(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetLocalHostID("L"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	bundle := filepath.Join(dir, "b.tar.gz")
	res, err := Build(BuildOptions{
		OutPath: bundle, Direction: DirectionUpload,
		Selection: Selection{SessionID: "s1", Scope: ScopePinned},
		Origin:    Origin{HostID: "L", SessionID: "s1", BoundarySequence: 5},
		Workspace: WorkspaceInfo{Strategy: "none"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = res
	p := &Planner{Store: s, Home: home}
	_, err = p.ApplyIncoming(ApplyOptions{
		BundlePath: bundle, ExtractRoot: filepath.Join(dir, "x"),
		NewOwner: OwnerCloud, PinOnApply: true,
		DestinationVector: VersionVector{BoundarySequence: 9},
	})
	if err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("expected stale handoff refusal, got %v", err)
	}
}

func TestGitWorkspaceRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	// The round-trip must compare exact bytes; a Windows runner's global
	// core.autocrlf=true would rewrite worktree text on checkout.
	run("config", "core.autocrlf", "false")
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("committed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "a.txt")
	run("commit", "-qm", "initial")
	// An uncommitted tracked change must survive the handoff.
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("committed\nmodified\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	untracked := []byte("new file\n")
	if err := os.WriteFile(filepath.Join(root, "b.txt"), untracked, 0o600); err != nil {
		t.Fatal(err)
	}

	staging := filepath.Join(t.TempDir(), "stage")
	wsDir, info, err := PrepareWorkspace(root, staging, nil)
	if err != nil {
		t.Fatalf("PrepareWorkspace: %v", err)
	}
	if !info.IsGit || !info.Dirty {
		t.Fatalf("unexpected workspace info: %+v", info)
	}
	if wsDir != staging {
		t.Fatal("git workspace should be staged")
	}

	bundle := filepath.Join(t.TempDir(), "b.tar.gz")
	res, err := Build(BuildOptions{
		OutPath: bundle, Direction: DirectionUpload,
		Selection:    Selection{SessionID: "s", Scope: ScopePinned},
		Origin:       Origin{HostID: "L", SessionID: "s"},
		WorkspaceDir: wsDir, Workspace: info,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(res.Manifest.Files) == 0 {
		t.Fatal("empty git bundle")
	}
	opened, err := OpenBundle(bundle)
	if err != nil {
		t.Fatal(err)
	}
	extract := filepath.Join(t.TempDir(), "extract")
	if err := opened.Extract(extract); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	dest := filepath.Join(t.TempDir(), "restored")
	if err := ApplyWorkspace(filepath.Join(extract, "workspace"), dest, info); err != nil {
		t.Fatalf("ApplyWorkspace: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "a.txt"))
	if err != nil || string(got) != "committed\nmodified\n" {
		t.Fatalf("tracked diff not restored: %q %v", got, err)
	}
	got, err = os.ReadFile(filepath.Join(dest, "b.txt"))
	if err != nil || string(got) != string(untracked) {
		t.Fatalf("untracked file not restored: %q %v", got, err)
	}
}
