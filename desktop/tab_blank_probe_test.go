package main

import (
	"testing"

	"reasonix/internal/control"
	"reasonix/internal/provider"
)

type blankProbeController struct {
	stubSessionAPI
	messages []provider.Message
	status   control.RuntimeStatus
}

func (c *blankProbeController) History() []provider.Message          { return c.messages }
func (c *blankProbeController) RuntimeStatus() control.RuntimeStatus { return c.status }
func (c *blankProbeController) Close()                               {}

func TestTabSessionIsBlankReportsUntouchedBlankSessions(t *testing.T) {
	a := newManualSessionTestApp(t)

	a.mu.Lock()
	a.tabs["probe-blank"] = &WorkspaceTab{ID: "probe-blank", Scope: "global", Ctrl: &blankProbeController{}}
	a.tabs["probe-content"] = &WorkspaceTab{ID: "probe-content", Scope: "global", Ctrl: &blankProbeController{
		messages: []provider.Message{{Role: provider.RoleUser, Content: "hello"}}}}
	a.tabs["probe-running"] = &WorkspaceTab{ID: "probe-running", Scope: "global", Ctrl: &blankProbeController{
		status: control.RuntimeStatus{Running: true}}}
	a.tabs["probe-readonly"] = &WorkspaceTab{ID: "probe-readonly", Scope: "global", ReadOnly: true, Ctrl: &blankProbeController{}}
	a.mu.Unlock()

	cases := map[string]struct {
		want bool
	}{
		"probe-blank":    {want: true},
		"probe-content":  {want: false},
		"probe-running":  {want: false},
		"probe-readonly": {want: false},
	}
	for tabID, tc := range cases {
		got, err := a.TabSessionIsBlank(tabID)
		if err != nil {
			t.Fatalf("%s: %v", tabID, err)
		}
		if got != tc.want {
			t.Fatalf("%s: TabSessionIsBlank = %v, want %v", tabID, got, tc.want)
		}
	}

	if _, err := a.TabSessionIsBlank(""); err == nil {
		t.Fatal("empty tab id must be an error")
	}
	if _, err := a.TabSessionIsBlank("missing-tab"); err == nil {
		t.Fatal("unknown tab must be an error")
	}
}

func TestTabSessionIsBlankSeesFreshManualCreationAsBlank(t *testing.T) {
	a := newManualSessionTestApp(t)

	view, err := a.BeginManualSessionCreation(ManualSessionCreationRequest{OperationID: "blank-probe-create", Scope: "global"})
	if err != nil {
		t.Fatal(err)
	}
	a.manualCreationTasks.Wait()

	a.mu.RLock()
	var tabID string
	for id, tab := range a.tabs {
		if tab != nil && tab.SessionID == view.Ref.SessionID {
			tabID = id
		}
	}
	a.mu.RUnlock()
	if tabID == "" {
		t.Fatal("created blank session has no tab")
	}

	blank, err := a.TabSessionIsBlank(tabID)
	if err != nil {
		t.Fatal(err)
	}
	if !blank {
		t.Fatal("a freshly created manual session must probe blank")
	}
}
