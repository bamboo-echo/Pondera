import { BrowserWindow, type WebContents } from "electron";
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

  constructor(private readonly deps: AssistantWindowDeps) {}

  get browserWindow(): BrowserWindow | null {
    return this.win && !this.win.isDestroyed() ? this.win : null;
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
    win.webContents.on("did-fail-load", (_event: Electron.Event, errorCode: number, description: string) => {
      deps.log.warn(`assistant window failed to load (${errorCode}): ${description}`);
    });
    win.once("ready-to-show", () => win.show());
    win.on("closed", () => {
      this.win = null;
    });
    void win.loadURL(assistantWindowURL(deps.appURL)).catch((error: unknown) => {
      deps.log.warn(`assistant window load failed: ${errorText(error)}`);
    });
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
