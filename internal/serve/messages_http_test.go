package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pondera/internal/agent"
	"pondera/internal/config"
	"pondera/internal/control"
	"pondera/internal/provider"
)

// fixtureRunner mirrors what the real agent runner does: append the visible
// user turn (followed by a reply) to the session, then report the input. The
// controller records the fork boundary before the runner is called, so this is
// what makes a turn forkable through the message endpoints.
type fixtureRunner struct {
	got     chan string
	session *agent.Session
}

func (r fixtureRunner) Run(_ context.Context, input string) error {
	r.session.Add(provider.Message{Role: provider.RoleUser, Content: input})
	r.session.Add(provider.Message{Role: provider.RoleAssistant, Content: "reply: " + input})
	r.got <- input
	return nil
}

// messageOpsFixture builds a legacy (path-backed) serve host with a real
// controller, a frame tag and a session-lease keeper, so message operations can
// be exercised end to end through their HTTP handlers.
type messageOpsFixture struct {
	server *Server
	ctrl   *control.Controller
	bc     *Broadcaster
	tag    *sessionTagSink
	leases *control.SessionLeaseKeeper
	parent string
	runner chan string
}

// newMessageOpsFixture builds a legacy (path-backed) serve host with a real
// controller and a frame tag. When withLeases is set the host also owns a
// session-lease keeper, which installs the controller's transition hook, so
// the fork route/tag/lease are routed by the controller itself; without a
// keeper the listener only sees what the endpoints hand off explicitly.
func newMessageOpsFixture(t *testing.T, withLeases bool) *messageOpsFixture {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "source.jsonl")
	bc := NewBroadcaster()
	tag := newSessionTagSink(bc)
	runner := make(chan string, 8)
	sess := agent.NewSession("sys")
	exec := agent.New(nil, nil, sess, agent.Options{}, tag)
	ctrl := control.New(control.Options{
		Runner: fixtureRunner{got: runner, session: sess}, Executor: exec, Sink: tag,
		SessionDir: dir, SessionPath: path, Label: "test",
	})
	t.Cleanup(ctrl.Close)
	tag.SetPath(path)
	bc.SetCurrentSession(path)

	server := New(ctrl, bc, config.ServeConfig{})
	server.RegisterSessionTag(ctrl, tag)
	f := &messageOpsFixture{
		server: server, ctrl: ctrl, bc: bc, tag: tag,
		parent: agent.CanonicalSessionPath(path), runner: runner,
	}
	if withLeases {
		leases := control.NewSessionLeaseKeeper()
		t.Cleanup(leases.Release)
		if err := leases.Rebind(path); err != nil {
			t.Fatalf("seed parent lease: %v", err)
		}
		if err := server.SetSessionLeases(leases); err != nil {
			t.Fatalf("wire lease keeper: %v", err)
		}
		f.leases = leases
	}
	return f
}

// turn runs one complete turn. A completed turn is the only forkable boundary,
// so message operations need at least one before they can resolve a target.
func (f *messageOpsFixture) turn(t *testing.T, input string) {
	t.Helper()
	f.ctrl.Submit(input)
	select {
	case got := <-f.runner:
		if got != input {
			t.Fatalf("runner input = %q, want %q", got, input)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("runner never received %q", input)
	}
	waitNotRunning(t, f.ctrl)
}

func firstUserMessageIndex(msgs []provider.Message) int {
	for i, m := range msgs {
		if m.Role == provider.RoleUser {
			return i
		}
	}
	return -1
}

func postMessageOp(t *testing.T, baseURL, route string, body any) (*http.Response, messageOpResponse) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(baseURL+route, "application/json", strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out messageOpResponse
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
	}
	return resp, out
}

