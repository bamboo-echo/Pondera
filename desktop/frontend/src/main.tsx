import "./lib/compat";
import { lazy, StrictMode, Suspense } from "react";
import { createRoot } from "react-dom/client";
import App from "./App";
import { ErrorBoundary } from "./components/ErrorBoundary";
import { installPerformancePressureMonitor } from "./lib/crash";
import { installGlobalCrashHandlers } from "./lib/globalCrashHandlers";
import { desktopHost } from "./lib/desktopHost";
import { installBreadcrumbConsoleHook } from "./lib/breadcrumbs";
import { installPerfDebugHook } from "./lib/perfDebug";
import { LocaleProvider, preloadDetectedLocale, t } from "./lib/i18n";
import { ToastProvider } from "./lib/toast";
import { initFontFamily } from "./lib/fontFamily";
import { initTextSize } from "./lib/textSize";
import { initTypographyPreferences } from "./lib/typographyPreferences";
import { initTheme } from "./lib/theme";
import { initConversationWidth } from "./lib/conversationWidth";
import appShellStylesheetURL from "./styles.css?url";

// The assistant shell lives in its own window (`?assistant=1`). Keeping it on a
// dynamic import means the main entry never pays for its code or CSS.
const AssistantApp = lazy(() => import("./assistant/AssistantApp"));

// Install first so startup/runtime failures paint a useful error instead of a
// featureless webview background, with the recent console trail attached.
installGlobalCrashHandlers();
installBreadcrumbConsoleHook();
installPerformancePressureMonitor();
installPerfDebugHook();

// Apply the saved appearance (auto/light/dark) before the first paint.
function initTypographyPlatform() {
  if (typeof document === "undefined" || typeof navigator === "undefined") return;
  const params = new URLSearchParams(window.location.search);
  const override = params.get("platform");
  const marker = `${navigator.platform} ${navigator.userAgent}`;
  const platform =
    override === "darwin" || override === "windows" || override === "linux"
      ? override
      : /Win/i.test(marker)
        ? "windows"
        : /Mac/i.test(marker)
          ? "darwin"
          : "linux";
  document.documentElement.setAttribute("data-platform", platform);
}

initTypographyPlatform();
initTheme();
initConversationWidth();
initTextSize();
initFontFamily();
initTypographyPreferences();

// Pre-warm font fallback stacks so the first frame doesn't flicker between the
// browser default font and the app's configured typeface. Inserting a hidden span
// with CJK + emoji + math glyphs forces the OS font subsystem to resolve and
// cache the fallback chains before React mounts.
function prewarmFontFallbacks() {
  const span = document.createElement("span");
  span.style.cssText = "position:absolute;visibility:hidden;font-size:1px;pointer-events:none";
  span.textContent = "中文日本語한국어 математика 😀🎉✓⚠∑∏∫";
  document.body.appendChild(span);
  // Force layout so the browser resolves font fallback chains.
  void span.offsetHeight;
  requestAnimationFrame(() => {
    requestAnimationFrame(() => {
      span.remove();
    });
  });
}
prewarmFontFallbacks();


// Inside the desktop shell, suppress the webview's default right-click menu — its
// Reload / Back / Inspect entries are easy to hit by accident and can reset or
// navigate away from the app. Text inputs keep their native Cut/Copy/Paste menu;
// the terminal area is exempt so its own context menu can offer copy/paste.
// Left alone in a plain browser (pnpm dev) so devtools stay reachable.
if (desktopHost().kind !== "none") {
  window.addEventListener("contextmenu", (e) => {
    const target = e.target as HTMLElement | null;
    if (!target?.closest("input, textarea, .chat-transcript") && !target?.closest(".terminal-view")) e.preventDefault();
  });
}

const root = document.getElementById("root");
if (!root) throw new Error("missing #root");
const rootElement = root;

// Startup must always end in something the user can act on: the boot shell is
// replaced by the app (styled or degraded) or by a visible error with a retry
// action. A dependency that hangs or fails may never strand the splash screen.
const BOOT_DEPENDENCY_TIMEOUT_MS = 10_000;

