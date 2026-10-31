import assert from "node:assert/strict";
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { JSDOM } from "jsdom";
import { installDesktopHostStub } from "./desktopHostStub";
import { makeSessionUIMock } from "../lib/sessionUIMock";

import { useSessionNavigationCommands, type SessionNavigationCommandsInput } from "../app-runtime/useSessionNavigationCommands";

const dom = new JSDOM("<div id='root'></div>");
Object.assign(globalThis, {
  window: dom.window,
  document: dom.window.document,
  IS_REACT_ACT_ENVIRONMENT: true,
});

let intent = 0;
let commands!: ReturnType<typeof useSessionNavigationCommands>;
let blank = false;
let probeFailure: Error | null = null;
const probeCalls: string[] = [];
const created: string[] = [];
const enqueued: Array<{ request: unknown; intent: number }> = [];

installDesktopHostStub(makeSessionUIMock(async (_scope, _root, id) => { created.push(id); }));

function Probe({ remote }: { remote?: unknown }) {
  commands = useSessionNavigationCommands({
    activeTab: remote
      ? { id: "fixture", scope: "global", workspaceRoot: "", remote } as SessionNavigationCommandsInput["activeTab"]
      : { id: "fixture", scope: "project", workspaceRoot: "/workspace" },
    showToast: () => {},
    closeTransientOverlays: () => {},
    clearImDetail: () => {},
    prepareBlankWorkspace: () => {},
    navigation: {
      enqueueNavigation: async () => {},
      enqueueNavigationWithIntent: async (request, navigationIntent) => {
        enqueued.push({ request, intent: navigationIntent });
      },
      openRemoteProject: async () => ({ status: "cancelled", reason: "superseded" }),
    },
    noteNavigationIntent: () => ++intent,
    beginNavigationSurface: () => {},
    settleNavigationSurface: () => {},
    isNavigationIntentCurrent: (candidate) => candidate === intent,
    markProjectChanged: () => {},
    refreshTabMetas: async () => {},
    refreshHistoryView: () => {},
    enterConversation: () => {},
    pickWorkspace: async () => "",
    switchWorkspace: async () => {},
    ports: {
      openTaskSessionForTab: async () => ({ ok: false }),
      listSessionsForTab: async () => [],
      tabSessionIsBlank: async (tabId: string) => {
        probeCalls.push(tabId);
        if (probeFailure) throw probeFailure;
        return blank;
      },
    },
  } as SessionNavigationCommandsInput);
  return null;
}

const root = createRoot(document.getElementById("root")!);
try {
  await act(async () => { root.render(<Probe />); });

  blank = true;
  await act(async () => { await commands.handleNewTab(); });
  assert.deepEqual(created, [], "an untouched blank session is reused instead of creating another formal identity");
  assert.deepEqual(probeCalls, ["fixture"]);
  assert.deepEqual(enqueued, [], "reuse keeps the current surface without a new navigation");

  blank = false;
  await act(async () => { await commands.handleNewTab(); });
  assert.equal(created.length, 1, "a session with content still gets a new formal creation");
  assert.deepEqual(enqueued.at(-1)?.request, {
    kind: "canonical-session", ref: { hostId: "local", sessionId: created[0] },
  }, "creation still lands on the new canonical session");

  probeFailure = new Error("older host without the blank probe");
  await act(async () => { await commands.handleNewTab(); });
  assert.equal(created.length, 2, "a failed blank probe falls back to formal creation");
  probeFailure = null;

  const probeCount = probeCalls.length;
  enqueued.length = 0;
  await act(async () => { root.render(<Probe remote={{ hostId: "fixture", workspace: "waiting" }} />); });
  await act(async () => { await commands.handleNewTab(); });
  assert.equal(probeCalls.length, probeCount, "remote tabs never consult the local blank probe");

  await act(async () => { root.unmount(); });
  console.log("new tab: blank sessions are reused, content and probe failures still create, remote never probes");
} finally {
  dom.window.close();
}
