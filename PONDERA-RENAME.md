# Pondera 改名记录（2026-10-30）

本仓库从 Reasonix 改名为 **Pondera**，目标是与本机正装的 Reasonix 成为**互不影响的两套应用**。

## 已完成

### 运行时身份（用户可见面）
- **产品名/exe**：`Pondera.exe`；服务二进制 `pondera-desktop.exe`；窗口标题、托盘、菜单、关于页、启动画面、三语言文案（en/zh/zh-TW）
- **独立数据目录**：Windows `%APPDATA%\pondera`、Unix `~/.pondera`（Go `config.PonderaHomeDir` 与壳 `home.ts` 同步修改）——会话、配置、凭据、日志、Electron profile 全部与正装版隔离；**两版可同时运行**（单实例锁按数据目录算）
- **URI scheme**：`pondera://`；Electron IPC 通道、渲染进程 CustomEvent、storage key 全部 `pondera:` 前缀
- **快捷方式隔离**：AUMID `io.pondera.desktop`，只认 `pondera*` 命名的快捷方式；不再"收养" `Reasonix`/`io.reasonix.desktop` 等旧 ID → 两个 app 不会互相修复对方的快捷方式
- **更新器脱钩**：本地打包统一用 `channel=mod` → `desktopUpdaterEnabled()` 按自身策略返回 false，fork 永不拉官方 Reasonix 更新
- **崩溃上报**：`crashEndpoint` 改为 `https://crash.pondera.io/v1/report`（域名不存在→静默失败，不污染上游）
- **深层品牌**：项目配置 `pondera.toml`、记忆文件 `PONDERA.md`（仓库自身说明文件已改名，`CLAUDE.md` 指向已更新）、导出 `pondera-session.md`、插件 API `pondera.io/plugin/v2`、slash 命令前缀 `/pondera:`、主题作者
- **打包/发布面**：NSIS 安装器（注册表键、安装目录、宏名、卸载器）、Linux nfpm/polkit（`io.pondera.desktop.update.policy`）、desktop entry、附属二进制（`pondera-guard`、`pondera-update-helper`、`pondera-uninstall`、`pondera-cli`、`pondera-legacy-migrator` 目录）、`.signpath` 签名配置、`scripts/` 发布脚本、`.github/workflows`

## 有意保留（编译期/内部标识，运行时不可见）
- Go 模块名 `reasonix` 与全部 `reasonix/...` 导入路径（保上游可合并性）
- `REASONIX_*` 环境变量（含 `REASONIX_HOME`、`REASONIX_REQUIRE_PAYLOAD_MANIFEST`）
- preload API `window.reasonixDesktop`、`__reasonix*` 渲染进程全局、协议内部分类前缀 `/__reasonix_*`

## 本地构建方法（2026-10-31 实测可用）
```powershell
# 前置：Go 1.26+、Node 24+、pnpm 10、Git Bash、NSIS 3.12（解压后加入 PATH）
# Electron 44.2.0 win32-x64 已缓存在 %LOCALAPPDATA%\electron\Cache
$env:Path = 'C:\Program Files\Go\bin;<nsis 解压目录>;' + $env:Path
& 'C:\Program Files\Git\usr\bin\bash.exe' -c 'export PATH="/usr/bin:/bin:$PATH"; export ELECTRON_MIRROR="https://npmmirror.com/mirrors/electron/"; scripts/desktop-build.sh windows/amd64 v0.1.0 mod'
# 注意：PATH 必须把 /usr/bin 放最前，否则脚本里的 find 会命中 Windows FIND.EXE 而中断
```
正式产物：`repo\dist\Pondera-windows-amd64-installer.exe`（NSIS 安装包）、`repo\dist\Pondera-windows-amd64.zip`（portable，versioned 布局）。
不传 `-Version` 时，`scripts/fork-windows-build.ps1` 会读取 `.zwork/pondera-version.txt` 并自动递增 patch（从 `0.1.1` 起），每次打包版本都不同；About 的构建标识同时显示通道和 commit 短哈希。
`repo\desktop\build\electron\windows-amd64\app\Pondera.exe` 只是未压缩的中间产物：直接双击会因 `resources\service\pondera-desktop.exe` 缺失报 `service_failure`（`spawn ... ENOENT`）。要调试它，先执行：
```powershell
New-Item -ItemType Directory -Force repo\desktop\build\electron\windows-amd64\app\resources\service
Copy-Item repo\desktop\build\windows\installer\pondera-desktop.exe `
  repo\desktop\build\electron\windows-amd64\app\resources\service\pondera-desktop.exe -Force
