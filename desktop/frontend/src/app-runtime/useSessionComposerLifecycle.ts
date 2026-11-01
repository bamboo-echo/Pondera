import { useEffect } from "react";

declare global {
  interface Window {
    __ponderaFlushSessionDraft?: () => Promise<void>;
    __ponderaResumeSessionDraftEditing?: () => void;
  }
}

/** The shell's historical callback names now belong only to formal input. */
export function useSessionComposerLifecycle() {
  useEffect(() => {
    const flush = async () => {
      const input = await import("../lib/sessionComposerPersistence");
      try {
        await input.flushAllSessionComposers();
      } catch (error) {
        input.resumeSessionComposerEditing();
        throw error;
      }
    };
    const resume = () => {
      void import("../lib/sessionComposerPersistence").then(input => input.resumeSessionComposerEditing());
    };
    window.__ponderaFlushSessionDraft = flush;
    window.__ponderaResumeSessionDraftEditing = resume;
    return () => {
      if (window.__ponderaFlushSessionDraft === flush) delete window.__ponderaFlushSessionDraft;
      if (window.__ponderaResumeSessionDraftEditing === resume) delete window.__ponderaResumeSessionDraftEditing;
    };
  }, []);
}
