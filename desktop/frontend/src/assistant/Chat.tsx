import { useState } from "react";
import { assistantCopy, fill } from "./copy";
import { useI18n } from "../lib/i18n";
import type { AssistantChatResult } from "../generated/desktopContract.generated";

export interface ChatProps {
  configured: boolean;
  busy: boolean;
  error: string | null;
  last: AssistantChatResult | null;
  onAsk: (message: string) => Promise<void>;
  onConfigure: () => void;
}

// Chat is the natural-language entry: ask, explain, or produce a patch preview.
export function Chat({ configured, busy, error, last, onAsk, onConfigure }: ChatProps) {
  const { locale } = useI18n();
  const copy = assistantCopy(locale);
  const [message, setMessage] = useState("");
  if (!configured) {
    return (
      <div className="assistant-card">
        <p>{copy.chatUnconfigured}</p>
        <button type="button" className="assistant-primary" onClick={onConfigure}>
          {copy.chatConfigure}
        </button>
      </div>
    );
  }
  return (
    <div className="assistant-card assistant-chat">
      <form
        onSubmit={(event) => {
          event.preventDefault();
          if (!message.trim()) return;
          void onAsk(message.trim());
        }}
      >
        <textarea
          value={message}
          rows={4}
          onChange={(event) => setMessage(event.target.value)}
          placeholder={copy.chatPlaceholder}
        />
        <div className="assistant-actions">
          <button type="submit" className="assistant-primary" disabled={busy}>
            {busy ? copy.chatThinking : copy.chatAsk}
          </button>
        </div>
      </form>
      {error ? <div className="assistant-error">{error}</div> : null}
      {last ? (
        <div className="assistant-reply">
          <p>{last.reply}</p>
          {last.rejection ? (
            <div className="assistant-error">
              {fill(copy.chatRejected, { reason: last.rejection.reason, message: last.rejection.message })}
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
