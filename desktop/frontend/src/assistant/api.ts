// Thin typed wrapper over the host assistant RPC. UI code never touches disk.
import { app } from "../lib/bridge";
import type {
  AssistantApplyResult,
  AssistantChatResult,
  AssistantConnectivity,
  AssistantDiagnoseResult,
  AssistantHistoryRecord,
  AssistantRollbackResult,
  AssistantStatusView,
} from "../generated/desktopContract.generated";

export const assistantApi = {
  status: (): Promise<AssistantStatusView> => app.AssistantStatus(),
  saveSettings: (endpoint: string, key: string, model: string, kind: string): Promise<AssistantStatusView> =>
    app.AssistantSaveSettings(endpoint, key, model, kind),
  chat: (message: string): Promise<AssistantChatResult> => app.AssistantChat(message),
  applyPlan: (planId: string): Promise<AssistantApplyResult> => app.AssistantApplyPlan(planId),
  diagnose: (): Promise<AssistantDiagnoseResult> => app.AssistantDiagnose(),
  testConnectivity: (provider: string, model: string, draftKey: string): Promise<AssistantConnectivity> =>
    app.AssistantTestConnectivity(provider, model, draftKey),
  testEndpoint: (endpoint: string, kind: string, model: string, draftKey: string): Promise<AssistantConnectivity> =>
    app.AssistantTestEndpoint(endpoint, kind, model, draftKey),
  history: (): Promise<AssistantHistoryRecord[]> => app.AssistantHistory(),
  rollback: (backupId: string): Promise<AssistantRollbackResult> => app.AssistantRollback(backupId),
};