```

## 已验证
- 两 Go 模块 `go build ./...` 绿；`go vet` 绿；`internal/...` 全量测试绿
- `desktop` 聚焦回归（manual creation / blank reuse / EnsureBlankTab / TabSessionIsBlank / winuninstall / 打包契约等）绿
- 前端 `typecheck` + `test:typecheck` + `pnpm build`（含打包预算）绿；全量 shim 套件中改名引入的 2 个失败已修（租约短语、slash 夹具）
- Electron 壳 typecheck/build 绿；`Pondera.exe` 冒烟启动成功，独立数据目录创建

## 2026-10-31 复跑、补完与打包
- **文档改名完成**：README.md / README.zh-CN.md / docs/*.md（含 zh-CN）/ 各级 README（desktop、electron、sdk、benchmarks、tools、superpowers、eval 等）/ docs/index.html。正文品牌、`pondera.toml`、`~/.pondera`、`%APPDATA%\pondera`、`pondera://`、`Pondera.exe/.app`、`pondera-desktop/cli/guard/uninstall/update-helper` 全部对齐；上游链接、CLI 命令 `reasonix`、`REASONIX_*` 环境变量、Go 模块路径、`DeepSeek-Reasonix`、SignPath 项目名、CHANGELOG 保留不动。
- **`desktop` 全量 `go test ./...` 已跑（602s）**，6 个失败全部定位：
  - symlink EPERM ×3（browser upload ×2、cold compatibility readers）——Windows 权限环境，非回归
  - `TestVerifyWindowsPortableVersionedLayout`——全量并发下 bash/find 环境差异，单独跑通过
  - `TestHistoryPageFromProviderMessagesWindowsVisibleUsers`——中文 locale 断言，`LANG=en_US.UTF-8` 下通过
  - `TestActivateVersionedWindowsFromStagingRejectsManifestDrift`——真回归：payload 清单常量未改名，已修，复跑通过
- **补齐改名残留**：
  - `cmd/reasonix-launcher` → `cmd/pondera-launcher`、`cmd/reasonix-cli-launcher` → `cmd/pondera-cli-launcher`（`scripts/desktop-build.sh` 与 `guard_packaging_test.go` 早已按新名引用，目录未跟上）
  - payload 清单 `reasonix-payload.json` → `pondera-payload.json`（Go 常量、package/finalize 脚本、release workflow、测试夹具）
  - `scripts/package-windows-desktop.sh` 改认 `pondera_project.nsh` / `PONDERA_VERSION_TAG`
  - `scripts/check-windows-uninstaller.mjs` 改用 `ARG_PONDERA_*` 与 `pondera_host.nsh`
- **Windows 打包成功**：`repo/dist/Pondera-windows-amd64-installer.exe`（151 MB，NSIS，channel=mod）、`repo/dist/Pondera-windows-amd64.zip`（215 MB，versioned portable，`verify.mjs --kind windows-portable-zip` 通过）；portable 冒烟启动成功（`Pondera.exe` → `pondera-desktop` → Electron，独立数据目录创建）。
- **剩余环境性失败（与改名前 HEAD 一致）**：symlink EPERM；`scripts/desktop-release-artifacts.test.mjs` 20/22（Windows `find` 与 symlink 用例）。macOS/Linux 产物按用户要求不做。

