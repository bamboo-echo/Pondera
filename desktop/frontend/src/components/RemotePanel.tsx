import { ErrorMessage } from "./ErrorMessage";
import { useAppNavigationStore } from "../store/appNavigation";
import { useEffect, useMemo, useRef, useState } from "react";

import { app } from "../lib/bridge";
import { useT } from "../lib/i18n";
import { isRemoteDegradedWarning, isRemoteTerminalFailure, remoteConnectionErrorSummaryKey } from "../lib/remoteErrors";
import { resolveRemoteWorkspace } from "../lib/remoteWorkspace";
import { publishNavigationIntent } from "../lib/useNavigationIntentFence";
import { fileNavigationOwner } from "../lib/fileNavigationCommands";
import type { FileNavigationOwner } from "../lib/fileNavigationOwner";
import { useFileNavigationRecord } from "../app-shell/useFileNavigation";
import { useRemoteStore, type RemoteExplorerTab } from "../store/remote";
import type { RemoteForwardView } from "../lib/types";
import { RemoteFilesView } from "./RemoteFilesView";
import { RemoteStatusChip } from "./RemoteHostsPage";

const EMPTY_REMOTE_FORWARDS: RemoteForwardView[] = [];

/** RemotePanel is the right-dock remote work surface: a host header with
 *  Files / Ports / Server tabs. */
export function RemotePanel({ onClose, tabId, dockTabId, fileNavigation: fileNavigationProp, navigationSignal }: { onClose: () => void; tabId?: string; dockTabId?: string; fileNavigation?: FileNavigationOwner; navigationSignal?: AbortSignal }) {
  const t = useT();
  const hostId = useRemoteStore((s) => s.explorerHostId);
  const host = useRemoteStore((s) => s.hosts.find((item) => item.id === hostId));
  const tab = useRemoteStore((s) => s.explorerTab);
  const setTab = useRemoteStore((s) => s.setExplorerTab);
  const status = useRemoteStore((s) => (hostId ? s.statuses[hostId] : undefined));
  const activeWorkspace = useRemoteStore((s) => s.activeWorkspace);
  const setSettingsTarget = useAppNavigationStore((s) => s.setSettingsTarget);
  const [fallbackFileNavigation] = useState(fileNavigationOwner);
  const fileNavigation = fileNavigationProp ?? fallbackFileNavigation;
  const fileScope = useMemo(() => ({ sessionTabId: tabId ?? "", dockTabId: dockTabId ?? "" }), [dockTabId, tabId]);
  // Another host is another resource space: binding it replaces this dock's
  // record, so no path or access context crosses between hosts.
  const fileKey = useMemo(
    () => ({ resource: hostId ?? "", session: `${tabId ?? ""}\u0000${hostId ?? ""}` }),
    [hostId, tabId],
  );
  const fileRecord = useFileNavigationRecord(fileNavigation, fileScope, fileKey);

  if (!hostId) return null;
  const connected = status?.state === "connected" || status?.state === "degraded";
  const busy = status?.state === "connecting" || status?.state === "reconnecting" || status?.state === "pending_hostkey" || status?.state === "pending_secret";
  const terminalFailure = isRemoteTerminalFailure(status);
  const degradedWarning = isRemoteDegradedWarning(status);
  const target = host ? `${host.user ? `${host.user}@` : ""}${host.host}${host.port && host.port !== 22 ? `:${host.port}` : ""}` : hostId;
  // The tree is rooted at the workspace the active remote tab is bound to, so
  // it shows the directory the user picked instead of the remote home.
  const treeRoot = activeWorkspace && activeWorkspace.hostId === hostId ? activeWorkspace.workspace : ".";

  return (
    <section className="remote-panel" aria-label={t("remote.explorer")}>
      <header className="remote-panel__header">
        <span className="remote-panel__host-copy">
          <span className="remote-panel__host">{host?.label || hostId}</span>
          <span className="remote-panel__target">{target}</span>
        </span>
        <RemoteStatusChip state={status?.state ?? "stopped"} />
        <div className="remote-panel__header-actions">
          {connected ? (
            <button className="btn btn--small" onClick={() => void app.DisconnectRemoteHost(hostId).catch(() => {})}>
              {t("remote.disconnect")}
            </button>
          ) : (
            <button className="btn btn--small btn--primary" disabled={busy} onClick={() => void app.ConnectRemoteHost(hostId).catch(() => {})}>
              {busy ? t(`remote.status.${status?.state ?? "connecting"}`) : t("remote.connect")}
            </button>
          )}
          <button className="btn btn--ghost" onClick={() => setSettingsTarget("remote")}>
            {t("remote.manageHosts")}
          </button>
          <button className="btn btn--ghost" onClick={onClose} aria-label={t("rightDock.collapse")}>
            ×
          </button>
        </div>
      </header>

      {(terminalFailure || degradedWarning) && status && (
        <div className={`remote-panel__error-banner ${degradedWarning ? "remote-panel__error-banner--warning" : ""}`} role="alert">
          <strong>{t(degradedWarning ? "remote.status.degraded" : "remote.status.failed")}</strong>
          <span>{t(remoteConnectionErrorSummaryKey(status), { host: host?.label || hostId })}</span>
        </div>
      )}

      {status?.state === "reconnecting" && (
        <div className="remote-panel__banner" role="status">
          {t("remote.banner.reconnecting", { n: status.attempt ?? 1 })}
        </div>
      )}

      <nav className="remote-panel__tabs" role="tablist">
        {(["files", "ports", "server"] as RemoteExplorerTab[]).map((id) => (
          <button
            key={id}
            role="tab"
            aria-selected={tab === id}
            className={`remote-panel__tab ${tab === id ? "is-active" : ""}`}
            onClick={() => setTab(id)}
          >
            {t(`remote.tab.${id}`)}
          </button>
        ))}
      </nav>

      <div className="remote-panel__body">
        {tab === "files" && (
          <RemoteFilesView
            key={hostId}
            hostId={hostId}
            rootPath={treeRoot}
            connected={connected}
            navigationSignal={navigationSignal}
            fileNavigation={fileNavigation}
            fileScope={fileScope}
            record={fileRecord}
          />
        )}
        {tab === "ports" && <RemotePortsTab hostId={hostId} connected={connected} />}
        {tab === "server" && <RemoteServerTab hostId={hostId} connected={connected} defaultWorkspace={host?.defaultWorkspace} />}
      </div>
    </section>
  );
}

