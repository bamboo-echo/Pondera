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

function Row({ row }: { row: AssistantPlanRow }) {
  return (
    <tr>
      <td className="assistant-path">{row.label}</td>
      <td className="assistant-before">{row.before}</td>
      <td className="assistant-after">{row.after}</td>
      <td>
        <span className={`assistant-badge assistant-badge-${row.risk}`}>
          {row.risk === "benign" ? "低风险" : "需确认"}
        </span>
        {row.sensitive ? <span className="assistant-badge assistant-badge-sensitive">脱敏</span> : null}
      </td>
    </tr>
  );
}

// PatchPreview is the confirmation gate: nothing is written until the user
// presses 确认写入.
export function PatchPreview({ plan, applying, error, applied, onApply, onDismiss }: PatchPreviewProps) {
  return (
    <div className="assistant-card assistant-preview">
      <header className="assistant-preview-head">
        <h3>{plan.intent || "待确认的修改"}</h3>
        <span className="assistant-muted">{plan.op_count} 项 · 预览 {plan.expires_at} 过期</span>
      </header>
      <table className="assistant-diff">
        <thead>
          <tr>
            <th>配置项</th>
            <th>当前</th>
            <th>修改后</th>
            <th>风险</th>
          </tr>
        </thead>
        <tbody>
          {plan.diff.map((row, index) => (
            <Row key={`${row.label}-${index}`} row={row} />
          ))}
        </tbody>
      </table>
      <div className="assistant-warning">{plan.comment_warning}</div>
      {plan.requires_confirmation ? (
        <div className="assistant-warning">包含会改变行为或密钥的操作，请确认后再写入。</div>
      ) : null}
      {error ? <div className="assistant-error">{error}</div> : null}
      {applied ? <div className="assistant-success">已写入并完成回读校验。</div> : null}
      <div className="assistant-actions">
        {!applied ? (
          <button type="button" className="assistant-primary" disabled={applying} onClick={() => void onApply(plan.id)}>
            {applying ? "写入中…" : "确认写入"}
          </button>
        ) : null}
        <button type="button" onClick={onDismiss} disabled={applying}>
          {applied ? "完成" : "放弃"}
        </button>
      </div>
    </div>
  );
}
