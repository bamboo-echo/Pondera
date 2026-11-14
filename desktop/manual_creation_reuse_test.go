package main

import (
	"sync"
	"testing"
)

func reuseTestVisibleTabs(a *App) int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	count := 0
	for _, tab := range a.tabs {
		if tab != nil && !tab.removed {
			count++
		}
	}
	return count
}

func reuseTestDurableSessions(t *testing.T, a *App) int {
	t.Helper()
	state, err := a.workspaceRegistry().Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, workspace := range state.Workspaces {
		total += len(workspace.SessionIDs)
	}
	return total
}

// Clicking "new session" while an untouched conversation is already open must
// hand that conversation back instead of reserving another empty identity.
func TestManualCreationReusesUntouchedSessionInSameWorkspace(t *testing.T) {
	a := newManualSessionTestApp(t)
	first, err := a.BeginManualSessionCreation(ManualSessionCreationRequest{OperationID: "reuse-blank-first", Scope: "global"})
	if err != nil {
		t.Fatal(err)
	}
	a.manualCreationTasks.Wait()

	again, err := a.BeginManualSessionCreation(ManualSessionCreationRequest{OperationID: "reuse-blank-second", Scope: "global"})
	if err != nil {
		t.Fatal(err)
	}
	if again.Ref != first.Ref {
		t.Fatalf("second click reserved %v, want the untouched %v", again.Ref, first.Ref)
	}
	if again.Phase != "ready" || !again.SurfaceReady {
		t.Fatalf("reused creation = phase %q surfaceReady=%v, want a surface to land on", again.Phase, again.SurfaceReady)
	}
	a.manualCreationTasks.Wait()
	if got := reuseTestVisibleTabs(a); got != 1 {
		t.Fatalf("visible tabs = %d, want the one reused session", got)
	}
	if got := reuseTestDurableSessions(t, a); got != 1 {
		t.Fatalf("durable sessions = %d, want one blank per click storm", got)
	}
}

// A creation still owned by its own reservation is left alone, so a click that
// lands mid-build never gets handed a half-built identity. Once that build
// finishes, the next click reuses it instead of reserving a third time.
func TestManualCreationLeavesInFlightCreationAlone(t *testing.T) {
	a := newManualSessionTestApp(t)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	a.tabBuildStartHook = func(string) { once.Do(func() { close(started) }); <-release }
	defer func() { a.tabBuildStartHook = nil }()

	first, err := a.BeginManualSessionCreation(ManualSessionCreationRequest{OperationID: "reuse-inflight-first", Scope: "global"})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	during, err := a.BeginManualSessionCreation(ManualSessionCreationRequest{OperationID: "reuse-inflight-second", Scope: "global"})
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	if during.Ref == first.Ref {
		t.Fatalf("click during the build took over the in-flight %v", during.Ref)
	}
	a.manualCreationTasks.Wait()

	after, err := a.BeginManualSessionCreation(ManualSessionCreationRequest{OperationID: "reuse-inflight-third", Scope: "global"})
	if err != nil {
		t.Fatal(err)
	}
	if after.Ref != first.Ref && after.Ref != during.Ref {
		t.Fatalf("third click reserved %v, want one finished blank", after.Ref)
	}
	if got := reuseTestDurableSessions(t, a); got != 2 {
		t.Fatalf("durable sessions = %d, want the two reservations and no third", got)
	}
}

// The reuse is scoped: another workspace keeps its own untouched conversation.
func TestManualCreationKeepsOneUntouchedSessionPerWorkspace(t *testing.T) {
	a := newManualSessionTestApp(t)
	global, err := a.BeginManualSessionCreation(ManualSessionCreationRequest{OperationID: "reuse-scope-global", Scope: "global"})
	if err != nil {
		t.Fatal(err)
	}
	a.manualCreationTasks.Wait()

	root := t.TempDir()
	project, err := a.BeginManualSessionCreation(ManualSessionCreationRequest{OperationID: "reuse-scope-project", Scope: "project", WorkspaceRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	a.manualCreationTasks.Wait()
	if project.Ref == global.Ref {
		t.Fatalf("project click reused the global conversation %v", project.Ref)
	}

	another, err := a.BeginManualSessionCreation(ManualSessionCreationRequest{OperationID: "reuse-scope-project-again", Scope: "project", WorkspaceRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	if another.Ref != project.Ref {
		t.Fatalf("second project click reserved %v, want %v", another.Ref, project.Ref)
	}
	if got := reuseTestDurableSessions(t, a); got != 2 {
		t.Fatalf("durable sessions = %d, want one blank per workspace", got)
	}
}
