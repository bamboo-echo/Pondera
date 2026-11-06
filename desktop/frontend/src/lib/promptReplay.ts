import { app, appCommand } from "./bridge";

const replayInFlight = new Set<string>();

export function replayPendingPromptsForActiveTab(
  activeTabId: string | undefined,
  replay: (tabId: string) => Promise<void> = (tabId) => {
    // Older test/dev bindings fall back to the reconnect-compatible global call.
    const scopedReplay = appCommand("ReplayPendingPromptsForTab");
    return typeof scopedReplay === "function" ? scopedReplay(tabId) : app.ReplayPendingPrompts();
  },
): void {
  if (!activeTabId) return;
  if (replayInFlight.has(activeTabId)) return;
  replayInFlight.add(activeTabId);
  void replay(activeTabId).catch(() => {}).finally(() => replayInFlight.delete(activeTabId));
}
