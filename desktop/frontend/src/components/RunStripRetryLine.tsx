import { ChevronDown } from "lucide-react";
import { useEffect, useId, useRef, useState, type ReactNode } from "react";
import type { Translator } from "../lib/i18n";
import { retryAttemptLine, type WireAttemptFailure } from "../lib/recoveryStatus";

interface RunStripRetryLineProps {
  t: Translator;
  /** Already-rendered status text for the row (retry state included). */
  stateText: string;
  /** Failed attempts that make the row a disclosure trigger. */
  history?: WireAttemptFailure[];
  /** Trailing metrics; they sit inside the trigger so the whole row toggles. */
  children?: ReactNode;
}

// RunStripRetryLine renders the run-strip status row. When attempts have failed
// the whole row is the trigger — text, metrics, chevron and the empty space
// between them — rather than only the chevron. Escape, a pointer press outside
// the row, or pressing the row again closes the details; the row also closes
// itself as soon as the turn stops retrying, so a later retry never inherits an
// open panel. Collapsed, the panel is hidden rather than removed, keeping
// aria-controls resolvable without leaving any visible residue.
export function RunStripRetryLine({ t, stateText, history, children }: RunStripRetryLineProps) {
  const [open, setOpen] = useState(false);
  const toggleRef = useRef<HTMLButtonElement>(null);
  const detailsRef = useRef<HTMLDivElement>(null);
  const detailsId = useId();
  const expanded = Boolean(history?.length && open);

  useEffect(() => {
    if (!history?.length && open) setOpen(false);
  }, [history?.length, open]);

  useEffect(() => {
    if (!expanded) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      setOpen(false);
      toggleRef.current?.focus();
    };
    const onPointer = (event: PointerEvent) => {
      const target = event.target as Node | null;
      if (target && (detailsRef.current?.contains(target) || toggleRef.current?.contains(target))) return;
      setOpen(false);
    };
    window.addEventListener("keydown", onKey);
    window.addEventListener("pointerdown", onPointer);
    return () => {
      window.removeEventListener("keydown", onKey);
      window.removeEventListener("pointerdown", onPointer);
    };
  }, [expanded]);

  if (!history?.length) {
    return (
      <>
        <span className="composer-run-strip__state">{stateText}</span>
        {children}
      </>
    );
  }
  return (
    <>
      <button
        ref={toggleRef}
        type="button"
        className="composer-run-strip__toggle composer-run-strip__toggle--wide"
        aria-expanded={expanded}
        aria-controls={detailsId}
        title={t("status.retryDetails")}
        onClick={() => setOpen((value) => !value)}
      >
        <span className="composer-run-strip__state">{stateText}</span>
        {children}
        <ChevronDown size={12} aria-hidden="true" />
      </button>
      <div ref={detailsRef} id={detailsId} className="composer-retry-detail" role="dialog" aria-label={t("status.retryDetails")} hidden={!expanded}>
        <div className="composer-retry-detail__title">{t("status.retryDetails")}</div>
        <ul>
          {history.map((failure) => (
            <li key={`${failure.attempt}-${failure.kind}`}>{retryAttemptLine(t, failure)}</li>
          ))}
        </ul>
      </div>
    </>
  );
}
