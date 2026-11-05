// Run: node --import ./scripts/svg-stub-register.mjs --import tsx src/__tests__/plugin-marketplace-domain.test.tsx
// Domain chips are aggregated from the DTO. Category + domain + search compose
// with AND semantics, and the marketplace must still refresh after an install.

import { JSDOM } from "jsdom";
import React, { act } from "react";
import { createRoot } from "react-dom/client";

import { PluginMarketplacePanel } from "../components/PluginMarketplacePanel";
import type { PluginMarketplaceEntryView, PluginMarketplaceView } from "../generated/desktopContract.generated";
import { LocaleProvider } from "../lib/i18n";
import {
  ALL_DOMAINS,
  filterMarketplaceEntries,
  marketplaceDomainOptions,
} from "../lib/pluginMarketplaceFilters";
import { flushPromises, waitFor } from "../test-support/settingsTestFixtures";
import { installDesktopHostStub } from "./desktopHostStub";

let passed = 0;
let failed = 0;

function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

function eq(actual: unknown, expected: unknown, label: string) {
  ok(actual === expected, `${label}: expected ${JSON.stringify(expected)}, got ${JSON.stringify(actual)}`);
}

function entry(overrides: Partial<PluginMarketplaceEntryView> & Pick<PluginMarketplaceEntryView, "name">): PluginMarketplaceEntryView {
  return {
    category: "dev",
    installed: false,
    installSource: `https://example.com/${overrides.name}.git#0123456789abcdef0123456789abcdef01234567`,
    ...overrides,
  };
}

const ENTRIES: PluginMarketplaceEntryView[] = [
  entry({ name: "cad-widget", description: "parametric widget", category: "dev", domain: "CAD" }),
  entry({ name: "cad-batch", description: "batch export", category: "dev", domain: "CAD" }),
  entry({ name: "blender-shade", description: "shader nodes", category: "design", domain: "Blender", featured: true }),
  entry({ name: "blender-rig", description: "rig helper", category: "design", domain: "Blender" }),
  entry({ name: "blender-sim", description: "simulation", category: "design", domain: "Blender" }),
  entry({ name: "legacy-tool", description: "untagged legacy entry", category: "dev" }),
  entry({ name: "single-domain", description: "lonely domain", category: "content", domain: "Single" }),
];

const VIEW: PluginMarketplaceView = {
  configured: true,
  source: "file:///marketplace/.claude-plugin/marketplace.json",
  sourceKind: "local",
  entries: ENTRIES,
  cached: false,
  offline: false,
};

console.log("\nplugin marketplace domain filters");

const options = marketplaceDomainOptions(ENTRIES);
eq(options.map((option) => `${option.domain}:${option.count}`).join(","), "Blender:3,CAD:2", "domain chips sort by count and hide single-entry domains");
ok(!options.some((option) => option.domain === "Single"), "a one-entry domain never becomes a chip");

const all = filterMarketplaceEntries(ENTRIES, { query: "", category: "all", domain: ALL_DOMAINS });
eq(all.length, ENTRIES.length, "the default filter keeps every entry");
const cad = filterMarketplaceEntries(ENTRIES, { query: "", category: "all", domain: "CAD" });
eq(cad.map((item) => item.name).join(","), "cad-widget,cad-batch", "a domain chip narrows the list");
const devCad = filterMarketplaceEntries(ENTRIES, { query: "", category: "dev", domain: "CAD" });
eq(devCad.length, 2, "category and domain compose with AND");
const devCadQuery = filterMarketplaceEntries(ENTRIES, { query: "parametric", category: "dev", domain: "CAD" });
eq(devCadQuery.map((item) => item.name).join(","), "cad-widget", "search narrows category + domain");
const designCad = filterMarketplaceEntries(ENTRIES, { query: "", category: "design", domain: "CAD" });
eq(designCad.length, 0, "an impossible category + domain intersection is empty");
const untagged = filterMarketplaceEntries(ENTRIES, { query: "", category: "all", domain: ALL_DOMAINS });
ok(untagged.some((item) => item.name === "legacy-tool"), "entries without a domain still render in the unfiltered list");
const searchDomain = filterMarketplaceEntries(ENTRIES, { query: "blender", category: "all", domain: ALL_DOMAINS });
eq(searchDomain.length, 3, "search also matches the domain label");

