# Pondera 扩展开发记录（阶段 1）

> 日期：2026-11-06 · 基线：`bamboo-echo/Pondera` main 快照
> 对应方案：`/workspace/pondera-feature-plan.md`
>
> 本轮按「手动开关式云同步」的澄清需求开发：**默认全部会话纯本地，只有用户显式选中的会话 + 任务才参与上云；未选中的完全不受影响**。没有后台守护进程、没有轮询、没有自动上传。

---

## 1. 本轮做了什么

| # | 需求 | 本轮状态 | 落地位置 |
| --- | --- | --- | --- |
| ① | 撤回消息 | ✅ 后端 API + 精确定位 | `internal/messageops/`、`POST /messages/retract` |
| ② | 编辑已发消息 | ✅ 后端 API（分叉重发） | `POST /messages/edit`、`GET /message-targets` |
| ⑤a | 手动选择式云同步 | ✅ 完整可运行链路 | `internal/cloudsync/` + `pondera cloud ...` + `/cloud/*` |
| ⑤b | 手机端发指令 / 看进度 / 审批 | ✅ 响应式 PWA 原型 | `internal/serve/mobile.html`、`GET /mobile` |
| ⑤c | 云端执行 | ♻️ 复用既有能力，未重写 | `internal/remote` + `bootstrap`（项目原有） |
| ⑤d | 云地双向传输 | ⚠️ 打包 / 导入闭环已实现；**跨机搬运仍需手工 scp** | `cloud pack` / `cloud apply` / `cloud confirm` |
| ③ | UI 大优化 | ⏸ 按你的要求后置 | — |
| ④ | 设置界面大优化 | ⏸ 按你的要求后置 | — |
| — | CI | ✅ 新建 | `.github/workflows/ci.yml` |

> 说明：③④ 按你的要求完全没动。移动端是**响应式 Web / PWA**，不是原生 APP —— 一期用它能最快在手机上真正跑起来，且不触碰 26 万行桌面前端。原生壳（推送 / 离线队列）在下一阶段。

---

## 2. 手动同步是怎么工作的

**默认态**：状态文件 `~/.pondera/cloud/sync-state.json` 里没有任何记录的会话 = `local-only`，永远不会上传。

**开关**：`pondera cloud pin <session> [--task goal:xxx]` 只是打标记，**不上传任何数据**。

**一次完整移交**（每一步都是你手动触发）：

```bash
# 本地：选中（可选只关注某些任务）
pondera cloud pin sess-abc --title "修复认证" --task goal:fix-auth

# 本地：停掉该会话上的任务后，显式打包（必须 --confirm-quiesced）
pondera cloud pack sess-abc --workspace-dir . --confirm-quiesced

# 自己把 bundle 送到云主机（项目已有 SFTP，后续可接成一键）
scp ~/.pondera/cloud/out/task-*.tar.gz my-cloud:~/incoming/

# 云主机：导入并成为唯一写者
pondera cloud apply ~/incoming/task-xxx.tar.gz --owner cloud --pin

# 本地：确认云端导入成功后才移交所有权
pondera cloud confirm sess-abc --bundle ~/.pondera/cloud/out/task-xxx.tar.gz

# 拉回本地时反向执行：云端 pack → 本地 apply --owner local
```

其他命令：`cloud list` / `cloud status <id>` / `cloud unpin <id>` / `cloud mappings`。

网页与手机端也能查看/切换开关：`GET /cloud/selections`、`POST /cloud/pin`、`POST /cloud/unpin`（**只有查看和标记，不能触发打包**，避免远程误上传）。

### 关键安全性质（都有测试覆盖）

- **失败不切换 owner**：云端 `apply` 校验失败时本地仍是写者，bundle 保留可重试；只有 `cloud confirm` 成功才翻转所有权。
- **单写者**：云持有写者时，本地再次 `pack` 会被拒绝。
- **凭据绝不入包**：`.env`、`*.key`、SSH 私钥、构建产物默认排除；解包时还有第二道敏感文件名拒绝。
- **防篡改**：manifest 全字段 SHA-256 封印 + 每个文件逐条校验；路径穿越 / 绝对路径 / 符号链接一律拒绝；包大小与条目数有上限。
- **身份重映射**：`SessionRef{HostID, SessionID}` 绑定主机，跨机导入必须重新签发；`origin 映射表`记录本地↔云端是同一个逻辑任务，支持反向拉回。
- **版本向量**：目标端序号比 bundle 新 → 拒绝导入（`stale handoff`），不做自动合并。
- **工作区**：git 仓库用 `git bundle` + 二进制 diff 携带**未提交改动** + untracked 文件，且不修改用户仓库（从不 add/commit/stash）；非 git 目录走文件清单。

