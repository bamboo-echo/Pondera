// 工作区菜单的远程入口：只允许一个，且点击后必须真的弹出选择器。
// Run: node --import ./scripts/svg-stub-register.mjs --import tsx src/__tests__/workspace-menu-remote-entry.test.tsx
import { JSDOM } from "jsdom";
import React from "react";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { LocaleProvider } from "../lib/i18n";
import type { AppBindings } from "../lib/bridge";
import { ComposerWorkspaceContextBar, type ComposerWorkspaceContext } from "../components/ComposerWorkspaceContextBar";
import { installDesktopHostStub } from "./desktopHostStub";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) { process.stdout.write(`  PASS  ${label}\n`); passed += 1; }
  else { process.stdout.write(`  FAIL  ${label}\n`); failed += 1; }
}
const flush = () => new Promise<void>((r) => setTimeout(r, 0));

const dom = new JSDOM("<!doctype html><html><body></body></html>", { pretendToBeVisual: true, url: "http://localhost/" });
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.Node = dom.window.Node;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Event = dom.window.Event;
globalThis.CustomEvent = dom.window.CustomEvent;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.localStorage = dom.window.localStorage;
globalThis.sessionStorage = dom.window.sessionStorage;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
Object.defineProperty(dom.window.HTMLElement.prototype, "attachEvent", { configurable: true, value: () => undefined });
Object.defineProperty(dom.window.HTMLElement.prototype, "detachEvent", { configurable: true, value: () => undefined });
window.scrollTo = () => {};
window.matchMedia = () => ({ matches: true, media: "", onchange: null, addListener: () => undefined, removeListener: () => undefined, addEventListener: () => undefined, removeEventListener: () => undefined, dispatchEvent: () => false });

installDesktopHostStub(({
  main: {
    App: {
      ListProjects: async () => [],
      RemoteHosts: async () => [{ id: "xlt", name: "xlt", host: "101.43.1.123", port: 22, user: "root", connected: false }],
      ListRemoteDir: async () => ({ entries: [] }),
      GetGitCommitView: async () => null,
    } as Partial<AppBindings> as AppBindings,
  },
}).main.App);

const context: ComposerWorkspaceContext = {
  scope: "project", workspaceRoot: "/work/demo", workspaceName: "demo", scopeKey: "project:/work/demo",
  onSwitchWorkspace: async () => undefined, onWorkWithoutProject: async () => undefined, onRefreshProjects: async () => undefined,
};

const el = document.createElement("div");
document.body.appendChild(el);
const root = createRoot(el);
await act(async () => {
  root.render(
    <LocaleProvider>
      <ComposerWorkspaceContextBar context={context} tabId="tab-1" />
    </LocaleProvider>,
  );
  await flush(); await flush();
});

const trigger = el.querySelector<HTMLButtonElement>(".composer-workspace-bar__choice");
ok(trigger !== null, "workspace bar renders its project trigger");
await act(async () => { trigger?.click(); await flush(); });
const actionItems = Array.from(el.querySelectorAll(".composer-workspace-menu__actions [role=menuitem]"));
const labels = actionItems.map((item) => (item.textContent ?? "").trim());
const remoteLabels = labels.filter((label) => label.includes("SSH") || label.includes("远程"));
ok(remoteLabels.length === 1, `exactly one remote entry in the workspace menu (got ${JSON.stringify(remoteLabels)})`);
ok(!labels.some((label) => label === "远程连接"), "the legacy 远程连接 entry is gone");

const remoteItem = actionItems.find((item) => (item.textContent ?? "").includes("SSH"));
await act(async () => { (remoteItem as HTMLButtonElement | undefined)?.click(); await flush(); await flush(); });
await act(async () => { await new Promise((r) => setTimeout(r, 50)); });
const dialog = el.ownerDocument.querySelector('[role=dialog]') ?? document.querySelector('[role=dialog]');
ok(dialog !== null, "clicking the remote entry opens the remote workspace picker");

await act(async () => { root.unmount(); });
el.remove();
process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
