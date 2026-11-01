import type { ProviderModelCapabilityView } from "./providerModelCapability";
import type { ProviderModelOverrideView } from "./types";

/**
 * The standard reasoning-effort vocabulary offered when a connection has not
 * declared its own level list. Provider and protocol owners differ (DeepSeek
 * speaks "disabled", OpenAI gpt-5.1 accepts "none", codex-max adds "xhigh"),
 * so declared levels always take precedence over this fallback.
 */
export const STANDARD_EFFORT_LEVELS = ["none", "low", "medium", "high", "xhigh", "max"] as const;

export type ProviderEffortOptionsView = {
  options: string[];
  /** True when the levels come from the connection instead of the standard fallback. */
  declared: boolean;
};

/**
 * Resolves the level list shown in the add/edit-model dialog: capability
 * metadata (catalog or probe) union the model override, else the provider's
 * configured supported_efforts; an empty union falls back to the standard
 * vocabulary unless the connection explicitly reports no adjustable reasoning.
 */
export function providerEffortOptionsForView(input: {
  capability?: Pick<ProviderModelCapabilityView, "reasoning">;
  override?: Pick<ProviderModelOverrideView, "supportedEfforts"> | null;
  providerSupportedEfforts?: string[];
}): ProviderEffortOptionsView {
  const declared = Array.from(new Set([
    ...(input.capability?.reasoning?.options ?? []).map((option) => option.id),
    ...(input.override?.supportedEfforts ?? input.providerSupportedEfforts ?? []),
  ])).filter(Boolean);
  if (declared.length > 0) return { options: declared, declared: true };
  if (input.capability?.reasoning?.state === "unsupported") return { options: [], declared: false };
  return { options: [...STANDARD_EFFORT_LEVELS], declared: false };
}
