// Mock implementation of the assistant RPC surface for browser-only renders.
// It mirrors the shapes in desktopContract.generated.ts and never touches disk.
import type { AppBindings } from "../lib/bridge";

export type MockAssistantBindings = Pick<
  AppBindings,
  | "AssistantStatus"
  | "AssistantSaveSettings"
  | "AssistantChat"
  | "AssistantApplyPlan"
  | "AssistantDiagnose"
  | "AssistantTestConnectivity"
  | "AssistantTestEndpoint"
  | "AssistantHistory"
  | "AssistantRollback"
>;

const mockStatus = {
  configured: true,
  endpoint: "https://api.deepseek.com/v1",
  model: "deepseek-chat",
  kind: "openai",
  key_set: true,
  config_path: "C:\\Users\\mock\\AppData\\Roaming\\pondera\\config.toml",
  home_dir: "C:\\Users\\mock\\AppData\\Roaming\\pondera\\assistant",
  note: "mock",
};

export function makeMockAssistantBindings(): MockAssistantBindings {
  return {
    async AssistantStatus() {
      return { ...mockStatus };
    },
    async AssistantSaveSettings() {
      return { ...mockStatus };
    },
    async AssistantChat(message: string) {
      if (message.includes("温度")) {
        return {
          reply: "我会把默认温度改为 0.6。",
          plan: {
            id: "mock-plan",
            intent: "调整默认温度",
            reply: "我会把默认温度改为 0.6。",
            diff: [{ label: "agent.temperature", before: "0.2", after: "0.6", sensitive: false, risk: "benign" }],
            requires_confirmation: false,
            comment_warning: "保存会重写 config.toml。",
            op_count: 1,
            expires_at: new Date(Date.now() + 30 * 60_000).toISOString(),
          },
        };
      }
      return { reply: "context_window 决定何时触发上下文压缩。" };
    },
    async AssistantApplyPlan() {
      return {
        backup_id: "mock-backup",
        backup_dir: "/mock/backups",
        applied_ops: 1,
        message: "配置已保存",
        revision: "mock-config-revision",
        credential_revision: "mock-credential-revision",
      };
    },
    async AssistantDiagnose() {
      return {
        checked: 8,
        findings: [
          { code: "compact_disabled", severity: "info", title: "未设置上下文窗口", detail: "mock", provider: "deepseek", fix: "设置 context_window" },
        ],
      };
    },
    async AssistantTestConnectivity(provider: string, model: string) {
      return { ok: true, category: "ok", message: "连接成功", latency_ms: 120, provider, model };
    },
    async AssistantTestEndpoint(_endpoint: string, _kind: string, model: string) {
      return { ok: true, category: "ok", message: "连接成功", latency_ms: 120, provider: "assistant", model };
    },
    async AssistantHistory() {
      return [];
    },
    async AssistantRollback() {
      return { backup_id: "mock-backup", message: "已还原" };
    },
  };
}
