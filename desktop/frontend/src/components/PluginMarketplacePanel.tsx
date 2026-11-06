import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { CSSProperties } from "react";
import { Download, RefreshCw, Search, Star, X } from "lucide-react";
import { app } from "../lib/bridge";
import { getLocale, useT, type Locale } from "../lib/i18n";
import { ErrorMessage } from "./ErrorMessage";
import { ModalCloseButton } from "./ModalCloseButton";
import type { PluginMarketplaceEntryView, PluginMarketplaceView } from "../generated/desktopContract.generated";
import {
  ALL_DOMAINS,
  filterMarketplaceEntries,
  marketplaceDomainOptions,
  type MarketplaceCategory,
} from "../lib/pluginMarketplaceFilters";

// PluginMarketplacePanel is the lazy marketplace surface inside
// Settings → Capabilities → Plugins. The index itself is fetched and cached by
// the desktop host; this component only filters the DTO and calls the existing
// plugin install endpoint.
type CategoryKey = MarketplaceCategory;

type Copy = {
  tabInstalled: string;
  tabMarketplace: string;
  search: string;
  refresh: string;
  sourceTitle: string;
  sourceHint: string;
  sourceSave: string;
  sourcePlaceholder: string;
  sourceLabel: string;
  configureTitle: string;
  configureHint: string;
  featured: string;
  empty: string;
  install: string;
  installed: string;
  installing: string;
  cached: string;
  detailTitle: string;
  author: string;
  version: string;
  category: string;
  permissions: string;
  noPermissions: string;
  sha: string;
  installFailed: string;
  installedNotice: string;
  noInstallSource: string;
  allDomains: string;
  domainEmpty: string;
  domains: Record<string, string>;
  categories: Record<CategoryKey, string>;
};

