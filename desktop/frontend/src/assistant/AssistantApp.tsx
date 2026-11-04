import { useEffect, useState } from "react";
import { assistantApi } from "./api";
import { Chat } from "./Chat";
import { Diagnose } from "./Diagnose";
import { History } from "./History";
import { Onboarding } from "./Onboarding";
import { PatchPreview } from "./PatchPreview";
import type {
  AssistantChatResult,
  AssistantPlanView,
  AssistantStatusView,
} from "../generated/desktopContract.generated";
import "./assistant.css";

type Tab = "chat" | "diagnose" | "history";

// AssistantApp is the separate assistant window surface. It talks only to the
// host RPC; all writes go through internal/assist/writer.
export default function AssistantApp() {
  const [status, setStatus] = useState<AssistantStatusView | null>(null);
  const [tab, setTab] = useState<Tab>("chat");
  const [editing, setEditing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const [busy, setBusy] = useState(false);
  const [last, setLast] = useState<AssistantChatResult | null>(null);
  const [plan, setPlan] = useState<AssistantPlanView | null>(null);
  const [applying, setApplying] = useState(false);
  const [applyError, setApplyError] = useState<string | null>(null);
  const [applied, setApplied] = useState(false);
  const [refreshKey, setRefreshKey] = useState(0);

  useEffect(() => {
    void assistantApi
      .status()
      .then(setStatus)
      .catch((err: unknown) => setError(err instanceof Error ? err.message : String(err)));
  }, []);

  const save = async (endpoint: string, key: string, model: string, kind: string) => {
    setSaving(true);
    setError(null);
    try {
      const next = await assistantApi.saveSettings(endpoint, key, model, kind);
      setStatus(next);
      setEditing(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  };

  const ask = async (message: string) => {
    setBusy(true);
    setError(null);
    setApplied(false);
    setApplyError(null);
    setPlan(null);
    try {
      const result = await assistantApi.chat(message);
      setLast(result);
      if (result.plan) setPlan(result.plan);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  const apply = async (planId: string) => {
    setApplying(true);
    setApplyError(null);
    try {
      await assistantApi.applyPlan(planId);
      setApplied(true);
      setRefreshKey((value) => value + 1);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      setApplyError(
        message.includes("stale_plan")
          ? "配置文件在预览后被改动，请重新提问以生成新的预览。"
          : message,
      );
    } finally {
      setApplying(false);
    }
  };

  const onboarding = !status || (!status.configured && !last) || editing;

  return (
    <div className="assistant-shell">
      <header className="assistant-header">
        <div>
          <h1>Pondera 配置助手</h1>
          <p className="assistant-muted">
            {status?.configured ? `${status.model} · ${status.endpoint}` : "尚未配置助手模型"}
          </p>
        </div>
        <div className="assistant-tabs">
          <button type="button" className={tab === "chat" ? "active" : ""} onClick={() => setTab("chat")}>
            对话
          </button>
          <button type="button" className={tab === "diagnose" ? "active" : ""} onClick={() => setTab("diagnose")}>
            诊断
          </button>
          <button type="button" className={tab === "history" ? "active" : ""} onClick={() => setTab("history")}>
            历史
          </button>
          <button type="button" onClick={() => setEditing(true)}>
            设置
          </button>
        </div>
      </header>

      {error ? <div className="assistant-error">{error}</div> : null}

      {onboarding ? (
        <Onboarding
          status={status}
          saving={saving}
          error={error}
          onSave={save}
          onCancel={status?.configured ? () => setEditing(false) : undefined}
        />
      ) : null}

      {!onboarding && tab === "chat" ? (
        <>
          <Chat configured={Boolean(status?.configured)} busy={busy} error={null} last={last} onAsk={ask} onConfigure={() => setEditing(true)} />
          {plan ? (
            <PatchPreview
              plan={plan}
              applying={applying}
              error={applyError}
              applied={applied}
              onApply={apply}
              onDismiss={() => {
                setPlan(null);
                setApplied(false);
                setLast(null);
              }}
            />
          ) : null}
        </>
      ) : null}

      {!onboarding && tab === "diagnose" ? <Diagnose refreshKey={refreshKey} /> : null}
      {!onboarding && tab === "history" ? <History refreshKey={refreshKey} /> : null}

      <footer className="assistant-footer assistant-muted">
        写入链路：备份 → 加锁 → 严格重读 → 修订号比对 → apply → 原子写 → 回读校验 → 失败自动回滚。
        密钥不进入模型请求与界面原文。
      </footer>
    </div>
  );
}
