import assert from "node:assert/strict";
import { detectReasoningProtocolForModel } from "../lib/reasoningProtocolDetect";

const cases: Array<[string, string, string]> = [
  ["deepseek-v4.1-flash", "deepseek", "DeepSeek V4 family"],
  ["deepseek-v4-pro", "deepseek", "DeepSeek Pro"],
  ["deepseek-chat", "", "older DeepSeek chat stays unset"],
  ["glm-5.2", "glm", "GLM 5.2"],
  ["glm-5.3-flash", "glm", "GLM 5.3 Flash"],
  ["zai_glm-5.3-flash", "glm", "prefixed GLM gateway ID"],
  ["gateway/glm-5.2", "glm", "namespaced GLM gateway ID"],
  ["mimo-v2.6-pro", "glm", "MiMo binary thinking toggle"],
  ["LongCat-2.5-Preview", "glm", "LongCat binary thinking toggle"],
  ["kimi-k3", "kimi-k3", "Kimi K3 fixed levels"],
  ["gpt-6-sol", "openai", "GPT-6 reasoning_effort"],
  ["gpt-5.1", "openai", "GPT-5 reasoning_effort"],
  ["o3-mini", "openai", "o-series reasoning_effort"],
  ["grok-4.7", "openai", "Grok reasoning_effort"],
  ["seed2.1-pro-0915", "openai", "Seed reasoning_effort"],
  ["gemini-3.8-flash", "openai", "Gemini effort levels"],
  ["claude-sonnet-5.5", "", "Claude keeps the Anthropic protocol"],
  ["qwen3.8-max", "", "Qwen needs its own enable_thinking knob"],
  ["some-custom-model", "", "unknown models stay on auto"],
  ["", "", "empty input stays on auto"],
];

for (const [model, want, label] of cases) {
  assert.equal(detectReasoningProtocolForModel(model), want, `${label}: ${model || "(empty)"}`);
}

console.log(`reasoning protocol detection: ${cases.length} model IDs mapped`);
