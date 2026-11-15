package main

import "strings"

// A "new session" click must never allocate a second untouched conversation in
// the same workspace. The desktop frontend probes the active tab first
// (TabSessionIsBlank), but that probe fails open: a host without the command, a
// stale tab id, and a click that lands before any tab is published all fall
// through to creation, and every entry point had to remember to ask on its own.
// One durable empty session per click was the result.
//
// The decision therefore belongs next to the identity reservation:
// reuseManualCreationTarget names the conversation the click should land on and
// BeginManualSessionCreation hands that identity back instead of reserving a
// new one.
type manualCreationTarget struct {
	sessionID string
	topicID   string
	settings  SessionDraftSettings
}

// reuseManualCreationTarget prefers the active tab, then tab order, so a click
// keeps landing on the surface the user already has instead of moving them.
func (a *App) reuseManualCreationTarget(scope, workspaceRoot, workspaceID string) (manualCreationTarget, bool) {
	a.mu.RLock()
	candidates := make([]manualCreationTarget, 0, len(a.tabs))
	seen := make(map[string]struct{}, len(a.tabs))
	collect := func(tab *WorkspaceTab) {
		target, ok := a.manualCreationTargetLocked(tab, scope, workspaceRoot, workspaceID)
		if !ok {
			return
		}
		if _, duplicate := seen[target.sessionID]; duplicate {
			return
		}
		seen[target.sessionID] = struct{}{}
		candidates = append(candidates, target)
	}
	collect(a.activeTabLocked())
	for _, id := range a.orderedTabIDsLocked() {
		collect(a.tabs[id])
	}
	a.mu.RUnlock()

	// The durable projection decides, not the cached history: a tab whose
	// history is not hydrated yet reports no messages while its session holds a
	// conversation. A projection that cannot answer disqualifies the candidate,
	// which is also how an archived or foreign session stays out of here.
	for _, candidate := range candidates {
		if blank, known := a.sessionHasNoConversationContent(candidate.sessionID); !known || !blank {
			continue
		}
		return candidate, true
	}
	return manualCreationTarget{}, false
}

// manualCreationTargetLocked reports one tab as a reuse candidate. The tab
// carries a bound session identity, is not a read-only transcript, owns no
// running work, and has not been written to yet.
func (a *App) manualCreationTargetLocked(tab *WorkspaceTab, scope, workspaceRoot, workspaceID string) (manualCreationTarget, bool) {
	if tab == nil || tab.removed || tab.ReadOnly {
		return manualCreationTarget{}, false
	}
	if strings.TrimSpace(tab.SessionID) == "" {
		return manualCreationTarget{}, false
	}
	if workspaceID != "" && tab.SessionWorkspace.ID != "" && tab.SessionWorkspace.ID != workspaceID {
		return manualCreationTarget{}, false
	}
	if tab.Scope != scope {
		return manualCreationTarget{}, false
	}
	if scope == "project" && !sameProjectRoot(tab.WorkspaceRoot, workspaceRoot) {
		return manualCreationTarget{}, false
	}
	// A reservation still owned by its creation, or a tab with no controller,
	// belongs to a build in flight: that creation publishes its own surface, and
	// clicking during it is the case the frontend probe already answers. Reusing
	// here would hand out a half-built identity.
	if tab.Ctrl == nil || tab.PendingCreateOperationID != "" {
		return manualCreationTarget{}, false
	}
	if tab.hasActiveRuntimeWork() {
		return manualCreationTarget{}, false
	}
	if messagesHaveConversationContent(tab.Ctrl.History()) {
		return manualCreationTarget{}, false
	}
	target := manualCreationTarget{sessionID: tab.SessionID, topicID: tab.TopicID}
	// The reused conversation keeps its own settings; nothing here re-derives
	// them from the configured defaults the way a fresh reservation does.
	target.settings = SessionDraftSettings{
		Model:            tab.model,
		ModelSource:      draftModelSourceExplicit,
		QualityFloor:     tab.qualityFloorSafe(),
		Mode:             tab.mode,
		ToolApprovalMode: tab.toolApprovalMode,
		DisabledMCP:      cloneServerViewMap(tab.disabledMCP),
		MCPOrder:         append([]string(nil), tab.mcpOrder...),
	}
	if tab.effort != nil {
		target.settings.Effort = *tab.effort
	}
	return target, true
}

// reusableManualCreationTopic keeps the topic a reused conversation already
// reports, and derives a stable one only when the tab never got a topic.
func reusableManualCreationTopic(sessionID, topicID string) string {
	if topicID = strings.TrimSpace(topicID); topicID != "" {
		return topicID
	}
	return "manual-" + strings.TrimPrefix(sessionID, "desktop-manual-")
}
