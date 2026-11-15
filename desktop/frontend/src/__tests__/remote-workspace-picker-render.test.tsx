// Run: node --import ./scripts/svg-stub-register.mjs --import tsx src/__tests__/remote-workspace-picker-render.test.tsx
// Renders the real RemoteWorkspacePicker in jsdom: host list, SFTP browse,
// directory descent, and the finish sequence the dialog's button triggers.

import { JSDOM } from "jsdom";
import { act } from "react";
import type { AppBindings } from "../lib/bridge";
import type { RemoteDirEntry, RemoteHostView } from "../lib/types";
import { installDesktopHostStub } from "./desktopHostStub";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

console.log("\nRemote workspace picker (rendered)");
const dom = new JSDOM("<!doctype html><html><body><div id='root'></div></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Event = dom.window.Event;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.KeyboardEvent = dom.window.KeyboardEvent;
Object.defineProperty(dom.window.HTMLElement.prototype, "attachEvent", { configurable: true, value: () => {} });
Object.defineProperty(dom.window.HTMLElement.prototype, "detachEvent", { configurable: true, value: () => {} });

const [{ createRoot }, { RemoteWorkspacePicker }, { LocaleProvider }, { useRemoteStore }] = await Promise.all([
  import("react-dom/client"),
  import("../components/RemoteWorkspacePicker"),
  import("../lib/i18n"),
  import("../store/remote"),
]);

const hosts: RemoteHostView[] = [{
  id: "xlt", label: "xlt", host: "101.43.1.123", port: 22, user: "root",
  identityFile: "~/.ssh/id_ed25519", proxyJump: "", defaultWorkspace: "~",
  serveInstall: "auto", credentialMode: "remote", useSSHConfig: true,
}];
const dirs: Record<string, RemoteDirEntry[]> = {
  "~": [
    { name: "work", path: "/root/work", isDir: true, size: 0, mtimeUnix: 0, symlink: false },
    { name: "notes.md", path: "/root/notes.md", isDir: false, size: 12, mtimeUnix: 0, symlink: false },
  ],
  "/root/work": [
    { name: "api", path: "/root/work/api", isDir: true, size: 0, mtimeUnix: 0, symlink: false },
  ],
};
const tape: string[] = [];
let closed = false;
let mergedMessage = "";
let refreshed = 0;
let navigated: { hostId: string; workspace: string } | null = null;

installDesktopHostStub({
  async RemoteHosts() { tape.push("RemoteHosts"); return hosts.slice(); },
  async ConnectRemoteHost(id: string) {
    tape.push(`Connect:${id}`);
    // The picker waits for the status event before browsing, exactly like the
    // real kernel publishes it after the dial succeeds.
    useRemoteStore.getState().applyStatus({ hostId: id, state: "connected" });
  },
  async ListRemoteDir(_hostId: string, path: string) {
    tape.push(`ListDir:${path}`);
    return dirs[path] ?? [];
  },
  async AddRemoteProject(hostId: string, workspace: string) {
    tape.push(`AddProject:${hostId}:${workspace}`);
    return { hostId, workspace, merged: false };
  },
  async RemoveRemoteProject() { tape.push("RemoveProject"); },
  async RegisterNavigationIntent(token: string) { tape.push(`Intent:${token}`); },
} satisfies Partial<AppBindings> as unknown as Record<string, unknown>);

// The picker navigates through the remote navigation context.
const { RemoteNavigationContext } = await import("../lib/remoteNavigationCommands");
const React = await import("react");

const root = createRoot(document.getElementById("root")!);
await act(async () => {
  root.render(
    React.createElement(LocaleProvider, null,
      React.createElement(RemoteNavigationContext.Provider, {
        value: async (remote, _options) => {
          navigated = { hostId: remote.hostId, workspace: remote.workspace };
          return { status: "ok", value: undefined };
        },
      },
        React.createElement(RemoteWorkspacePicker, {
          onClose: () => { closed = true; },
          onRefresh: async () => { refreshed += 1; },
          onConfigureHost: () => {},
          onMerged: (message) => { mergedMessage = message; },
        }),
      ),
    ),
  );
});
for (let i = 0; i < 40; i++) await Promise.resolve();
await act(async () => { await new Promise((r) => setTimeout(r, 30)); });

const dialog = document.querySelector('[role="dialog"]');
ok(Boolean(dialog), "the picker renders a modal dialog");
ok((dialog?.getAttribute("aria-label") ?? "").length > 0, "the dialog carries an accessible name");

const hostOption = document.querySelector<HTMLOptionElement>("select option");
ok(hostOption?.textContent === "xlt", `the host list offers the saved host (got ${hostOption?.textContent})`);
ok(tape.includes("Connect:xlt"), "the picker connects the host before browsing");
ok(tape.includes("ListDir:~"), "the picker lists the remote home first");

const rows = [...document.querySelectorAll<HTMLButtonElement>(".remote-wizard__dir")];
const workRow = rows.find((row) => row.textContent?.includes("work"));
ok(Boolean(workRow), "the browse list shows the remote directories");
ok(!rows.some((row) => row.textContent?.includes("notes.md")), "the browse list hides non-directories");

await act(async () => { workRow!.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true })); });
for (let i = 0; i < 40; i++) await Promise.resolve();
ok(tape.includes("ListDir:/root/work"), "clicking a directory descends into it");

const openButton = [...document.querySelectorAll<HTMLButtonElement>("button")]
  .find((b) => b.className.includes("remote-wizard__pick-btn"));
ok(Boolean(openButton), "the dialog offers the open-here action");

await act(async () => { openButton!.dispatchEvent(new dom.window.MouseEvent("click", { bubbles: true })); });
for (let i = 0; i < 60; i++) await Promise.resolve();
await act(async () => { await new Promise((r) => setTimeout(r, 30)); });

ok(tape.some((entry) => entry.startsWith("AddProject:xlt:")), "confirming registers the remote project");
ok(navigated?.hostId === "xlt", `confirming opens an in-app remote tab (got ${JSON.stringify(navigated)})`);
ok(refreshed > 0, "confirming refreshes the project tree");
ok(closed, "confirming closes the dialog");
ok(!tape.includes("RemoveProject"), "a successful open does not roll the project back");

await act(async () => { root.unmount(); });
dom.window.close();
process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
