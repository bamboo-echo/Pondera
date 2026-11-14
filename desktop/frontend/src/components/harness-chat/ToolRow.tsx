// Adapted from DeepSeek Harness c291e7961a, ui-tool/components/ToolRow.tsx (MIT).
// Pondera provides authorized content and inspection through slots, without Cordis.
import { useState, type ReactNode } from "react";
import { ScanSearch } from "lucide-react";
import { useT } from "../../lib/i18n";
import { presentError } from "../../lib/errorPresentation";
import { DisclosureRow } from "./DisclosureRow";
import { StateDot, type StateDotState } from "./StateDot";
import css from "./ToolRow.styles";

export function ToolRow({ icon, title, summary, state, dot, statusLabel, errorSummary, children, inspectLabel, inspect, beforeToggle }: {
  dot?: StateDotState;
  icon: ReactNode; title: string; summary: string; state: "running" | "done" | "error" | "stopped" | "unknown";
  statusLabel: string; errorSummary?: string; children: ReactNode;
  inspectLabel: string; inspect: (trigger: HTMLElement) => void; beforeToggle: () => void;
}) {
  const [open, setOpen] = useState(false);
  const t = useT();
  // The raw failure is the content the user needs. The localised summary
  // ("操作出现问题…") is a neutral fallback for framework noise, not a
  // replacement for the message: it used to hide the real error one click
  // behind an affordance that had no visual cue. Under the English locale
  // presentError hoists an unknown English message into `summary` and blanks
  // `detail`, so fall back to the summary to keep the raw cause visible.
  const presentation = state === "error" && errorSummary ? presentError(errorSummary, t) : undefined;
  const failureLine = presentation ? presentation.detail || presentation.summary || undefined : undefined;
  const leading = dot ? <StateDot state={dot} /> : state === "error" || state === "stopped"
    ? <StateDot state={state === "error" ? "error" : "warning"} /> : icon;
  return <div className={`${css.root} chat-tool`} data-state={state}>
    {state !== "done" && state !== "error" && <span className="sr-only">{statusLabel}</span>}
    <DisclosureRow icon={leading} title={title} open={open} expandable expandOnRowClick keepContentWhenOpen
      rowClassName={css.row} leadingClassName={css.leading} titleClassName={css.title} chevronClassName={css.chevron}
      onToggle={() => { beforeToggle(); setOpen(value => !value); }}
      collapsedContent={<>
        {state === "error" ? <span className={`${css.stateLabel} ${css.errorLabel}`}>{statusLabel}</span> : <span className={css.sep} aria-hidden />}
        {(failureLine || summary) && <span className={`${css.summary}${failureLine ? ` ${css.errorSummary}` : ""}`} title={failureLine || summary}>{failureLine || summary}</span>}</>}>
      <div className={css.bodyWrap}>
        {children}
        <button className={css.inspectButton} type="button" onClick={event => inspect(event.currentTarget)}><ScanSearch size={12} />{inspectLabel}</button>
      </div>
    </DisclosureRow>
  </div>;
}
