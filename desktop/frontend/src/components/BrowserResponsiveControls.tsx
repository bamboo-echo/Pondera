import type { BrowserTabView } from "../lib/browserHost";
import { useBrowserPanelStore } from "../lib/browserPanelStore";
import { useI18n } from "../lib/i18n";
import { useState } from "react";
import { browserElementDrafts } from "../lib/browserElementDrafts";

export function BrowserResponsiveControls({ tab }: { tab: BrowserTabView }) {
  const locale = useI18n().locale;
  const tr = (en: string, hans: string, hant: string) => locale === "en" ? en : locale === "zh-TW" ? hant : hans;
  const viewport = tab.viewport;
  const [picking, setPicking] = useState(false);
  const pick = async () => {
    const { host, notify } = useBrowserPanelStore.getState();
    const target = browserElementDrafts.getState().target;
    if (!target || !host?.pickElement) { notify(tr("The current task cannot receive an element attachment yet", "当前任务暂不能接收元素附件", "目前工作階段暫不能接收元素附件")); return; }
    if (tab.taskId !== "user" && (tab.taskId !== target.taskId || tab.sessionId && tab.sessionId !== target.sessionId)) { notify(tr("Return to the task that owns this page", "请切回此页面所属的任务", "請切回此頁面所屬的工作階段")); return; }
    setPicking(true);
    try {
      const selection = await host.pickElement(tab.id);
      if (selection) browserElementDrafts.getState().add(target.taskId, target.sessionId, selection);
    } catch (error) { notify(String(error)); }
    finally { setPicking(false); }
  };
  const set = (next: NonNullable<BrowserTabView["viewport"]> | null) => {
    const { host, notify } = useBrowserPanelStore.getState();
    void host?.setViewport?.(tab.id, next).catch(error => notify(String(error)));
  };
  return <details className="browser-responsive">
    <summary>{tr("Responsive", "响应式", "響應式")}{viewport ? ` · ${viewport.width}×${viewport.height}` : ""}</summary>
    <div className="browser-responsive__controls">
      {tab.restorePreview && <button type="button" className="btn btn--small" onClick={() => { const { host, notify } = useBrowserPanelStore.getState(); void host?.restorePreview?.(tab.id).catch(error => notify(String(error))); }}>{tr("Reauthorize and reopen preview", "重新授权并打开预览", "重新授權並開啟預覽")}</button>}
      <button type="button" className="btn btn--small" disabled={picking} onClick={() => void pick()}>{picking ? tr("Click an element · Esc to cancel", "点击元素 · Esc 退出", "點擊元素 · Esc 退出") : tr("Select element for chat", "选择元素加入聊天", "選擇元素加入聊天")}</button>
      <select aria-label={tr("Viewport preset", "视口预设", "視口預設")} value={viewport ? `${viewport.width}x${viewport.height}` : "natural"} onChange={event => {
        if (event.target.value === "natural") set(null);
        else if (event.target.value !== "custom") { const [width, height] = event.target.value.split("x").map(Number); set({ width, height, scale: viewport?.scale ?? "fit" }); }
      }}>
        <option value="natural">{tr("Natural", "自然尺寸", "自然尺寸")}</option>
        <option value="393x852">{tr("Phone", "手机", "手機")} 393×852</option>
        <option value="768x1024">{tr("Tablet", "平板", "平板")} 768×1024</option>
        <option value="1280x720">{tr("Desktop", "桌面", "桌面")} 1280×720</option>
        {viewport && !["393x852", "768x1024", "1280x720"].includes(`${viewport.width}x${viewport.height}`) && <option value={`${viewport.width}x${viewport.height}`}>{tr("Custom", "自定义", "自訂")}</option>}
      </select>
      <form key={`${tab.id}:${viewport?.width}:${viewport?.height}`} onSubmit={event => {
        event.preventDefault(); const data = new FormData(event.currentTarget);
        set({ width: Number(data.get("width")), height: Number(data.get("height")), scale: viewport?.scale ?? "fit" });
      }}>
        <input name="width" aria-label={tr("CSS width", "CSS 宽度", "CSS 寬度")} type="number" min="320" max="3840" defaultValue={viewport?.width ?? 1280} required /> ×
        <input name="height" aria-label={tr("CSS height", "CSS 高度", "CSS 高度")} type="number" min="320" max="2160" defaultValue={viewport?.height ?? 720} required />
        <button type="submit" className="btn btn--small">{tr("Apply", "应用", "套用")}</button>
      </form>
      {viewport && <>
        <button type="button" className="btn btn--small" disabled={viewport.width > 2160} onClick={() => set({ ...viewport, width: viewport.height, height: viewport.width })}>{tr("Rotate", "旋转", "旋轉")}</button>
        <select aria-label={tr("Display scale", "显示比例", "顯示比例")} value={viewport.scale} onChange={event => set({ ...viewport, scale: event.target.value === "fit" ? "fit" : Number(event.target.value) })}>
          <option value="fit">Fit</option>{[0.5, 0.75, 1, 1.25, 1.5, 2].map(scale => <option key={scale} value={scale}>{scale * 100}%</option>)}
        </select>
      </>}
    </div>
  </details>;
}