// TestEditMessageHandsOffForkedSession guards the gap where edit forked but only
// cleared the old ledger: the child session must become the listener's current
// route and the frame tag must stamp child frames. The fixture has no lease
// keeper here (and none is needed: the controller refuses to fork without its
// transition hook), so these assertions only pass if the endpoint itself
// performs the handoff.
func TestEditMessageHandsOffForkedSession(t *testing.T) {
	f := newMessageOpsFixture(t, false)
	f.turn(t, "first prompt")
	history := f.ctrl.History()
	targetIdx := firstUserMessageIndex(history)
	if targetIdx < 0 {
		t.Fatalf("turn did not record a user message: %+v", history)
	}
	httpServer := httptest.NewServer(operatorHandler(f.server))
	defer httpServer.Close()

	resp, out := postMessageOp(t, httpServer.URL, "/messages/edit", map[string]string{
		"messageId": history[targetIdx].ID, "newInput": "edited prompt",
		"submissionId": "edit-submission", "operationId": "op-" + t.Name(),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("edit status = %d, want 200", resp.StatusCode)
	}
	if out.ForkedSessionPath == "" || agent.CanonicalSessionPath(out.ForkedSessionPath) != agent.CanonicalSessionPath(f.ctrl.SessionPath()) {
		t.Fatalf("forkedSessionPath = %q, controller path = %q", out.ForkedSessionPath, f.ctrl.SessionPath())
	}
	child := agent.CanonicalSessionPath(f.ctrl.SessionPath())
	if child == f.parent {
		t.Fatalf("edit did not move the controller to a child session: %q", child)
	}
	if got := f.bc.CurrentSession(); got != child {
		t.Fatalf("broadcaster current session = %q, want child %q", got, child)
	}
	if got := f.tag.Path(); got != child {
		t.Fatalf("tag path = %q, want child %q", got, child)
	}
	select {
	case got := <-f.runner:
		if got != "edited prompt" {
			t.Fatalf("resend input = %q, want edited prompt", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("edit did not resend into the forked session")
	}
	// The resend is asynchronous: wait for the turn to finish persisting before
	// the test returns, or TempDir cleanup races the session writer.
	waitNotRunning(t, f.ctrl)
}

// TestRetractMessageHandsOffForkedSessionAndReportsAffected covers both halves
// of the retract contract: the conversation scope forks (so the lease/route
// handoff must follow) and the reported affected count must equal the messages
// the fork actually dropped.
func TestRetractMessageHandsOffForkedSessionAndReportsAffected(t *testing.T) {
	f := newMessageOpsFixture(t, true)
	f.turn(t, "first prompt")
	f.turn(t, "second prompt")

	before := f.ctrl.History()
	targetIdx := firstUserMessageIndex(before)
	if targetIdx < 0 {
		t.Fatalf("turns did not record a user message: %+v", before)
	}
	httpServer := httptest.NewServer(operatorHandler(f.server))
	defer httpServer.Close()

	resp, out := postMessageOp(t, httpServer.URL, "/messages/retract", map[string]string{
		"messageId": before[targetIdx].ID, "scope": "conversation", "operationId": "op-" + t.Name(),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("retract status = %d, want 200", resp.StatusCode)
	}
	wantAffected := len(before) - targetIdx
	if out.Affected != wantAffected {
		t.Fatalf("affected = %d, want %d (history %d, boundary %d)", out.Affected, wantAffected, len(before), targetIdx)
	}
	if got := len(f.ctrl.History()); got != targetIdx {
		t.Fatalf("live transcript after retract = %d messages, want %d", got, targetIdx)
	}
	child := agent.CanonicalSessionPath(f.ctrl.SessionPath())
	if child == f.parent {
		t.Fatalf("retract did not move the controller to a child session: %q", child)
	}
	if got := f.bc.CurrentSession(); got != child {
		t.Fatalf("broadcaster current session = %q, want child %q", got, child)
	}
	if got := f.tag.Path(); got != child {
		t.Fatalf("tag path = %q, want child %q", got, child)
	}
	if got := f.leases.HeldPath(); got != child {
		t.Fatalf("lease held on %q, want child %q", got, child)
	}
}

// TestRewindEndpointHandsOffForkedSession proves the pre-existing /rewind route
// shared the same gap as retract/edit and is fixed by the same handoff.
func TestRewindEndpointHandsOffForkedSession(t *testing.T) {
	f := newMessageOpsFixture(t, true)
	f.turn(t, "first prompt")

	httpServer := httptest.NewServer(operatorHandler(f.server))
	defer httpServer.Close()
	resp, err := http.Post(httpServer.URL+"/rewind", "application/json",
		strings.NewReader(`{"turn":0,"scope":"conversation"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		body, _ := readAll(resp)
		t.Fatalf("rewind status = %d (body %q), want 204", resp.StatusCode, body)
	}
	child := agent.CanonicalSessionPath(f.ctrl.SessionPath())
	if child == f.parent {
		t.Fatalf("rewind did not move the controller to a child session: %q", child)
	}
	if got := f.bc.CurrentSession(); got != child {
		t.Fatalf("broadcaster current session = %q, want child %q", got, child)
	}
	if got := f.tag.Path(); got != child {
		t.Fatalf("tag path = %q, want child %q", got, child)
	}
	if got := f.leases.HeldPath(); got != child {
		t.Fatalf("lease held on %q, want child %q", got, child)
	}
}
