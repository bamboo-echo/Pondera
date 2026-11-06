package messageops

import (
	"strings"
	"testing"

	"pondera/internal/provider"
)

func msgs(n int) []provider.Message {
	out := make([]provider.Message, n)
	for i := range out {
		role := provider.RoleAssistant
		if i%2 == 0 {
			role = provider.RoleUser
		}
		out[i] = provider.Message{ID: "m" + string(rune('a'+i)), Role: role, Content: "text"}
	}
	return out
}

func TestResolveBoundaryExact(t *testing.T) {
	m := msgs(8)
	// Turns start at message indices 0, 2, 4, 6.
	turns := map[int]int{0: 1, 2: 2, 4: 3, 6: 4}
	b, err := ResolveBoundary(m, turns, "me") // index 4
	if err != nil {
		t.Fatalf("ResolveBoundary: %v", err)
	}
	if b.Turn != 3 || b.MessageIndex != 4 || b.Affected != 4 || !b.Exact {
		t.Fatalf("unexpected boundary: %+v", b)
	}
}

func TestResolveBoundaryFallback(t *testing.T) {
	m := msgs(8)
	turns := map[int]int{0: 1, 2: 2, 6: 4}
	b, err := ResolveBoundary(m, turns, "mf") // index 5, inside turn 2
	if err != nil {
		t.Fatalf("ResolveBoundary: %v", err)
	}
	// The cut happens at the nearest preceding boundary (index 2), so the
	// truncation drops indices 2..7 (six messages), not just 5..7 (three).
	if b.Turn != 2 || b.Exact || b.MessageIndex != 5 || b.Affected != 6 {
		t.Fatalf("expected inexact fallback to turn 2 with affected 6: %+v", b)
	}
}

func TestResolveBoundaryRefusals(t *testing.T) {
	m := msgs(4)
	if _, err := ResolveBoundary(m, map[int]int{0: 1}, ""); codeOf(err) != CodeInvalid {
		t.Fatalf("empty id: %v", err)
	}
	if _, err := ResolveBoundary(m, map[int]int{0: 1}, "zzz"); codeOf(err) != CodeNotFound {
		t.Fatalf("unknown id: %v", err)
	}
	if _, err := ResolveBoundary(m, nil, "ma"); codeOf(err) != CodeUnavailable {
		t.Fatalf("no boundaries: %v", err)
	}
	// Message predates every recorded boundary.
	if _, err := ResolveBoundary(m, map[int]int{2: 2}, "ma"); codeOf(err) != CodeCompacted {
		t.Fatalf("compacted: %v", err)
	}
}

func TestValidateEditable(t *testing.T) {
	m := msgs(4)
	if err := ValidateEditable(m, Boundary{MessageIndex: 0}); err != nil {
		t.Fatalf("user message should be editable: %v", err)
	}
	if err := ValidateEditable(m, Boundary{MessageIndex: 1}); codeOf(err) != CodeInvalid {
		t.Fatalf("assistant message must not be editable: %v", err)
	}
}

func TestParseScopeAndInput(t *testing.T) {
	for raw, want := range map[string]string{"": "conversation", "CONVERSATION": "conversation", "both": "both", "code": "code"} {
		got, err := ParseScope(raw)
		if err != nil || got != want {
			t.Errorf("ParseScope(%q) = %q, %v", raw, got, err)
		}
	}
	if _, err := ParseScope("nope"); err == nil {
		t.Fatal("expected invalid scope to fail")
	}
	if _, err := ValidateNewInput("   "); err == nil {
		t.Fatal("expected empty input to fail")
	}
	if _, err := ValidateNewInput("!rm -rf /"); err == nil {
		t.Fatal("expected shell command to fail")
	}
	got, err := ValidateNewInput("  hello  ")
	if err != nil || got != "hello" {
		t.Fatalf("ValidateNewInput = %q, %v", got, err)
	}
	if _, err := ValidateNewInput(strings.Repeat("x", (1<<20)+1)); err == nil {
		t.Fatal("expected oversize input to fail")
	}
}

func codeOf(err error) Code {
	if e, ok := AsError(err); ok {
		return e.Code
	}
	return Code("?")
}
