// Small transcript row cards: phase lines, steer bubbles, notice cards with
// decision receipts, and compaction cards.

import { useState } from "react";
import { ErrorMessage } from "./ErrorMessage";
import { CheckCheck, ChevronRight, CirclePlay, ClipboardCheck, Copy, FileSearch, Info, TriangleAlert } from "lucide-react";
import { useT } from "../lib/i18n";
import { redactDiagnosticText, retryAttemptLine, type WireAttemptFailure } from "../lib/recoveryStatus";
import { writeClipboardText } from "../lib/clipboard";
import type { Item } from "../lib/useController";
import { sessionOperationStatus } from "../lib/sessionMaintenanceOperation";
type CompactionItem = Extract<Item, { kind: "compaction" }>;
type NoticeItem = Extract<Item, { kind: "notice" }>;
import type { WireCompletionSummary } from "../lib/types";
import { TurnResultSummary } from "./TurnResultSummary";
import { STEER_NOTICE_PREFIX } from "../lib/useController";
import { ProcessCompactIcon, ProcessPhaseIcon } from "./ProcessCard";

export function PhaseCard({ id, text }: { id: string; text: string }) {
  return <div className="phase" data-entrance={id}><ProcessPhaseIcon size={12} /><span>{text}</span></div>;
}

// A mid-turn steer is the user's own message, so it renders on the user side
// of the transcript instead of disappearing into the work fold.
export function SteerCard({ id, text }: { id: string; text: string }) {
  const t = useT();
  const body = text.startsWith(STEER_NOTICE_PREFIX) ? text.slice(STEER_NOTICE_PREFIX.length) : text;
  return (
    <div className="steer-line" data-entrance={id}>
      <div className="steer-line__bubble" title={t("transcript.steer")}>
        <span className="steer-line__icon" aria-hidden="true">↪</span>
        <span className="steer-line__text">{body}</span>
      </div>
    </div>
  );
}

function DecisionReceiptLine({ receipt }: { receipt: NonNullable<NoticeItem["decisionReceipt"]> }) {
  const t = useT();
  const titleKey = receipt.kind === "ask"
    ? "notice.decisionReceiptAsk"
    : receipt.kind === "plan"
    ? "notice.decisionReceiptPlan"
    : receipt.kind === "recovery"
    ? "notice.decisionReceiptRecovery"
    : "notice.decisionReceiptTool";
  const outcomeKeys: Record<string, string> = {
    allow_once: "notice.decisionAllowOnce",
    allow_session: "notice.decisionAllowSession",
    allow_persistent: "notice.decisionAllowPersistent",
    deny: "notice.decisionDeny",
    start_execution: "notice.decisionStartExecution",
    revise_plan: "notice.decisionRevisePlan",
    exit_plan: "notice.decisionExitPlan",
    recovery_continue: "notice.decisionRecoveryContinue",
    recovery_continue_task: "notice.decisionRecoveryContinueTask",
    recovery_revise: "notice.decisionRecoveryRevise",
    answered: "notice.decisionAnswered",
  };
  const outcome = outcomeKeys[receipt.outcome]
    ? t(outcomeKeys[receipt.outcome] as never)
    : receipt.outcome || t("notice.decisionReceiptTitle");
  const showOutcome = receipt.kind !== "ask" || receipt.outcome !== "answered";
  return (
    <div className="notice-line__decision-receipt">
      <span className="notice-line__decision-title">{t(titleKey as never)}</span>
      {showOutcome && <span className="notice-line__decision-outcome">{outcome}</span>}
      {receipt.tool && <code>{receipt.tool}</code>}
      {receipt.subject && <span className="notice-line__decision-subject">{receipt.subject}</span>}
    </div>
  );
}

// One-click bug-report payload: every failed attempt plus the final message,
// redacted client-side as defense in depth even though the server already
// omits response bodies and credentials.
function retryDiagnosticText(item: NoticeItem, timeline: WireAttemptFailure[]): string {
  const lines = ["Pondera retry diagnostics", `final: ${item.text}`];
  for (const failure of timeline) {
    lines.push(
      `#${failure.attempt}/${failure.max} kind=${failure.kind} status=${failure.status ?? "-"} code=${failure.code ?? "-"}`
      + ` retryable=${failure.retryable} backoff=${failure.backoffMs ?? 0}ms elapsed=${failure.elapsedMs ?? 0}ms`
      + ` provider=${failure.providerId ?? "-"} model=${failure.model ?? "-"} trace=${failure.traceId ?? "-"}`,
    );
  }
  return redactDiagnosticText(lines.join("\n"));
}

