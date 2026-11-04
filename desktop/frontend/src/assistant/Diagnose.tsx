import { useEffect, useState } from "react";
import { assistantApi } from "./api";
import type { AssistantConnectivity, AssistantDiagnoseResult } from "../generated/desktopContract.generated";

// Diagnose is read-only: it never writes configuration. Connectivity sends one
// minimal request through the configured adapter.
export function Diagnose({ refreshKey }: { refreshKey: number }) {
  const [report, setReport] = useState<AssistantDiagnoseResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [provider, setProvider] = useState("");
  const [model, setModel] = useState("");
  const [draftKey, setDraftKey] = useState("");
  const [probe, setProbe] = useState<AssistantConnectivity | null>(null);
  const [probing, setProbing] = useState(false);

  useEffect(() => {
    let cancelled = false;
    void assistantApi
      .diagnose()
      .then((result) => {
        if (!cancelled) setReport(result);
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err));
      });
    return () => {
      cancelled = true;
    };
  }, [refreshKey]);

  const test = async () => {
    setProbing(true);
    setError(null);
    try {
      setProbe(await assistantApi.testConnectivity(provider.trim(), model.trim(), draftKey.trim()));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setProbing(false);
    }
  };

  return (
    <div className="assistant-card assistant-diagnose">
      <h3>配置诊断</h3>
      {error ? <div className="assistant-error">{error}</div> : null}
      <p className="assistant-muted">{report ? `已检查 ${report.checked} 项` : "检查中…"}</p>
      <ul>
        {report?.findings.map((finding, index) => (
          <li key={`${finding.code}-${index}`} className={`assistant-finding assistant-finding-${finding.severity}`}>
            <div className="assistant-history-head">
              <strong>{finding.title}</strong>
              <span className="assistant-badge">{finding.code}</span>
            </div>
            <p>{finding.detail}</p>
            {finding.fix ? <p className="assistant-muted">建议：{finding.fix}</p> : null}
          </li>
        ))}
      </ul>

      <h4>连通测试</h4>
      <div className="assistant-probe-form">
        <label>
          provider
          <input value={provider} onChange={(event) => setProvider(event.target.value)} placeholder="deepseek-flash" />
        </label>
        <label>
          模型（可选）
          <input value={model} onChange={(event) => setModel(event.target.value)} placeholder="deepseek-chat" />
        </label>
        <label>
          临时 key（可选）
          <input type="password" autoComplete="off" value={draftKey} onChange={(event) => setDraftKey(event.target.value)} />
        </label>
        <button type="button" disabled={probing || !provider.trim()} onClick={() => void test()}>
          {probing ? "测试中…" : "测试连接"}
        </button>
      </div>
      {probe ? (
        <div className={probe.ok ? "assistant-success" : "assistant-error"}>
          {probe.category} · {probe.message}（{probe.latency_ms} ms）
        </div>
      ) : null}
    </div>
  );
}
