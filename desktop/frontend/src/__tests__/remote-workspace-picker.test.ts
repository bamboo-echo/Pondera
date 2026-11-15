// Run: tsx src/__tests__/remote-workspace-picker.test.ts
// Source-contract test: a remote SSH directory must be selectable from the
// main workbench and opened as an in-app tab, never as a separate window.

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

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

console.log("\nRemote workspace picker contract");
const here = dirname(fileURLToPath(import.meta.url));
const read = (relative: string) => readFileSync(resolve(here, relative), "utf8");
const pickerSource = read("../components/RemoteWorkspacePicker.tsx");
const hookSource = read("../components/useProjectCreation.tsx");
const treeSource = read("../components/ProjectTree.tsx");
const addControlsSource = read("../components/ProjectTreeAddControls.tsx");
const composerSource = read("../components/ComposerWorkspaceContextBar.tsx");
const statusSource = read("../app-runtime/useRemoteWorkspaceCommands.ts");
const locales = ["en", "zh", "zh-TW"].map((name) => read(`../locales/${name}.ts`));

// The picker itself: pick a host, walk the remote tree over SFTP, confirm.
ok(
  /app\.RemoteHosts\(\)/.test(pickerSource) && /app\.ListRemoteDir\(/.test(pickerSource),
  "the picker lists configured hosts and browses the remote tree over SFTP",
);
ok(
  /app\.AddRemoteProject\(/.test(pickerSource) &&
    /navigateRemote\(\{ hostId, workspace: canonical \}, \{ newSession: true \}\)/.test(pickerSource),
  "the confirmed directory becomes a remote project and opens an in-app session",
);
ok(
  /app\.ConnectRemoteHost\(/.test(pickerSource) && /waitForRemoteConnection\(/.test(pickerSource),
  "the picker connects the host before browsing it",
);
ok(
  /app\.RemoveRemoteProject\(/.test(pickerSource),
  "a failed open removes the project it just registered",
);

// The entry points live in the main workbench, not in a separate window.
ok(
  /onRemoteWorkspace: \(\) => \{ closeMenu\(\); openRemoteWorkspaceFlow\(\); \}/.test(treeSource) &&
    /key: "remote-workspace"/.test(addControlsSource),
  "the workbench + menu offers the remote directory item",
);
ok(
  /ProjectTreeRemoteAction label=\{t\("projectTree\.remoteWorkspace"\)\}[\s\S]*?openRemoteWorkspaceFlow/.test(treeSource),
  "the empty project tree offers the remote directory action",
);
ok(
  /creation\.openRemoteWorkspaceFlow\(\)/.test(composerSource),
  "the composer workspace menu offers the remote directory action",
);
ok(
  /lazy\(\(\) => import\("\.\/RemoteWorkspacePicker"\)/.test(hookSource) &&
    /remoteWorkspaceFlow: remoteWorkspaceOpen/.test(hookSource),
  "the picker mounts lazily through useProjectCreation",
);

// The status-bar remote entry opens the same in-app tab; it must not spawn a
// native window running the remote Serve web client.
ok(
  /input\.openRemoteProject\(\{ hostId: host\.id, workspace \}, \{ newSession: true \}\)/.test(statusSource),
  "the status bar remote entry opens the workspace as an in-app tab",
);
ok(
  !/app\.OpenRemoteWorkspace\(/.test(statusSource),
  "the status bar remote entry no longer opens a separate native window",
);

// Localized labels exist in every supported locale.
for (const [index, source] of locales.entries()) {
  ok(/"projectTree\.remoteWorkspace"/.test(source), `projectTree.remoteWorkspace present in locale #${index + 1}`);
  ok(/"remotePicker\.title"/.test(source) && /"remotePicker\.openHere"/.test(source), `remotePicker labels present in locale #${index + 1}`);
}

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
