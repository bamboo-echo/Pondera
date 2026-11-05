// Run: tsx src/__tests__/retry-timeline-hydration.test.tsx
// Restart hydration check: persisted retry attempts become a read-only notice
// timeline without ever replaying a running-state banner.
import assert from "node:assert/strict";
import React from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { NoticeCard } from "../components/TranscriptCards";
import { historyMessagesToItems } from "../lib/historyItems";
import { LocaleProvider } from "../lib/i18n";
import type { HistoryMessage } from "../lib/types";

const timeline = [
  { attempt: 1, max: 3, kind: "timeout" as const, retryable: true, status: 504, elapsedMs: 1200 },
  { attempt: 2, max: 3, kind: "output_limit" as const, retryable: false, code: "max_tokens", elapsedMs: 900 },
  { attempt: 3, max: 3, kind: "protocol_replay" as const, retryable: false, elapsedMs: 700 },
];

const failed: HistoryMessage = {
  role: "notice",
  level: "warn",
  code: "provider_request_failed",
  content: "DeepSeek · 请求失败（HTTP 504）。",
  retryTimeline: timeline,
};
const interrupted: HistoryMessage = {
  role: "notice",
  level: "info",
  code: "cancelled_turn_display",
  content: "This turn was interrupted.",
};

const { items } = historyMessagesToItems([failed, interrupted], "he-");
assert.equal(items.length, 2);

const notice = items[0];
assert.equal(notice.kind, "notice");
if (notice.kind !== "notice") throw new Error("expected a notice item");
assert.equal(notice.retryTimeline?.length, timeline.length);
assert.deepEqual(notice.retryTimeline, timeline);

const html = renderToStaticMarkup(
  <LocaleProvider>
    <NoticeCard item={notice} />
  </LocaleProvider>,
);
assert.match(html, /notice-retry-timeline/);
assert.match(html, /#1 · 504 · timed out/);
assert.match(html, /#3 · protocol replay/);
assert.match(html, /output limit/);

const second = items[1];
if (second.kind !== "notice") throw new Error("expected a second notice item");
assert.equal(second.retryTimeline, undefined, "an in-progress turn must not hydrate a timeline");

console.log("Retry timeline hydration: persisted attempts restore as read-only notice details; in-progress turns stay clean.");
