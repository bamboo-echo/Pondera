import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";

import { app } from "../lib/bridge";
import { useT } from "../lib/i18n";
import { ErrorMessage } from "./ErrorMessage";
import { CodeViewer } from "./CodeViewer";
import type { FileNavigationOwner, FileNavigationScope, FileNavigationSnapshot } from "../lib/fileNavigationOwner";
import type { RemoteDirEntry } from "../lib/types";

/**
 * RemoteFilesView is the remote file surface: a lazy tree over SFTP plus a
 * read/edit view for the selected file.
 *
 * It lives in its own module because the right dock shows it in two places:
 * the remote panel's Files tab, and the ordinary Files dock slot when the
 * active session tab is a remote workspace. In the second case the tree is
 * rooted at that workspace, so the panel shows the directory the user picked
 * instead of the remote home.
 */
export function RemoteFilesView({
  hostId,
  rootPath,
  connected,
  navigationSignal,
  fileNavigation,
  fileScope,
  record,
}: {
  hostId: string;
  /** Directory the tree is rooted at; "." is the remote home. */
  rootPath: string;
  connected: boolean;
  navigationSignal?: AbortSignal;
  fileNavigation: FileNavigationOwner;
  fileScope: FileNavigationScope;
  record: FileNavigationSnapshot | null;
}) {
  const t = useT();
  const [entriesByDir, setEntriesByDir] = useState<Record<string, RemoteDirEntry[]>>({});
  const [openDirs, setOpenDirs] = useState<Set<string>>(new Set());
  // One preview at a time, chosen by the dock's committed navigation: the panel
  // reads a result rather than keeping a second selection of its own.
  const selectedEntry = record?.selected ?? null;
  const selected = selectedEntry?.resource.path ?? null;
  const presentedSelection = selectedEntry?.resource.access.source === "presented" ? selected : null;
  const [loadErr, setLoadErr] = useState("");
  const lifetime = useRef(0);
  const loads = useRef(new Map<string, number>());
  useEffect(() => () => { lifetime.current++; loads.current.clear(); }, []);

  const loadDir = useCallback(
    async (path: string, signal?: AbortSignal) => {
      const owner = lifetime.current;
      const generation = (loads.current.get(path) ?? 0) + 1;
      loads.current.set(path, generation);
      const current = () => !signal?.aborted && !navigationSignal?.aborted && owner === lifetime.current && loads.current.get(path) === generation;
      try {
        const entries = await app.ListRemoteDir(hostId, path);
        if (!current()) return;
        setEntriesByDir((m) => ({ ...m, [path]: entries }));
        setLoadErr("");
      } catch (e) {
        if (!current()) return;
        setLoadErr(t("remote.tree.loadError", { err: String(e) }));
      }
    },
    [hostId, t, navigationSignal],
  );

  useEffect(() => {
    if (connected) void loadDir(rootPath);
  }, [connected, loadDir, rootPath]);

  // Expanding the ancestors of a committed selection is the remote tree's whole
  // reveal. It runs from the record's revision, so a remount re-expands the
  // retained selection without replaying the command that opened it.
  const appliedRevealRef = useRef("");
  const revealRevision = selected ? `${record?.generation ?? 0}:${record?.contentRevision ?? 0}:${record?.treeReveal ?? 0}:${selected}` : "";
  useEffect(() => {
    if (!connected || !selected || !revealRevision) return;
    if (appliedRevealRef.current === revealRevision) return;
    appliedRevealRef.current = revealRevision;
    const slashPath = selected.replaceAll("\\\\", "/");
    const parts = slashPath.split("/").filter(Boolean);
    const absolute = slashPath.startsWith("/");
    const ancestors: string[] = [];
    for (let i = 1; i < parts.length; i += 1) {
      ancestors.push(`${absolute ? "/" : ""}${parts.slice(0, i).join("/")}`);
    }
    const lifetimeAtStart = lifetime.current;
    void (async () => {
      for (const dir of ancestors) {
        if (lifetime.current !== lifetimeAtStart || navigationSignal?.aborted) return;
        await loadDir(dir, record?.signal);
        if (lifetime.current !== lifetimeAtStart || navigationSignal?.aborted) return;
        setOpenDirs(prev => new Set(prev).add(dir));
      }
    })();
  }, [connected, loadDir, navigationSignal, record?.signal, revealRevision, selected]);

  const toggleDir = (path: string) => {
    setOpenDirs((prev) => {
      const next = new Set(prev);
      if (next.has(path)) {
        next.delete(path);
      } else {
        next.add(path);
        if (!entriesByDir[path]) void loadDir(path);
      }
      return next;
    });
  };

  const renderDir = (path: string, depth: number): ReactNode => {
    const entries = entriesByDir[path];
    if (!entries) return null;
    if (entries.length === 0) return <li className="remote-tree__empty">{t("remote.tree.empty")}</li>;
    return entries.map((e) => (
      <li key={e.path} className="remote-tree__item" style={{ paddingLeft: depth * 12 }}>
        {e.isDir ? (
          <>
            <button className="remote-tree__row" onClick={() => toggleDir(e.path)} role="treeitem" aria-expanded={openDirs.has(e.path)}>
              {openDirs.has(e.path) ? "▾" : "▸"} {e.name}/
            </button>
            {openDirs.has(e.path) && <ul>{renderDir(e.path, depth + 1)}</ul>}
          </>
        ) : (
          <button
            className={`remote-tree__row ${selected === e.path ? "is-selected" : ""}`}
            onClick={() => { void Promise.resolve(fileNavigation.selectPath(fileScope, { hostId, path: e.path })); }}
            role="treeitem"
          >
            {e.name}
          </button>
        )}
      </li>
    ));
  };

  if (!connected) return <p className="remote-panel__hint">{t("remote.status.stopped")}</p>;

  return (
    <div className="remote-files">
      <div className="remote-files__tree" role="tree">
        {loadErr && <p className="remote-panel__error" role="alert"><ErrorMessage error={loadErr} /></p>}
        {presentedSelection && (
          <button
            className="remote-tree__row is-selected remote-tree__presented"
            onClick={() => { void Promise.resolve(fileNavigation.selectPath(fileScope, { hostId, path: presentedSelection })); }}
            role="treeitem"
            title={presentedSelection}
          >
            {presentedSelection.split(/[\\/]/).filter(Boolean).slice(-1)[0] || presentedSelection}
          </button>
        )}
        <ul>{renderDir(rootPath, 0)}</ul>
      </div>
      <div className="remote-files__view">
        {selected ? (
          <RemoteFileView
            key={`${hostId}::${selected}`}
            hostId={hostId}
            path={selected}
            connected={connected}
            dockGeneration={record?.generation ?? 0}
            forceReadOnly={selected === presentedSelection}
          />
        ) : null}
      </div>
    </div>
  );
}

