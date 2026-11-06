import { useEffect, useState } from "react";
import { assistantApi } from "./api";
import { assistantCopy } from "./copy";
import { useI18n } from "../lib/i18n";
import { InlineConfirmButton } from "../components/InlineConfirmButton";
import type { AssistantHistoryRecord } from "../generated/desktopContract.generated";

// The list grows with every assistant write; render it in pages so a long-lived
// install never mounts an unbounded number of rows in one commit.
const HISTORY_PAGE_SIZE = 20;

// History lists committed changes newest first and offers one-click restore.
export function History({ refreshKey, onRestored }: { refreshKey: number; onRestored?: () => void }) {
  const { locale } = useI18n();
  const copy = assistantCopy(locale);
  const [records, setRecords] = useState<AssistantHistoryRecord[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [visible, setVisible] = useState(HISTORY_PAGE_SIZE);

  useEffect(() => {
    let cancelled = false;
    void assistantApi
      .history()
      .then((list) => {
        if (!cancelled) {
          setRecords(list);
          setVisible(HISTORY_PAGE_SIZE);
        }
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
      setRecords(await assistantApi.history());
      // The restore rewrote config underneath the other assistant panels; let
      // the owner drop stale previews and refresh its own views.
      onRestored?.();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusyId(null);
    }
  };

  return (
    <div className="assistant-card assistant-history">
      <h3>{copy.historyTitle}</h3>
      {error ? <div className="assistant-error">{error}</div> : null}
      {records.length === 0 ? <p className="assistant-muted">{copy.historyEmpty}</p> : null}
      <ul>
        {records.slice(0, visible).map((record) => (
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
              busyId === record.backup_id ? (
                <button type="button" disabled>{copy.restoring}</button>
              ) : (
                <InlineConfirmButton
                  label={copy.restore}
                  confirmLabel={copy.confirmRestore}
                  cancelLabel={copy.cancel}
                  danger
                  onConfirm={() => rollback(record.backup_id)}
                />
              )
            ) : null}
          </li>
        ))}
      </ul>
      {records.length > visible ? (
        <button type="button" onClick={() => setVisible((value) => value + HISTORY_PAGE_SIZE)}>
          {copy.showMore}
        </button>
      ) : null}
    </div>
  );
}
