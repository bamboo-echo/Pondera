export const STATUS_BAR_ITEM_IDS = [
  "workspace",
  "cache",
  "cache_avg",
  "session_tokens",
  "turn_tokens",
  "turn_tps",
  "turn_output_tokens",
  "turn_cache_tokens",
  "turn_cost",
  "session_turns",
  "context",
  "compact",
  "cost",
  "balance",
] as const;

export type StatusBarItemId = typeof STATUS_BAR_ITEM_IDS[number];

// Three readings by default: where you are, how much context is left, what
// this session has cost. The rest are diagnostics — available in the editor
// and the usage panel, not parked in the status bar as a debug strip.
export const DEFAULT_STATUS_BAR_ITEMS: StatusBarItemId[] = ["workspace", "context", "cost"];

const statusBarItemSet = new Set<string>(STATUS_BAR_ITEM_IDS);

export function normalizeStatusBarItems(items: readonly string[] | null | undefined): StatusBarItemId[] {
  const out: StatusBarItemId[] = [];
  const seen = new Set<string>();
  let removedModel = false;
  for (const raw of items ?? []) {
    const value = String(raw ?? "").trim();
    if (value === "model") removedModel = true;
    // Older preferences stored workspace and branch independently. Keep their
    // first configured position while rendering one combined workspace item.
    const id = value === "git_branch" ? "workspace" : value;
    if (!statusBarItemSet.has(id) || seen.has(id)) continue;
    out.push(id as StatusBarItemId);
    seen.add(id);
  }
  return out.length > 0 ? out : removedModel ? ["workspace"] : [...DEFAULT_STATUS_BAR_ITEMS];
}