const COPY: Record<Locale, Copy> = {
  en: {
    tabInstalled: "Installed",
    tabMarketplace: "Marketplace",
    search: "Search plugins",
    refresh: "Refresh",
    sourceTitle: "Index source",
    sourceHint: "Local directory, file:// path, or an HTTPS marketplace.json URL.",
    sourceSave: "Save source",
    sourcePlaceholder: "./marketplace or https://example.com/.claude-plugin/marketplace.json",
    sourceLabel: "Source",
    configureTitle: "No marketplace index configured",
    configureHint: "Set an index source to browse plugins from a .claude-plugin/marketplace.json file.",
    featured: "Featured",
    empty: "No plugins match this filter.",
    install: "Install",
    installed: "Installed",
    installing: "Installing…",
    cached: "Cached index: the live source is unreachable. Install links still use their declared source.",
    detailTitle: "Plugin details",
    author: "Author",
    version: "Version",
    category: "Category",
    permissions: "Permissions",
    noPermissions: "Not declared by the index. Capabilities are parsed after install.",
    sha: "Pinned SHA",
    installFailed: "Install failed",
    installedNotice: "{name} installed.",
    noInstallSource: "This entry has no installable source.",
    allDomains: "All domains",
    domainEmpty: "No plugins in this domain yet.",
    domains: {
      "开发工具": "Developer tools",
      "開發工具": "Developer tools",
      "效率提升": "Productivity",
      "界面设计": "UI design",
      "介面設計": "UI design",
      "调研分析": "Research",
      "調研分析": "Research",
      "内容创作": "Content",
      "內容創作": "Content",
      "其他": "Other",
    },
    categories: {
      all: "All",
      recommended: "Recommended",
      dev: "Developer tools",
      productivity: "Productivity",
      design: "UI design",
      research: "Research",
      content: "Content",
      other: "Other",
    },
  },
  zh: {
    tabInstalled: "已安装",
    tabMarketplace: "插件市场",
    search: "搜索插件",
    refresh: "刷新",
    sourceTitle: "索引源",
    sourceHint: "支持本地目录、file:// 路径或 HTTPS 的 marketplace.json 地址。",
    sourceSave: "保存索引源",
    sourcePlaceholder: "./marketplace 或 https://example.com/.claude-plugin/marketplace.json",
    sourceLabel: "来源",
    configureTitle: "尚未配置插件市场索引",
    configureHint: "设置一个 .claude-plugin/marketplace.json 索引源后即可浏览插件。",
    featured: "精选",
    empty: "没有符合当前筛选的插件。",
    install: "安装",
    installed: "已安装",
    installing: "安装中…",
    cached: "缓存索引：当前索引源不可达，安装仍按索引声明的来源执行。",
    detailTitle: "插件详情",
    author: "作者",
    version: "版本",
    category: "分类",
    permissions: "权限摘要",
    noPermissions: "索引未声明；安装后解析实际能力清单。",
    sha: "锁定 SHA",
    installFailed: "安装失败",
    installedNotice: "已安装 {name}。",
    noInstallSource: "该条目没有可安装的来源。",
    allDomains: "全部领域",
    domainEmpty: "该领域下暂无插件。",
    domains: {
      "开发工具": "开发工具",
      "開發工具": "开发工具",
      "效率提升": "效率提升",
      "界面设计": "界面设计",
      "介面設計": "界面设计",
      "调研分析": "调研分析",
      "調研分析": "调研分析",
      "内容创作": "内容创作",
      "內容創作": "内容创作",
      "其他": "其他",
    },
    categories: {
      all: "全部",
      recommended: "推荐",
      dev: "开发工具",
      productivity: "效率提升",
      design: "界面设计",
      research: "调研分析",
      content: "内容创作",
      other: "其他",
    },
  },
  "zh-TW": {
    tabInstalled: "已安裝",
    tabMarketplace: "外掛市場",
    search: "搜尋外掛",
    refresh: "重新整理",
    sourceTitle: "索引來源",
    sourceHint: "支援本機目錄、file:// 路徑或 HTTPS 的 marketplace.json 位址。",
    sourceSave: "儲存索引來源",
    sourcePlaceholder: "./marketplace 或 https://example.com/.claude-plugin/marketplace.json",
    sourceLabel: "來源",
    configureTitle: "尚未設定外掛市場索引",
    configureHint: "設定一個 .claude-plugin/marketplace.json 索引來源後即可瀏覽外掛。",
    featured: "精選",
    empty: "沒有符合目前篩選的外掛。",
    install: "安裝",
    installed: "已安裝",
    installing: "安裝中…",
    cached: "快取索引：目前索引來源無法連線，安裝仍依索引宣告的來源執行。",
    detailTitle: "外掛詳情",
    author: "作者",
    version: "版本",
    category: "分類",
    permissions: "權限摘要",
    noPermissions: "索引未宣告；安裝後解析實際能力清單。",
    sha: "鎖定 SHA",
    installFailed: "安裝失敗",
    installedNotice: "已安裝 {name}。",
    noInstallSource: "此條目沒有可安裝的來源。",
    allDomains: "所有領域",
    domainEmpty: "此領域暫無外掛。",
    domains: {
      "开发工具": "開發工具",
      "開發工具": "開發工具",
      "效率提升": "效率提升",
      "界面设计": "介面設計",
      "介面設計": "介面設計",
      "调研分析": "調研分析",
      "調研分析": "調研分析",
      "内容创作": "內容創作",
      "內容創作": "內容創作",
      "其他": "其他",
    },
    categories: {
      all: "全部",
      recommended: "推薦",
      dev: "開發工具",
      productivity: "效率提升",
      design: "介面設計",
      research: "調研分析",
      content: "內容創作",
      other: "其他",
    },
  },
};

const CATEGORY_ORDER: CategoryKey[] = ["all", "recommended", "dev", "productivity", "design", "research", "content", "other"];

