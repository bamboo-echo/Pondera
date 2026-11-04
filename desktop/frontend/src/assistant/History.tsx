import { useEffect, useState } from "react";
import { assistantApi } from "./api";
import type { AssistantHistoryRecord } from "../generated/desktopContract.generated";

// History lists committed changes newest first and offers one-click restore.
export function History({ refreshKey }: { refreshKey: number }) {
  const [records, setRecords] = useState<AssistantHistoryRecord[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    void assistantApi
      .history()
      .then((list) => {
        if (!cancelled) setRecords(list);
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err));
      });
    return () => {
      cancelled = true;
    };
  }, [refreshKey]);

  const rollback = async (id: string) => {
    setBusyId(id);
    setError(null);
    try {
      await assistantApi.rollback(id);
      const list = await assistantApi.history();
      setRecords(list);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusyId(null);
    }
  };

  return (
    <div className="assistant-card assistant-history">
      <h3>修改历史</h3>
      {error ? <div className="assistant-error">{error}</div> : null}
      {records.length === 0 ? <p className="assistant-muted">还没有通过助手写入的修改。</p> : null}
      <ul>
        {records.map((record) => (
          <li key={record.id}>
            <div className="assistant-history-head">
              <strong>{record.intent || record.kind}</strong>
              <span className="assistant-muted">{record.created_at}</span>
            </div>
            {record.diff.length > 0 ? (
              <div className="assistant-history-diff">
                {record.diff.map((row, index) => (
                  <span key={`${row.label}-${index}`}>
                    {row.label}: {row.before} → {row.after}
                  </span>
                ))}
              </div>
            ) : null}
            {record.backup_id ? (
              <button type="button" disabled={busyId === record.backup_id} onClick={() => void rollback(record.backup_id)}>
                {busyId === record.backup_id ? "还原中…" : "还原到此版本"}
              </button>
            ) : null}
          </li>
        ))}
      </ul>
    </div>
  );
}
