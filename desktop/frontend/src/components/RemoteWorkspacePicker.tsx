import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { ChevronUp, Cloud, Folder, FolderOpen, Server } from "lucide-react";
import { app } from "../lib/bridge";
import { useT } from "../lib/i18n";
import { useRemoteNavigationCommand } from "../lib/remoteNavigationCommands";
import { useRemoteStore, waitForRemoteConnection } from "../store/remote";
import { parentOf } from "./remoteWizardFormat";
import type { RemoteDirEntry, RemoteHostView } from "../lib/types";
import "./RemoteConnectWizard.css";
import "./RemoteWorkspacePicker.css";

/**
 * RemoteWorkspacePicker picks a directory on an SSH host and opens it as a
 * workspace in THIS window.
 *
 * The connect wizard can already browse remote directories, but it makes the
 * user walk through connection setup first and ends in the remote surface. This
 * picker starts from the hosts that already exist: choose one, walk the remote
 * tree over SFTP, confirm. The confirmed directory becomes a remote project
 * and the session runs on the remote host inside the main workbench.
 */
export function RemoteWorkspacePicker({
  onClose,
  onRefresh,
  onConfigureHost,
  onMerged,
}: {
  onClose: () => void;
  onRefresh: () => Promise<void>;
  onConfigureHost?: () => void;
  onMerged?: (message: string) => void;
}) {
  const t = useT();
  const navigateRemote = useRemoteNavigationCommand();
  const statuses = useRemoteStore((s) => s.statuses);
  const [hosts, setHosts] = useState<RemoteHostView[] | null>(null);
  const [hostId, setHostId] = useState("");
  const [path, setPath] = useState("~");
  const [entries, setEntries] = useState<RemoteDirEntry[] | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const requestRef = useRef(0);
  const dialogRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    let alive = true;
    app.RemoteHosts().then((list) => {
      if (!alive) return;
      setHosts(list);
      setHostId((current) => current || list.find((host) => ["connected", "degraded"].includes(statuses[host.id]?.state ?? ""))?.id || list[0]?.id || "");
    }).catch((e) => {
      if (!alive) return;
      setHosts([]);
      setError(e instanceof Error ? e.message : String(e));
    });
    return () => { alive = false; };
    // Host statuses are read once for the initial pick; later changes arrive
    // through the store and re-run the listing effect below.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const openDir = async (host: string, next: string) => {
    if (!host) return;
    const requestId = ++requestRef.current;
    setPath(next);
    setEntries(null);
    setError("");
    try {
      const listed = await app.ListRemoteDir(host, next);
      if (requestId !== requestRef.current) return;
      setEntries(listed);
    } catch (e) {
      if (requestId !== requestRef.current) return;
      setEntries([]);
      setError(e instanceof Error ? e.message : String(e));
    }
  };

  // Picking a host connects it when needed, then lists its home directory.
  useEffect(() => {
    if (!hostId) return;
    let alive = true;
    const host = hosts?.find((item) => item.id === hostId);
    const start = host?.defaultWorkspace?.trim() || "~";
    const run = async () => {
      setBusy(true);
      setError("");
      try {
        const state = statuses[hostId]?.state;
        if (state !== "connected" && state !== "degraded") {
          await app.ConnectRemoteHost(hostId);
          await waitForRemoteConnection(hostId);
        }
        if (!alive) return;
        await openDir(hostId, start);
      } catch (e) {
        if (!alive) return;
        setEntries([]);
        setError(e instanceof Error ? e.message : String(e));
      } finally {
        if (alive) setBusy(false);
      }
    };
    void run();
    return () => { alive = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [hostId, hosts]);

  const confirm = async () => {
    const target = path.trim();
    if (!hostId || !target || busy) return;
    setBusy(true);
    setError("");
    try {
      let project: Awaited<ReturnType<typeof app.AddRemoteProject>> | null = null;
      try {
        project = await app.AddRemoteProject(hostId, target);
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
        return;
      }
      const canonical = project.merged ? project.workspace : target;
      if (project.merged) onMerged?.(t("remoteWizard.mergedProject", { path: canonical }));
      try {
        const outcome = await navigateRemote({ hostId, workspace: canonical }, { newSession: true });
        if (outcome.status === "cancelled") return;
        if (outcome.status === "failed") throw outcome.error;
      } catch (e) {
        setError(e instanceof Error ? e.message : String(e));
        if (!project.merged) {
          try {
            await app.RemoveRemoteProject(hostId, target);
          } catch {
            await onRefresh().catch(() => {});
          }
        }
        return;
      }
      await onRefresh();
      onClose();
    } finally {
      setBusy(false);
    }
  };

  const dirs = (entries ?? []).filter((entry) => entry.isDir);
  const parent = parentOf(path);

  return createPortal(
    <div
      data-app-overlay=""
      className="modal-backdrop remote-wizard-backdrop"
      role="presentation"
      onMouseDown={(event) => {
        if (!busy && event.target === event.currentTarget) onClose();
      }}
    >
      <div ref={dialogRef} className="modal remote-wizard remote-workspace-picker" role="dialog" aria-modal="true" aria-label={t("remotePicker.title")}>
        <div className="modal__title">{t("remotePicker.title")}</div>
        <div className="remote-wizard__frame">
          <div className="remote-wizard__body">
            <div className="remote-wizard__field-row">
              <label className="remote-wizard__field">
                <span>{t("remotePicker.host")}</span>
                <select
                  className="mem-select"
                  value={hostId}
                  disabled={busy || !hosts?.length}
                  onChange={(event) => setHostId(event.target.value)}
                >
                  {(hosts ?? []).map((host) => (
                    <option key={host.id} value={host.id}>
                      {host.label || host.host}
                    </option>
                  ))}
                </select>
              </label>
              <label className="remote-wizard__field">
                <span>{t("remotePicker.path")}</span>
                <input
                  className="remote-wizard__path-input"
                  value={path}
                  disabled={busy}
                  onChange={(event) => setPath(event.target.value)}
                  onKeyDown={(event) => {
                    if (event.key === "Enter") {
                      event.preventDefault();
                      void openDir(hostId, event.currentTarget.value.trim() || "~");
                    }
                  }}
                />
              </label>
            </div>

            {error ? <div className="remote-wizard__error">{error}</div> : null}
            {!hosts?.length && !error ? (
              <div className="remote-wizard__empty">
                <Server size={22} aria-hidden="true" />
                <span>{t("remotePicker.noHosts")}</span>
                {onConfigureHost ? (
                  <button type="button" className="btn btn--small" onClick={onConfigureHost}>
                    <Cloud size={14} aria-hidden="true" />{t("remotePicker.configureHost")}
                  </button>
                ) : null}
              </div>
            ) : (
              <div className="remote-wizard__browse">
                {parent && parent !== path ? (
                  <button type="button" className="remote-wizard__dir" onClick={() => void openDir(hostId, parent)}>
                    <ChevronUp size={14} aria-hidden="true" />
                    <span className="remote-wizard__dir-name">{t("remotePicker.parent")}</span>
                  </button>
                ) : null}
                {entries === null ? (
                  <div className="remote-wizard__empty">{t("common.loading")}</div>
                ) : dirs.length === 0 ? (
                  <div className="remote-wizard__empty">{t("remotePicker.noDirectories")}</div>
                ) : (
                  dirs.map((entry) => (
                    <button key={entry.path} type="button" className="remote-wizard__dir" onClick={() => void openDir(hostId, entry.path)}>
                      <Folder size={14} aria-hidden="true" />
                      <span className="remote-wizard__dir-name">{entry.name}</span>
                    </button>
                  ))
                )}
              </div>
            )}
          </div>
          <div className="remote-wizard__footer">
            <button type="button" className="btn" disabled={busy} onClick={onClose}>{t("common.cancel")}</button>
            <button type="button" className="btn btn--primary remote-wizard__pick-btn" disabled={busy || !hostId || !path.trim()} onClick={() => void confirm()}>
              <FolderOpen size={14} aria-hidden="true" />{t("remotePicker.openHere")}
            </button>
          </div>
        </div>
      </div>
    </div>,
    document.body,
  );
}