// ── Ports tab ──

function RemotePortsTab({ hostId, connected }: { hostId: string; connected: boolean }) {
  const t = useT();
  const forwards = useRemoteStore((s) => s.forwards[hostId] ?? EMPTY_REMOTE_FORWARDS);
  const setForwards = useRemoteStore((s) => s.setForwards);
  const [localPort, setLocalPort] = useState(8080);
  const [remoteHost, setRemoteHost] = useState("127.0.0.1");
  const [remotePort, setRemotePort] = useState(80);
  const [label, setLabel] = useState("");
  const [actionErr, setActionErr] = useState("");

  useEffect(() => {
    if (connected) void app.RemoteForwards(hostId).then((f) => setForwards(hostId, f));
  }, [hostId, connected, setForwards]);

  const add = async () => {
    try {
      await app.AddRemoteForward(hostId, { localPort, remoteHost, remotePort, label });
      setLabel("");
      setActionErr("");
    } catch (e) {
      setActionErr(String(e));
    }
  };

  const remove = async (forwardId: string) => {
    try {
      await app.RemoveRemoteForward(hostId, forwardId);
      setActionErr("");
    } catch (e) {
      setActionErr(String(e));
    }
  };

  return (
    <div className="remote-ports">
      {actionErr && <p className="remote-panel__error" role="alert"><ErrorMessage error={actionErr} /></p>}
      {forwards.length === 0 ? (
        <p className="remote-panel__hint">{t("remote.ports.empty")}</p>
      ) : (
        <ul className="remote-ports__list">
          {forwards.map((f: RemoteForwardView) => (
            <li key={f.id} className="remote-ports__row">
              <span className={`remote-dot remote-dot--${f.state}`} aria-hidden />
              <span>{f.label || f.id}</span>
              {f.error && <span className="remote-panel__error"><ErrorMessage error={f.error} /></span>}
              <button className="btn btn--ghost" onClick={() => void remove(f.id)}>
                {t("remote.ports.remove")}
              </button>
            </li>
          ))}
        </ul>
      )}
      <div className="remote-ports__form">
        <input type="number" min={1} max={65535} aria-label={t("remote.ports.localPort")} value={localPort} onChange={(e) => setLocalPort(Number(e.target.value) || 0)} />
        <input aria-label={t("remote.ports.remoteHost")} value={remoteHost} onChange={(e) => setRemoteHost(e.target.value)} />
        <input type="number" min={1} max={65535} aria-label={t("remote.ports.remotePort")} value={remotePort} onChange={(e) => setRemotePort(Number(e.target.value) || 0)} />
        <input aria-label={t("remote.ports.label")} placeholder={t("remote.ports.label")} value={label} onChange={(e) => setLabel(e.target.value)} />
        <button className="btn btn--primary" disabled={!connected || !remoteHost.trim() || localPort < 1 || localPort > 65535 || remotePort < 1 || remotePort > 65535} onClick={() => void add()}>{t("remote.ports.add")}</button>
      </div>
    </div>
  );
}

