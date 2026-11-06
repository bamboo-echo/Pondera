package agent

import (
	"testing"

	"pondera/internal/provider"
)

// A gateway that streams one call by index without an id emits Start, progress
// ticks and the completed call for the same invocation. The partial record must
// keep one entry, not one per chunk.
func TestUpsertPartialToolCallMergesChunksWithoutID(t *testing.T) {
	var calls []provider.ToolCall
	calls = upsertPartialToolCall(calls, provider.ToolCall{Name: "write_file"})
	calls = upsertPartialToolCall(calls, provider.ToolCall{Name: "write_file", Arguments: `{"path":"a"`})
	calls = upsertPartialToolCall(calls, provider.ToolCall{Name: "write_file", Arguments: `{"path":"a","text":"x"}`})
	if len(calls) != 1 {
		t.Fatalf("partial entries = %d, want 1: %+v", len(calls), calls)
	}
	if calls[0].Arguments != `{"path":"a","text":"x"}` {
		t.Fatalf("partial arguments = %q, want the latest snapshot", calls[0].Arguments)
	}

	calls = upsertPartialToolCall(calls, provider.ToolCall{Name: "read_file"})
	if len(calls) != 2 || calls[1].Name != "read_file" {
		t.Fatalf("a new call must append: %+v", calls)
	}
}

func TestUpsertPartialToolCallDedupesChunksWithStableID(t *testing.T) {
	var calls []provider.ToolCall
	calls = upsertPartialToolCall(calls, provider.ToolCall{ID: "call_0", Name: "write_file"})
	calls = upsertPartialToolCall(calls, provider.ToolCall{ID: "call_0", Name: "write_file", Arguments: `{"path":"a"}`})
	calls = upsertPartialToolCall(calls, provider.ToolCall{ID: "call_1", Name: "read_file"})
	calls = upsertPartialToolCall(calls, provider.ToolCall{ID: "call_0", Name: "write_file", Arguments: `{"path":"a","text":"x"}`})
	if len(calls) != 2 {
		t.Fatalf("partial entries = %d, want 2: %+v", len(calls), calls)
	}
	if calls[0].Arguments != `{"path":"a","text":"x"}` || calls[1].Name != "read_file" {
		t.Fatalf("upsert keyed the wrong entry: %+v", calls)
	}
}
