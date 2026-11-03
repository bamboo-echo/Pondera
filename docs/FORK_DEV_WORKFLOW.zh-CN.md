# Pondera Fork 开发与打包流程（Windows）

适用：在本机对 fork 改代码、加功能、重新打包与安装。上游 CI、签名、发布流程不在此列。
配套脚本：[scripts/fork-windows-build.ps1](../scripts/fork-windows-build.ps1)。

## 1. 一次性环境

| 组件 | 要求 | 本机位置/说明 |
| --- | --- | --- |
| Go | 1.26+ | `C:\Program Files\Go\bin` |
| Node | 24+ | `C:\Program Files\nodejs` |
| pnpm | 10 | `%APPDATA%\npm\pnpm.cmd` |
| Git Bash | 5.x | `C:\Program Files\Git\usr\bin\bash.exe` |
| NSIS | 3.12 | `.zwork\nsis\nsis-3.12`（含 `makensis.exe`） |
| Electron | 44.2.0 win32-x64 | `%LOCALAPPDATA%\electron\Cache`（约 151 MB） |

规则：

- **任何下载或安装之前先征得用户同意**（须得到「我同意了」）。NSIS 压缩包约 2.3 MB，SHA256 `56581f90db321581c5381193d796fffcf2d24b2f8fed2160a6c6a3baa67f2c4f`。
- Electron 缓存按镜像 URL 哈希索引，必须设置 `ELECTRON_MIRROR=https://npmmirror.com/mirrors/electron/` 才会命中缓存；换了镜像就等于重新下载约 150 MB。

## 2. 日常闭环

1. 改代码。
2. 前端静态检查：`pnpm --dir desktop/frontend exec tsc --noEmit`。
3. 两个 Go 模块编译：根目录与 `desktop/` 各跑一次 `go build ./...`。
4. 按改动面跑聚焦测试（见 §6）。
5. 打包：`scripts/fork-windows-build.ps1`（或 §5 手动命令）。
6. 安装：脚本默认保留用户数据。
7. 打包冒烟必须 PASS，然后启动应用确认。
8. 提交（双语 Conventional Commit，见 §8）。

## 3. 数据策略（重要）

- 用户数据固定在 `%APPDATA%\pondera` 与 `%LOCALAPPDATA%\pondera`，包含会话、供应商配置、密钥、设置。
- **升级默认保留数据**：静默卸载只移除安装目录，不删除上面两个目录（已验证）。
- 只有明确需要「全新状态」时才删；删除前确认没有要保留的会话，或先复制 `%APPDATA%\pondera` 做备份。
- 清理示例（仅在确认后执行，应用必须先退出）：

```powershell
Remove-Item "$env:APPDATA\pondera" -Recurse -Force
Remove-Item "$env:LOCALAPPDATA\pondera" -Recurse -Force
```

不要删除 `%LOCALAPPDATA%\Programs\Pondera`（安装本体，卸载请走卸载器）。

## 4. 一键脚本

```powershell
powershell -ExecutionPolicy Bypass -File scripts/fork-windows-build.ps1
```

| 参数 | 默认 | 作用 |
| --- | --- | --- |
| `-Version` | `v0.1.0` | 构建版本号 |
| `-Channel` | `mod` | 更新渠道；`mod` 表示永不拉上游更新 |
| `-SkipBuild` | 关 | 跳过编译，直接用 `dist/` 现有产物重装 |
| `-NoInstall` | 关 | 只构建，不卸载/安装 |
| `-NoLaunch` | 关 | 安装后不自动打开 |
| `-NsisDir` | `<工作区>\.zwork\nsis\nsis-3.12` | NSIS 解压目录 |

脚本步骤：构建 → 结束旧进程 → 静默卸载（保留数据）→ 静默安装 → 等待 `current.json` 就绪 → 打包冒烟 → `explorer.exe` 启动。
完整日志：`.zwork\fork-build.log`。

## 5. 手动打包命令（等价）

```powershell
$env:Path = 'C:\Program Files\Go\bin;<NSIS 目录>;' + $env:Path
& 'C:\Program Files\Git\usr\bin\bash.exe' -c 'export PATH="/usr/bin:/bin:$PATH"; export ELECTRON_MIRROR="https://npmmirror.com/mirrors/electron/"; scripts/desktop-build.sh windows/amd64 v0.1.0 mod'
```