---

## 3. 撤回 / 编辑的语义

| 端点 | 行为 |
| --- | --- |
| `POST /messages/retract` | `messageId` → 用 `CheckpointTurnsByMessageIndex()` 精确定位回合 → `Rewind(turn, conversation\|both\|code)`。conversation 回退会分叉，**父分支完整保留**，可用现有 `undoRewind` 恢复 |
| `POST /messages/edit` | 校验只能编辑 user 消息 → `ForkNamed(turn, name)` 截断到该消息之前 → `SubmitIdentified` 用新文本重发。**原分支不动** |
| `GET /message-targets` | 前端据此渲染"哪些消息可撤回 / 可编辑" |

两个刻意遵守的约束：

1. **编辑不是原地改显示**。`message/upsert` 只更新显示投影，不改发给模型的 workset；这里直接走 `ForkNamed`（内部 `Rewrite` + `SnapshotRewrite`），保证界面与模型上下文一致。
2. **文件副作用不自动回滚**。checkpoint 有 `CoverageGap`，bash 副作用不在覆盖内，因此 `scope=both` 会在响应里带 warning，编辑也会提示"此前工具改动的文件不会自动还原"。

所有变更都要求 `expected-session` header 防陈旧、`operationId` 幂等（断线重放不会二次 fork），回合运行中 / 只读旁观态一律 409 拒绝。

---

## 4. 手机端怎么用

1. 云主机（Linux）配 `[remote]`，**`credential_mode = "remote"`**（Key 在云端，电脑才能关机）。
2. 用 `pondera remote connect` 引导云端常驻 headless `pondera serve`（项目原有能力，断开本地不影响云端）。
3. 通过 **Tailscale / WireGuard 私有网**（推荐）或 HTTPS 反代把 serve 暴露给手机。**不要把 `auth=none` 的 serve 直接绑公网**。
4. 手机浏览器打开 `https://<host>/mobile#token=<token>`，可"添加到主屏幕"当 APP 用。

界面已支持：会话列表与同步状态徽标、实时 SSE 输出、发送指令、停止、审批（允许/拒绝）、点击消息后的撤回 / 编辑、断网重连后重拉历史基线、暗色与安全区适配。

---

## 5. 已验证

阶段 2 收尾时按 `.github/workflows/ci.yml` 在本地完整复跑了一遍，9/9 全通过；另用真实二进制跑了一次端到端冒烟，15/15 全通过。

| 项目 | 结果 |
| --- | --- |
| `go build ./...`（根模块） | ✅ 通过 |
| `cd desktop && go build ./...` | ✅ 通过 |
| `go vet`（cloudsync / messageops / costgate / webpush / serve / cli / session） | ✅ 无告警 |
| `go test ./internal/cloudsync/... ./internal/messageops/... ./internal/costgate/... ./internal/webpush/... -count=1` | ✅ 全部通过 |
| `go test ./internal/session/ -run 'TestInspectSliceTurns\|TestSliceExport\|TestMatchTurnRefs' -count=1` | ✅ 全部通过（含真实 `Replay` + 导入校验） |
| `go test ./internal/serve/ -run 'TestMobile\|TestMessageOp\|TestCloud\|TestCost\|TestMirror\|TestPush' -count=1` | ✅ 全部通过 |
| 前端 3 个 HTML 内联脚本 + `mobile-sw.js` 的 `node --check` | ✅ 语法通过 |
| `cd mobile-app && node scripts/verify.mjs` | ✅ Capacitor 工程配置有效 |
| `gofmt -l`（全部新增/修改 Go 文件） | ✅ 无未格式化文件 |
| 阶段 1 端到端冒烟（pin → pack → apply → confirm → 单写者拒绝 → 未选中拒绝） | ✅ 全部通过，`.env` 未泄漏 |

**阶段 2 真实二进制 E2E**（`cloud push` / `cloud pull` 走 filesystem transport，远端是独立 `PONDERA_HOME` 的真实子进程）：