## 2026-10-31 第二轮：可见残留深挖清理
- **CLI 文案**（`internal/i18n` en/zh/zh-TW）：usage/help、setup/run/remote/report/telemetry 提示中的命令名统一为 `pondera`；三语言 catalog parity 测试绿。
- **Web 页面**（`internal/serve`）：index / login / provider_setup / logo-wordmark 品牌改 Pondera。
- **内置技能**：`reasonix-guide` → `pondera-guide`（目录、embed、测试、引用），slash 技能名同步。
- **前端可见面**：心跳建议文案不再指向失效 URL（只读本地 `release-notes/releases.json`）；前端诊断导出文件名 `pondera-frontend-diagnostics-*.json`；MCP App clientInfo 改为 `pondera`；设置里的反馈按钮改回上游真实仓库（原改写后的 `DeepSeek-Pondera` 地址 404）；dev mock 的 `.reasonix` 路径 → `.pondera`。
- **真实数据路径修复**：附件显示/解析原来只认 `.reasonix/attachments/`，而 Go 端已用 `.pondera/attachments/`。现同时识别两种前缀（旧会话里的 `.reasonix` 附件仍可正常显示）。
- **发布说明按钮**：删除已无外部发布页的“查看发布说明”命令与设置里的更新日志区块（同步删除三语言 `changelog.*` 键和对应断言），更新横幅只保留本地提示。
- **应用图标**：替换为自研 Gaze Sweep 标（深色圆角底板、青绿→蓝→紫扫弧、冰白光核）：`build/appicon.png`（1024）、`build/appicon.svg`、`build/windows/icon.ico`（16/24/32/48/64/128/256）、`build/trayTemplate.png`/`@2x`。Go 图标资产测试改为校验“内缩深色底板 + 四边透明”，并支持 ICO 中小尺寸的 DIB 帧解码。仅 Windows 产物更新；macOS `build/darwin` 资产未动（按用户要求不做 macOS）。
- **应用内字标**：侧栏、欢迎空态页和 `internal/serve` 网页的 `logo-wordmark.svg` 换成同一设计语言的 Pondera 字标（纯矢量 path、单色 `#4C7DFF`、无字体依赖），深色主题仍由 CSS `brightness(0) invert(1)` 转白。Windows 无边框标题栏、启动闪屏和启动占位页使用的旧 R 图形标 `logo-symbol.svg` 也换成 Gaze Sweep mark（扫弧 + 独立光核，渐变填充，22px mask 与 64px 闪屏均清晰）。
- **Prompt golden 更新**：系统提示中的会话上下文说明改为 Pondera，`prefix_shape.json` / `provider_request.json` / `system_prompt.txt` 用 `REASONIX_UPDATE_GOLDEN=1` 重生成。缓存影响：前缀哈希变化一次，升级后首轮会重建 provider prompt cache，其余提示与工具 schema 未变。
- **验证**：前端 `typecheck`、附件/设置/心跳/About 相关测试绿；`internal/i18n`、`internal/skill`、`internal/serve`、`internal/boot`（golden 重生成后）绿；desktop 图标资产测试与 `cmd/windows-resource` 测试绿；两个 Go 模块 build 绿。
- **提交**：`f6f235c8e` chore: finish Pondera rebrand in shipped surfaces；`fd4fd4886` test: refresh prompt golden after Pondera branding；`4aacb5f64` feat: replace app icon with the Pondera gaze-sweep mark；`38c40b1ba` feat: replace the in-app Reasonix wordmark with Pondera lettering。
- **仍保留（非用户可见）**：Go 模块路径、`REASONIX_*` 环境变量、`window.reasonixDesktop`、`reasonix_error:` 协议错误码、`/__reasonix_workspace_media/` 媒体路由、CLI 二进制源码目录 `cmd/reasonix`、`reasonix` 独立 CLI 命令名、上游链接与 `DeepSeek-Reasonix` 字样、正版 Reasonix 的注册表/包名。
- **最终打包/安装（2026-10-31 18:26）**：NSIS 安装包 158,890,468 字节、portable zip 226,472,114 字节；静默卸载→静默安装→打包冒烟 5 项全部 PASS（handshake、Version 调用、shell/service 存活、正常退出）；`explorer.exe` 启动成功（Pondera.exe + pondera-desktop 服务在跑）；安装版 `resources/icons/appicon.png` 哈希与仓库 `desktop/build/appicon.png` 一致；`%APPDATA%\pondera` 保留。

## 之前的两个功能（同分支已提交）
- `77d8f887f` fix: 空会话点 + 不再堆积（TabSessionIsBlank 探测 + handleNewTab 复用）
- `0c461ce9e` feat: 模型编辑弹窗推理档位标准六级兜底