- `/usr/bin` 必须放在 bash PATH 最前：否则 `find` 会命中 `C:\Windows\System32\FIND.EXE`，报 `File not found - *installer*.exe` 并中断。
- 产物：`dist\Pondera-windows-amd64-installer.exe`（NSIS，约 151 MB）与 `dist\Pondera-windows-amd64.zip`（portable，约 215 MB）。
- 未做代码签名，SmartScreen 会提示未知发布者，属预期。

## 6. 按改动面选测试

**前端（UI/文案/交互）**

```powershell
pnpm --dir desktop/frontend exec tsc --noEmit
# 单套测试（本机中文 locale 必须加 en-US shim + SVG stub）
node --import "$env:TEMP\pondera-locale-en.mjs" --import ./scripts/svg-stub-register.mjs --import tsx src/__tests__/<name>.test.tsx
```

- shim 内容：把 `navigator.language` 置为 `en-US`（否则英文断言必失败，属环境问题）。
- 全量前端套件：工作区根目录 `node run-tests-shim.mjs`（约 538 套，耗时较长）。
- 必须在 `desktop/frontend` 目录下执行。

**Go**

- 根模块：`go build ./...`、`go test ./internal/...`
- `desktop` 模块：`go build ./...`；聚焦 `go test . -run '<TestName>'`；全量 `go test ./...`（约 10 分钟）
- 中文 locale 断言：`$env:LANG='en_US.UTF-8'; $env:LC_ALL='en_US.UTF-8'; go test ...`

**App 命令/契约**

新增或改名 Go 侧 App 方法后必须重新生成契约，否则 `package.mjs` 的 drift check 会失败：

```powershell
cd desktop
go run . -emit-contract frontend/src/generated
```

**打包/安装/更新**

```powershell
node desktop/packaging/smoke.mjs "<安装目录>\versions\<ver>\app" --service "<安装目录>\versions\<ver>\pondera-desktop.exe" --timeout 90
```

五项 PASS 才算通过：握就绪、renderer 调服务、双进程存活、正常退出、子进程随之退出。

**改名/品牌改动**

全仓搜索旧品牌名（大小写不敏感），结果应当为零；只有 `LICENSE` 的原始版权声明、
`desktop/testdata/session-history-upgrade` 的历史夹具，以及 `internal/session` 的
旧会话 codec 兼容别名例外。新增代码、文案与资源统一使用 `Pondera`、`pondera`、
`PONDERA_*`、`ponderaDesktop`。

## 7. 本机环境坑

1. **Trae 终端回收子进程**：从终端 `Start-Process` 启动的应用会在命令结束后被连带杀掉。要留下运行实例，用 `explorer.exe <exe>` 启动（脚本已内置）。
2. **Windows FIND**：见 §5，bash PATH 必须让 `/usr/bin` 优先。
3. **symlink EPERM**：browser upload、cold compatibility readers 等测试需要创建符号链接；Windows 普通权限下必失败，非回归。
4. **中文 locale**：前端测试需要 en-US shim；Go 测试设置 `LANG`。
5. **CRLF 警告**：`git add` 时 `desktop/frontend/dist/.gitkeep` 的 LF/CRLF 警告可忽略。
6. **Electron 镜像哈希**：见 §1。
7. **全量 `desktop` 测试**可能因并发下 bash/find 环境差异偶发失败，单跑该用例可确认。

## 8. 提交与记录

- 提交信息用双语 Conventional Commits：`type(scope): english summary / 中文摘要`。
- 只加已跟踪文件：`git add -u`；`dist/`、`desktop/build/`、`node_modules/` 均被忽略。
- 每次「打包」完成后，直接推送本仓库 `main` 到 `origin`（`git push origin main`，只允许快进）；只有用户明确说「这次不用推」才跳过。不再导出额外的快照仓库。
- 功能或流程变化时同步更新本文件；改名/品牌规则见 §6。

## 9. 速查表

| 目的 | 命令 |
| --- | --- |
| 一键构建+重装+冒烟+启动 | `powershell -File scripts/fork-windows-build.ps1` |
| 只重装现有产物 | `... -SkipBuild` |
| 只构建 | `... -NoInstall` |
| 前端静态检查 | `pnpm --dir desktop/frontend exec tsc --noEmit` |
| Go 编译（根/desktop） | `go build ./...` |
| 全量 Go 测试（desktop） | `cd desktop; go test ./...` |
| 打包冒烟 | `node desktop/packaging/smoke.mjs <app 目录> --service <服务 exe>` |
| 重新生成 App 契约 | `cd desktop; go run . -emit-contract frontend/src/generated` |
| 产物目录 | `repo\dist\` |
| 用户数据 | `%APPDATA%\pondera`、`%LOCALAPPDATA%\pondera`（默认保留） |