| 断言 | 结果 |
| --- | --- |
| `cloud pin` 只打标记、不上传数据；`cloud status` 显示 `owner=local` | ✅ |
| `cloud push` 全链路：pack → 上传 → 远端 `cloud apply` → 所有权移交 | ✅ `quiesce=writer-lease-free`，远端会话成功安装 |
| **回归：bundle 内不含 `writer.lock` / `*.tmp` / `*.uploading`** | ✅ 源目录故意注入这 3 类残留，bundle 内只剩 `manifest.json`、`session/events.frames`、`session/manifest.json` |
| 任务选择无匹配时 fail-open 带整段会话并显式标注 | ✅ `no-task-match-fallback-whole-session` |
| 远端 / 拉回的会话均可 `ReplayExport` | ✅ 两处都通过 |
| 清理只作用于副本，本地源目录不被破坏 | ✅ 注入的 3 个文件仍在 |
| `cloud pull` 在全新 `PONDERA_HOME`（无 `sync-state.json`）下成功 | ✅ 自动建立身份，写者回到 `local` |
| push 后本地 `owner=cloud`，含 `lastUpload` handoff 与摘要 | ✅ |

CI 覆盖：build 两个模块 + vet + 全部新包测试 + session 裁剪测试 + serve 端点测试 + 前端脚本语法 + Capacitor 校验 + gofmt 检查。

---

## 6. 阶段 2：原「未完成」8 项已全部落地

| # | 原待办 | 当前实现 | 验证 |
| --- | --- | --- | --- |
| 1 | 跨机搬运自动化 | `internal/cloudsync/transport.go` 定义 `Transport`；`internal/cli/cloud_transfer.go` 用项目现成的 `remote.Client` + `sftpfs.FS` 实现 SSH 适配器；`pondera cloud push/pull` 把 `pack → 上传 → 云端 apply → 移交所有权` 合成一条命令 | `TestPushEndToEnd`（真实子进程导入）、`TestPushFailureLeavesLocalWriter`、`TestPullDownloadsAndInstalls`；真实二进制端到端冒烟通过 |
| 2 | 自动 quiesce | `internal/cloudsync/quiesce.go`：探测 `GET /status` 的 `running`/`pendingPrompt`/`runtimeState`，回落 `writer.lock` 租约，`drain` 模式自动 `POST /cancel-session` 并轮询到空闲；无法证明空闲时 fail-closed，`--force` 才可覆盖 | 5 个测试覆盖 probe / 拒绝 / drain / 租约 / 失效；`cloud pack` 默认 `--quiesce drain`，`--confirm-quiesced` 保留为 force 别名 |
| 3 | 任务级裁剪 | `internal/session/slice_export.go`：在 session 包内用官方 codec 读出 V4 commit log，按选中任务匹配的 turn 过滤，**重新编号成连续序列**、重算 operation hash、丢弃派生索引，产出仍可 `Replay` / 被 `Service.Import` 接受的自包含导出；无匹配时 fail-open 并显式标注 `no-task-match-fallback` | 5 个 session 测试（含真实 `Replay` + `exportDirectory` 导入校验）+ 5 个 cloudsync 编排测试 |
| 4 | 会话目录接入 | `DiscoverSessionDir` 自动解析 `sessions-v4/<id>` 并校验可重放；`PrepareTaskSlice` 先做冷导出成自包含目录再裁剪；`ApplyIncoming --session-root` 把会话安装到目标机 sessions-v4 根并二次校验 | `TestDiscoverSessionDir`、`TestPrepareUploadAutoDiscoveryAndManifestWindow`、`TestInstallSessionExportRefusesOverwrite` |
| 5 | 原生 APP / 推送 / 离线 / 生物识别 | `mobile-app/` Capacitor 7 工程（引导页 + 配置 + 校验脚本 + README，可直接 `npx cap add android` 出 APK）；`internal/webpush/` 自研 VAPID(ES256) + RFC 8291 `aes128gcm` 加密发送；`mobile-sw.js` 离线 shell + 通知渲染 + Background Sync 补发 IndexedDB 队列；`mobile.html` WebAuthn 平台认证器解锁 | Web Push 4 个测试（含加解密 round-trip、JWT 验签、真实 POST 请求）；`TestPushEndpoints`（httptest push service 全链路）；`node scripts/verify.mjs`；3 个前端脚本 `node --check` |
| 6 | 跨机镜像帧 | `internal/cloudsync/mirror.go`：序号单调校验、去重、gap 检测、SHA-256 帧摘要、ring buffer 补帧，默认心跳窗口 **3 分钟**（原 30s 是同机假设）；`internal/serve/cloud_ops_http.go` 提供 `/mirror/publish|stream|replay|status`；`pondera cloud watch --url … --relay …` 把云端 SSE 封帧转发到本地只读旁观 | 5 个 mirror 单测 + 3 个 serve 端点测试（含 gap 409、篡改拒绝） |
| 7 | 成本闸门 | `internal/costgate/`：持久账本、价目表（可覆盖）、单会话/每日/单任务 USD 上限、并发数、单回合超时、fail-closed `Check`；`/submit` 前置 `costGatedSubmit` 超限返回 429；`/cost/*` 端点 + `pondera cloud cost status|check|set|record|active|reset`；手机端收到 usage 事件自动记账 | 8 个 costgate 测试 + `TestCostEndpointsAndGate`（含 429 拦截与放行） |
| 8 | ③④ UI / 设置 | `internal/serve/settings.html`：统一设计令牌、分组侧栏、跨组搜索、常用置顶、真实读写成本/同步/推送配置；`internal/serve/mobile.html` 全量重做（气泡式消息、卡片式审批、深浅色自适应、大触控区、安全区适配），桌面 26 万行 TS 一行未动 | 2 个 embed 测试 + 全部脚本语法检查 |

