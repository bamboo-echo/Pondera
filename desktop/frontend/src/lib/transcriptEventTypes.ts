import type { StreamAttemptAction } from "./types";

import type { WireAttemptFailure } from "./recoveryStatus";

export interface WireStreamAttempt {
  id: string;
  action: StreamAttemptAction;
  attempt?: number;
  max?: number;
  /** Action reason (retry / output_limit / context_limit / interrupt enum). */
  reason?: string;
  /** Classified failure for a discarded attempt. */
  failure?: WireAttemptFailure;
}
export interface WireCompaction {
  trigger?: string; // "auto" | "manual"
  messages?: number; // done: how many messages were folded into the summary
  summary?: string; // done: the briefing (empty on an aborted pass)
  archive?: string; // done: archive path, if any
}
export interface WireSessionOperation {
  operationId: string;
  kind: string;
  activity: string;
  status: string;
  operationRevision?: number;
  runtimeEpoch?: string;
  errorCode?: string;
  detail?: string;
  applied?: boolean;
  inputTokens?: number;
  resultTokens?: number;
  messages?: number;
  summary?: string;
  archive?: string;
}
