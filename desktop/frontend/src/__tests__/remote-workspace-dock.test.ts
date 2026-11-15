// Run: tsx src/__tests__/remote-workspace-dock.test.ts
// Source-contract test: a remote session tab must show the remote workspace
// in the ordinary Files dock slot, rooted at the directory the user picked.

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

console.log("\nRemote workspace dock contract");
const here = dirname(fileURLToPath(import.meta.url));
const read = (relative: string) => readFileSync(resolve(here, relative), "utf8");
const dockSource = read("../app-shell/WorkspaceDockRegion.tsx");
const buildersSource = read("../app-shell/dockRegionBuilders.ts");
const viewSource = read("../app-shell/AppRuntimeView.tsx");
const storeSource = read("../store/remote.ts");
const filesSource = read("../components/RemoteFilesView.tsx");
const panelSource = read("../components/RemotePanel.tsx");

// The dock's Files slot renders the remote workspace for a remote tab.
ok(
  /remote && tab\.type === "file"/.test(dockSource) &&
    /<RemoteWorkspaceFiles/.test(dockSource) &&
    /case "file"/.test(dockSource) === false,
  "the Files slot switches to the remote workspace without a stray case",
);
ok(
  /rootPath=\{remote\.workspace\}/.test(dockSource) && /hostId=\{remote\.hostId\}/.test(dockSource),
  "the remote files view is bound to the active remote host and workspace",
);
ok(
  /function RemoteWorkspaceFiles\(/.test(dockSource) &&
    /useFileNavigationRecord\(/.test(dockSource) &&
    /useRemoteStore\(/.test(dockSource),
  "the remote files surface owns its hooks in a component boundary",
);
ok(
  /case "browser"/.test(dockSource) && /default:/.test(dockSource) && /<WorkspacePanel/.test(dockSource),
  "local tabs keep the local workspace panel",
);

// The active remote workspace reaches the dock.
ok(
  /remoteWorkspace\?: \{ hostId: string; workspace: string \}/.test(dockSource) &&
    /remoteWorkspace\?: \{ hostId: string; workspace: string \}/.test(buildersSource),
  "the dock props carry the active remote workspace",
);
ok(
  /const remoteWorkspace = activeTab\?\.remote/.test(viewSource) &&
    /setActiveWorkspace\(remoteWorkspace \?\? null\)/.test(viewSource),
  "the active remote tab publishes its workspace to the shell",
);
ok(
  /activeWorkspace: \{ hostId: string; workspace: string \} \| null/.test(storeSource) &&
    /setActiveWorkspace: \(value: \{ hostId: string; workspace: string \} \| null\) => void/.test(storeSource),
  "the remote store owns the active workspace",
);

// The tree is rooted at the workspace, not at the remote home.
ok(
  /rootPath: string/.test(filesSource) && /void loadDir\(rootPath\)/.test(filesSource),
  "the remote file tree loads the given root path",
);
ok(
  /treeRoot = activeWorkspace && activeWorkspace\.hostId === hostId \? activeWorkspace\.workspace : "\."/.test(panelSource),
  "the remote panel roots its tree at the active workspace of that host",
);
ok(
  !/function RemoteFilesTab\(/.test(panelSource) && !/function RemoteFileView\(/.test(panelSource),
  "the remote panel no longer owns the file tree it shares with the dock",
);

process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
