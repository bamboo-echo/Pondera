import type { Translator } from "./i18n";

export interface RecoveryStatus {
  state?: "recovery_required" | string;
  call_id?: string;
  attempt_id?: string;
  requires_user_decision?: boolean;
  read_only?: boolean;
  phase?: string;
  reason?: string;
  next_attempt_at?: number;
  waited_ms?: number;
  wait_budget_ms?: number;
  waiting?: boolean;
}

export type RetryFailureKind =
  | "rate_limit"
  | "server_error"
  | "auth"
  | "quota"
  | "timeout"
  | "network"
  | "stream_interrupted"
  | "empty_response"
  | "context_overflow"
  | "output_limit"
  | "protocol_replay"
  | "client_error"
  | "unknown";

// Mirrors provider.AttemptFailure. Display-safe by construction: the backend
// never puts response bodies, URLs, or credentials in these fields.
export interface WireAttemptFailure {
  attempt: number;
  max: number;
  kind: RetryFailureKind;
  phase?: string;
  status?: number;
  code?: string;
  retryable: boolean;
  backoffMs?: number;
  elapsedMs?: number;
  at?: number;
  providerId?: string;
  model?: string;
  traceId?: string;
}

export interface RecoveryRetry {
  attempt: number;
  max: number;
  failure?: WireAttemptFailure;
  /** All failed attempts for the current turn, oldest first. */
  history?: WireAttemptFailure[];
  recovery?: RecoveryStatus;
}

export interface RecoveryEventFields {
  recovery?: RecoveryStatus;
  retryAttempt?: number;
  retryMax?: number;
  attemptFailure?: WireAttemptFailure;
}

export function recoveryNextAttemptSeconds(recovery: RecoveryStatus, now: number): number {
  return Math.max(0, Math.ceil(((recovery.next_attempt_at ?? now) - now) / 1000));
}

export function recoveryStatusText(t: Translator, retry: RecoveryRetry, now: number): string {
  if (!retry.recovery?.waiting) {
    const base = t("status.retrying", { attempt: retry.attempt, max: retry.max });
    const reason = retryFailureText(t, retry.failure, now);
    return reason ? `${base.replace(/…$/, "")}${t("status.retryReason", { reason })}` : base;
  }
  const phase = t(retry.recovery.phase === "connect" ? "status.recoveryNetwork" : "status.recoveryProvider");
  return t("status.recoveryWaiting", { seconds: recoveryNextAttemptSeconds(retry.recovery, now), phase });
}

// retryCountdownSeconds is the live wait before the next attempt. The backend
// stamps the failure with its local `at` and the planned `backoffMs`, so the
// next attempt is due at at + backoffMs; deriving the remainder from the
// caller's `now` keeps the row ticking instead of restating a fixed value.
// A missing `at` (older session replay) falls back to the full backoff.
export function retryCountdownSeconds(failure: WireAttemptFailure | undefined, now: number): number {
  if (!failure?.backoffMs) return 0;
  const due = (failure.at ?? now) + failure.backoffMs;
  return Math.max(0, Math.ceil((due - now) / 1000));
}

function retryKindLabel(t: Translator, failure: WireAttemptFailure): string {
  const key = `status.retryKind.${failure.kind}`;
  const label = t(key as never);
  return label === key ? t("status.retryKind.unknown") : label;
}

// Human-readable reason for one failed attempt, e.g. "限流，4 秒后重试".
// Once the countdown reaches zero the attempt is already being sent, so the
// tail becomes "正在重试…" instead of "0 秒后重试".
export function retryFailureText(t: Translator, failure?: WireAttemptFailure, now = Date.now()): string {
  if (!failure) return "";
  const reason = retryKindLabel(t, failure);
  const seconds = retryCountdownSeconds(failure, now);
  if (seconds > 0) return t("status.retryBackoff", { reason, seconds });
  return failure.backoffMs ? t("status.retryingNow") : reason;
}

// Timeline line, e.g. "#3 429 限流 · 退避 4s · 耗时 1.2s".
export function retryAttemptLine(t: Translator, failure: WireAttemptFailure): string {
  const parts = [`#${failure.attempt}`];
  if (failure.status) parts.push(String(failure.status));
  parts.push(retryKindLabel(t, failure));
  if (failure.backoffMs) parts.push(t("status.retryElapsedBackoff", { seconds: Math.ceil(failure.backoffMs / 1000) }));
  if (failure.elapsedMs != null) parts.push(t("status.retryElapsed", { seconds: (failure.elapsedMs / 1000).toFixed(1) }));
  return parts.join(" · ");
}

// Defense in depth for clipboard export. The server already omits raw bodies,
// but this keeps a stray credential out of the user's clipboard regardless.
export function redactDiagnosticText(value: string): string {
  return value
    .replace(/\bsk-[A-Za-z0-9_-]{6,}/g, "[redacted]")
    .replace(/\bBearer\s+[A-Za-z0-9._~+/-]+=*/gi, "Bearer [redacted]")
    .replace(/(authorization\s*[:=]\s*)[^\s,"}]+/gi, "$1[redacted]")
    .replace(/(api[_-]?key\s*[:=]\s*)[^\s,"}]+/gi, "$1[redacted]");
}