const dom = new JSDOM("<!doctype html><html><body></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.Node = dom.window.Node;
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Event = dom.window.Event;
globalThis.localStorage = dom.window.localStorage;
window.matchMedia = (() => ({ matches: true, addEventListener() {}, removeEventListener() {} })) as any;
globalThis.requestAnimationFrame = dom.window.requestAnimationFrame.bind(dom.window);
globalThis.cancelAnimationFrame = dom.window.cancelAnimationFrame.bind(dom.window);
localStorage.clear();

let marketCalls = 0;
let installCalls = 0;
const stub = installDesktopHostStub({
  PluginMarketplace: async () => {
    marketCalls += 1;
    return VIEW;
  },
  PluginMarketplaceSource: async () => ({ source: VIEW.source ?? "", kind: "local" }),
  InstallPlugin: async () => {
    installCalls += 1;
  },
});

const host = document.createElement("div");
document.body.appendChild(host);
const root = createRoot(host);

await act(async () => {
  root.render(
    <LocaleProvider>
      <PluginMarketplacePanel />
    </LocaleProvider>,
  );
  await flushPromises();
});
await waitFor("marketplace initial load", () => marketCalls === 1 && host.textContent?.includes("cad-widget") === true);

const domainTabs = Array.from(host.querySelectorAll('[role="tablist"]'))[1]?.querySelectorAll('[role="tab"]') ?? [];
const domainLabels = Array.from(domainTabs).map((tab) => tab.textContent?.trim());
eq(domainLabels.join("|"), "All domains|Blender|CAD", "the domain row renders the aggregated chips in count order");

await act(async () => {
  (Array.from(domainTabs).find((tab) => tab.textContent?.trim() === "CAD") as HTMLButtonElement).click();
  await flushPromises();
});
ok(host.textContent?.includes("cad-widget") === true && host.textContent?.includes("cad-batch") === true, "selecting CAD keeps both CAD entries");
ok(!host.textContent?.includes("blender-shade"), "selecting CAD hides other domains");

await act(async () => {
  const designTab = Array.from(host.querySelectorAll('[role="tablist"]'))[0]?.querySelectorAll('[role="tab"]') ?? [];
  (Array.from(designTab).find((tab) => tab.textContent?.trim() === "UI design") as HTMLButtonElement).click();
  await flushPromises();
});
ok(host.textContent?.includes("No plugins in this domain yet.") === true, "an empty category + domain intersection explains itself");
ok(!host.textContent?.includes("No plugins match this filter."), "the domain empty state differs from the general empty state");

await act(async () => {
  (Array.from(host.querySelectorAll('[role="tablist"]'))[1]?.querySelectorAll('[role="tab"]') ?? [])
    .forEach((tab) => { if (tab.textContent?.trim() === "All domains") (tab as HTMLButtonElement).click(); });
  (Array.from(host.querySelectorAll('[role="tablist"]'))[0]?.querySelectorAll('[role="tab"]') ?? [])
    .forEach((tab) => { if (tab.textContent?.trim() === "All") (tab as HTMLButtonElement).click(); });
  await flushPromises();
});
eq(marketCalls, 1, "chip filtering does not refetch the index");

const installButton = Array.from(host.querySelectorAll("button")).find((button) => button.textContent?.includes("Install")) as HTMLButtonElement | undefined;
if (!installButton) throw new Error("install action did not render");
await act(async () => {
  installButton.click();
  await flushPromises();
});
await waitFor("install refresh", () => marketCalls === 2 && installCalls === 1);
eq(installCalls, 1, "install calls the existing InstallPlugin endpoint once");
eq(marketCalls, 2, "install refreshes the marketplace through the existing PluginMarketplace call");

await act(async () => {
  root.unmount();
});
stub.uninstall();
dom.window.close();

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
