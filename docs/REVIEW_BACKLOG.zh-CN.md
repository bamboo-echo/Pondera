# REVIEW BACKLOG（待整改清单）

> 本文件用于在上下文压缩后仍能完整保留剩余整改项。已完成项不再重复：
> 沙箱受保护状态根、前端启动壳死锁、助手窗口 IPC 越权、回合状态清零竞态、
> 重试浮层＋倒计时、Retry-After 接入。

## 批次 A（先做，前端 P2 共 9 条）

- P2-1 `components/CapabilitiesPanel.tsx:65`、`:178-179`：`Capabilities()` 失败被 `.catch(() => 空快照)` 吞成"0 个 MCP/0 个 Skill/0 个插件"；`view` 为 null 时只渲染 `caps.loading`，错误横幅在 view 分支内永远看不到，形成永久"加载中"。改为 catch 时 setErr 并渲染错误空态＋重试，`!view` 分支按 err 区分加载/失败。
- P2-2 `components/PluginMarketplacePanel.tsx:437-457`、`:453`：drawer 是 `role="dialog"` 但无 `aria-modal`、无初始焦点、无焦点陷阱、Esc 无效、点外部不关闭；`zIndex: 60` 硬编码绕过 `--z-*` 契约。对照同项目 `ChatNodes.tsx:361` 的完整实现补齐，z-index 改用 token。
- P2-3 同上 `:401-408` 与 `:421-425`：卡片 `role="button"` 的外层 `onKeyDown` 不区分 target，Enter 会一边安装一边开详情；Space 被外层 preventDefault 吞掉。给 `onKeyDown` 加 `e.target === e.currentTarget`，或卡片主体改真实 button、安装按钮移出角色区域。
- P2-4 `components/harness-chat/ansi.ts:49,51,53,55`（配合 `ChatTranscript.css:19-22`、`TerminalBlock.tsx:174`）：4 个 token 从未定义且无 fallback——`--dsw-alias-state-error-secondary`、`--dsw-alias-state-success-secondary`、`--dsw-alias-state-warn-secondary`、`--dsw-static-blue-400`，导致 BrightRed/Green/Yellow/Blue 亮色的语义全丢。补齐 token 或写成 `var(--x, 原色)`。
- P2-5 `scripts/check-z-index-tokens.mjs:7-8,14-24` 与 `package.json:9,14`：门禁默认只读 `src/styles.css`，build 只传 3 个 CSS，TSX 内联 `zIndex` 完全不扫。实测传入其余 CSS 立刻失败：`ChatTranscript.css:196 z=3`、`DiffBlock.css:21 z=1`、`TurnNavigator.css:7 z=7`、`heartbeat.css:51/208/251/1614 = 3/2/5/10`。扩到 src 全部 CSS 并解析 TSX 的 `zIndex`，加负例自测。
- P2-6 `scripts/check-theme-token-contract.mjs:73`：只收 `.css`，TSX 内联 `var(--*)` 全数漏检（P2-4 那 4 个就在 TSX）。扩到 `*.ts/tsx`，排除注释与字符串。
- P2-7 繁中回落简体与文案混排：`BrowserPanel.tsx:36`、`BrowserEvidenceControls.tsx:8`、`BrowserResponsiveControls.tsx:8`、`Composer.tsx:4562,4919`、`TurnStats.tsx:94` 用 `locale !== "en"`/`startsWith("zh")` 走简体，改按 `zh-TW` 分支；`locales/zh-TW.ts:1010` 为"确认刪除？"、`:1607` 简繁混排（"刪除所有远程会话鏡像？远端 Serve 会继续執行，当前会话不受影响。"）、`:2987` `status.tpsLabel` 未译；`PluginMarketplacePanel.tsx:366` 领域芯片直接渲染索引中文域名，英文界面也显示中文，需做映射。
- P2-8 `src/assistant` 全目录无 `useI18n`：`AssistantApp.tsx:97,107-113,155`、`Onboarding.tsx:117,127-145`、`History.tsx:42,63` 硬编码中文（含 aria-label、placeholder）。迁到 locale 表并补三语。
- P2-9 `eslint.config.js:22-30`：`react-hooks/exhaustive-deps` 只对 `app-runtime/app-shell/app-features/app-domain/useCommitted*` 生效，`src/components`、`src/lib`、`src/assistant` 没开。扩上去。

