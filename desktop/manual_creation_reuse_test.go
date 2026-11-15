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

// A click that lands while the first creation is still building must land on
// that same conversation. It is already a workspace member with no content, so
// the durable fallback answers the click and no second empty identity is
// reserved: a click storm produces one conversation, not one per click.
func TestManualCreationReusesCreationStillBuilding(t *testing.T) {
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
	if during.Ref != first.Ref {
		t.Fatalf("click during the build reserved %v, want the building %v", during.Ref, first.Ref)
	}
	a.manualCreationTasks.Wait()

	after, err := a.BeginManualSessionCreation(ManualSessionCreationRequest{OperationID: "reuse-inflight-third", Scope: "global"})
	if err != nil {
		t.Fatal(err)
	}
	if after.Ref != first.Ref {
		t.Fatalf("third click reserved %v, want the finished blank %v", after.Ref, first.Ref)
	}
	if got := reuseTestDurableSessions(t, a); got != 1 {
		t.Fatalf("durable sessions = %d, want the single blank a click storm may own", got)
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

// A click that lands after the creation surface was replaced (the tab no longer
// holds the session) must still land on the untouched conversation the workspace
// already owns instead of reserving another empty identity. Tabs are
// presentation; the durable registry is the authority.
func TestManualCreationReusesUntouchedSessionWithoutItsTab(t *testing.T) {
	a := newManualSessionTestApp(t)
	first, err := a.BeginManualSessionCreation(ManualSessionCreationRequest{OperationID: "reuse-detached-first", Scope: "global"})
	if err != nil {
		t.Fatal(err)
	}
	a.manualCreationTasks.Wait()

	a.mu.Lock()
	for id := range a.tabs {
		delete(a.tabs, id)
	}
	a.tabOrder = nil
	a.activeTabID = ""
	a.mu.Unlock()

	second, err := a.BeginManualSessionCreation(ManualSessionCreationRequest{OperationID: "reuse-detached-second", Scope: "global"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Ref.SessionID != first.Ref.SessionID {
		t.Fatalf("second click reserved %s, want the untouched %s", second.Ref.SessionID, first.Ref.SessionID)
	}
	if second.Phase != "ready" {
		t.Fatalf("reused creation phase = %q, want ready", second.Phase)
	}
	if got := reuseTestDurableSessions(t, a); got != 1 {
		t.Fatalf("durable sessions = %d, want the one blank the workspace owns", got)
	}
}
