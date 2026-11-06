package serve

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"pondera/internal/control"
	"pondera/internal/event"
	"pondera/internal/messageops"
)

// messageOpResult is the cached outcome of an idempotent message operation.
// A reconnecting client replays the same operationId and receives the first
// result instead of creating a second fork / rewind.
type messageOpResult struct {
	body   any
	status int
	at     time.Time
}

var (
	messageOpsMu     sync.Mutex
	messageOpsCache  = map[string]messageOpResult{}
	messageOpTTL     = 30 * time.Minute
	messageOpMaxKeys = 1024
)

func cachedMessageOp(operationID string) (messageOpResult, bool) {
	if operationID == "" {
		return messageOpResult{}, false
	}
	messageOpsMu.Lock()
	defer messageOpsMu.Unlock()
	r, ok := messageOpsCache[operationID]
	return r, ok
}

func rememberMessageOp(operationID string, status int, body any) {
	if operationID == "" {
		return
	}
	messageOpsMu.Lock()
	defer messageOpsMu.Unlock()
	now := time.Now()
	for k, v := range messageOpsCache {
		if now.Sub(v.at) > messageOpTTL {
			delete(messageOpsCache, k)
		}
	}
	if len(messageOpsCache) >= messageOpMaxKeys {
		oldest := ""
		var oldestAt time.Time
		for k, v := range messageOpsCache {
			if oldest == "" || v.at.Before(oldestAt) {
				oldest, oldestAt = k, v.at
			}
		}
		delete(messageOpsCache, oldest)
	}
	messageOpsCache[operationID] = messageOpResult{body: body, status: status, at: now}
}

type messageOpResponse struct {
	OK                bool   `json:"ok"`
	OperationID       string `json:"operationId,omitempty"`
	MessageID         string `json:"messageId"`
	Turn              int    `json:"turn"`
	Affected          int    `json:"affected"`
	Exact             bool   `json:"exact"`
	Scope             string `json:"scope,omitempty"`
	Action            string `json:"action"`
	ForkedSessionPath string `json:"forkedSessionPath,omitempty"`
	Warning           string `json:"warning,omitempty"`
	Reused            bool   `json:"reused,omitempty"`
}

type messageOpError struct {
	Code    messageops.Code `json:"code"`
	Message string          `json:"message"`
}

