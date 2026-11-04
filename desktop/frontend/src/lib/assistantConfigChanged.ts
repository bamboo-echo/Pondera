// Normalizes the `assistant:config-changed` event payload. Only redacted
// labels and revision hashes are read; unknown fields are ignored.

export interface AssistantConfigChanged {
  revision: string;
  credentialRevision: string;
  backupId: string;
  labels: string[];
}

export function readAssistantConfigChanged(payload: unknown): AssistantConfigChanged {
  const row = payload && typeof payload === "object" ? (payload as Record<string, unknown>) : {};
  const summary = Array.isArray(row.summary) ? row.summary : [];
  const labels = summary.flatMap((entry) => {
    if (!entry || typeof entry !== "object") return [];
    const label = (entry as Record<string, unknown>).label;
    if (typeof label !== "string" || !label) return [];
    return [(entry as Record<string, unknown>).sensitive === true ? `${label}（脱敏）` : label];
  });
  return {
    revision: typeof row.revision === "string" ? row.revision : "",
    credentialRevision: typeof row.credential_revision === "string" ? row.credential_revision : "",
    backupId: typeof row.backup_id === "string" ? row.backup_id : "",
    labels,
  };
}
