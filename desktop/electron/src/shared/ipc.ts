export const IPC = {
  contract: "pondera:contract",
  invoke: "pondera:invoke",
  event: "pondera:event",
  serviceState: "pondera:service-state",
  serviceStateGet: "pondera:service-state:get",
  processDiagnostics: "pondera:native:process-diagnostics",
  captureRendererProfile: "pondera:native:capture-renderer-profile",
  cancelRendererProfile: "pondera:native:cancel-renderer-profile",
  exportHeapSnapshot: "pondera:native:export-heap-snapshot",
  rendererDiagnostic: "pondera:native:renderer-diagnostic",
  openExternal: "pondera:native:open-external",
  clipboardWrite: "pondera:native:clipboard-write",
  clipboardRead: "pondera:native:clipboard-read",
  windowMinimise: "pondera:native:window-minimise",
  windowToggleMaximise: "pondera:native:window-toggle-maximise",
  windowIsMaximised: "pondera:native:window-is-maximised",
  windowClose: "pondera:native:window-close",
  windowGetBounds: "pondera:native:window-get-bounds",
  windowSetTheme: "pondera:native:window-set-theme",
  windowSetBackground: "pondera:native:window-set-background",
  appZoomGet: "pondera:native:app-zoom-get",
  appZoomSet: "pondera:native:app-zoom-set",
  appZoomReset: "pondera:native:app-zoom-reset",
  graphicsGet: "pondera:native:graphics-get",
  graphicsSet: "pondera:native:graphics-set",
  browserControlGet: "pondera:native:browser-control-get",
  browserControlSetEnabled: "pondera:native:browser-control-set-enabled",
  browserControlSetIgnoreCertificateErrors: "pondera:native:browser-control-set-ignore-certificate-errors",
  browserControlClearCache: "pondera:native:browser-control-clear-cache",
  browserControlClearAll: "pondera:native:browser-control-clear-all",
  browserControlImportChrome: "pondera:native:browser-control-import-chrome",
  browserList: "pondera:browser:list",
  browserOpen: "pondera:browser:open",
  browserClose: "pondera:browser:close",
  browserActivate: "pondera:browser:activate",
  browserNavigate: "pondera:browser:navigate",
  browserSetZoom: "pondera:browser:set-zoom",
  browserSetViewport: "pondera:browser:set-viewport",
  browserPickElement: "pondera:browser:pick-element",
  browserRecord: "pondera:browser:record",
  browserDiagnostics: "pondera:browser:diagnostics",
  browserScreenshot: "pondera:browser:screenshot",
  browserRestorePreview: "pondera:browser:restore-preview",
  browserToggleDevTools: "pondera:browser:toggle-devtools",
  browserResume: "pondera:browser:resume",
  browserUserTakeover: "pondera:browser:user-takeover",
  browserSetLayout: "pondera:browser:set-layout",
  browserSetOverlay: "pondera:browser:set-overlay",
  browserTabs: "pondera:browser:tabs",
  browserDownload: "pondera:browser:download",
  browserTakeover: "pondera:browser:takeover",
} as const;

export type ServicePhase = "starting" | "ready" | "restarting" | "stopping" | "failed" | "exited";

export interface ServiceState {
  phase: ServicePhase;
  generation: string;
  error?: string;
}

export interface ContractInfo {
  protocolVersion: number;
  digest: string;
  commands: readonly string[];
}

export interface EventFrame {
  seq: number;
  generation: string;
  name: string;
  args: unknown[];
}

export type IpcResult = { ok: true; value: unknown } | { ok: false; message: string; code?:number; data?:unknown };

export interface WindowBounds {
  x: number;
  y: number;
  width: number;
  height: number;
  maximised: boolean;
}

export type WindowTheme = "system" | "light" | "dark";

export type HostOS = "darwin" | "windows" | "linux";

export type BrowserControlWarning = "invalid-config" | "unreadable-config" | "unsupported-version";

export interface BrowserControlState {
  controlEnabled: boolean;
  ignoreCertificateErrors: boolean;
  writable: boolean;
  warning: BrowserControlWarning | null;
}

export type ChromeImportFailure =
  | "chrome-missing"
  | "profile-not-found"
  | "cookies-unreadable"
  | "safe-storage-denied"
  | "safe-storage-unavailable"
  | "unsupported-platform";

export type ChromeImportOutcome =
  | { ok: true; profile: string; cookies: number; skipped: number }
  | { ok: false; reason: ChromeImportFailure };

export function hostOS(platform: string): HostOS {
  if (platform === "darwin") return "darwin";
  if (platform === "win32") return "windows";
  return "linux";
}

export type BrowserTabMode = "agent" | "human";

export interface BrowserTabView {
  restorePreview?: boolean;
  sessionId?: string;
  id: string;
  taskId: string;
  url: string;
  title: string;
  loading: boolean;
  canGoBack: boolean;
  canGoForward: boolean;
  temporary: boolean;
  mode: BrowserTabMode;
  epoch: number;
  zoom: number;
  viewport?: { width: number; height: number; scale: "fit" | number } | null;
  operation?: { id: string; phase: string; message?: string };
  active: boolean;
  error: { code: number; description: string } | null;
}

export type BrowserDownloadState = "progressing" | "completed" | "cancelled" | "interrupted";

export interface BrowserDownloadView {
  id: string;
  tabId: string;
  url: string;
  filename: string;
  path: string;
  state: BrowserDownloadState;
  received: number;
  total: number;
}

export interface BrowserLayoutRect {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface BrowserOpenOptions {
  temporary?: boolean;
  taskId?: string;
}

export type BrowserNavigateAction = "back" | "forward" | "reload" | "stop";

export interface BrowserNavigateTarget {
  url?: string;
  action?: BrowserNavigateAction;
}

export type BrowserTakeoverKind = "mousedown" | "keydown" | "wheel" | "touchstart" | "pointerdown";
