// Tracks unsaved drafts inside the settings page so an external config change
// (for example one applied by the assistant) never reloads over a user's edits.
import { useEffect } from "react";

let draftCount = 0;

// Components call this with their local dirty flag. The hook intentionally has
// no React context so leaf editors can opt in without threading props.
export function useSettingsDraftGuard(dirty: boolean) {
  useEffect(() => {
    if (!dirty) return;
    draftCount += 1;
    return () => {
      draftCount -= 1;
    };
  }, [dirty]);
}

export function settingsHasUnsavedDrafts(): boolean {
  return draftCount > 0;
}