// ── Server tab ──

function RemoteServerTab({ hostId, connected, defaultWorkspace }: { hostId: string; connected: boolean; defaultWorkspace?: string }) {
  const t = useT();
  const hostServers = useRemoteStore((s) => s.servers[hostId]);
  const setServer = useRemoteStore((s) => s.setServer);
  const [workspace, setWorkspace] = useState("");
  const [logs, setLogs] = useState("");
  const [actionErr, setActionErr] = useState("");
  const logsOpen = useRef(false);
  const workspaceEdited = useRef(false);

  useEffect(() => {
    let cancelled = false;
    workspaceEdited.current = false;
    setWorkspace(resolveRemoteWorkspace(undefined, defaultWorkspace));
    void app.RemoteLastWorkspace(hostId)
      .then((lastWorkspace) => {
        if (!cancelled && !workspaceEdited.current) {
          setWorkspace(resolveRemoteWorkspace(lastWorkspace, defaultWorkspace));
        }
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, [defaultWorkspace, hostId]);

  // The panel manages one workspace at a time: the input value, or the host's
  // first registered serve while the input is still empty.
  const statusWorkspace = workspace || Object.keys(hostServers ?? {})[0] || "";
  const server = statusWorkspace ? hostServers?.[statusWorkspace] : undefined;

  useEffect(() => {
    if (!statusWorkspace) return;
    let cancelled = false;
    void app.RemoteServerStatus(hostId, statusWorkspace)
      .then((s) => {
        if (!cancelled) setServer(s);
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, [hostId, statusWorkspace, setServer]);

  const refreshLogs = async () => {
    if (!statusWorkspace) return;
    logsOpen.current = true;
    try {
      setLogs(await app.RemoteServerLogs(hostId, statusWorkspace, 200));
      setActionErr("");
    } catch (e) {
      setLogs("");
      setActionErr(String(e));
    }
  };

  const start = async () => {
    try {
      setActionErr("");
      await publishNavigationIntent("remote-workspace");
      await app.OpenRemoteWorkspace(hostId, workspace);
    } catch (e) {
      setActionErr(String(e));
    }
  };

  const stop = async () => {
    if (!statusWorkspace) return;
    try {
      setActionErr("");
      await app.StopRemoteServer(hostId, statusWorkspace);
    } catch (e) {
      setActionErr(String(e));
    }
  };

  const state = server?.state ?? "stopped";
  const busy = ["starting", "detect", "install", "waiting_lock", "launch", "health_check", "reuse"].includes(state);
  const stateLabel = state === "ready"
    ? t("remote.server.state.ready")
    : state === "error"
      ? t("remote.server.state.error")
      : busy
        ? t("remote.server.state.starting")
        : t("remote.server.state.stopped");
  const canManageServer = connected && Boolean(server?.workspace) && state !== "stopped";
  return (
    <div className="remote-server">
      <label className="remote-server__ws">
        {t("remote.server.workspace")}
        <input
          value={workspace}
          onChange={(e) => {
            workspaceEdited.current = true;
            setWorkspace(e.target.value);
          }}
          placeholder="~"
        />
      </label>
      <div className="remote-server__status">
        {stateLabel}
        {server?.message ? ` — ${server.message}` : ""}
        {server?.error ? <ErrorMessage error={server.error} /> : null}
        {actionErr ? <ErrorMessage error={actionErr} /> : null}
      </div>
      <div className="remote-server__actions">
        <button className="btn btn--primary" disabled={!connected || !workspace || busy} onClick={() => void start()}>
          {t("remote.server.openWeb")}
        </button>
        <button className="btn" disabled={!canManageServer || busy} onClick={() => void stop()}>
          {t("remote.server.stop")}
        </button>
        <button className="btn btn--ghost" disabled={!canManageServer} onClick={() => void refreshLogs()}>
          {t("remote.server.logs")}
        </button>
      </div>
      <p className="remote-panel__hint">{t("remote.server.providerHint")}</p>
      {logsOpen.current && (
        <pre className="remote-server__logs">
          {logs}
          <button className="btn btn--ghost" onClick={() => void refreshLogs()}>{t("remote.server.refreshLogs")}</button>
        </pre>
      )}
    </div>
  );
}
