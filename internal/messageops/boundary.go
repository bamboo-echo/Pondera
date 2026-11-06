// Package messageops resolves a stable message identity to an atomic turn
// boundary, which is the shared primitive behind "retract a sent message" and
// "edit a sent message".
//
// It deliberately contains no control/session mutation: callers (the HTTP
// server) take the resolved boundary and drive the existing Rewind / Fork
// machinery. That keeps this layer small, pure and independently testable.
package messageops

import (
	"errors"
	"fmt"
	"strings"

	"pondera/internal/provider"
)

// Code is a stable, machine-readable refusal reason. Frontends must surface
// these instead of reinterpreting a stale request against the current tab.
type Code string

const (
	CodeInvalid     Code = "invalid"
	CodeNotFound    Code = "not_found"
	CodeUnavailable Code = "unavailable"
	CodeCompacted   Code = "compacted"
	CodeReadOnly    Code = "read_only"
	CodeBusy        Code = "busy"
	CodeStaleSource Code = "stale_source"
)

// Error is a structured refusal.
type Error struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }

func fail(c Code, format string, args ...any) error {
	return &Error{Code: c, Message: fmt.Sprintf(format, args...)}
}

// Boundary is the resolved cut point.
type Boundary struct {
	MessageID    string `json:"messageId"`
	MessageIndex int    `json:"messageIndex"`
	Turn         int    `json:"turn"`
	// Affected is how many messages (including the target) are dropped when
	// the conversation is truncated at this boundary.
	Affected int  `json:"affected"`
	Exact    bool `json:"exact"`
}

// ResolveBoundary finds the turn that owns messageID.
//
// turnsByIndex is Controller.CheckpointTurnsByMessageIndex(): a map from the
// message index at which a turn started to that turn's number. When the target
// message has no exact entry (it sits in the middle of a turn, or the session
// was compacted), we fall back to the nearest preceding recorded boundary and
// mark the result as inexact so the UI can warn before a destructive rewind.
func ResolveBoundary(msgs []provider.Message, turnsByIndex map[int]int, messageID string) (Boundary, error) {
	messageID = strings.TrimSpace(messageID)
	if messageID == "" {
		return Boundary{}, fail(CodeInvalid, "messageId is required")
	}
	idx := -1
	for i, m := range msgs {
		if m.ID == messageID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Boundary{}, fail(CodeNotFound, "message %q is not in this session", messageID)
	}
	if len(turnsByIndex) == 0 {
		return Boundary{}, fail(CodeUnavailable, "no checkpoint boundaries; retract/edit unavailable for this session")
	}

	if turn, ok := turnsByIndex[idx]; ok {
		return Boundary{
			MessageID:    messageID,
			MessageIndex: idx,
			Turn:         turn,
			Affected:     len(msgs) - idx,
			Exact:        true,
		}, nil
	}
	// Nearest preceding recorded boundary.
	bestIdx, bestTurn, found := -1, 0, false
	for boundaryIdx, turn := range turnsByIndex {
		if boundaryIdx <= idx && (!found || boundaryIdx > bestIdx) {
			bestIdx, bestTurn, found = boundaryIdx, turn, true
		}
	}
	if !found {
		return Boundary{}, fail(CodeCompacted,
			"message %q predates every recorded boundary (compacted session)", messageID)
	}
	return Boundary{
		MessageID:    messageID,
		MessageIndex: idx,
		Turn:         bestTurn,
		Affected:     len(msgs) - idx,
		Exact:        false,
	}, nil
}

// ValidateEditable enforces the product rule that only user messages can be
// edited. Assistant/tool messages are retractable but not editable.
func ValidateEditable(msgs []provider.Message, b Boundary) error {
	if b.MessageIndex >= len(msgs) {
		return fail(CodeStaleSource, "message index is stale")
	}
	if msgs[b.MessageIndex].Role != provider.RoleUser {
		return fail(CodeInvalid, "only user messages can be edited")
	}
	return nil
}

// ParseScope maps the wire value to a scope name, rejecting unknown values.
// The HTTP layer maps the result to control.RewindScope.
func ParseScope(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "conversation":
		return "conversation", nil
	case "both":
		return "both", nil
	case "code":
		return "code", nil
	default:
		return "", fail(CodeInvalid, "scope must be conversation, both, or code")
	}
}

// ValidateNewInput checks an edited message before it is submitted to a fork.
func ValidateNewInput(input string) (string, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", fail(CodeInvalid, "newInput must not be empty")
	}
	if len(trimmed) > 1<<20 {
		return "", fail(CodeInvalid, "newInput is too large")
	}
	if strings.HasPrefix(trimmed, "!") {
		return "", fail(CodeInvalid, "shell commands are unavailable over HTTP")
	}
	return trimmed, nil
}

// AsError unwraps a structured refusal for HTTP status selection.
func AsError(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}