**取舍**：没有做双端实时合并（内核强单写者，agent 副作用是文件系统而非可合并文本）；桌面前端仍是原样，新增的是 Web 端 UI；③④ 采用「新增统一 Web 设置页 + 重做手机端」而非推倒重来，避免触发大量回归。

### 仍然存在的边界（不属于上述 8 项）

- **磁盘配额**：成本闸门目前管钱、并发、回合时长，不管云主机磁盘用量。需要的话加一个 `du` 检查钩子即可。
- **FCM 原生推送**：APK 内页面由云端托管，Web Push 已可用；若要求 WebView 被系统杀掉后仍能收到系统级推送，需要再加 `@capacitor/push-notifications` 与云端 FCM 发送分支。
- **视觉回归测试**：仓库原本没有 CI 与截图基线；本轮补了 CI（build / vet / 全部新包测试 / 前端语法 / Capacitor 校验 / gofmt），但没有引入 Playwright 截图对比。
- **跨机搬运仍是一次显式命令**：按用户要求保持「手动开关」，没有后台守护进程，也不会自动同步。

---

## 7. 工程约束（沿用方案里的判断）

- 这是 rebrand fork、无上游 CI、31 次提交。本轮**所有新代码集中在新包**（`internal/cloudsync/`、`internal/messageops/`）和新文件，对内核只改了 `serve.go` 的路由注册与 `cli.go` 的一行分发，便于将来与上游合并。
- 本地 Windows 无 OS 级沙箱；云端 Linux 容器反而更安全，但不要依赖 `hostGuard` 当认证（它只防 DNS rebinding）。
- 云端要执行 agent 就必须能读到源码，**端到端加密在此场景不可行**；靠私有网 + 静态加密 + 密钥隔离 + 排除表 + 可随时销毁云上工作区来控制风险。
- `credential_mode = "local-proxy"` 与"电脑不用开着"冲突，手机场景必须用 `remote`。

---

## 8. 阶段 1 新增/修改文件清单

阶段 2 新增的文件与命令见第 9 节。

**新增**
```
internal/cloudsync/policy.go          选择状态、原子持久化、host-id
internal/cloudsync/identity.go        HostID 映射、版本向量、陈旧检测
internal/cloudsync/manifest.go        TaskBundle v1 结构、封印、路径安全
internal/cloudsync/bundle.go          打包 / 解包 / 逐文件校验 / 排除规则
internal/cloudsync/workspace.go       git bundle / diff / untracked / 还原
internal/cloudsync/cloudsync.go       PrepareUpload / CommitUpload / ApplyIncoming
internal/cloudsync/cloudsync_test.go  11 个单元测试
internal/messageops/boundary.go       消息 → 回合边界解析、校验
internal/messageops/boundary_test.go
internal/serve/messages_http.go       /messages/retract|edit、/message-targets
internal/serve/cloud_http.go          /mobile、/cloud/*
internal/serve/cloud_http_test.go
internal/serve/mobile.html            手机端 PWA
internal/cli/cloud.go                 pondera cloud 子命令
.github/workflows/ci.yml
DEVLOG.md
```

