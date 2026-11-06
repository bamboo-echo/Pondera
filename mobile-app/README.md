# Pondera Mobile · 原生 APP（Capacitor 壳）

给自己在手机上用的原生 APP。**它不重写业务逻辑**：APP 内是一个引导页，打开后跳转到你自己云服务器上的 `/mobile` PWA。这样做有四个实际好处：

1. 页面永远跟云端 serve 一起更新，不用重新安装 APK；
2. origin 就是云端，不存在跨域 / CORS 问题；
3. Service Worker、Web Push、WebAuthn 都在安全上下文（HTTPS）里正常工作；
4. 桌面端 26 万行前端代码一行都不用动。

## 前置条件

云主机（Linux，建议 Tailscale 私有网）：

```bash
# 1. 编译并上传 pondera（云主机与本地同架构时可直接用 fork-windows-build 之外的普通构建）
go build -o /usr/local/bin/pondera ./cmd/pondera

# 2. 配置 credential_mode = "remote"：API Key 只放在云主机，电脑不需要开机
#    ~/.pondera/pondera.toml
#    [remote]
#    ...
#    credential_mode = "remote"

# 3. 常驻启动 headless serve（项目已有 internal/remote/bootstrap，也可用 systemd）
pondera serve --port 8787 --host 0.0.0.0
# 记下启动时打印的 token，或 /auth/token 握手得到的 token
```

> 手机端必须能访问这个地址。**不要直接暴露公网**，用 Tailscale / WireGuard / 反向代理 + HTTPS。
> Web Push 与生物识别要求 HTTPS 安全上下文；自建证书请用受信任的证书或 Tailscale HTTPS。

## 构建 APK

需要 Node.js ≥ 20、Android Studio（SDK 34+）、JDK 17。

```bash
cd mobile-app
npm install
npm run verify                 # 校验工程配置
npx cap add android            # 首次：生成 android/ 原生工程
npx cap sync android
npx cap open android           # Android Studio 里 Build > Build APK(s)
# 或者命令行：
npm run build:apk              # android/app/build/outputs/apk/debug/app-debug.apk
```

装到手机后：打开 APP → 填云端地址（例如 `https://pondera.tailnet.ts.net:8787`）与 token → 点「连接并进入手机端」。配置只存在本机 `localStorage`。

## 手机端能做什么

| 能力 | 实现位置 |
| --- | --- |
| 发指令 / 看 SSE 实时输出 / 停止回合 | `/mobile` |
| 审批工具调用 | `/approve` + 页面卡片 |
| 撤回消息 / 编辑已发消息重跑 | `/messages/retract`、`/messages/edit` |
| 查看/切换云同步选择（开关） | `/cloud/selections`、`/cloud/pin`、`/cloud/unpin` |
| 离线指令队列（断网排队、幂等补发） | IndexedDB `pondera/outbox` + Service Worker Background Sync |
| 任务完成 / 待审批推送 | Web Push：`/push/vapid` + `/push/subscribe` + `/push/notify`（VAPID + RFC 8291） |
| 设备生物识别解锁 | WebAuthn 平台认证器，本地开关 |
| 成本闸门与花费展示 | `/cost/status`、`/cost/record`；超限 `/submit` 返回 429 |
| 完整设置界面 | `/settings`（分组导航 + 搜索） |

在 APP 内点右上角 ⚙ 可以直接进入完整设置页。若想完全不装 APK，直接用浏览器访问 `https://<host>/mobile#token=<token>` 并「添加到主屏幕」，功能一致（这就是 PWA 模式）。

## 原生壳 vs PWA

| | PWA | Capacitor APK |
| --- | --- | --- |
| 安装 | 添加到主屏幕 | 安装 APK |
| 图标 / 启动屏 | 系统生成 | 可自定义 |
| 后台推送 | 支持（Web Push） | 支持（同 PWA，页面由云端托管） |
| 生物识别 | WebAuthn | WebAuthn |
| 离线 | Service Worker | 同 PWA |
| 更新 | 自动 | 自动（页面在云端） |

如果以后需要**真正的系统级原生推送**（FCM，WebView 被杀也能收），再加 `@capacitor/push-notifications`，并在云端的 `/push/notify` 里增加 FCM 发送分支；当前 Web Push 对个人使用已经足够。

## 目录

```
mobile-app/
├── www/index.html           本地引导页（填地址 + token）
├── capacitor.config.json    appId / allowNavigation / 状态栏
├── package.json             Capacitor 7 依赖与构建脚本
├── scripts/verify.mjs       工程配置校验
└── README.md
```

`android/` 由 `npx cap add android` 生成，不入库（见 .gitignore）。
