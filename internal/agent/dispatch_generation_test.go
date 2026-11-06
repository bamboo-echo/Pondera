package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"pondera/internal/provider"
	"pondera/internal/tool"
)

type genShiftTool struct {
	gen string
}

func (g *genShiftTool) Name() string            { return "shift" }
func (g *genShiftTool) Description() string     { return "" }
func (g *genShiftTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (g *genShiftTool) ReadOnly() bool          { return true }
func (g *genShiftTool) Execute(context.Context, json.RawMessage) (string, error) {
	return "", nil
}
func (g *genShiftTool) ClassifyCall(json.RawMessage) tool.CallClass {
	return tool.CallClass{Known: true, ReadOnly: true, ParallelSafe: true, Generation: g.gen}
}

func TestDispatchGenerationGateBlocksStaleClassification(t *testing.T) {
	target := &genShiftTool{gen: "v1"}
	a := &Agent{}
	a.turn = a.allocateTurn()
	a.turn.loop.setDispatchClasses(map[string]tool.CallClass{
		"c1": {Known: true, ReadOnly: true, ParallelSafe: true, Generation: "v1"},
	})
	plan := &toolCallPlan{
		call: provider.ToolCall{ID: "c1", Name: "shift", Arguments: `{}`},
		tool: target,
	}
	if _, blocked := a.applyDispatchGenerationGate(plan); blocked {
		t.Fatal("matching generation must dispatch")
	}
	target.gen = "v2"
	out, blocked := a.applyDispatchGenerationGate(plan)
	if !blocked || !strings.Contains(out.output, "generation changed") {
		t.Fatalf("stale generation outcome = %+v", out)
	}
}

// argGenTool classifies a call by a generation embedded in its arguments so two
// calls of the same tool can carry different scheduled classes.
type argGenTool struct{}

func (t *argGenTool) Name() string            { return "argshift" }
func (t *argGenTool) Description() string     { return "" }
func (t *argGenTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (t *argGenTool) ReadOnly() bool          { return true }
func (t *argGenTool) Execute(context.Context, json.RawMessage) (string, error) {
	return "", nil
}
func (t *argGenTool) ClassifyCall(raw json.RawMessage) tool.CallClass {
	var v struct {
		Gen string `json:"gen"`
	}
	_ = json.Unmarshal(raw, &v)
	return tool.CallClass{Known: true, ReadOnly: true, ParallelSafe: true, Generation: v.Gen}
}

func TestDispatchGenerationGateKeysIdLessCallsBySlot(t *testing.T) {
	reg := tool.NewRegistry()
	target := &argGenTool{}
	reg.Add(target)
	a := &Agent{svc: agentServices{tools: reg}}
	a.turn = a.allocateTurn()
	calls := []provider.ToolCall{
		{Name: "argshift", Arguments: `{"gen":"v1"}`},
		{Name: "argshift", Arguments: `{"gen":"v2"}`},
	}
	assignMissingCallIDs(calls)
	a.snapshotDispatchClasses(calls)
	for i := range calls {
		out, blocked := a.applyDispatchGenerationGate(&toolCallPlan{call: calls[i], tool: target})
		if blocked {
			t.Fatalf("call %d with its own scheduled class was blocked as stale: %+v", i, out)
		}
	}
}
