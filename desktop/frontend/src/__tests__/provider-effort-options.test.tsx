import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";
import React, { act } from "react";

const testDir = dirname(fileURLToPath(import.meta.url));
const dom = new JSDOM("<div id='root'></div>", { url: "http://localhost/", pretendToBeVisual: true });
Object.assign(globalThis, { window: dom.window, document: dom.window.document, Node: dom.window.Node, HTMLElement: dom.window.HTMLElement, Event: dom.window.Event, localStorage: dom.window.localStorage, IS_REACT_ACT_ENVIRONMENT: true });
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
window.HTMLDialogElement.prototype.showModal = function() { this.setAttribute("open", ""); };
const { createRoot } = await import("react-dom/client");
const { LocaleProvider } = await import("../lib/i18n");
const { providerEffortOptionsForView, STANDARD_EFFORT_LEVELS } = await import("../lib/providerEffortOptions");
const { default: ProviderModelDialog } = await import("../components/ProviderModelDialog");

// --- providerEffortOptionsForView ---

const capability = (state?: string, ids?: string[]) => ({
  reasoning: { state, options: (ids ?? []).map(id => ({ id, name: id })) },
});

const declaredUnion = providerEffortOptionsForView({
  capability: capability("supported", ["low", "high"]),
  override: { supportedEfforts: ["high", "max"] },
});
assert.deepEqual(declaredUnion, { options: ["low", "high", "max"], declared: true },
  "capability and override levels union into the declared list");

const providerLevels = providerEffortOptionsForView({
  capability: capability("unknown", []),
  override: null,
  providerSupportedEfforts: ["disabled", "high"],
});
assert.deepEqual(providerLevels, { options: ["disabled", "high"], declared: true },
  "without an override, provider-level supported_efforts is the declared list");

const standardFallback = providerEffortOptionsForView({ capability: capability("unknown", []), override: null, providerSupportedEfforts: [] });
assert.deepEqual(standardFallback, { options: [...STANDARD_EFFORT_LEVELS], declared: false },
  "an undeclared connection falls back to the standard vocabulary");
assert.deepEqual(STANDARD_EFFORT_LEVELS, ["none", "low", "medium", "high", "xhigh", "max"]);

const noCapability = providerEffortOptionsForView({ override: null, providerSupportedEfforts: [] });
assert.deepEqual(noCapability.options, [...STANDARD_EFFORT_LEVELS], "a missing capability still offers the standard levels");

const unsupported = providerEffortOptionsForView({ capability: capability("unsupported", []), override: null, providerSupportedEfforts: [] });
assert.deepEqual(unsupported, { options: [], declared: false }, "an explicitly reasoning-less connection offers no levels");

// --- ProviderModelDialog renders the fallback with an explanation ---

const el = document.getElementById("root")!;
const root = createRoot(el);
const render = async (node: React.ReactNode) => { await act(async () => { root.render(<LocaleProvider>{node}</LocaleProvider>); }); };

await render(<ProviderModelDialog key="fallback" candidates={[]} busy={false}
  effortOptions={[...STANDARD_EFFORT_LEVELS]} effortOptionsDeclared={false}
  onClose={() => {}} onApply={() => {}} />);
let dialog = document.querySelector("dialog")!;
const numberInputs = dialog.querySelectorAll<HTMLInputElement>(".provider-model-dialog input[type='number']");
assert.equal(numberInputs[0]?.value, "1000000", "a new model defaults to a 1M context window");
assert.equal(numberInputs[1]?.value, "100000", "a new model defaults to a 100K output limit");
assert.equal(dialog.querySelectorAll(".provider-model-dialog__effort-card input[type='checkbox']").length, 6,
  "the standard vocabulary shows six level chips");
assert.ok(dialog.textContent?.includes("This connection has not declared its own levels"),
  "the fallback explains that the connection did not declare levels");
assert.equal([...dialog.querySelectorAll("select option")].at(-1)?.value, "max",
  "the default selector offers the standard levels");

await render(<ProviderModelDialog key="declared" candidates={[]} busy={false}
  effortOptions={["low", "high"]} effortOptionsDeclared={true}
  onClose={() => {}} onApply={() => {}} />);
dialog = document.querySelector("dialog")!;
assert.equal(dialog.querySelectorAll(".provider-model-dialog__effort-card input[type='checkbox']").length, 2);
assert.ok(!dialog.textContent?.includes("This connection has not declared its own levels"),
  "declared connections keep the dialog silent about the fallback");

// Untouched fallback chips never freeze the standard levels into the override.
let applied: unknown;
await render(<ProviderModelDialog key="apply" candidates={[]} busy={false}
  effortOptions={[...STANDARD_EFFORT_LEVELS]} effortOptionsDeclared={false}
  onClose={() => {}} onApply={(draft) => { applied = draft; }} />);
dialog = document.querySelector("dialog")!;
const modelInput = dialog.querySelector(".provider-model-dialog__id input") as HTMLInputElement;
await act(async () => {
  Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, "value")!.set!.call(modelInput, "gpt-5.1");
  modelInput.dispatchEvent(new window.Event("input", { bubbles: true }));
});
await act(async () => { dialog.querySelector("form")!.dispatchEvent(new window.Event("submit", { bubbles: true, cancelable: true })); });
assert.deepEqual((applied as { supportedEfforts: string[] }).supportedEfforts, [],
  "leaving the fallback chips untouched keeps inheriting the backend defaults");
assert.equal((applied as { contextWindow: string }).contextWindow, "1000000",
  "the default context window is submitted with the new model");
assert.equal((applied as { maxOutputTokens: number }).maxOutputTokens, 100000,
  "the default output limit is submitted with the new model");

// --- The settings panel wires the fallback into the dialog props ---

const settingsSource = readFileSync(resolve(testDir, "../components/SettingsPanel.tsx"), "utf8");
assert.ok(/effortOptions=\{modelDialogEffort\.options\}/.test(settingsSource) &&
  /effortOptionsDeclared=\{modelDialogEffort\.declared\}/.test(settingsSource),
  "the model dialog consumes the resolved effort options");

await act(async () => { root.unmount(); });
console.log("provider effort options: declared lists win, standard fallback appears with a hint, untouched chips stay implicit");
