package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"pondera/internal/provider"
	"pondera/internal/tool"
)

// A cancelled batch can leave a tool goroutine inside the agent, still holding
// the turnRuntime it was handed. The next Run must not swap that state out from
// under it: it waits for the goroutine, and when the goroutine refuses to leave
// it refuses the turn with a readable error instead of racing the new turn.
func TestNextRunRefusesWhileAbandonedToolStillOwnsTheTurn(t *testing.T) {
	oldGrace := parallelStragglerGrace
	parallelStragglerGrace = 100 * time.Millisecond
	t.Cleanup(func() { parallelStragglerGrace = oldGrace })

	stub := stubbornTool{once: &sync.Once{}, started: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(stub.release) }) }
	t.Cleanup(release)
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
	go func() { done <- a.Run(withNoClosedLoop(ctx), "first") }()
	waitForToolStart(t, stub.started)
	waitForToolStart(t, fast.started)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("first Run = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled batch stayed wedged behind a tool that ignores its context")
	}

	before := a.turn
	genBefore := a.reads.runGen
	if live := a.stragglers.live.Load(); live == 0 {
		t.Fatal("the abandoned tool already left the agent; the test premise does not hold")
	}

	// The straggler still owns `before`. A second turn must be refused rather
	// than handed a state object that the straggler is still writing to.
	if err := a.Run(withNoClosedLoop(context.Background()), "second"); !errors.Is(err, ErrPreviousTurnStragglers) {
		t.Fatalf("second Run = %v, want ErrPreviousTurnStragglers", err)
	}
	if a.turn != before {
		t.Fatal("a refused turn still replaced the turn runtime")
	}
	if a.reads.runGen != genBefore {
		t.Fatalf("a refused turn advanced the read generation to %d, want %d", a.reads.runGen, genBefore)
	}

	// Once the straggler leaves, the next turn goes through and gets a fresh
	// runtime and a fresh read registry.
	release()
	deadline := time.Now().Add(5 * time.Second)
	for a.stragglers.live.Load() > 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if live := a.stragglers.live.Load(); live != 0 {
		t.Fatalf("%d stragglers never left the agent", live)
	}
	if err := a.Run(withNoClosedLoop(context.Background()), "third"); err != nil {
		t.Fatalf("Run after the straggler left = %v", err)
	}
	if a.turn == before {
		t.Fatal("the new turn did not get its own turnRuntime")
	}
	if a.reads.runGen != genBefore+1 {
		t.Fatalf("read generation = %d, want %d", a.reads.runGen, genBefore+1)
	}
}

func waitForToolStart(t *testing.T, started chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("tool never started")
	}
}
