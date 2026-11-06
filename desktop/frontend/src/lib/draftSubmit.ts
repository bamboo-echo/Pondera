import { appTargetSupports } from "./bridge";
import type { AppBindings } from "./bridge";
import { draftIDsFromSubmit } from "./draftCredentials";

export function draftSubmit(app: AppBindings, tabId: string, submit: string, submissionId: string) {
  const draftIDs = draftIDsFromSubmit(submit);
  if (draftIDs.length > 0 && appTargetSupports(app, "StartTurnForTabWithDrafts")) {
    return app.StartTurnForTabWithDrafts!(tabId, submit, submissionId, draftIDs);
  }
  if (appTargetSupports(app, "StartTurnForTab")) return app.StartTurnForTab!(tabId, submit, submissionId);
  return app.SubmitToTabWithID(tabId, submit, submissionId);
}
