import { useState } from "react";
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
  const [message, setMessage] = useState("");
  if (!configured) {
    return (
      <div className="assistant-card">
        <p>还没有配置助手自己的模型。</p>
        <button type="button" className="assistant-primary" onClick={onConfigure}>
          去配置
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
          placeholder="例如：把默认模型换成 deepseek-v4-pro；或者问 compact_ratio 是干什么的"
        />
        <div className="assistant-actions">
          <button type="submit" className="assistant-primary" disabled={busy}>
            {busy ? "思考中…" : "提问"}
          </button>
        </div>
      </form>
      {error ? <div className="assistant-error">{error}</div> : null}
      {last ? (
        <div className="assistant-reply">
          <p>{last.reply}</p>
          {last.rejection ? (
            <div className="assistant-error">
              已拒绝（{last.rejection.reason}）：{last.rejection.message}
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
