import { assistantCopy, fill, type AssistantCopy } from "./copy";
import { useI18n } from "../lib/i18n";
import type {
  AssistantPlanRow,
  AssistantPlanView,
} from "../generated/desktopContract.generated";

export interface PatchPreviewProps {
  plan: AssistantPlanView;
  applying: boolean;
  error: string | null;
  applied: boolean;
  onApply: (planId: string) => Promise<void>;
  onDismiss: () => void;
}

function Row({ row, copy }: { row: AssistantPlanRow; copy: AssistantCopy }) {
  return (
    <tr>
      <td className="assistant-path">{row.label}</td>
      <td className="assistant-before">{row.before}</td>
      <td className="assistant-after">{row.after}</td>
      <td>
        <span className={`assistant-badge assistant-badge-${row.risk}`}>
          {row.risk === "benign" ? copy.riskLow : copy.riskConfirm}
        </span>
        {row.sensitive ? <span className="assistant-badge assistant-badge-sensitive">{copy.masked}</span> : null}
      </td>
    </tr>
  );
}

// PatchPreview is the confirmation gate: nothing is written until the user
// presses 确认写入.
export function PatchPreview({ plan, applying, error, applied, onApply, onDismiss }: PatchPreviewProps) {
  const { locale } = useI18n();
  const copy = assistantCopy(locale);
  return (
    <div className="assistant-card assistant-preview">
      <header className="assistant-preview-head">
        <h3>{plan.intent || copy.pendingTitle}</h3>
        <span className="assistant-muted">{fill(copy.opCount, { count: plan.op_count, expires: plan.expires_at })}</span>
      </header>
      <table className="assistant-diff">
        <thead>
          <tr>
            <th>{copy.colKey}</th>
            <th>{copy.colBefore}</th>
            <th>{copy.colAfter}</th>
            <th>{copy.colRisk}</th>
          </tr>
        </thead>
        <tbody>
          {plan.diff.map((row, index) => (
            <Row key={`${row.label}-${index}`} row={row} copy={copy} />
          ))}
        </tbody>
      </table>
      <div className="assistant-warning">{plan.comment_warning}</div>
      {plan.requires_confirmation ? (
        <div className="assistant-warning">{copy.behaviorWarning}</div>
      ) : null}
      {error ? <div className="assistant-error">{error}</div> : null}
      {applied ? <div className="assistant-success">{copy.applied}</div> : null}
      <div className="assistant-actions">
        {!applied ? (
          <button type="button" className="assistant-primary" disabled={applying} onClick={() => void onApply(plan.id)}>
            {applying ? copy.writing : copy.confirmWrite}
          </button>
        ) : null}
        <button type="button" onClick={onDismiss} disabled={applying}>
          {applied ? copy.done : copy.discard}
        </button>
      </div>
    </div>
  );
}