## 批次 B（前端 P3，16 条）

1. `PluginMarketplacePanel.tsx:197-213`、`:290`、`:209`：无请求代号可被旧响应覆盖；成功后不清 `err`，旧错误横幅常驻；`current || source.source` 把用户清空的输入回填。加 request id、成功清错、只在空串且未编辑时回填。
2. `:414`：条目缺 description 时拿分类名当描述渲染，应显示"无描述"。
3. `:259` 与 `:424`：`replace: entry.installed` 是死分支（按钮已禁用），删掉或放开按钮。
4. `:398`：索引本身 0 条时提示"没有匹配筛选条件"，掩盖真实空索引。
5. `lib/bridge.ts:1181` + `lib/desktopHost.ts:219-227`：命令不在 `contract.commands` 时返回 `undefined`，`app.X` 同步 TypeError，`.catch` 接不住，`void reload()` 未处理拒绝，界面永久加载。改为对缺失命令返回明确的"不支持"结果或哨兵 Promise。
6. `index.html:2,5`：启动壳写死 `data-theme="dark"`，浅色主题用户先黑屏闪一下；`user-scalable=no` 禁缩放。主题预置提到 inline script。
7. `assistant/History.tsx:62-64`：还原到此版本无二次确认，且只刷新本列表、不刷新其他面板。
8. `assistant/History.tsx:45-68`：历史无上限、无虚拟化。
9. `lib/toast.tsx:65`：可点关闭的 div 不是按钮。
10. `components/harness-chat/use-copy-feedback.ts:16`：复制失败 `.catch(() => {})` 静默。
11. `PluginMarketplacePanel.tsx:331-368`：两组 `role="tablist"` 无 `aria-controls`/`tabpanel`、无 roving tabindex 与方向键。
12. 三语键数实测为 3621（前端审查结论里写 3622），以实测为准，仅记录不修。
13. 预算顶格：`initial raw` 当前 2057.3/2057.5（余 0.2 KiB），zh-TW 63.6/64.3；后续每改前端先压体积，不要抬上限。
14. `CapabilitiesPanel.tsx:70-74`：interval 依赖 `view?.servers` 数组引用，每次 reload 清/建定时器。
15. `lib/pluginMarketplaceFilters.ts:46`：`localeCompare(..., "zh")` 用于 en/zh-TW 界面。
16. `lib/pluginMarketplaceFilters.ts:17,69`：域名 `all` 与"全部领域"哨兵冲突。

## 批次 C（后端 8 条）