function loadAppShellStylesheet(): Promise<void> {
  return new Promise<void>((resolve, reject) => {
    const link = document.createElement("link");
    link.rel = "stylesheet";
    link.href = appShellStylesheetURL;
    link.onload = () => resolve();
    link.onerror = () => reject(new Error(`failed to load desktop stylesheet: ${appShellStylesheetURL}`));
    document.head.appendChild(link);
  });
}

// Renders a fixed, style-free notice above the app so it stays readable even
// when the app stylesheet itself is what failed.
function showBootNotice(message: string) {
  const notice = document.createElement("div");
  notice.setAttribute("role", "alert");
  notice.style.cssText =
    "position:fixed;left:50%;bottom:24px;transform:translateX(-50%);z-index:2147483647;" +
    "display:flex;align-items:center;gap:12px;max-width:min(560px,90vw);padding:10px 14px;" +
    "border-radius:10px;background:#2a1d16;color:#f4f4f3;font:13px/1.4 Inter,-apple-system,'Segoe UI',sans-serif;" +
    "box-shadow:0 6px 24px rgba(0,0,0,.45)";
  const text = document.createElement("span");
  text.textContent = message;
  notice.appendChild(text);
  const retry = document.createElement("button");
  retry.type = "button";
  retry.textContent = t("common.retry");
  retry.style.cssText =
    "flex:none;padding:4px 10px;border-radius:6px;border:1px solid #e58a3a;background:transparent;" +
    "color:#e58a3a;font:inherit;font-weight:600;cursor:pointer";
  retry.onclick = () => window.location.reload();
  notice.appendChild(retry);
  document.body.appendChild(notice);
}

// Resolves to "ok" once stylesheet and locale are ready, "failed" when the
// stylesheet cannot load, and "timeout" when either dependency hangs.
async function waitForBootDependencies(): Promise<"ok" | "failed" | "timeout"> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const timeout = new Promise<"timeout">((resolve) => {
    timer = setTimeout(() => resolve("timeout"), BOOT_DEPENDENCY_TIMEOUT_MS);
  });
  const dependencies = Promise.allSettled([loadAppShellStylesheet(), preloadDetectedLocale()]).then(([style, locale]) => {
    if (locale.status === "rejected") console.error("failed to preload desktop locale", locale.reason);
    if (style.status === "rejected") {
      console.error("failed to load desktop stylesheet", style.reason);
      return "failed" as const;
    }
    return "ok" as const;
  });
  const outcome = await Promise.race([dependencies, timeout]);
  if (timer) clearTimeout(timer);
  return outcome;
}

async function mountApp() {
  // The HTML boot shell paints immediately with critical inline styles. Load
  // the full stylesheet and detected locale in parallel, then replace that
  // shell in one React commit so users never see an unstyled application.
  const boot = await waitForBootDependencies();
  const assistantSurface = new URLSearchParams(window.location.search).get("assistant") === "1";
  createRoot(rootElement).render(
    <StrictMode>
      <ErrorBoundary>
        <LocaleProvider>
          <ToastProvider>
            {assistantSurface ? (
              <Suspense fallback={null}>
                <AssistantApp />
              </Suspense>
            ) : (
              <App />
            )}
          </ToastProvider>
        </LocaleProvider>
      </ErrorBoundary>
    </StrictMode>,
  );
  // Tell the HTML boot watchdog (which covers a failed main bundle) that the
  // app did mount, then surface any degraded startup.
  (window as Window & { __ponderaBootMounted?: boolean }).__ponderaBootMounted = true;
  if (boot === "timeout") showBootNotice(t("app.bootTimeout"));
  else if (boot === "failed") showBootNotice(t("app.bootStylesheetFailed"));

  void import("./lib/desktopWebViewHeartbeat").then(({ installDesktopWebViewHeartbeat }) => {
    installDesktopWebViewHeartbeat();
  });
}

void mountApp();
