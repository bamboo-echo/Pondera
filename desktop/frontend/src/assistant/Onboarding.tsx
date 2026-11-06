import { useState } from "react";
import { assistantCopy, fill } from "./copy";
import { useI18n } from "../lib/i18n";
import type { AssistantConnectivity, AssistantStatusView } from "../generated/desktopContract.generated";

const PRESETS = [
  { label: "DeepSeek", endpoint: "https://api.deepseek.com/v1", model: "deepseek-chat", kind: "openai" },
  { label: "OpenAI", endpoint: "https://api.openai.com/v1", model: "gpt-4o-mini", kind: "openai" },
  { label: "Anthropic", endpoint: "https://api.anthropic.com", model: "claude-3-5-sonnet-latest", kind: "anthropic" },
];

export interface OnboardingProps {
  status: AssistantStatusView | null;
  saving: boolean;
  error: string | null;
  onSave: (endpoint: string, key: string, model: string, kind: string) => Promise<void>;
  onTest?: (endpoint: string, key: string, model: string, kind: string) => Promise<AssistantConnectivity>;
  onCancel?: () => void;
}

// Onboarding collects the assistant's own endpoint + key + model. The key is
// write-only: it is never echoed back into this form, logged, or emitted.
export function Onboarding({ status, saving, error, onSave, onTest, onCancel }: OnboardingProps) {
  const { locale } = useI18n();
  const copy = assistantCopy(locale);
  const [endpoint, setEndpoint] = useState(status?.endpoint ?? "");
  const [model, setModel] = useState(status?.model ?? "");
  const [kind, setKind] = useState(status?.kind || "openai");
  const [key, setKey] = useState("");
  const [testing, setTesting] = useState(false);
  const [probe, setProbe] = useState<AssistantConnectivity | null>(null);
  const [verified, setVerified] = useState(false);

  // Any edit invalidates the previous probe; the form never claims a stale
  // draft is verified.
  const edit = <T,>(setter: (value: T) => void) => (value: T) => {
    setter(value);
    setVerified(false);
    setProbe(null);
  };

  const runTest = async () => {
    if (!onTest) return;
    setTesting(true);
    setProbe(null);
    try {
      const result = await onTest(endpoint.trim(), key.trim(), model.trim(), kind);
      setProbe(result);
      setVerified(result.ok);
    } catch (err) {
      setProbe({
        ok: false,
        category: "unknown",
        message: String(err),
        latency_ms: 0,
        provider: "assistant",
        model: model.trim(),
      });
      setVerified(false);
    } finally {
      setTesting(false);
    }
  };

  return (
    <form
      className="assistant-card assistant-onboarding"
      onSubmit={(event) => {
        event.preventDefault();
        void onSave(endpoint.trim(), key.trim(), model.trim(), kind);
      }}
    >
      <h2>{copy.onboardTitle}</h2>
      <p className="assistant-muted">
        {copy.onboardNoteBefore}<code>%AppData%\pondera\assistant</code>{copy.onboardNoteAfter}
      </p>
      <div className="assistant-presets">
        {PRESETS.map((preset) => (
          <button
            type="button"
            key={preset.label}
            className="assistant-chip"
            onClick={() => {
              edit(setEndpoint)(preset.endpoint);
              setModel(preset.model);
              setKind(preset.kind);
              setVerified(false);
              setProbe(null);
            }}
          >
            {preset.label}
          </button>
        ))}
      </div>
      <label>
        Endpoint
        <input
          value={endpoint}
          onChange={(event) => edit(setEndpoint)(event.target.value)}
          placeholder="https://api.deepseek.com/v1"
        />
      </label>
      <label>
        {copy.modelLabel}
        <input value={model} onChange={(event) => edit(setModel)(event.target.value)} placeholder="deepseek-chat" />
      </label>
      <label>
        {copy.kindLabel}
        <select value={kind} onChange={(event) => edit(setKind)(event.target.value)}>
          <option value="openai">{copy.kindOpenAI}</option>
          <option value="anthropic">Anthropic</option>
        </select>
      </label>
      <label>
        API key
        <input
          type="password"
          autoComplete="off"
          value={key}
          onChange={(event) => edit(setKey)(event.target.value)}
          placeholder={status?.key_set ? copy.keySet : "sk-..."}
        />
      </label>
      {onTest ? (
        <div className="assistant-connectivity">
          <button
            type="button"
            onClick={() => void runTest()}
            disabled={testing || saving || !endpoint.trim() || !model.trim()}
          >
            {testing ? copy.testing : copy.testConnection}
          </button>
          {probe ? (
            <span className={probe.ok ? "assistant-probe assistant-probe--ok" : "assistant-probe assistant-probe--fail"}>
              {probe.ok
                ? fill(copy.probeOk, { latency: probe.latency_ms })
                : fill(copy.probeFail, { message: probe.message, category: probe.category })}
            </span>
          ) : (
            <span className="assistant-muted">{copy.probeHint}</span>
          )}
        </div>
      ) : null}
      {error ? <div className="assistant-error">{error}</div> : null}
      <div className="assistant-actions">
        <button type="submit" className="assistant-primary" disabled={saving}>
          {saving ? copy.saving : copy.save}
        </button>
        {!verified ? <span className="assistant-badge assistant-badge--unverified">{copy.unverified}</span> : null}
        {onCancel ? (
          <button type="button" onClick={onCancel} disabled={saving}>
            {copy.back}
          </button>
        ) : null}
      </div>
    </form>
  );
}
