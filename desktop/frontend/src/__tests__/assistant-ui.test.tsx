// Run: tsx src/__tests__/assistant-ui.test.tsx
// Covers the three assistant UI behaviours that previously had only tsc as a
// safety net: the settings draft guard, the onboarding "unverified" badge, and
// the three connectivity states.
import { JSDOM } from "jsdom";
import React, { act, useEffect } from "react";
import { createRoot } from "react-dom/client";

import { Onboarding } from "../assistant/Onboarding";
import { SettingsPanel } from "../components/SettingsPanel";
import { useSettingsDraftGuard } from "../lib/settingsDrafts";
import type { AssistantConnectivity, AssistantStatusView } from "../generated/desktopContract.generated";
import { LocaleProvider } from "../lib/i18n";
import { baseSettings, flushPromises, installCanvasMock, waitFor } from "../test-support/settingsTestFixtures";
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

function eq(actual: unknown, expected: unknown, label: string) {
  ok(actual === expected, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

console.log("\nassistant UI automation");

const dom = new JSDOM("<!doctype html><html><body></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
Object.defineProperty(dom.window.HTMLElement.prototype, "attachEvent", { configurable: true, value: () => {} });
Object.defineProperty(dom.window.HTMLElement.prototype, "detachEvent", { configurable: true, value: () => {} });
installCanvasMock(dom.window as unknown as Window);
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.Node = dom.window.Node;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Event = dom.window.Event;
globalThis.CustomEvent = dom.window.CustomEvent;
globalThis.KeyboardEvent = dom.window.KeyboardEvent;
globalThis.MouseEvent = dom.window.MouseEvent;
globalThis.localStorage = dom.window.localStorage;
globalThis.sessionStorage = dom.window.sessionStorage;
window.matchMedia = (() => ({ matches: true, addEventListener() {}, removeEventListener() {} })) as any;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
window.scrollTo = () => {};
localStorage.clear();

const status = {
  configured: true,
  endpoint: "https://api.example.com/v1",
  model: "draft-model",
  kind: "openai",
  key_set: true,
  config_path: "",
  home_dir: "",
  note: "",
} as AssistantStatusView;

const rootEl = document.createElement("div");
document.body.appendChild(rootEl);
const root = createRoot(rootEl);
let resolveProbe: ((value: AssistantConnectivity) => void) | undefined;
let probeCall = 0;
let failNextProbe = false;
const nextProbe = () => new Promise<AssistantConnectivity>((resolve) => { resolveProbe = resolve; });

function probeMode() {
  probeCall += 1;
  return failNextProbe
    ? Promise.resolve<AssistantConnectivity>({ ok: false, category: "rate_limit", message: "限流", latency_ms: 0, provider: "assistant", model: "draft-model" })
    : nextProbe();
}

await act(async () => {
  root.render(
    <Onboarding status={status} saving={false} error={null} onSave={async () => {}} onTest={() => probeMode()} />,
  );
  await flushPromises();
});

const testButton = Array.from(rootEl.querySelectorAll("button")).find((button) => button.textContent?.trim() === "测试连接") as HTMLButtonElement | undefined;
if (!testButton) throw new Error("test connection button did not render");
eq(rootEl.querySelectorAll(".assistant-badge--unverified").length, 1, "a fresh form starts unverified");

await act(async () => {
  testButton.click();
  await flushPromises();
});
ok(testButton.textContent?.includes("测试中…") === true, "testing state is visible while the probe is pending");
ok(testButton.disabled, "testing state disables the button to prevent duplicate probes");

await act(async () => {
  resolveProbe?.({ ok: true, category: "ok", message: "", latency_ms: 12, provider: "assistant", model: "draft-model" });
  await flushPromises();
});
ok(rootEl.textContent?.includes("通过（12 ms）") === true, "passing probe renders the latency");
eq(rootEl.querySelectorAll(".assistant-badge--unverified").length, 0, "passing probe removes the unverified badge");

const presetButton = Array.from(rootEl.querySelectorAll("button")).find((button) => button.textContent?.trim() === "DeepSeek") as HTMLButtonElement | undefined;
if (!presetButton) throw new Error("preset button did not render");
await act(async () => {
  presetButton.click();
  await flushPromises();
});
ok(!rootEl.textContent?.includes("通过（12 ms）"), "editing a field discards the previous probe result");
ok(rootEl.querySelectorAll(".assistant-badge--unverified").length === 1, "editing a field brings the unverified badge back");

failNextProbe = true;
await act(async () => {
  testButton.click();
  await flushPromises();
});
ok(rootEl.textContent?.includes("失败：限流（rate_limit）") === true, "failing probe renders the readable reason and category");
eq(rootEl.querySelectorAll(".assistant-badge--unverified").length, 1, "failing probe keeps the unverified badge");
eq(probeCall, 2, "each test click issues one probe");

await act(async () => {
  root.unmount();
});

function DraftKeeper() {
  useSettingsDraftGuard(true);
  return null;
}

const settingsSnapshots = [baseSettings("standard")];
let settingsCallCount = 0;
const desktopStub = installDesktopHostStub({
  Settings: async () => {
    settingsCallCount += 1;
    return settingsSnapshots[0];
  },
});

const draftRootEl = document.createElement("div");
document.body.appendChild(draftRootEl);
const draftRoot = createRoot(draftRootEl);
await act(async () => {
  draftRoot.render(
    <LocaleProvider>
      <DraftKeeper />
      <SettingsPanel initialTab="general" desktopPlatform="linux" onClose={() => {}} onChanged={() => {}} />
    </LocaleProvider>,
  );
  await flushPromises();
});
await waitFor("settings initial load", () => settingsCallCount === 1 && draftRootEl.textContent?.includes("Desktop & language") === true);

await act(async () => {
  desktopStub.emit("assistant:config-changed", {
    revision: "rev-2",
    credential_revision: "cred-2",
    backup_id: "bak-1",
    summary: [{ label: "agent.temperature", risk: "confirm", sensitive: false }],
  });
  await flushPromises();
});
eq(settingsCallCount, 1, "an unsaved draft blocks the automatic settings reload");
ok(draftRootEl.textContent?.includes("Settings changed by the assistant") === true, "draft guard shows the assistant-change banner");
ok(draftRootEl.textContent?.includes("agent.temperature") === true, "draft guard names the changed field");

const reloadNow = Array.from(draftRootEl.querySelectorAll("button")).find((button) => button.textContent?.trim() === "Reload now") as HTMLButtonElement | undefined;
if (!reloadNow) throw new Error("reload now action did not render");
await act(async () => {
  reloadNow.click();
  await flushPromises();
});
await waitFor("explicit reload", () => settingsCallCount === 2);
ok(draftRootEl.textContent?.includes("settings were reloaded") === true, "explicit reload reports that settings were reloaded");
ok(!draftRootEl.textContent?.includes("Settings changed by the assistant"), "explicit reload clears the draft prompt");

await act(async () => {
  draftRoot.unmount();
  await flushPromises();
});

const autoRootEl = document.createElement("div");
document.body.appendChild(autoRootEl);
const autoRoot = createRoot(autoRootEl);
settingsCallCount = 0;
await act(async () => {
  autoRoot.render(
    <LocaleProvider>
      <SettingsPanel initialTab="general" desktopPlatform="linux" onClose={() => {}} onChanged={() => {}} />
    </LocaleProvider>,
  );
  await flushPromises();
});
await waitFor("settings load without draft", () => settingsCallCount === 1);
await act(async () => {
  desktopStub.emit("assistant:config-changed", {
    revision: "rev-3",
    credential_revision: "cred-3",
    backup_id: "bak-2",
    summary: [{ label: "providers[deepseek-flash].model", risk: "confirm", sensitive: false }],
  });
  await flushPromises();
});
await waitFor("automatic settings reload", () => settingsCallCount === 2);
ok(autoRootEl.textContent?.includes("settings were reloaded") === true, "a clean settings page reloads automatically and reports it");
ok(!autoRootEl.textContent?.includes("Settings changed by the assistant"), "automatic reload does not show the draft prompt");

await act(async () => {
  autoRoot.unmount();
  desktopStub.uninstall();
});
dom.window.close();

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
