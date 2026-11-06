package cloudsync

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"pondera/internal/session"
)

func mustPin(t *testing.T, store *Store, id string) {
	t.Helper()
	if err := store.Pin(id, "push/pull test", "ws-1", []TaskRef{{ID: "build", Kind: "job"}}); err != nil {
		t.Fatal(err)
	}
}

func TestShellQuote(t *testing.T) {
	in := "a'b c"
	quoted := ShellQuote(in)
	if quoted != `'a'\''b c'` {
		t.Fatalf("unexpected quoting: %s", quoted)
	}
}

func TestFilesystemTransportRejectsTraversal(t *testing.T) {
	tr := &FilesystemTransport{Root: t.TempDir()}
	if err := tr.Put(context.Background(), "../../escape", strings.NewReader("x"), 1); err == nil {
		t.Fatal("expected path traversal to be rejected")
	}
}

// TestPushEndToEnd emulates the destination import inside ExecFunc so the
// complete prepare -> upload -> apply -> commit chain is exercised.
func TestPushEndToEnd(t *testing.T) {
	ctx := context.Background()
	localStore, localHome := newTestStore(t)
	cloudStore, cloudHome := newTestStore(t)
	id := "sess-push"
	mustPin(t, localStore, id)
	makeSessionSkeleton(t, filepath.Join(localHome, "sessions-v4", id), id)

	remoteRoot := t.TempDir()
	cloudPlanner := &Planner{Store: cloudStore, Home: cloudHome}
	applyRE := regexp.MustCompile(`cloud apply '([^']+)'`)
	var applied string
	var tr *FilesystemTransport
	tr = &FilesystemTransport{
		Root: remoteRoot,
		ExecFunc: func(_ context.Context, cmd string) (string, error) {
			m := applyRE.FindStringSubmatch(cmd)
			if len(m) != 2 {
				return "", fmt.Errorf("malformed remote command: %s", cmd)
			}
			localPath, err := tr.resolve(m[1])
			if err != nil {
				return "", err
			}
			res, err := cloudPlanner.ApplyIncoming(ApplyOptions{
				BundlePath: localPath, ExtractRoot: filepath.Join(cloudHome, "incoming"),
				NewOwner: OwnerCloud, PinOnApply: true,
				SessionRoot: filepath.Join(cloudHome, "sessions-v4"),
			})
			if err != nil {
				return "", err
			}
			applied = res.InstalledSessionDir
			return "imported " + res.HandoffID, nil
		},
	}
	localPlanner := &Planner{Store: localStore, Home: localHome}
	res, err := Push(ctx, PushOptions{
		Planner: localPlanner, Transport: tr, RemoteDir: "incoming",
		QuiesceOpts: &QuiesceOptions{Mode: QuiesceForce},
		Prepare: PrepareUploadOptions{
			SessionID: id, OutDir: filepath.Join(localHome, "out"),
			CloudHostID: "cloud-host", CloudSessionID: "cloud-" + id,
		},
		SessionRoot: filepath.Join(cloudHome, "sessions-v4"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Quiesce == nil || !res.Quiesce.Quiesced {
		t.Fatal("missing quiesce report")
	}
	if applied == "" {
		t.Fatal("destination import did not run")
	}
	if _, err := session.ReplayExport(applied); err != nil {
		t.Fatalf("installed cloud session is not replayable: %v", err)
	}
	if st, ok := cloudStore.Get(id); !ok || st.Owner != OwnerCloud || !st.IsPinned() {
		t.Fatalf("cloud state not updated: %+v", st)
	}
	if st, ok := localStore.Get(id); !ok || st.Owner != OwnerCloud {
		t.Fatalf("local ownership did not flip: %+v", st)
	}
	if len(localStore.Mappings()) != 1 {
		t.Fatal("origin mapping not recorded")
	}
	// A second push must be refused by the single-writer rule.
	if _, err := Push(ctx, PushOptions{
		Planner: localPlanner, Transport: tr, RemoteDir: "incoming",
		QuiesceOpts: &QuiesceOptions{Mode: QuiesceForce},
		Prepare: PrepareUploadOptions{
			SessionID: id, OutDir: filepath.Join(localHome, "out"),
			CloudHostID: "cloud-host", CloudSessionID: "cloud-" + id,
		},
	}); err == nil {
		t.Fatal("expected the second push to be refused")
	}
}

func TestPushFailureLeavesLocalWriter(t *testing.T) {
	ctx := context.Background()
	s, home := newTestStore(t)
	id := "sess-fail"
	mustPin(t, s, id)
	tr := &FilesystemTransport{
		Root: t.TempDir(),
		ExecFunc: func(context.Context, string) (string, error) {
			return "", fmt.Errorf("simulated remote failure")
		},
	}
	_, err := Push(ctx, PushOptions{
		Planner: &Planner{Store: s, Home: home}, Transport: tr, RemoteDir: "incoming",
		QuiesceOpts: &QuiesceOptions{Mode: QuiesceForce},
		Prepare: PrepareUploadOptions{
			SessionID: id, OutDir: filepath.Join(home, "out"),
			CloudHostID: "cloud-host", CloudSessionID: "cloud-" + id,
		},
	})
	if err == nil {
		t.Fatal("expected the push to fail")
	}
	if st, _ := s.Get(id); st.Owner == OwnerCloud {
		t.Fatal("ownership must stay local when the destination import fails")
	}
}

func TestPullDownloadsAndInstalls(t *testing.T) {
	ctx := context.Background()
	s, home := newTestStore(t)
	id := "sess-pull"
	makeSessionSkeleton(t, filepath.Join(home, "sessions-v4", id), id)
	planner := &Planner{Store: s, Home: home}
	// Build a bundle on the "cloud" side by reusing the local planner in
	// download-irrelevant mode, then stage it in the remote root.
	if err := s.Pin(id, "pull", "ws", nil); err != nil {
		t.Fatal(err)
	}
	plan, err := planner.PrepareUpload(PrepareUploadOptions{
		SessionID: id, OutDir: filepath.Join(home, "out"),
		CloudHostID: "cloud-host", CloudSessionID: "cloud-" + id,
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	remoteRoot := t.TempDir()
	remoteDir := "out"
	tr := &FilesystemTransport{Root: remoteRoot}
	f, err := os.Open(plan.BundlePath)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := f.Stat()
	if err := tr.Put(ctx, filepath.Join(remoteDir, filepath.Base(plan.BundlePath)), f, st.Size()); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	// A fresh destination with no prior state pulls into its own root.
	destStore, destHome := newTestStore(t)
	destPlanner := &Planner{Store: destStore, Home: destHome}
	res, err := Pull(ctx, PullOptions{
		Planner: destPlanner, Transport: tr, RemoteDir: remoteDir,
		LocalDir:    filepath.Join(destHome, "downloads"),
		SessionRoot: filepath.Join(destHome, "sessions-v4"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Apply.InstalledSessionDir == "" {
		t.Fatal("pull did not install the session")
	}
	if _, err := session.ReplayExport(res.Apply.InstalledSessionDir); err != nil {
		t.Fatalf("pulled session is not replayable: %v", err)
	}
	if st, ok := destStore.Get(id); !ok || st.Owner != OwnerLocal {
		t.Fatalf("pulled state wrong: %+v", st)
	}
}
