// Model-ID based reasoning protocol detection for gateways whose host name
// does not reveal the vendor. Mirrors the Go config heuristic so the settings
// dropdown can preselect the same mode the runtime will use.
export type ReasoningProtocolId = "" | "deepseek" | "glm" | "kimi-k3" | "openai" | "none";

export function detectReasoningProtocolForModel(modelId: string): ReasoningProtocolId {
  let id = modelId.trim().toLowerCase();
  if (!id) return "";
  // Gateways commonly namespace models (zai/glm-5.3, openai:gpt-6-sol).
  const separator = Math.max(id.lastIndexOf("/"), id.lastIndexOf(":"));
  if (separator >= 0) id = id.slice(separator + 1).trim();
  for (const prefix of ["zai_", "crwzai_", "azure_", "openai_"]) {
    if (id.startsWith(prefix)) id = id.slice(prefix.length);
  }
  if (id.startsWith("deepseek-v4") || id.startsWith("deepseek-reasoner")) return "deepseek";
  if (id.startsWith("glm") || id.includes("glm-")) return "glm";
  // MiMo and LongCat expose a binary thinking.type switch, the same wire shape
  // the GLM protocol sends.
  if (id.startsWith("mimo") || id.startsWith("longcat")) return "glm";
  if (id.startsWith("kimi-k3")) return "kimi-k3";
  if (/^(gpt-|o1|o3|o4|grok|seed|gemini)/.test(id)) return "openai";
  return "";
}
