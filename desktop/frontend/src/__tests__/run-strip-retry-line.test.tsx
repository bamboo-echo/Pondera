// Run: node --import ./scripts/css-stub-register.mjs --import tsx src/__tests__/run-strip-retry-line.test.tsx

import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
import React, { act } from "react";
import { createRoot } from "react-dom/client";
import { RunStripRetryLine } from "../components/RunStripRetryLine";
import type { Translator } from "../lib/i18n";
import { recoveryStatusText, retryCountdownSeconds, type WireAttemptFailure } from "../lib/recoveryStatus";

const dom = new JSDOM('<div id="root"></div>', { pretendToBeVisual: true, url: "http://localhost/" });
Object.assign(globalThis, {
  window: dom.window, document: dom.window.document, HTMLElement: dom.window.HTMLElement, Node: dom.window.Node,
  MouseEvent: dom.window.MouseEvent, KeyboardEvent: dom.window.KeyboardEvent, IS_REACT_ACT_ENVIRONMENT: true,
});

const messages: Record<string, string> = {
  "status.retryDetails": "重试详情",
  "status.retrying": "正在重试 ({attempt}/{max})…",
  "status.retryReason": " · {reason}",
  "status.retryBackoff": "{reason}，{seconds} 秒后重试",
  "status.retryingNow": "正在重试…",
  "status.retryKind.rate_limit": "限流",
  "status.retryKind.unknown": "未知错误",
  "status.retryElapsedBackoff": "退避 {seconds}s",
  "status.retryElapsed": "耗时 {seconds}s",
};
const t = ((key: string, vars?: Record<string, string | number>) => {
  const text = messages[key] ?? key;
  return text.replace(/\{(\w+)\}/g, (all, name: string) => (vars && name in vars ? String(vars[name]) : all));
}) as unknown as Translator;

const failedAttempt: WireAttemptFailure = {
  attempt: 3, max: 4, kind: "rate_limit", retryable: true, at: 1_000, backoffMs: 4_000, elapsedMs: 1_200,
};

const root = createRoot(document.getElementById("root")!);
async function render(history: WireAttemptFailure[] | undefined) {
  await act(async () => root.render(
    <RunStripRetryLine t={t} stateText={history?.length ? recoveryStatusText(t, { attempt: 3, max: 4, failure: history[history.length - 1]! }, 2_000) : "思考中…"} history={history}>
      <span className="composer-run-strip__metrics"> 1.2k tokens</span>
    </RunStripRetryLine>,
  ));
}

await render([failedAttempt]);
const toggle = document.querySelector<HTMLButtonElement>(".composer-run-strip__toggle--wide");
assert.ok(toggle, "the row itself is the disclosure trigger");
assert.equal(toggle.getAttribute("aria-expanded"), "false");
assert.equal(document.querySelector(".composer-retry-detail")?.hasAttribute("hidden"), true, "collapsed details are not visible");
assert.ok(toggle.textContent?.includes("1.2k tokens"), "metrics sit inside the trigger so the whole row is clickable");

await act(async () => toggle.click());
assert.equal(toggle.getAttribute("aria-expanded"), "true", "clicking the row expands the details");
assert.equal(document.querySelector(".composer-retry-detail")?.hasAttribute("hidden"), false);
assert.match(document.querySelector(".composer-retry-detail li")?.textContent ?? "", /#3/);

await act(async () => {
  window.dispatchEvent(new dom.window.KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
});
assert.equal(toggle.getAttribute("aria-expanded"), "false", "Escape closes the details");
assert.equal(document.activeElement, toggle, "closing with Escape returns focus to the row");

await act(async () => toggle.click());
const outside = document.createElement("div");
document.body.appendChild(outside);
await act(async () => {
  outside.dispatchEvent(new dom.window.Event("pointerdown", { bubbles: true }));
});
assert.equal(toggle.getAttribute("aria-expanded"), "false", "a pointer press outside closes the details");

// A new turn clears the retry history, which must also forget the open state.
await act(async () => toggle.click());
await render(undefined);
assert.equal(document.querySelector(".composer-run-strip__toggle--wide"), null, "no button without retry history");
assert.equal(document.querySelector(".composer-retry-detail"), null, "no empty detail row without retry history");
await render([failedAttempt]);
assert.equal(document.querySelector(".composer-run-strip__toggle--wide")?.getAttribute("aria-expanded"), "false", "a later retry starts collapsed");

// Countdown: derived from the failure timestamp plus the planned backoff, so
// every tick shortens the displayed wait until the attempt is under way.
const at = failedAttempt.at!;
const backoff = failedAttempt.backoffMs!;
assert.equal(retryCountdownSeconds(failedAttempt, at), 4);
assert.equal(retryCountdownSeconds(failedAttempt, at + 1_000), 3);
assert.equal(retryCountdownSeconds(failedAttempt, at + 3_000), 1);
assert.equal(retryCountdownSeconds(failedAttempt, at + backoff), 0);
assert.match(recoveryStatusText(t, { attempt: 3, max: 4, failure: failedAttempt }, at), /4 秒后重试/);
assert.match(recoveryStatusText(t, { attempt: 3, max: 4, failure: failedAttempt }, at + 1_000), /3 秒后重试/);
assert.equal(recoveryStatusText(t, { attempt: 3, max: 4, failure: failedAttempt }, at + backoff), "正在重试 (3/4) · 正在重试…", "at zero the row says it is retrying now");
assert.match(recoveryStatusText(t, { attempt: 3, max: 4, failure: failedAttempt }, at), /(3\/4)/, "the (n/N) counters are unchanged");

// Unmounting an open row must remove the window listeners it added.
let keydownAdds = 0;
let keydownRemoves = 0;
const addListener = window.addEventListener.bind(window);
const removeListener = window.removeEventListener.bind(window);
window.addEventListener = ((type: string, ...rest: never[]) => {
  if (type === "keydown") keydownAdds += 1;
  return addListener(type, ...rest);
}) as typeof window.addEventListener;
window.removeEventListener = ((type: string, ...rest: never[]) => {
  if (type === "keydown") keydownRemoves += 1;
  return removeListener(type, ...rest);
}) as typeof window.removeEventListener;
await render([failedAttempt]);
await act(async () => document.querySelector<HTMLButtonElement>(".composer-run-strip__toggle--wide")!.click());
await act(async () => root.unmount());
assert.equal(keydownAdds, 1);
assert.equal(keydownRemoves, 1, "unmount removes the Escape listener");

console.log("run-strip-retry-line: ok");