function RemoteFileView({ hostId, path, connected, dockGeneration, forceReadOnly = false }: { hostId: string; path: string; connected: boolean; dockGeneration: number; forceReadOnly?: boolean }) {
  const t = useT();
  const [body, setBody] = useState("");
  const [draft, setDraft] = useState<string | null>(null);
  const [mtime, setMtime] = useState(0);
  const [binary, setBinary] = useState(false);
  const [truncated, setTruncated] = useState(false);
  const [saving, setSaving] = useState(false);
  const [conflict, setConflict] = useState(false);
  const [err, setErr] = useState("");
  const operation = useRef(0);
  // This view's identity: a receipt is only applied while the same dock, host
  // and path it was issued for are still the ones on screen.
  const identity = `${dockGeneration}\u0000${hostId}\u0000${path}`;
  const identityRef = useRef(identity);
  identityRef.current = identity;
  useEffect(() => () => { operation.current++; }, []);

  // The remote read entry point is host-scoped: it revalidates connectivity on
  // the host the user authenticated, not the presented tool scope an entry was
  // opened with. A presented remote file is therefore read with host
  // credentials and shown read-only; parity with the local presented entry
  // points needs a bridge entry point this change does not add.
  const load = useCallback(async () => {
    const generation = ++operation.current;
    const at = identity;
    const current = () => operation.current === generation && identityRef.current === at;
    try {
      const p = await app.ReadRemoteFile(hostId, path);
      if (!current()) return;
      setBody(p.body);
      setDraft(null);
      setMtime(p.mtimeUnix);
      setBinary(p.binary);
      setTruncated(p.truncated);
      setErr(p.err ?? "");
    } catch (error) { if (current()) setErr(String(error)); }
  }, [hostId, identity, path]);

  useEffect(() => {
    void load();
  }, [load]);

  const editable = !forceReadOnly && connected && !binary && !truncated && !err;
  const dirty = draft !== null && draft !== body;

  const save = async (force: boolean) => {
    if (draft === null) return;
    const generation = ++operation.current;
    const submitted = draft;
    // The write itself is issued for the file captured here and completes
    // against it; navigating away only takes away the right to update this
    // editor and to report the receipt to it.
    const at = identity;
    const ownsReceipt = () => operation.current === generation && identityRef.current === at;
    setSaving(true);
    try {
      const res = await app.WriteRemoteFile(hostId, path, draft, force ? 0 : mtime);
      if (!ownsReceipt()) return;
      if (res.conflict) {
        setConflict(true);
        return;
      }
      setBody(submitted);
      setDraft(current => current === submitted ? null : current);
      setMtime(res.newMtimeUnix);
      setConflict(false);
    } catch (error) {
      if (ownsReceipt()) setErr(String(error));
    } finally {
      if (ownsReceipt()) setSaving(false);
    }
  };

  return (
    <div className="remote-file-view">
      <div className="remote-file-view__toolbar">
        <span className="remote-file-view__path">{path}</span>
        {err && <span className="remote-panel__error"><ErrorMessage error={err} /></span>}
        {binary && <span className="remote-panel__hint">{t("remote.editor.binaryBlocked")}</span>}
        {truncated && <span className="remote-panel__hint">{t("remote.editor.truncatedBlocked")}</span>}
        {editable && draft === null && (
          <button className="btn" onClick={() => setDraft(body)}>{t("remote.editor.edit")}</button>
        )}
        {draft !== null && (
          <button className="btn btn--primary" disabled={saving || !dirty || !connected} onClick={() => void save(false)}>
            {saving ? t("remote.editor.saving") : t("remote.editor.save")}
          </button>
        )}
        {draft !== null && !connected && <span className="remote-panel__hint">{t("remote.editor.readOnlyDisconnected")}</span>}
      </div>
      {draft === null ? (
        <CodeViewer value={body} readOnly />
      ) : (
        <textarea
          className="remote-file-view__editor"
          value={draft}
          spellCheck={false}
          onChange={(e) => setDraft(e.target.value)}
        />
      )}
      {conflict && (
        <div className="remote-file-view__conflict" role="alertdialog">
          <p><strong>{t("remote.editor.conflictTitle")}</strong></p>
          <p>{t("remote.editor.conflictBody")}</p>
          <button className="btn" onClick={() => void load()}>{t("remote.editor.reload")}</button>
          <button className="btn btn--danger" onClick={() => void save(true)}>{t("remote.editor.overwrite")}</button>
        </div>
      )}
    </div>
  );
}
