// Adapted from Harness c291e7961a: async full-content loading and lifecycle fencing.
import { useCallback, useEffect, useRef, useState } from "react";
import { writeClipboard } from "./clipboard";

export function useCopyFeedback(text: string, getText?: () => Promise<string>) {
  const [copied, setCopied] = useState(false);
  const [failed, setFailed] = useState(false);
  const active = useRef(true);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  useEffect(() => { active.current = true; return () => { active.current = false; clearTimeout(timer.current); }; }, []);
  const onCopy = useCallback(() => {
    if (copied || failed) return;
    const markFailed = () => {
      if (!active.current) return;
      setFailed(true);
      clearTimeout(timer.current);
      timer.current = setTimeout(() => { if (active.current) setFailed(false); }, 1500);
    };
    void (getText ? getText() : Promise.resolve(text)).then(async value => {
      if (!active.current) return;
      if (!await writeClipboard(value) || !active.current) { markFailed(); return; }
      setCopied(true);
      clearTimeout(timer.current);
      timer.current = setTimeout(() => { if (active.current) setCopied(false); }, 1000);
    }).catch(() => { /* The content owner surfaces fetch failures; clipboard failures show here. */ markFailed(); });
  }, [copied, failed, getText, text]);
  return { copied, failed, onCopy };
}