func writeMessageOpError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if e, ok := messageops.AsError(err); ok {
		switch e.Code {
		case messageops.CodeInvalid:
			status = http.StatusBadRequest
		case messageops.CodeNotFound:
			status = http.StatusNotFound
		default:
			status = http.StatusConflict
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(messageOpError{Code: e.Code, Message: e.Message})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(messageOpError{Code: "internal", Message: err.Error()})
}

type retractRequest struct {
	MessageID   string `json:"messageId"`
	Scope       string `json:"scope"`
	OperationID string `json:"operationId"`
}

// retractMessage implements "retract a sent message" by rewinding to the
// atomic turn boundary that owns the message. The existing Rewind machinery
// forks the conversation for conversation scopes, so the parent branch stays
// intact and the operation remains undoable via the existing undoRewind path.
func (s *Server) retractMessage(w http.ResponseWriter, r *http.Request) {
	var body retractRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if cached, ok := cachedMessageOp(strings.TrimSpace(body.OperationID)); ok {
		writeJSONStatus(w, cached.status, cached.body)
		return
	}
	scopeName, err := messageops.ParseScope(body.Scope)
	if err != nil {
		writeMessageOpError(w, err)
		return
	}
	scope := control.RewindConversation
	switch scopeName {
	case "both":
		scope = control.RewindBoth
	case "code":
		scope = control.RewindCode
	}

	s.bindMu.Lock()
	defer s.bindMu.Unlock()
	if !s.validateExpectedSessionLocked(w, r) {
		return
	}
	ctrl := s.ctl()
	if ctrl.Running() {
		writeMessageOpError(w, messageopsError(messageops.CodeBusy,
			"cannot retract while a turn is running; wait or cancel it first"))
		return
	}
	if s.foregroundMirroredLocked() {
		writeMessageOpError(w, messageopsError(messageops.CodeReadOnly, errSessionTakenOver))
		return
	}
	boundary, err := messageops.ResolveBoundary(ctrl.History(), ctrl.CheckpointTurnsByMessageIndex(), body.MessageID)
	if err != nil {
		writeMessageOpError(w, err)
		return
	}
	if !ctrl.CheckpointHasBoundary(boundary.Turn) {
		writeMessageOpError(w, messageopsError(messageops.CodeUnavailable,
			"turn %d has no checkpoint boundary (resumed session)", boundary.Turn))
		return
	}
	if err := ctrl.Rewind(boundary.Turn, scope); err != nil {
		writeMessageOpError(w, err)
		return
	}
	if scope != control.RewindCode {
		s.bc.ResetSession()
	}
	s.bc.Emit(event.Event{
		Kind:  event.Notice,
		Level: event.LevelInfo,
		Text:  "已撤回该消息及其后的 " + itoa(boundary.Affected) + " 条消息；原分支保留，可用撤销恢复。",
	})

	out := messageOpResponse{
		OK: true, OperationID: strings.TrimSpace(body.OperationID),
		MessageID: boundary.MessageID, Turn: boundary.Turn,
		Affected: boundary.Affected, Exact: boundary.Exact,
		Scope: scopeName, Action: "retract",
	}
	if scopeName == "both" {
		out.Warning = "文件回滚只覆盖 checkpoint 捕获到的改动；bash 副作用与 coverage gap 无法保证还原。"
	}
	rememberMessageOp(out.OperationID, http.StatusOK, out)
	writeJSON(w, out)
}

type editRequest struct {
	MessageID    string `json:"messageId"`
	NewInput     string `json:"newInput"`
	Name         string `json:"name,omitempty"`
	OperationID  string `json:"operationId"`
	SubmissionID string `json:"submissionId"`
}

// editMessage implements "edit a sent message" as an atomic fork-and-resend.
// It never mutates a message in place: a message already sent to the model
// cannot be unsent, and its tool side effects may already be on disk. Instead
// the controller forks at the owning turn boundary and immediately submits the
// edited text into the child session. The original branch stays untouched.
func (s *Server) editMessage(w http.ResponseWriter, r *http.Request) {
	var body editRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if cached, ok := cachedMessageOp(strings.TrimSpace(body.OperationID)); ok {
		writeJSONStatus(w, cached.status, cached.body)
		return
	}
	newInput, err := messageops.ValidateNewInput(body.NewInput)
	if err != nil {
		writeMessageOpError(w, err)
		return
	}
	submissionID := strings.TrimSpace(body.SubmissionID)
	if submissionID == "" {
		writeMessageOpError(w, messageopsError(messageops.CodeInvalid, "submissionId is required (idempotency key)"))
		return
	}

	s.bindMu.Lock()
	defer s.bindMu.Unlock()
	if !s.validateExpectedSessionLocked(w, r) {
		return
	}
	ctrl := s.ctl()
	if ctrl.Running() {
		writeMessageOpError(w, messageopsError(messageops.CodeBusy,
			"cannot edit while a turn is running; wait or cancel it first"))
		return
	}
	if s.foregroundMirroredLocked() {
		writeMessageOpError(w, messageopsError(messageops.CodeReadOnly, errSessionTakenOver))
		return
	}
	history := ctrl.History()
	boundary, err := messageops.ResolveBoundary(history, ctrl.CheckpointTurnsByMessageIndex(), body.MessageID)
	if err != nil {
		writeMessageOpError(w, err)
		return
	}
	if err := messageops.ValidateEditable(history, boundary); err != nil {
		writeMessageOpError(w, err)
		return
	}
	if !ctrl.CheckpointHasBoundary(boundary.Turn) {
		writeMessageOpError(w, messageopsError(messageops.CodeUnavailable,
			"turn %d has no checkpoint boundary (resumed session)", boundary.Turn))
		return
	}
	concrete, ok := ctrl.(*control.Controller)
	if !ok {
		writeMessageOpError(w, messageopsError(messageops.CodeUnavailable, "controller does not support forks"))
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = "edited: " + truncateForName(newInput)
	}
	forkPath, err := concrete.ForkNamed(boundary.Turn, name)
	if err != nil {
		writeMessageOpError(w, err)
		return
	}
	if routing, ok := ctrl.(interface{ SetTurnSubmissionID(string) }); ok {
		routing.SetTurnSubmissionID(submissionID)
	}
	if _, err := concrete.SubmitIdentified(control.SubmissionRequest{
		ID: submissionID, HTTP: true, Input: newInput,
	}); err != nil {
		writeMessageOpError(w, err)
		return
	}
	s.bc.ResetSession()

	out := messageOpResponse{
		OK: true, OperationID: strings.TrimSpace(body.OperationID),
		MessageID: boundary.MessageID, Turn: boundary.Turn,
		Affected: boundary.Affected, Exact: boundary.Exact,
		Action: "edit", ForkedSessionPath: forkPath,
		Warning: "已在新分支重新发送；原分支完整保留。此前工具改动的文件不会自动还原。",
	}
	rememberMessageOp(out.OperationID, http.StatusOK, out)
	writeJSON(w, out)
}

// messageTargets lists which messages are retractable / editable in the
// current session so a client can render the correct affordances without
// duplicating boundary logic.
func (s *Server) messageTargets(w http.ResponseWriter, _ *http.Request) {
	s.bindMu.Lock()
	ctrl := s.ctl()
	history := ctrl.History()
	turns := ctrl.CheckpointTurnsByMessageIndex()
	s.bindMu.Unlock()
	type target struct {
		MessageID   string `json:"messageId"`
		Index       int    `json:"index"`
		Role        string `json:"role"`
		Turn        int    `json:"turn,omitempty"`
		Retractable bool   `json:"retractable"`
		Editable    bool   `json:"editable"`
		Exact       bool   `json:"exact"`
	}
	out := make([]target, 0, len(history))
	for i, m := range history {
		b, err := messageops.ResolveBoundary(history, turns, m.ID)
		item := target{MessageID: m.ID, Index: i, Role: string(m.Role)}
		if err == nil {
			item.Turn = b.Turn
			item.Retractable = ctrl.CheckpointHasBoundary(b.Turn)
			item.Editable = item.Retractable && string(m.Role) == "user"
			item.Exact = b.Exact
		}
		out = append(out, item)
	}
	writeJSON(w, map[string]any{"messages": out})
}

func messageopsError(code messageops.Code, format string, args ...any) error {
	return &messageops.Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [24]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func truncateForName(s string) string {
	runes := []rune(s)
	if len(runes) > 32 {
		return string(runes[:32]) + "…"
	}
	return s
}

func writeJSONStatus(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
