import { BrowserWindow, type WebContents, type WebFrameMain } from "electron";
import { APP_ORIGIN } from "./protocol.js";
import { errorText, type Logger } from "./log.js";

export interface AssistantWindowDeps {
  preloadPath: string;
  appURL: string;
  platform: NodeJS.Platform;
  icon?: string;
  log: Logger;
}

// Assistant URL is the main app shell with ?assistant=1. The renderer picks
// AssistantApp for that surface (see frontend/src/main.tsx).
export function assistantWindowURL(appURL: string): string {
  try {
    const base = new URL(appURL);
    base.searchParams.set("assistant", "1");
    return base.toString();
  } catch {
    return appURL.includes("?") ? `${appURL}&assistant=1` : `${appURL}?assistant=1`;
  }
}

// AssistantWindowHost owns the separate, small config-assistant window. It
// shares the host preload bridge but has its own lifecycle and geometry.
export class AssistantWindowHost {
  private win: BrowserWindow | null = null;
  // Timestamps of automatic rebuilds, used to bound a recreate loop when the
  // window keeps failing to load (e.g. the service is down).
  private recoveries: number[] = [];
  private static readonly RECOVERY_WINDOW_MS = 10_000;
  private static readonly RECOVERY_LIMIT = 2;

  constructor(private readonly deps: AssistantWindowDeps) {}

  get browserWindow(): BrowserWindow | null {
    return this.win && !this.win.isDestroyed() ? this.win : null;
  }

  // The assistant window shares the host preload and thus the renderer IPC
  // contract, so the main process must accept it as a trusted sender too.
  isTrustedSender(sender: WebContents, frame: WebFrameMain | null | undefined): boolean {
    const win = this.browserWindow;
    return Boolean(win) && sender === win?.webContents && frame != null && frame === win.webContents.mainFrame;
  }

  show(): void {
    const existing = this.browserWindow;
    if (existing) {
      if (existing.isMinimized()) existing.restore();
      existing.show();
      existing.focus();
      return;
    }
    const { deps } = this;
    const win = new BrowserWindow({
      width: 760,
      height: 780,
      minWidth: 520,
      minHeight: 560,
      show: false,
      title: "Pondera 配置助手",
      backgroundColor: "#111315",
      autoHideMenuBar: deps.platform !== "darwin",
      icon: deps.icon,
      webPreferences: {
        preload: deps.preloadPath,
        sandbox: true,
        contextIsolation: true,
        nodeIntegration: false,
        spellcheck: false,
      },
    });
    this.win = win;
    if (deps.platform !== "darwin") win.setMenuBarVisibility(false);
    win.webContents.setWindowOpenHandler(() => ({ action: "deny" }));
    win.webContents.on("will-attach-webview", (event) => event.preventDefault());
    let origin = APP_ORIGIN;
    try {
      origin = new URL(deps.appURL).origin;
    } catch {
      // Keep the packaged origin; a malformed dev URL fails at load time.
    }
    win.webContents.on("will-navigate", (event, url) => {
      if (url.startsWith(origin + "/")) return;
      event.preventDefault();
    });
    win.webContents.on("did-fail-load", (_event: Electron.Event, errorCode: number, description: string, url: string, isMainFrame: boolean) => {
      deps.log.warn(`assistant window failed to load (${errorCode}): ${description} url=${url} main=${isMainFrame}`);
      // -3 is ERR_ABORTED, which happens on an intentional reload/navigation.
      if (isMainFrame && errorCode !== -3) this.recover(`did-fail-load ${errorCode}`);
    });
    // A crashed renderer would otherwise leave an empty, unclosable shell.
    win.webContents.on("render-process-gone", (_event: Electron.Event, details: Electron.RenderProcessGoneDetails) => {
      deps.log.error(`assistant window renderer gone reason=${details.reason} exitCode=${details.exitCode}`);
      this.recover(`render-process-gone ${details.reason}`);
    });
    win.once("ready-to-show", () => win.show());
    win.on("closed", () => {
      this.win = null;
    });
    void win.loadURL(assistantWindowURL(deps.appURL)).catch((error: unknown) => {
      deps.log.warn(`assistant window load failed: ${errorText(error)}`);
    });
  }

  // Replaces a dead or failed window with a freshly built one. Bounded so a
  // persistent failure (service unavailable) cannot spin forever.
  private recover(reason: string): void {
    const now = Date.now();
    this.recoveries = this.recoveries.filter((at) => now - at < AssistantWindowHost.RECOVERY_WINDOW_MS);
    if (this.recoveries.length >= AssistantWindowHost.RECOVERY_LIMIT) {
      this.deps.log.error(`assistant window recovery abandoned after repeated failures: ${reason}`);
      return;
    }
    this.recoveries.push(now);
    this.deps.log.warn(`rebuilding assistant window: ${reason}`);
    this.destroy();
    this.show();
  }

  destroy(): void {
    const win = this.browserWindow;
    if (!win) return;
    this.win = null;
    win.destroy();
  }

  // Exposed for diagnostics; the assistant window should never host guest web
  // views, so this is only useful in tests.
  static webContents(host: AssistantWindowHost): WebContents | null {
    return host.browserWindow?.webContents ?? null;
  }
}