export function PluginMarketplacePanel({ onInstalled }: { onInstalled?: () => void }) {
  const t = useT();
  const copy = COPY[getLocale()] ?? COPY.en;
  const [view, setView] = useState<PluginMarketplaceView | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [category, setCategory] = useState<CategoryKey>("all");
  const [domain, setDomain] = useState<string>(ALL_DOMAINS);
  const [selected, setSelected] = useState<string | null>(null);
  const [sourceDraft, setSourceDraft] = useState("");
  const [sourceOpen, setSourceOpen] = useState(false);
  const detailRef = useRef<HTMLElement>(null);
  // Index reads can overlap (mount, Refresh, install, save). Only the newest
  // request may write state, so an older response cannot overwrite a fresher one.
  const requestSeq = useRef(0);
  // Once the user touches the source input it owns that value; a reload must
  // never refill text they cleared on purpose.
  const sourceEditedRef = useRef(false);

  // The detail drawer is a real modal: focus moves inside on open, Escape closes
  // it, Tab stays inside, and a pointer press elsewhere dismisses it.
  useEffect(() => {
    if (!selected) return;
    detailRef.current?.focus();
    const dismissOutside = (event: PointerEvent) => {
      const root = detailRef.current;
      if (root && !root.contains(event.target as Node)) setSelected(null);
    };
    document.addEventListener("pointerdown", dismissOutside, true);
    return () => document.removeEventListener("pointerdown", dismissOutside, true);
  }, [selected]);

  const reload = useCallback(async () => {
    const request = ++requestSeq.current;
    let failure: string | null = null;
    const [next, source] = await Promise.all([
      app.PluginMarketplace().catch((e) => {
        failure = String((e as Error)?.message ?? e);
        return null;
      }),
      app.PluginMarketplaceSource().catch(() => ({ source: "", kind: "" })),
    ]);
    if (request !== requestSeq.current) return;
    // A successful snapshot clears any older error banner; a failed one replaces
    // it, so a stale failure never outlives the read that produced it.
    setErr(failure);
    if (next) {
      setView(next);
      if (!next.configured) setSourceOpen(true);
    }
    setSourceDraft((current) => (!sourceEditedRef.current && current === "" ? source.source : current));
    if (!source.source) setSourceOpen(true);
  }, []);

  useEffect(() => { void reload(); }, [reload]);

  const entries = useMemo(
    () => filterMarketplaceEntries(view?.entries ?? [], { query, category, domain }),
    [view, query, category, domain],
  );

  // Domains come from the installed index, never from a hard-coded list. Chips
  // are ordered by entry count and long-tail single-entry domains are hidden.
  const domainOptions = useMemo(() => marketplaceDomainOptions(view?.entries ?? []), [view]);

  const featured = useMemo(() => {
    const list = view?.entries ?? [];
    const flagged = list.filter((entry) => entry.featured);
    return (flagged.length > 0 ? flagged : list).slice(0, 4);
  }, [view]);

  const selectedEntry = useMemo(
    () => view?.entries.find((entry) => entry.name === selected) ?? null,
    [view, selected],
  );

  const saveSource = async () => {
    setBusy(true);
    setErr(null);
    setNotice(null);
    try {
      await app.SetPluginMarketplaceSource(sourceDraft.trim());
      // The saved draft is now the stored value, so a later reload may resync it.
      sourceEditedRef.current = false;
      setSourceOpen(false);
      await reload();
    } catch (e) {
      setErr(String((e as Error)?.message ?? e));
    } finally {
      setBusy(false);
    }
  };

  const install = async (entry: PluginMarketplaceEntryView) => {
    if (!entry.installSource) {
      setErr(copy.noInstallSource);
      return;
    }
    setBusy(true);
    setErr(null);
    setNotice(null);
    try {
      await app.InstallPlugin(entry.installSource, { dryRun: false, link: false, replace: entry.installed });
      setNotice(copy.installedNotice.replace("{name}", entry.name));
      await reload();
      onInstalled?.();
    } catch (e) {
      setErr(`${copy.installFailed}: ${String((e as Error)?.message ?? e)}`);
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="mem-section" aria-label={copy.tabMarketplace}>
      {err && <div className="banner banner--error"><ErrorMessage error={err} /></div>}
      {notice && !err && <div className="banner banner--success">{notice}</div>}
      {view?.offline && <div className="banner banner--warning">{copy.cached}</div>}
      {view?.warning && <div className="banner banner--warning">{view.warning}</div>}

      <div className="settings-toolbar">
        <div style={{ display: "flex", alignItems: "center", gap: 8, flex: 1, minWidth: 0 }}>
          <div style={{ position: "relative", flex: 1, minWidth: 0 }}>
            <Search size={14} aria-hidden style={{ position: "absolute", left: 10, top: "50%", transform: "translateY(-50%)", color: "var(--fg-faint)" }} />
            <input
              className="mem-input"
              style={{ paddingLeft: 30, width: "100%" }}
              aria-label={copy.search}
              placeholder={copy.search}
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
          </div>
          <button className="btn btn--small" type="button" disabled={busy} onClick={() => void reload()}>
            <RefreshCw size={14} aria-hidden /> {copy.refresh}
          </button>
          <button className="btn btn--small" type="button" aria-expanded={sourceOpen} onClick={() => setSourceOpen((open) => !open)}>
            {copy.sourceTitle}
          </button>
        </div>
      </div>

      {sourceOpen && (
        <div style={{ display: "flex", flexDirection: "column", gap: 6, padding: "8px 0 12px" }}>
          <div style={{ fontSize: 12, color: "var(--fg-dim)" }}>{copy.sourceHint}</div>
          <div style={{ display: "flex", gap: 8 }}>
            <input
              className="mem-input"
              style={{ flex: 1, minWidth: 0 }}
              aria-label={copy.sourceTitle}
              placeholder={copy.sourcePlaceholder}
              value={sourceDraft}
              onChange={(e) => { sourceEditedRef.current = true; setSourceDraft(e.target.value); }}
            />
            <button className="btn btn--primary btn--small" type="button" disabled={busy} onClick={() => void saveSource()}>
              {copy.sourceSave}
            </button>
          </div>
          {view?.source && (
            <div style={{ fontSize: 12, color: "var(--fg-faint)" }}>{copy.sourceLabel}: {view.source}{view.sourceKind ? ` · ${view.sourceKind}` : ""}</div>
          )}
        </div>
      )}

      {!view && <div className="mem-empty">{t("caps.loading")}</div>}
      {view && !view.configured && (
        <div className="mem-empty mem-empty--cta">
          <strong>{copy.configureTitle}</strong>
          <span>{copy.configureHint}</span>
        </div>
      )}

      {view?.configured && (
        <>
          <div className="cap-tabs" role="tablist" aria-label={copy.tabMarketplace}>
            {CATEGORY_ORDER.map((key) => (
              <button
                key={key}
                className={`cap-tab${category === key ? " cap-tab--active" : ""}`}
                role="tab"
                aria-selected={category === key}
                type="button"
                onClick={() => setCategory(key)}
              >
                {copy.categories[key]}
              </button>
            ))}
          </div>

          {domainOptions.length > 0 && (
            <div className="cap-tabs" role="tablist" aria-label={copy.allDomains} style={{ marginTop: 8 }}>
              <button
                className={`cap-tab${domain === ALL_DOMAINS ? " cap-tab--active" : ""}`}
                role="tab"
                aria-selected={domain === ALL_DOMAINS}
                type="button"
                onClick={() => setDomain(ALL_DOMAINS)}
              >
                {copy.allDomains}
              </button>
              {domainOptions.map((option) => (
                <button
                  key={option.domain}
                  className={`cap-tab${domain === option.domain ? " cap-tab--active" : ""}`}
                  role="tab"
                  aria-selected={domain === option.domain}
                  type="button"
                  onClick={() => setDomain(option.domain)}
                >
                  {copy.domains[option.domain] ?? option.domain}
                </button>
              ))}
            </div>
          )}

          {featured.length > 0 && category === "all" && domain === ALL_DOMAINS && query.trim() === "" && (
            <div style={{ margin: "12px 0" }}>
              <div style={{ display: "flex", alignItems: "center", gap: 6, marginBottom: 8, fontWeight: 600 }}>
                <Star size={14} aria-hidden /> {copy.featured}
              </div>
              <div style={{ display: "flex", gap: 10, overflowX: "auto", paddingBottom: 4 }}>
                {featured.map((entry) => (
                  <button
                    key={entry.name}
                    type="button"
                    className="cap-market-featured"
                    style={cardStyle(true)}
                    onClick={() => setSelected(entry.name)}
                  >
                    <PluginIcon entry={entry} size={34} />
                    <span style={{ minWidth: 0, textAlign: "left" }}>
                      <span style={{ display: "block", fontWeight: 600, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{entry.name}</span>
                      <span style={{ display: "block", fontSize: 12, color: "var(--fg-dim)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{entry.description || copy.categories[entry.category as CategoryKey] || copy.categories.other}</span>
                    </span>
                  </button>
                ))}
              </div>
            </div>
          )}

          {entries.length === 0 ? (
            <div className="mem-empty">{domain === ALL_DOMAINS ? copy.empty : copy.domainEmpty}</div>
          ) : (
            <div style={{ display: "grid", gridTemplateColumns: "repeat(2, minmax(0, 1fr))", gap: 10 }}>
              {entries.map((entry) => (
                <div
                  key={entry.name}
                  role="button"
                  tabIndex={0}
                  style={cardStyle(false)}
                  onClick={() => setSelected(entry.name)}
                  onKeyDown={(e) => {
                    if (e.target !== e.currentTarget) return;
                    if (e.key === "Enter" || e.key === " ") { e.preventDefault(); setSelected(entry.name); }
                  }}
                >
                  <div style={{ display: "flex", gap: 10, alignItems: "flex-start", minWidth: 0 }}>
                    <PluginIcon entry={entry} size={38} />
                    <div style={{ minWidth: 0, flex: 1 }}>
                      <div style={{ fontWeight: 600, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{entry.name}</div>
                      <div style={{ fontSize: 12, color: "var(--fg-dim)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{entry.description || copy.categories[entry.category as CategoryKey] || copy.categories.other}</div>
                    </div>
                  </div>
                  <div style={{ display: "flex", alignItems: "center", justifyContent: "space-between", gap: 8, marginTop: 10 }}>
                    <span style={{ fontSize: 11, color: "var(--fg-faint)", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
                      {entry.author || copy.categories[entry.category as CategoryKey] || copy.categories.other}{entry.version ? ` · v${entry.version}` : ""}
                    </span>
                    <button
                      className="btn btn--small"
                      type="button"
                      disabled={busy || entry.installed || !entry.installSource}
                      onClick={(e) => { e.stopPropagation(); void install(entry); }}
                    >
                      {entry.installed ? copy.installed : <><Download size={13} aria-hidden /> {copy.install}</>}
                    </button>
                  </div>
                </div>
              ))}
            </div>
          )}
        </>
      )}

      {selectedEntry && (
        <aside
          ref={detailRef}
          role="dialog"
          aria-modal="true"
          aria-label={copy.detailTitle}
          tabIndex={-1}
          onKeyDown={(event) => {
            if (event.key === "Escape") {
              event.stopPropagation();
              setSelected(null);
            }
            if (event.key === "Tab") {
              const focusables = Array.from(detailRef.current?.querySelectorAll<HTMLElement>("button:not(:disabled), [href], [tabindex='0']") ?? []);
              const first = focusables[0];
              const last = focusables[focusables.length - 1];
              if (event.shiftKey && (document.activeElement === first || document.activeElement === detailRef.current)) {
                event.preventDefault();
                last?.focus();
              } else if (!event.shiftKey && document.activeElement === last) {
                event.preventDefault();
                first?.focus();
              }
            }
          }}
          style={{
            position: "fixed",
            top: 0,
            right: 0,
            bottom: 0,
            width: 360,
            maxWidth: "92vw",
            background: "var(--bg-elev)",
            borderLeft: "1px solid var(--border)",
            boxShadow: "var(--shadow-2)",
            padding: 20,
            overflowY: "auto",
            zIndex: "var(--z-popover)",
            display: "flex",
            flexDirection: "column",
            gap: 12,
          }}
        >
          <div style={{ display: "flex", alignItems: "flex-start", justifyContent: "space-between", gap: 10 }}>
            <div style={{ display: "flex", gap: 12, minWidth: 0 }}>
              <PluginIcon entry={selectedEntry} size={44} />
              <div style={{ minWidth: 0 }}>
                <div style={{ fontWeight: 700, fontSize: 15 }}>{selectedEntry.name}</div>
                <div style={{ fontSize: 12, color: "var(--fg-dim)" }}>{selectedEntry.description}</div>
              </div>
            </div>
            <ModalCloseButton label={t("common.close")} onClick={() => setSelected(null)} />
          </div>

          <DetailRow label={copy.author} value={selectedEntry.author} />
          <DetailRow label={copy.version} value={selectedEntry.version ? `v${selectedEntry.version}` : ""} />
          <DetailRow label={copy.category} value={copy.categories[selectedEntry.category as CategoryKey] ?? copy.categories.other} />
          <DetailRow label={copy.sourceLabel} value={selectedEntry.source} />
          {selectedEntry.sha && <DetailRow label={copy.sha} value={selectedEntry.sha} mono />}
          <div>
            <div style={{ fontSize: 12, color: "var(--fg-faint)", marginBottom: 4 }}>{copy.permissions}</div>
            {selectedEntry.permissions && selectedEntry.permissions.length > 0 ? (
              <div style={{ display: "flex", flexWrap: "wrap", gap: 6 }}>
                {selectedEntry.permissions.map((permission) => (
                  <span className="cap-source-badge" key={permission}>{permission}</span>
                ))}
              </div>
            ) : (
              <div style={{ fontSize: 12, color: "var(--fg-dim)" }}>{copy.noPermissions}</div>
            )}
          </div>

          {selectedEntry.installed && (
            <div className="banner banner--success">
              {copy.installed}{selectedEntry.installedVersion ? ` · v${selectedEntry.installedVersion}` : ""}
            </div>
          )}

          <div style={{ display: "flex", gap: 8 }}>
            <button
              className="btn btn--primary"
              type="button"
              disabled={busy || selectedEntry.installed || !selectedEntry.installSource}
              onClick={() => void install(selectedEntry)}
            >
              <Download size={14} aria-hidden /> {busy ? copy.installing : selectedEntry.installed ? copy.installed : copy.install}
            </button>
            <button className="btn" type="button" onClick={() => setSelected(null)}>
              <X size={14} aria-hidden /> {t("common.close")}
            </button>
          </div>
        </aside>
      )}
    </section>
  );
}

function cardStyle(compact: boolean): CSSProperties {
  return {
    display: "flex",
    flexDirection: "column",
    minWidth: compact ? 190 : 0,
    maxWidth: compact ? 240 : "none",
    padding: 12,
    border: "1px solid var(--border)",
    borderRadius: 10,
    background: "var(--bg-soft)",
    cursor: "pointer",
    textAlign: "left",
    color: "inherit",
    font: "inherit",
  };
}

function PluginIcon({ entry, size }: { entry: PluginMarketplaceEntryView; size: number }) {
  const [failed, setFailed] = useState(false);
  const box: CSSProperties = {
    width: size,
    height: size,
    flex: "0 0 auto",
    borderRadius: 8,
    background: "var(--bg-elev-2)",
    display: "flex",
    alignItems: "center",
    justifyContent: "center",
    overflow: "hidden",
    fontWeight: 700,
    color: "var(--fg-dim)",
  };
  if (entry.icon && !failed) {
    return <span style={box}><img src={entry.icon} alt="" width={size} height={size} style={{ objectFit: "cover" }} onError={() => setFailed(true)} /></span>;
  }
  return <span style={box} aria-hidden>{entry.name.slice(0, 1).toUpperCase()}</span>;
}

function DetailRow({ label, value, mono }: { label: string; value?: string; mono?: boolean }) {
  if (!value) return null;
  return (
    <div style={{ display: "flex", gap: 12, fontSize: 12 }}>
      <span style={{ width: 84, flex: "0 0 auto", color: "var(--fg-faint)" }}>{label}</span>
      <span style={{ minWidth: 0, wordBreak: "break-all", fontFamily: mono ? "ui-monospace, SFMono-Regular, monospace" : undefined }}>{value}</span>
    </div>
  );
}

export default PluginMarketplacePanel;
