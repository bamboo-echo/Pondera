<p align="center">
  <img src="docs/logo-ghost-wave-effect.svg" alt="Pondera" width="360"/>
</p>

<p align="center">
  <strong>English</strong>
  &nbsp;·&nbsp;
  <a href="./README.zh-CN.md">简体中文</a>
  &nbsp;·&nbsp;
  <a href="./docs/GUIDE.md">Guide</a>
  &nbsp;·&nbsp;
  <a href="./docs/ACP.md">ACP</a>
  &nbsp;·&nbsp;
  <a href="./docs/EXTENSIONS.md">Extensions</a>
  &nbsp;·&nbsp;
  <a href="./docs/SPEC.md">Spec</a>
  &nbsp;·&nbsp;
  <a href="https://github.com/bamboo-echo/Pondera/releases">Releases</a>
</p>

<p align="center">
  <a href="./LICENSE"><img src="https://img.shields.io/badge/license-MIT-8b949e.svg?style=flat-square&labelColor=161b22" alt="license"/></a>
  <a href="https://github.com/bamboo-echo/Pondera/releases"><img src="https://img.shields.io/badge/platform-Windows%20x64-0078D4.svg?style=flat-square&labelColor=161b22&logo=windows&logoColor=white" alt="platform"/></a>
</p>

<br/>

<p align="center"><strong>Open source · MIT · a single Go binary</strong></p>
<h3 align="center">A coding agent you can leave running.</h3>
<p align="center">One local engine, four ways in — terminal, desktop app, browser, or your editor over ACP. Plan mode, permissions, a workspace sandbox and per-turn checkpoints keep a long autonomous run something you can still read and undo.</p>

<br/>

> [!NOTE]
> **About this repository** — Pondera is a personal Windows desktop fork.
> It keeps its own data home (`%APPDATA%\pondera`), so it can run side by side
> with any other build. The installer and portable build are produced by
> `scripts/desktop-build.sh windows/amd64 <version> mod`, and the desktop
> artifact is `Pondera.exe`.

<br/>

## Features

- **Config-driven.** Providers, the agent, enabled tools, and plugins are all
  declared in `pondera.toml`. No hardcoded models.
- **Multi-model & composable.** DeepSeek ships as a preset; any
  OpenAI-compatible endpoint is a config entry, not new code. Optionally run
  two models together (executor + planner) in separate, cache-stable sessions.
- **Plugin-driven.** MCP servers contribute tools, prompts, and resources;
  Extension Protocol v1 sidecars can also intercept runtime events, contribute
  Providers and structured UI, and ship versioned plugin packages.
- **Cache-aware context maintenance.** Startup injects a small stable environment
  summary, stale tool output is snipped/pruned before summary compaction, and the
  built-in tool schema contract is documented for regression review.
- **Zero-friction distribution.** `CGO_ENABLED=0` single binary; cross-compile
  to six targets with one command. The result is a fully self-contained static
  binary — nothing to install on the target machine beyond the binary itself.

## Install

### Windows installer

Download the latest `Pondera-windows-amd64-installer.exe` (or the portable
`.zip`) from [GitHub Releases](https://github.com/bamboo-echo/Pondera/releases),
install, and configure a provider and model in the app. User data lives in
`%APPDATA%\pondera` and survives upgrades; the installer only touches its own
files.

### Build from source

Clone the repository:

```sh
git clone https://github.com/bamboo-echo/Pondera.git
cd Pondera
```

Requirements: **Go 1.27+**, **Node 24+**, **pnpm 11**
(`npm install -g pnpm@11`), and NSIS 3 for the
installer. The module pins a `toolchain` directive; keep `GOTOOLCHAIN=auto`
so Go can fetch the pinned 1.27 toolchain.

```sh
# CLI / TUI only -> bin/pondera.exe
make build

# Full Windows desktop build -> dist/Pondera-windows-amd64-installer.exe
scripts/desktop-build.sh windows/amd64 v0.1.0 mod

# Build + silent reinstall + smoke test + launch
powershell -ExecutionPolicy Bypass -File scripts/fork-windows-build.ps1
```

The full fork workflow (build, install, data preservation, troubleshooting) is
documented in [docs/FORK_DEV_WORKFLOW.zh-CN.md](./docs/FORK_DEV_WORKFLOW.zh-CN.md).

## Quick start

Configure a provider and model on first launch, then start a session.

### Desktop app

Launch `Pondera.exe` and add a provider (API base URL, key, and models) in
Settings. The model list can be fetched from the provider, or entered by hand.

### CLI / TUI

The same engine is available as a terminal binary:

```sh
pondera setup                      # configure a provider and model
pondera                            # start an interactive session
pondera run "implement the TODOs in main.go"
```

In an interactive session, run `/init` when you want Pondera to create project
instructions.

For advanced CLI usage and configuration, see the **[CLI reference](./docs/CLI.md)**,
**[Guide](./docs/GUIDE.md)**, and
**[configuration paths](./docs/CONFIG_PATHS.md)**.

## Documentation

- **Getting started:** [Guide](./docs/GUIDE.md) · [CLI reference](./docs/CLI.md) ·
  [Configuration paths](./docs/CONFIG_PATHS.md) · [ACP editor integration](./docs/ACP.md)
- **Features & troubleshooting:** [Subagent profiles](./docs/SUBAGENT_PROFILES.md) ·
  [Context Engine v2](./docs/SESSION_MEMORY_RETRIEVAL.md) ·
  [File deliverables and the `present` tool](./docs/PRESENT_TOOL.md) ·
  [Capability diagnostics](./docs/CAPABILITY_DIAGNOSTICS.md) ·
  [Recovery and updates](./docs/RECOVERY.md) · [Bot guide](./docs/BOT_GUIDE.md) ·
  [Checkpoints & rewind](./docs/CHECKPOINTS.md)
- **Engineering & migration:** [Spec](./docs/SPEC.md) ·
  [Task contracts & pause policy](./docs/TASK_CONTRACT.md) ·
  [Tool contract](./docs/TOOL_CONTRACT.md) · [Migrating from 0.x](./docs/MIGRATING.md)
- **Extension development:** [Extensions](./docs/EXTENSIONS.md) ·
  [Plugin packages and Manifest v1](./docs/PLUGIN_PACKAGES.md) ·
  [Extension Protocol](./docs/EXTENSION_PROTOCOL.md) ·
  [Go SDK and starter](./sdk/go/README.md)

## Acknowledgments

Pondera is a rebranded fork of an MIT-licensed open-source coding agent. The
original copyright notice and license text are retained in [LICENSE](./LICENSE);
the upstream project's history is not part of this repository's snapshot.

<p align="center">
  <sub>MIT — see <a href="./LICENSE">LICENSE</a></sub>
  <br/>
  <sub>Fork maintained at <a href="https://github.com/bamboo-echo/Pondera">bamboo-echo/Pondera</a></sub>
</p>