- P2 `internal/agent/agent.go:1592-1598`（调用点 1480/1495/1505）：`upsertPartialToolCall` 只在 `call.ID != ""` 去重，无 ID 的流式调用每次 `append`，中断时会把 N 份递增参数快照写入记录。无 ID 时按最后一项 index 或 Name+序号合并；最好在 provider 适配层给缺失 id 合成稳定 id。
- P2 `internal/agent/dispatch_generation.go:36`、`:41-56`：`snapshotDispatchClasses` 与 gate 都用 `call.ID` 当 key，空 ID 互相覆盖→误判"tool safety or schema generation changed"而 blocked。改用下标或合成 `#i`，两处同一下标。
- P2 `internal/provider/retry.go:314-320`：失败响应体 `io.Copy(io.Discard, resp.Body)` 无长度上限，只有 10s 定时器兜底。设上限（如 64 KiB），超出直接 Close。
- P3 `internal/provider/retry.go:102-107` + `openai/openai.go:599`、`:629-653`：`ApplyRequestAttemptCount` 写累计值，前缀续写 `mergeUsage(...,countRequests=true)` 把累计值相加（1+2=3、3 次记成 6）。合并前转 delta，或续写用 max。
- P3 `agent.go:1515-1518`：`chunk.Usage` 未判空即取字段，加 `if chunk.Usage != nil`。
- P3 `sampling_recovery.go:188`、`:194` 用固定分母 `maxSamplingAttempts=4`，而 `model_retry.go:97` 用动态分母，同回合出现两套口径。抽出统一的"本回合最大可能请求数"函数两处复用。
- P3 `execute_batch.go:382-384`：完成位只在收到 channel 后置位，`default:` 即结束 drain，已完成但未送达的 goroutine 被判"did not stop after cancellation"。在 goroutine 内先置完成位再发送。
- P3 `session_checkpoint.go:100-121`、`preflight.go:24`、`compact_projection.go:77`：`compactionMu` 普通 Mutex，持久化 I/O 在锁内且不响应 ctx 取消。I/O 移出锁或换可取消加锁。

## 批次 D（安全 2 条）

- `internal/installsource/apply.go:60-75`（applySkillRoot）：`register` 模式把 `act.Source` 原样写入 `[skills].paths`，可注册 `/` 或任意大目录为技能根。限制必须在 home 或项目根内，拒绝系统目录。
- `internal/sandbox/seatbelt_windows.go`：Windows 无 OS 级隔离，审批 UI 文案需明确"Windows 无系统沙箱，Shell 可写任意位置"。

## 批次 E（历史遗留，按此顺序）

- pnpm 版本自相矛盾：`scripts/check-desktop-build-contract.mjs:52` 断言 `engines.pnpm ">=11 <12"`、`:77` 断言 CI 集合 `["10"]`、`:84` 断言 README 含 `pnpm@10`，README 与 FORK_DEV_WORKFLOW 仍写 10。统一到 11。
- `docs/FORK_DEV_WORKFLOW.zh-CN.md:12,55,87`：pnpm 写 10、引用不存在的 `run-tests-shim.mjs`（真实入口 `desktop/frontend/scripts/run-tests.mjs`）、说 `-Version` 默认 v0.1.0（脚本实际自动递增）、"约 538 套"实为 543。
- `docs/ASSISTANT_DESIGN.zh-CN.md:145` 仍写"MVP 不发送 assistant:config-changed…留到 v1"，与 `:21` 及代码 `desktop/assistant_app.go:15`、`host_events.go:12`、`SettingsPanel.tsx:220` 已广播矛盾，改文档。
- `CHANGELOG.md` "Unreleased" 未记录配置小助手与插件市场/domain，补两节。
- `go.mod:3` `go 1.26.0`、`:5` `toolchain go1.26.10` 与 README "Go 1.26+"，而 `transport_diagnostic.go:52-77` 已按 Go 1.27 布局适配；升到 1.27 并同步 README。
- `internal/eval/replay`（median.go + test）孤儿包，全仓 0 引用且与 `cmd/e2ebench/report.go:288` 重复，删除。
- `lib/workStatus.ts` 全无引用（`Composer.tsx:351` 另有一份本地实现），删除库版。
- `assist/engine`、`assist/store`、`boundedllm`、`servecontract` 无包级测试，给 engine 与 store 各补 3–5 个表驱动单测。

## 明确跳过（等用户拍板，不要动）

CI 目录补回还是删守卫脚本；29 个前端死模块（10 个零引用 + 19 个只被测试引用）删除还是保留待接线；`legacy-migrator` 3 个红测试需先实跑确认。

## 执行要求

批次 A 全部完成后报一次（每条的 commit、实际测试输出、预算数字）；批次 B/C/D/E 各自完成或遇阻时报一次；某条改动明显超预期或需放宽护栏时先报再改。不要抬体积上限。
