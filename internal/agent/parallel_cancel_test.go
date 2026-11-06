package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"pondera/internal/provider"
	"pondera/internal/tool"
)

// stubbornTool ignores its context: it returns only when released.
type stubbornTool struct {
	once    *sync.Once
	started chan struct{}
	release chan struct{}
}

func (stubbornTool) Name() string            { return "stubborn" }
func (stubbornTool) Description() string     { return "ignores cancellation" }
func (stubbornTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (stubbornTool) ReadOnly() bool          { return true }
func (s stubbornTool) Execute(context.Context, json.RawMessage) (string, error) {
	s.once.Do(func() { close(s.started) })
	<-s.release
	return "late", nil
}

// fastTool reports when it has entered execution, so the test cancels only
// after both tools of the batch are running.
type fastTool struct {
	once    *sync.Once
	started chan struct{}
}

func (fastTool) Name() string            { return "fast" }
func (fastTool) Description() string     { return "always succeeds" }
func (fastTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (fastTool) ReadOnly() bool          { return true }
func (f fastTool) Execute(context.Context, json.RawMessage) (string, error) {
	f.once.Do(func() { close(f.started) })
	return "ok", nil
}

// A cancellation drain must report what run() actually finished: a call that
// returned before the drain is finished, a straggler that still ignores
// cancellation is not, and the returned slice must be a snapshot the straggler
// cannot mutate afterwards.
func TestRunParallelSnapshotsFinishedCalls(t *testing.T) {
	oldGrace := parallelStragglerGrace
	parallelStragglerGrace = 100 * time.Millisecond
	t.Cleanup(func() { parallelStragglerGrace = oldGrace })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var started sync.WaitGroup
	started.Add(2)
	release := make(chan struct{})
	defer close(release)

	type result struct {
		ranUntil int
		finished []bool
	}
	out := make(chan result, 1)
	go func() {
		ranUntil, finished := runParallel(ctx, 0, 2, func(i int) {
			started.Done()
			if i == 1 {
				<-release
			}
		})
		out <- result{ranUntil: ranUntil, finished: finished}
	}()

	started.Wait()
	cancel()
	select {
	case got := <-out:
		if got.ranUntil != 2 {
			t.Fatalf("ranUntil = %d, want 2", got.ranUntil)
		}
		if !got.finished[0] {
			t.Fatal("a call whose run() returned before the drain was reported unfinished")
		}
		if got.finished[1] {
			t.Fatal("a straggler that ignored cancellation was reported finished")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runParallel stayed wedged after cancellation")
	}
}

// A read-only parallel segment must not keep the whole turn wedged behind one
// tool that ignores cancellation: after the grace the batch reports that call
// as an unknown effect while the calls that did finish keep their results.
func TestParallelBatchAbandonsToolThatIgnoresCancellation(t *testing.T) {
	oldGrace := parallelStragglerGrace
	parallelStragglerGrace = 200 * time.Millisecond
	t.Cleanup(func() { parallelStragglerGrace = oldGrace })

	stub := stubbornTool{once: &sync.Once{}, started: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(func() { close(stub.release) })
	fast := fastTool{once: &sync.Once{}, started: make(chan struct{})}
	reg := tool.NewRegistry()
	reg.Add(stub)
	reg.Add(fast)
	prov := &scriptedProvider{name: "p", turns: [][]provider.Chunk{
		{toolCallChunk("stubborn-1", "stubborn", `{}`), toolCallChunk("fast-1", "fast", `{}`)},
		{{Type: provider.ChunkText, Text: "done"}},
	}}
	sess := NewSession("")
	a := New(prov, reg, sess, Options{}, &recordSink{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.Run(withNoClosedLoop(ctx), "go") }()
	select {
	case <-stub.started:
	case <-time.After(5 * time.Second):
		t.Fatal("stubborn tool never started")
	}
	select {
	case <-fast.started:
	case <-time.After(5 * time.Second):
		t.Fatal("fast tool never started")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run returned %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled batch stayed wedged behind a tool that ignores its context")
	}
	if got := toolResultByID(sess, "stubborn-1"); !strings.Contains(got, "did not stop after cancellation") {
		t.Fatalf("stubborn result = %q, want the abandoned marker", got)
	}
	if got := toolResultByID(sess, "fast-1"); !strings.Contains(got, "ok") {
		t.Fatalf("fast result = %q, want the finished tool's own output", got)
	}
}
