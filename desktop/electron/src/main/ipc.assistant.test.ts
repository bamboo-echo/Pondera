import assert from "node:assert/strict";
import type { IpcMain } from "electron";
import { test } from "node:test";
import { IPC, type IpcResult } from "../shared/ipc.js";
import { parseContract } from "./contract.js";
import { registerRendererIpc } from "./ipc.js";

function fakeIpc() {
  const handlers = new Map<string, (event: unknown, ...args: unknown[]) => Promise<IpcResult>>();
  const ipcMain = {
    handle: (channel: string, fn: (event: unknown, ...args: unknown[]) => Promise<IpcResult>) => handlers.set(channel, fn),
    on: () => undefined,
  } as unknown as IpcMain;
  return { ipcMain, handlers };
}

// The assistant window shares the main preload, so it must only reach the
// channels in the explicit allowlist; everything else answers with a readable
// error instead of borrowing a main-window capability.
test("assistant window is scoped to the explicit ipc channel allowlist", async () => {
  const { ipcMain, handlers } = fakeIpc();
  const mainSender = { id: 1 };
  const mainFrame = {};
  const assistantSender = { id: 2 };
  const assistantFrame = {};
  const warnings: string[] = [];
  const invoked: string[] = [];
  registerRendererIpc({
    ipcMain,
    contract: parseContract({ digest: "sha256:a", commands: ["AssistantStatus", "OpenProjectTab"] }),
    window: {
      isTrustedSender: (sender, frame) => sender === mainSender && frame === mainFrame,
      minimise() {},
      toggleMaximise() {},
      isMaximised: () => false,
      close() {},
      bounds: () => ({ x: 0, y: 0, width: 1, height: 1, maximised: false }),
      setTheme() {},
      setBackgroundColour() {},
      getAppZoom: async () => 1,
      setAppZoom: async () => 1,
      resetAppZoom: async () => 1,
    },
    serviceState: () => ({ phase: "ready", generation: "g-test" }),
    performance: {
      captureRendererProfile: async () => ({ status: "captured" }),
      cancelRendererProfile() {},
      exportHeapSnapshot: async () => ({ status: "saved" as const }),
      dispose() {},
    },
    invoke: async (method, args) => {
      invoked.push(method);
      return { method, args };
    },
    clipboard: { writeText: async () => undefined, readText: () => "clip" },
    openExternal: async () => undefined,
    extraTrustedSender: (sender, frame) => sender === assistantSender && frame === assistantFrame,
    log: { info() {}, warn: (message) => warnings.push(message), error() {} },
  });
  const main = { sender: mainSender, senderFrame: mainFrame };
  const assistant = { sender: assistantSender, senderFrame: assistantFrame };
  const call = (channel: string, event: unknown, ...args: unknown[]) => handlers.get(channel)!(event, ...args);

  const denied = "untrusted sender: assistant window channel not allowed";
  const deniedCalls: Array<[string, unknown[]]> = [
    [IPC.clipboardRead, []],
    [IPC.clipboardWrite, ["copied"]],
    [IPC.exportHeapSnapshot, []],
    [IPC.processDiagnostics, []],
    [IPC.openExternal, ["https://example.com"]],
    [IPC.rendererDiagnostic, [{ kind: "transcript" }]],
  ];
  for (const [channel, args] of deniedCalls) {
    const result = await call(channel, assistant, ...args);
    assert.deepEqual(result, { ok: false, message: denied }, `channel ${channel} must be denied`);
    assert.equal(typeof result.message, "string", "denials must carry a message, never undefined");
  }

  // Allowlisted channels keep working for the assistant window.
  assert.deepEqual(await call(IPC.serviceStateGet, assistant), { ok: true, value: { phase: "ready", generation: "g-test" } });
  assert.deepEqual(await call(IPC.windowGetBounds, assistant), { ok: true, value: { x: 0, y: 0, width: 1, height: 1, maximised: false } });
  assert.deepEqual(await call(IPC.windowSetTheme, assistant, "dark"), { ok: true, value: undefined });
  assert.deepEqual(await call(IPC.appZoomGet, assistant), { ok: true, value: 1 });

  // Assistant* RPCs pass, other commands do not.
  assert.deepEqual(await call(IPC.invoke, assistant, "AssistantStatus", []), { ok: true, value: { method: "AssistantStatus", args: [] } });
  assert.deepEqual(await call(IPC.invoke, assistant, "OpenProjectTab", ["/p"]), {
    ok: false,
    message: "untrusted sender: assistant window command not allowed",
  });

  // Main window behaviour is unchanged: every channel and command still works.
  assert.deepEqual(await call(IPC.clipboardRead, main), { ok: true, value: "clip" });
  assert.deepEqual(await call(IPC.openExternal, main, "https://example.com"), { ok: true, value: undefined });
  assert.deepEqual(await call(IPC.exportHeapSnapshot, main), { ok: true, value: { status: "saved" } });
  assert.deepEqual(await call(IPC.invoke, main, "OpenProjectTab", ["/p"]), { ok: true, value: { method: "OpenProjectTab", args: ["/p"] } });

  assert.deepEqual(invoked, ["AssistantStatus", "OpenProjectTab"]);
  assert.ok(warnings.some((message) => message.includes("assistant window channel not allowed")));
  assert.ok(warnings.some((message) => message.includes("assistant-window IPC command")));
});