export function NoticeCard({ item, onAction, onAccept, onOpenVerification, actionDisabled = false }: { item: NoticeItem; onAction?: () => void; onAccept?: () => void; onOpenVerification?: (summary: WireCompletionSummary) => void; actionDisabled?: boolean }) {
  const t = useT();
  const [diagnosticCopied, setDiagnosticCopied] = useState(false);
  const StatusIcon = item.level === "warn" ? TriangleAlert : Info;
  const ActionIcon = item.action === "open_changes" ? FileSearch : CirclePlay;
  const showVerification = item.variant === "completion" && Boolean(item.completionSummary && onOpenVerification);
  const result = item.variant === "completion" ? item.completionSummary : undefined;
  const retryTimeline = item.retryTimeline?.length ? item.retryTimeline : undefined;
  const showActions = Boolean((item.action && onAction) || onAccept || showVerification);
  return (
    <div className={`notice-line notice-line--${item.level}${item.variant ? ` notice-line--${item.variant}` : ""}`} data-entrance={item.id} role={item.code === "incomplete_read" ? "status" : undefined}>
      {!result && <StatusIcon className="notice-line__icon" size={14} aria-hidden="true" />}
      <div className="notice-line__text">
        {result ? <><div className="notice-line__title">{t("notice.completionChangesTitle")}</div><TurnResultSummary summary={result} /></> : item.decisionReceipt ? (
          <DecisionReceiptLine receipt={item.decisionReceipt} />
        ) : (
          <>
            {item.title ? <div className="notice-line__title">{item.title}</div> : null}
            <div className="notice-line__body">{item.level === "warn" ? <ErrorMessage error={item.text} /> : item.text}</div>
          </>
        )}
        {showActions ? (
          <div className="notice-line__actions">
            {item.action && onAction ? (
              <button className="btn btn--small" type="button" onClick={onAction} disabled={actionDisabled}>
                <ActionIcon size={13} aria-hidden="true" />
                <span>{item.action === "recover_context" ? t("notice.protocolRecoveryAction") : item.action === "open_changes" ? t("notice.completionViewChanges") : t("notice.deliveryIncompleteContinue")}</span>
              </button>
            ) : null}
            {showVerification ? (
              <button className="btn btn--small" type="button" onClick={() => item.completionSummary && onOpenVerification?.(item.completionSummary)}>
                <ClipboardCheck size={13} aria-hidden="true" />
                <span>{t("notice.completionViewVerification")}</span>
              </button>
            ) : null}
            {onAccept ? (
              <button className="btn btn--small" type="button" onClick={onAccept}>
                <CheckCheck size={13} aria-hidden="true" />
                <span>{t("notice.deliveryIncompleteAccept")}</span>
              </button>
            ) : null}
          </div>
        ) : null}
        {result ? (
          <details className="notice-line__details">
            <summary>{t("notice.details")}</summary>
            <pre>{JSON.stringify(result, null, 2)}</pre>
          </details>
        ) : retryTimeline ? (
          <details className="notice-line__details">
            <summary>{t("notice.retryDetails")}</summary>
            <ul className="notice-retry-timeline">
              {retryTimeline.map((failure) => (
                <li key={`${failure.attempt}-${failure.kind}`}>{retryAttemptLine(t, failure)}</li>
              ))}
            </ul>
            {item.detail ? <div>{item.detail}</div> : null}
            <button
              className="btn btn--small notice-retry-timeline__copy"
              type="button"
              onClick={async () => {
                if (await writeClipboardText(retryDiagnosticText(item, retryTimeline))) {
                  setDiagnosticCopied(true);
                  window.setTimeout(() => setDiagnosticCopied(false), 2000);
                }
              }}
            >
              <Copy size={13} aria-hidden="true" />
              <span>{diagnosticCopied ? t("notice.retryDiagnosticCopied") : t("notice.copyRetryDiagnostic")}</span>
            </button>
          </details>
        ) : item.detail ? (
          <details className="notice-line__details">
            <summary>{t("notice.details")}</summary>
            <div>{item.detail}</div>
          </details>
        ) : null}
      </div>
    </div>
  );
}

export function CompactionCard({ item }: { item: CompactionItem }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const status = item.operationId ? sessionOperationStatus(item.status, item.activity) : item.status;
  const stateLabel = status === "cancelling" ? t("compaction.stopping")
    : status === "finalizing" ? t("compaction.saving")
    : status === "noop" ? t("compaction.noHistory")
    : status === "cancelled" ? t("compaction.cancelled")
    : status === "partially_completed" ? t("compaction.cancelledPartial")
    : status === "failed" ? t("compaction.failed")
    : status === "recovery_required" ? t("compaction.recoveryRequired")
    : status === "interrupted" ? t("compaction.interrupted")
    : status === "confirming" ? t("compaction.confirming")
    : status === "loading" ? t("common.loading")
    : status === "unavailable" ? t("compaction.unavailable")
    : item.pending ? t("compaction.working") : t("compaction.title");
  if (item.pending || status === "noop" || status === "cancelled" || status === "interrupted" || status === "unavailable") {
    return <div className={`compaction${item.pending ? " compaction--pending" : ""}`} data-entrance={item.id} data-transcript-layout-variant="static" role="status">
      <ProcessCompactIcon className={item.pending ? "compaction__spinner" : undefined} size={item.pending ? 14 : 12} />
      <span>{stateLabel}{status === "running" || (!status && item.pending) ? <span className="compaction__hint">{t("compaction.workingHint")}</span> : null}</span>
    </div>;
  }
  const tokenMeta = item.inputTokens != null && item.resultTokens != null
    ? t("compaction.tokens", { before: item.inputTokens, after: item.resultTokens }) : "";
  return (
    <div className="compaction" data-entrance={item.id} data-transcript-layout-variant={open ? "compaction-expanded" : "compaction-collapsed"}>
      <button type="button" className="compaction__head" onClick={() => {  setOpen((v) => !v); }} aria-expanded={open}>
        <ProcessCompactIcon size={12} />
        <span>{stateLabel}</span>
        <span className="compaction__meta">{tokenMeta || t("compaction.messages", { n: item.messages })}</span>
        <ChevronRight className={open ? "compaction__chevron--open" : ""} size={12} />
      </button>
      {open && <pre className="compaction__body">{item.detail || item.summary}</pre>}
    </div>
  );
}
