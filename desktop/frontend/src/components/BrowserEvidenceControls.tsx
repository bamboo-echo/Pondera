import { useEffect, useState } from "react";
import type { DesktopBrowserHost } from "../lib/browserHost";
import { useBrowserPanelStore } from "../lib/browserPanelStore";
import { useI18n } from "../lib/i18n";

type Recording = Awaited<ReturnType<NonNullable<DesktopBrowserHost["record"]>>>;
export function BrowserEvidenceControls({ tabId }: { tabId: string }) {
  const locale = useI18n().locale;
  const tr = (en: string, hans: string, hant: string) => locale === "en" ? en : locale === "zh-TW" ? hant : hans;
  const [recording, setRecording] = useState<Recording>(null);
  const [busy, setBusy] = useState(false);
  const [diagnostics, setDiagnostics] = useState<string | null>(null);
  const [capture, setCapture] = useState<{ path: string; width: number; height: number } | null>(null);
  useEffect(() => {
    let disposed = false;
    const poll = async () => {
      try { const result = await useBrowserPanelStore.getState().host?.record?.(tabId, "status"); if (!disposed) setRecording(result ?? null); }
      catch { /* Availability is reported by the explicit action. */ }
    };
    void poll(); const timer = setInterval(() => void poll(), 1000);
    return () => { disposed = true; clearInterval(timer); };
  }, [tabId]);
  const active = recording && ["preparing", "recording", "finalizing"].includes(recording.state);
  const run = async (action: "start" | "stop" | "cancel") => {
    const { host, notify } = useBrowserPanelStore.getState();
    if (!host?.record) { notify(tr("Recording is unavailable", "当前宿主不支持录制", "目前宿主不支援錄影")); return; }
    setBusy(true);
    try { setRecording(await host.record(tabId, action)); } catch (error) { notify(String(error)); }
    finally { setBusy(false); }
  };
  return <div className="browser-evidence">
    <button type="button" className="btn btn--small" onClick={() => {
      const { host, notify } = useBrowserPanelStore.getState();
      void host?.screenshot?.(tabId).then(setCapture).catch(error => notify(String(error)));
    }}>{tr("Screenshot", "截图", "截圖")}</button>
    {capture && <button type="button" className="btn btn--small" title={capture.path} onClick={() => { void navigator.clipboard.writeText(capture.path); }}>{capture.width}×{capture.height} · {tr("Copy path", "复制路径", "複製路徑")}</button>}
    <button type="button" className="btn btn--small" title={tr("Stop from the View menu or Ctrl/⌘+Shift+R even when this panel is closed", "关闭面板后也可通过 View 菜单或 Ctrl/⌘+Shift+R 停止录制", "關閉面板後也可透過 View 選單或 Ctrl/⌘+Shift+R 停止錄影")} disabled={busy} onClick={() => void run(active ? "stop" : "start")}>{active ? tr("Stop recording", "停止录制", "停止錄影") : tr("Record · 20 s", "录制 · 20 秒", "錄影 · 20 秒")}</button>
    {active && <button type="button" className="btn btn--small" onClick={() => void run("cancel")}>{tr("Cancel recording", "取消录制", "取消錄影")}</button>}
    {recording && <span role="status" title={recording.path ?? recording.error}>{recording.state} · {Math.ceil(recording.bytes / 1024)} KiB</span>}
    {recording?.path && <button type="button" className="btn btn--small" onClick={() => { void navigator.clipboard.writeText(recording.path!); }}>{tr("Copy artifact path", "复制产物路径", "複製產物路徑")}</button>}
    <button type="button" className="btn btn--small" onClick={() => {
      const { host, notify } = useBrowserPanelStore.getState();
      void host?.diagnostics?.(tabId).then(result => setDiagnostics(result.available ? JSON.stringify(result.entries ?? [], null, 2) : tr("Diagnostics unavailable", "诊断不可用", "診斷不可用"))).catch(error => notify(String(error)));
    }}>{tr("Page diagnostics", "页面诊断", "頁面診斷")}</button>
    {diagnostics !== null && <details open><summary onClick={() => setDiagnostics(null)}>{tr("Close diagnostics", "关闭诊断", "關閉診斷")}</summary><pre>{diagnostics}</pre></details>}
  </div>;
}
