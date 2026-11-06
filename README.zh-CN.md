<p align="center">
  <img src="docs/logo-ghost-wave-effect.svg" alt="Pondera" width="360"/>
</p>

<p align="center">
  <a href="./README.md">English</a>
  &nbsp;·&nbsp;
  <strong>简体中文</strong>
  &nbsp;·&nbsp;
  <a href="./docs/GUIDE.zh-CN.md">指南</a>
  &nbsp;·&nbsp;
  <a href="./docs/ACP.zh-CN.md">ACP</a>
  &nbsp;·&nbsp;
  <a href="./docs/EXTENSIONS.zh-CN.md">扩展开发</a>
  &nbsp;·&nbsp;
  <a href="./docs/SPEC.zh-CN.md">规格</a>
  &nbsp;·&nbsp;
  <a href="https://github.com/bamboo-echo/Pondera/releases">发布页</a>
</p>

<p align="center">
  <a href="./LICENSE"><img src="https://img.shields.io/badge/license-MIT-8b949e.svg?style=flat-square&labelColor=161b22" alt="license"/></a>
  <a href="https://github.com/bamboo-echo/Pondera/releases"><img src="https://img.shields.io/badge/platform-Windows%20x64-0078D4.svg?style=flat-square&labelColor=161b22&logo=windows&logoColor=white" alt="platform"/></a>
</p>

<br/>

<p align="center"><strong>开源 · MIT · 单个 Go 二进制</strong></p>
<h3 align="center">可以一直开着跑的编码 Agent。</h3>
<p align="center">一套本地引擎,四个入口——终端、桌面端、浏览器,或通过 ACP 接入你的编辑器。计划模式、权限、工作区沙箱与逐轮 checkpoint,让长时间自治运行始终可读、可撤销。</p>

<br/>

> [!NOTE]
> **关于本仓库**：Pondera 是个人维护的 Windows 桌面分支，数据目录为
> `%APPDATA%\pondera`，可与其它版本同时运行。安装包由
> `scripts/desktop-build.sh windows/amd64 <version> mod` 产出，桌面产物为
> `Pondera.exe`。

## 特性

- **配置驱动**：provider、agent、启用的工具、插件全部在 `pondera.toml` 中声明，
  内核无硬编码模型。
- **多模型 · 可组合**：DeepSeek 作为预设内置；任何 OpenAI 兼容
  端点都只是一条配置。可选让两个模型协同（执行器 + 规划器），各自独立、缓存稳定的 session。
- **插件驱动**：MCP server 提供工具、提示词和资源；Extension Protocol v1
  Sidecar 还可以拦截运行时事件、提供 Provider 与结构化 UI，并通过版本化插件包分发。
- **缓存友好的上下文维护**：启动时注入稳定的环境摘要；旧工具输出会先 snip/prune，
  再进入摘要 compaction；内置工具 schema 合约有文档和回归测试保护。
- **零摩擦分发**：`CGO_ENABLED=0` 单二进制；一条命令交叉编译到六个目标平台。
  产物是完全自包含的静态二进制——目标机器上除二进制本身外无需安装任何东西。

## 安装

Windows 桌面版与 CLI 都使用同一套本地 Pondera 引擎。

### Windows 安装包

从 [GitHub Releases](https://github.com/bamboo-echo/Pondera/releases) 下载
`Pondera-windows-amd64-installer.exe`（或便携版 `.zip`），安装后在应用内配置
provider 和模型即可。用户数据保存在 `%APPDATA%\pondera`，升级时保留。

### 从源码构建

先克隆仓库：

```sh
git clone https://github.com/bamboo-echo/Pondera.git
cd Pondera
```

需要 **Go 1.27+**、**Node 24+**、**pnpm 11**（`npm install -g pnpm@11`），打安装包还需要 NSIS 3。模块固定了
`toolchain` 指令，保持 `GOTOOLCHAIN=auto` 让 Go 自动下载对应工具链。

```sh
# 只构建 CLI / TUI -> bin/pondera.exe
make build

# 完整 Windows 桌面构建 -> dist/Pondera-windows-amd64-installer.exe
scripts/desktop-build.sh windows/amd64 v0.1.0 mod

# 构建 + 静默重装 + 冒烟 + 启动
powershell -ExecutionPolicy Bypass -File scripts/fork-windows-build.ps1
```

完整的 fork 工作流（构建、安装、数据保留、排障）见
[docs/FORK_DEV_WORKFLOW.zh-CN.md](./docs/FORK_DEV_WORKFLOW.zh-CN.md)。

## 快速开始

首次启动后先配置 provider 和模型，再开始会话。

### 桌面端

启动 `Pondera.exe`，在设置里添加 provider（API Base URL、密钥和模型）。
模型列表可以从供应商拉取，也可以手动填写。

### CLI / TUI

同一套引擎也提供终端二进制：

```sh
pondera setup                      # 配置 provider 和模型
pondera                            # 启动交互式会话
pondera run "把 main.go 里的 TODO 实现掉"
```

需要项目指令时，可在交互式会话中运行 `/init`。

CLI 进阶用法和详细配置见 **[CLI 命令参考](./docs/CLI.zh-CN.md)**、
**[指南](./docs/GUIDE.zh-CN.md)** 和
**[配置路径](./docs/CONFIG_PATHS.zh-CN.md)**。

## 文档

- **开始使用：** [指南](./docs/GUIDE.zh-CN.md) ·
  [CLI 命令参考](./docs/CLI.zh-CN.md) · [配置路径](./docs/CONFIG_PATHS.zh-CN.md) ·
  [ACP 编辑器接入](./docs/ACP.zh-CN.md)
- **功能与排障：** [子智能体 Profile](./docs/SUBAGENT_PROFILES.zh-CN.md) ·
  [Context Engine v2](./docs/SESSION_MEMORY_RETRIEVAL.zh-CN.md) ·
  [文件成果与 `present` 工具](./docs/PRESENT_TOOL.zh-CN.md) ·
  [能力诊断](./docs/CAPABILITY_DIAGNOSTICS.zh-CN.md) ·
  [恢复与安全模式](./docs/RECOVERY.zh-CN.md) ·
  [机器人使用指南](./docs/BOT_GUIDE.zh-CN.md) ·
  [Checkpoints 与 rewind](./docs/CHECKPOINTS.zh-CN.md)
- **工程与迁移：** [规格](./docs/SPEC.zh-CN.md) ·
  [任务合约与暂停策略](./docs/TASK_CONTRACT.zh-CN.md) ·
  [工具合约](./docs/TOOL_CONTRACT.zh-CN.md) ·
  [从 0.x 迁移](./docs/MIGRATING.zh-CN.md)
- **扩展开发：** [扩展概览](./docs/EXTENSIONS.zh-CN.md) ·
  [插件包与 Manifest v1](./docs/PLUGIN_PACKAGES.zh-CN.md) ·
  [Extension Protocol](./docs/EXTENSION_PROTOCOL.zh-CN.md) ·
  [Go SDK 与 starter](./sdk/go/README.md)

## 致谢

Pondera 基于一个 MIT 许可的开源编码 Agent 改名而来。原始版权声明与许可文本
完整保留在 [LICENSE](./LICENSE)；上游仓库历史不包含在本仓库快照中。

<p align="center">
  <sub>MIT —— 见 <a href="./LICENSE">LICENSE</a></sub>
  <br/>
  <sub>分支维护于 <a href="https://github.com/bamboo-echo/Pondera">bamboo-echo/Pondera</a></sub>
</p>
