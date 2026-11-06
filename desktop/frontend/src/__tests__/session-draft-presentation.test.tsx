import assert from "node:assert/strict";
import { register } from "node:module";
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { JSDOM } from "jsdom";

import type { SessionDraftSurface } from "../app-runtime/useSessionDraftSurface";
import { initialState } from "../lib/useController";
import { LocaleProvider, type Translator } from "../lib/i18n";
import { creationHeroVisible } from "../app-shell/draftPresentation";

register(new URL("../../scripts/svg-loader.mjs", import.meta.url));
const { ChatPaneRegion } = await import("../app-shell/ChatPaneRegion");

const dom = new JSDOM("<div id='root'></div>", { url: "http://localhost" });
Object.assign(globalThis, {
  window: dom.window,
  document: dom.window.document,
  localStorage: dom.window.localStorage,
  IS_REACT_ACT_ENVIRONMENT: true,
});

const surface: SessionDraftSurface = {
  kind: "draft",
  draft: {
    id: "draft-project",
    workspaceId: "workspace-project",
    scope: "project",
    workspaceRoot: "/workspace/project",
    revision: 1,
    contentJson: "{}",
    settings: { model: "fixture/model", mode: "normal", toolApprovalMode: "ask", disabledMcp: {}, mcpOrder: [] },
    status: "active",
    updatedAt: 1,
  },
  content: { text: "", invocations: [], attachments: [], workspaceRefs: [], pastedBlocks: [], openPastedLabels: [], sessionRefs: [], selectedTextRefs: [] },
  settings: { model: "fixture/model", mode: "normal", toolApprovalMode: "ask", disabledMcp: {}, mcpOrder: [] },
  commands: [],
  servers: [],
  generation: 1,
  editVersion: 0,
  pendingTasks: 0,
  preparingSubmission: false,
  saveState: "saved",
};

const noop = () => {};
for (const emptyHero of [true, false]) {
  assert.equal(creationHeroVisible(null, emptyHero), emptyHero, "formal sessions keep their own layout");
  assert.equal(creationHeroVisible(surface, emptyHero), true, "healthy drafts own the welcome layout");
  for (const failure of [
    { saveState: "conflict" as const },
    { saveState: "error" as const },
    { taskError: "Attachment failed" },
    { submissionError: "Startup failed" },
  ]) {
    assert.equal(creationHeroVisible({ ...surface, ...failure }, emptyHero), false,
      "draft recovery is never collapsed by a hidden empty formal session");
  }
}
const commands = { onPrompt: noop, onFork: noop, onLoadOlderHistory: async () => false, onLoadNewerHistory: async () => false, onSurfacePaintReady: noop };
const transcript = {
  state: { ...initialState, meta: { ready: true, eventChannel: "fixture" } },
  items: [], tabId: undefined, geometrySessionKey: "draft", footerHeight: 0,
  invocationMetadata: undefined, surfaceCommitToken: undefined, liveStore: undefined,
  transcriptHydrating: false, navigationDataReady: true, readOnly: false, controllerReady: false,
  hydratePlaceholderActive: false, clearContextPending: false, availability: { kind: "ready", source: "history" } as const,
  rewind: { stateActive: false, committing: false },
};
const draftActions = {
  surface,
  onUseSaved: noop,
  onKeepLocal: noop,
  onRetrySave: noop,
  onDismissTaskError: noop,
  onResume: noop,
  onOpenSession: noop,
  onCheckSubmission: noop,
};

const root = createRoot(document.getElementById("root")!);
try {
  await act(async () => root.render(<LocaleProvider><ChatPaneRegion
    transitioning={false} t={((key: string) => key) as Translator} imDetail={null} remote={undefined}
    draft={draftActions} transcript={transcript} onRetryHistory={async () => {}} commands={commands}
  /></LocaleProvider>));
  assert.ok(document.querySelector(".main--draft-landing"), "a healthy draft uses the clean new-session landing surface");
  assert.equal(document.querySelector(".session-draft-surface"), null, "healthy drafts do not render the management page");

  await act(async () => root.render(<LocaleProvider><ChatPaneRegion
    transitioning={false} t={((key: string) => key) as Translator} imDetail={null} remote={undefined}
    draft={{ ...draftActions, surface: { ...surface, saveState: "conflict", conflict: surface.draft } }}
    transcript={transcript} onRetryHistory={async () => {}} commands={commands}
  /></LocaleProvider>));
  assert.ok(document.querySelector(".session-draft-attention[role=alert]"), "a conflict keeps its recovery actions visible");
  assert.equal(document.querySelectorAll(".session-draft-attention button").length, 2, "both conflict resolutions remain available");

  let resumed = 0;
  const runtimeFailure = "create session runtime: path unavailable";
  await act(async () => root.render(<LocaleProvider><ChatPaneRegion
    transitioning={false} t={((key: string) => key) as Translator} imDetail={null} remote={undefined}
    draft={{ ...draftActions, onResume: () => { resumed++; }, surface: { ...surface,
      submissionError: runtimeFailure, taskError: `Error: ${runtimeFailure}`,
      operation: { operationId: "original", requestId: "request", draftId: surface.draft.id, submissionId: "submission", phase: "runtime_failed", revision: 1, updatedAt: 1,
        canResume: true, canEdit: false, canCancel: true, canDiscard: false, error: runtimeFailure },
    } }} transcript={transcript} onRetryHistory={async () => {}} commands={commands}
  /></LocaleProvider>));
  assert.equal(document.querySelector('[role="status"]')?.textContent, "draft.startFailed");
  assert.equal(document.querySelectorAll(".session-draft-surface__error").length, 1, "one runtime error is rendered once");
  assert.ok(!document.body.textContent?.includes("draft.retrySave"), "runtime recovery does not offer a save retry");
  const resumeButton = [...document.querySelectorAll<HTMLButtonElement>(".session-draft-attention button")]
    .find((button) => button.textContent === "draft.resume");
  assert.ok(resumeButton, "runtime recovery offers a resume action");
  await act(async () => resumeButton!.click());
  assert.equal(resumed, 1, "runtime recovery retries the original submission");

  await act(async () => root.unmount());
  console.log("PASS session draft presentation: clean landing and exceptional recovery controls");
} finally {
  dom.window.close();
}
