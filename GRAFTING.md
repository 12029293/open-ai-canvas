# GRAFTING.md — 影策定制功能与上游更新嫁接指南

本仓库 = 上游 [ddcat-ai/open-ai-canvas](https://github.com/ddcat-ai/open-ai-canvas) + 影策定制功能。
git 基线已建立：`main` 分支的定制基线提交以**上游 v1.5.7 tag** 为父提交，因此每次上游更新都是标准三方合并。

```
git remote -v
# upstream  https://github.com/ddcat-ai/open-ai-canvas
```

## 标准更新流程

```bash
git fetch upstream
git merge upstream/main
# 解决冲突后：
git commit --no-edit
```

冲突处理三原则：

1. **纯新增文件**（见下表）永远保留本地，不接受上游删除。
2. **migrations.go 是硬冲突高发区**：上游迁移编号会与定制迁移 v32-v35 撞号。
   每次合并后必须把上游新增迁移**重编号**到当前最大版本之后（本次已把上游 v32 channel_model_tags 改为 v36），并同步上调 `CurrentSchemaVersion`。
3. **其余共享文件**按行合并，合并后对照下方"触点表"逐一核对定制接线还在。

## 纯新增文件（合并时直接保留，无冲突风险）

### 1. 豆包账号池（QR 登录 / 额度 / 多站点 / 代理绑定）

- `backend/internal/doubao/`：pool.go（池与冷却）、generate.go、client.go、qrlogin.go、quota_reset.go、nickname.go、trace.go、fallback.go（母片通道 = 真无水印主路径）、nomark.go / image_nomark.go（URL 改写去水印兜底）及全部测试
- `backend/internal/model/doubao_account.go`、`doubao_pool_meta.go`
- `backend/internal/handler/doubao_accounts.go`
- `backend/internal/app/doubao_bridge.go`、`doubao_channel.go`
- `web/src/services/api/doubao-accounts.ts`、`video-provider-doubao.ts`
- `web/src/stores/use-doubao-account-store.ts`
- `web/src/pages/accounts/index.tsx`（账号池管理页）

### 2. 网页中继池 webrelay（DeepSeek / 千问）+ 网络代理（IP 轮换）

- `backend/internal/webrelay/`：pool.go（取号/冷却/失败分类）、deepseek.go、qwen.go、pow*.go、client.go
- `backend/internal/netproxy/netproxy.go`：`LookupURL`（动态代理 `{sid}` 每次取号新出口 IP = 轮流更换 IP）、`LookupURLStable`（按账号 ID 派生固定 sid = 账号级固定 IP，豆包池采用）
- `backend/internal/model/network_proxy.go`、`web_relay_account.go`
- `backend/internal/handler/network_proxies.go`、`webrelay_accounts.go`、`webrelay_capture.go`
- `backend/internal/app/network_proxy.go`、`webrelay_bridge.go`、`webrelay_channel.go`、`webrelay_browser_import.go`、`webrelay_cdp_import.go`、`webrelay_qwen_cdp.go`
- `backend/internal/outbound/proxy.go`（ctx 注入代理的 HTTP 客户端）
- `web/src/services/api/webrelay-accounts.ts`、`network-proxies.ts`
- `web/src/pages/settings/network-proxy-pane.tsx`

### 3. webchat2api 本地中转插件

- `plugin-packages/webchat2api.yingce-plugin` + `plugin-packages/webchat2api/`
- 对接 NZX2007/webchat2api 四个本地服务（qwen/doubao/mimo/deepseek），声明式 OpenAI 协议
- 使用前提：`CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS=127.0.0.1,localhost` 精确放行本机上游
- 注意：webchat2api 进程的出口 IP 由 Python 侧决定（HTTPS_PROXY / 多实例多渠道），影策账号池的代理绑定只作用于 webrelay/doubao 直连上游的流量

### 4. 桌面版与本地化

- `build-desktop.bat`、`start.bat`
- `backend/cmd/server/desktop_mode_on.go` / `desktop_mode_off.go`（build tag `desktop`）
- `backend/internal/auth/local_mode.go`（`CANVAS_LOCAL_MODE=1` 单用户免登录）
- `backend/internal/desktopui/`、`backend/internal/webui/webui.go` / `stub.go`

## 共享文件触点表（合并后必查）

| 文件 | 定制内容 |
| --- | --- |
| `backend/internal/handler/api.go` | `RegisterNetworkProxyRoutes` / `RegisterDoubaoAccountRoutes` / `RegisterWebRelayAccountRoutes` 三个注册调用 |
| `backend/internal/database/migrations.go` | 定制迁移 v32-v35 + 上游迁移重编号（见冲突原则 2） |
| `backend/cmd/server/main.go` | `/webrelay-capture` 路径后缀特殊处理 |
| `backend/internal/app/provider*.go`、`task_creation.go` | `doubao-pool` / webrelay 渠道协议路由 |
| `web/src/services/api/video.ts` | `interfaceType === "doubao-pool"` 时走 `createDoubaoVideoTask`，`provider === "doubao-pool"` 轮询分发 |
| `web/src/router.tsx` | `/accounts` 路由 |
| `web/src/pages/settings/index.tsx` | 网络代理设置入口（Globe 图标项） |
| `web/src/stores/use-config-store.ts` | `videoWatermark` / `videoSeconds` 配置键归一 |
| `web/src/lib/model-capabilities.ts` | 豆包池 15 秒/30 秒时长档位（seedance2 → `maxSeconds: 15`）、账号池能力摘要文案 |

## 合并后验证

```bash
cd backend && go build ./... && go test ./...
cd web && bun run build
```

## 嫁接记录

- 2026-09-22：基线建立（v1.5.7 + 全部定制），合并上游 v1.5.7.1（28 个提交）。
  内容冲突 5 处（.gitignore、migrations.go、model-picker-groups.ts、settings/index.tsx、model-picker-groups.test.ts）+ 文档 modify/delete 6 处，全部解决；上游 channel_model_tags 迁移 v32 → v36。