**修改（各 1 处）**
```
internal/cli/cli.go                  注册 cloud 子命令
internal/serve/serve.go              注册 6 个新路由
```

---

## 9. 阶段 2 新增文件 / 使用方式

**新增 Go**
```
internal/session/slice_export.go         任务级 V4 log 裁剪 + 冷导出 + 空导出骨架
internal/session/slice_export_test.go
internal/cloudsync/taskslice.go          会话目录发现、任务窗口、会话安装
internal/cloudsync/taskslice_test.go
internal/cloudsync/quiesce.go            自动 quiesce（/status + lease + drain）
internal/cloudsync/quiesce_test.go
internal/cloudsync/transport.go          Transport 抽象 + filesystem 适配器 + push/pull 编排
internal/cloudsync/transport_test.go
internal/cloudsync/mirror.go             跨机镜像帧（序列/去重/gap/摘要/心跳/buffer）
internal/cloudsync/mirror_test.go
internal/costgate/costgate.go            成本闸门账本
internal/costgate/costgate_test.go
internal/webpush/webpush.go              VAPID + RFC 8291 aes128gcm
internal/webpush/webpush_test.go
internal/serve/cloud_ops_http.go         mirror / cost / push / settings / sw 端点
internal/serve/cloud_ops_http_test.go
internal/cli/cloud_transfer.go           SSH/SFTP 适配器 + cloud push/pull
internal/cli/cloud_ops.go                cloud watch / cloud cost
```

**新增前端与工程**
```
internal/serve/mobile.html              重做的手机端（离线队列/生物识别/推送/成本）
internal/serve/mobile-sw.js             Service Worker
internal/serve/settings.html           统一设置页
mobile-app/www/index.html              Capacitor 引导页
mobile-app/capacitor.config.json
mobile-app/package.json
mobile-app/scripts/verify.mjs
mobile-app/README.md
mobile-app/.gitignore
```

**修改（最小化）**
```
internal/cli/cloud.go                  push/pull/watch/cost 分发；pack 自动 quiesce；apply 会话安装
internal/serve/serve.go                注册 20 个新路由；/submit 挂成本闸门
internal/cloudsync/cloudsync.go        会话目录发现 + 任务窗口 + 会话安装
internal/cloudsync/bundle.go           manifest 带 TaskWindow
internal/cloudsync/manifest.go         Manifest.TaskWindow
.github/workflows/ci.yml               覆盖全部新包与前端
```

### 常用命令

```bash
# 一键上云（本地演练，不接 SSH，用子进程模拟云端）
pondera cloud push <session> --local-transport /tmp/remote --remote-home /tmp/cloud-home \
  --cloud-bin ./pondera --remote-dir incoming --quiesce force

# 真实云主机（复用 [remote.hosts] 里的 SSH 配置、TOFU 主机密钥、SFTP）
pondera cloud push <session> --to my-cloud --remote-dir /srv/pondera/incoming \
  --session-root /srv/pondera/.pondera/sessions-v4

# 拉回本地继续开发
pondera cloud pull --from my-cloud --remote-dir /srv/pondera/out

# 本地实时旁观云端任务（跨机镜像帧）
pondera cloud watch <session> --url https://cloud:8787 --token <t> \
  --relay http://127.0.0.1:8787 --relay-token <local-t>

# 成本闸门
pondera cloud cost status
pondera cloud cost set --session-usd 5 --day-usd 20 --jobs 2
pondera cloud cost check --session <id>
```

### 部署顺序

1. 云主机 `credential_mode = "remote"`，`pondera serve` 常驻（项目原有 `internal/remote/bootstrap` 已支持，电脑无需开机）。
2. 本地 `pondera cloud pin <session> --task <kind>:<id>` 选中要上云的会话 + 任务（未选中的永远纯本地）。
3. `pondera cloud push …` 一键完成；手机访问 `https://<host>/mobile#token=<t>` 或装 APK。
4. 需要回本地时 `pondera cloud pull …`，所有权回到本地，单写者保证不冲突。
