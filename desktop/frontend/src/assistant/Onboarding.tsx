import { useState } from "react";
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
      <h2>先给助手配一个自己的模型</h2>
      <p className="assistant-muted">
        这些设置只存在 <code>%AppData%\pondera\assistant</code>，不会并进主配置；密钥走 Pondera 凭据库。
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
        模型
        <input value={model} onChange={(event) => edit(setModel)(event.target.value)} placeholder="deepseek-chat" />
      </label>
      <label>
        接口类型
        <select value={kind} onChange={(event) => edit(setKind)(event.target.value)}>
          <option value="openai">OpenAI 兼容</option>
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
          placeholder={status?.key_set ? "已设置，留空表示不修改" : "sk-..."}
        />
      </label>
      {onTest ? (
        <div className="assistant-connectivity">
          <button
            type="button"
            onClick={() => void runTest()}
            disabled={testing || saving || !endpoint.trim() || !model.trim()}
          >
            {testing ? "测试中…" : "测试连接"}
          </button>
          {probe ? (
            <span className={probe.ok ? "assistant-probe assistant-probe--ok" : "assistant-probe assistant-probe--fail"}>
              {probe.ok
                ? `通过（${probe.latency_ms} ms）`
                : `失败：${probe.message}（${probe.category}）`}
            </span>
          ) : (
            <span className="assistant-muted">用当前草稿测一次，不会保存任何值</span>
          )}
        </div>
      ) : null}
      {error ? <div className="assistant-error">{error}</div> : null}
      <div className="assistant-actions">
        <button type="submit" className="assistant-primary" disabled={saving}>
          {saving ? "保存中…" : "保存"}
        </button>
        {!verified ? <span className="assistant-badge assistant-badge--unverified">未验证</span> : null}
        {onCancel ? (
          <button type="button" onClick={onCancel} disabled={saving}>
            返回
          </button>
        ) : null}
      </div>
    </form>
  );
}
