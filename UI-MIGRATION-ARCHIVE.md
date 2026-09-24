# 影策 UI 改动存档与上游迁移方案

> 生成时间：2026-09-22
> 本地基线：`88d5ee93` "Yingce custom baseline (upstream v1.5.7 + custom features)"
> 上游仓库：https://github.com/ddcat-ai/open-ai-canvas
> 上游最新 tag：**v1.5.7.1**（预览版，本地已拉取；GitHub Releases 最新发布为 v1.5.7，2026-09-20）
> 补丁存档目录：`.local/ui-archive/`（v1.5.7 → 本地 HEAD 的完整差异）

---

## 一、版本对照结论

| 项 | 版本 | 说明 |
| --- | --- | --- |
| 上游基线 | v1.5.7 | 本地定制代码的出发点 |
| 本地现状 | v1.5.7 + 定制（单 commit） | 182 个文件变更，其中 `web/` 前端 30+ 文件 |
| 上游最新 | **v1.5.7.1** | 相对 v1.5.7 前端改了 113 个文件（+2805/-705），含 Live2D 形象、管理后台密度/主题调整、模型选择器价格与标签等 |

迁移目标 = 在 v1.5.7.1 的代码上复现本地的 UI 定制。

---

## 二、本地 UI 改动清单（按类别）

### A. 工作台壳层与布局几何（纯 UI，迁移核心）

**改动意图：侧栏极简版。** 折叠后 64px 图标轨、顶栏按需消失、内容区放宽到 1920px、底部恢复「管理员后台/设置」入口、折叠态不再显示登录入口和头像。

| 文件 | 改动 |
| --- | --- |
| `web/src/components/layout/workspace-top-bar.tsx` | 顶栏大幅精简：移除面包屑、积分 pill、公告中心、主题切换、账号菜单，仅保留 ExtensionSlot |
| `web/src/styles/workspace-product.css` | ① `--product-content-max: 1480px → 1920px`；② 新增规则：扩展槽为空时整个顶栏 `display: none`（`:has(.app-workspace-topbar-extension:empty)`）；③ 64px 收起轮 padding（4px 5px 8px） |
| `web/src/styles/globals.css` | ① `--workspace-sidebar-collapsed-width: 86px → 64px`；② `--workspace-reference-topbar: 64px → 48px`；③ 移动端菜单恢复显示 |
| `web/src/components/layout/workspace-sidebar-nav.tsx` | 底部 footer 恢复「管理员后台/设置」入口；折叠态头部改为 AnimatedThemeToggler（主题切换替代展开按钮）；折叠态隐藏登录/头像/签到；新增「账号池」导航项；收起态 footer padding 调整 |
| `web/src/components/layout/workspace-sidebar-state.ts` | 工作台与管理后台侧栏折叠状态拆分为独立 controller（独立 storage key + 事件） |
| `web/src/components/layout/app-top-nav.tsx` | 移除 toggleSidebar 逻辑，顶栏不再控制侧栏开合 |
| `web/src/pages/canvas/canvas-project-top-bar.tsx` | 画布顶栏移除积分 pill（钱包余额显示） |
| `web/src/pages/admin/components/admin-shell.tsx` | 改用 admin 独立侧栏状态 API |
| `web/src/hooks/use-workspace-logout.ts` | 本地单机模式下跳过退出登录 |

### B. 模型选择器分组与文案（UI + 轻逻辑）

**改动意图：一级分组一律按渠道聚合（品牌名 = 渠道名），不把直连系统模型拆成独立品牌；模型行去掉「N 个渠道/模型」后缀，不再重复拼接渠道名。**

| 文件 | 改动 |
| --- | --- |
| `web/src/lib/model-picker-groups.ts` | `groupModelsForPicker` 重写：删除 `isDirectSystemModel` 拆分分支，按渠道直接聚合，scope 按渠道 system/自定义标注 |
| `web/src/components/model-picker.tsx` | 品牌卡片文案去掉「渠道/模型」后缀；`pickerModelDisplayName` 不再拼接渠道名后缀 |
| `web/src/lib/model-capabilities.ts` | 新增账号池维度限制定义（时长/条数/参考图模式）与 `videoLimitSummary` 摘要（供选择器与参数面板展示） |
| `web/test/model-picker-groups.test.ts` | 测试同步更新 |

### C. 新功能页面 UI（依赖本地后端 API，整体迁移或谨慎裁剪）

| 文件 | 说明 |
| --- | --- |
| `web/src/pages/accounts/index.tsx`（新增 827 行） | 「账号池」管理页（豆包/Dola 账号绑定与配额） |
| `web/src/pages/settings/network-proxy-pane.tsx`（新增 383 行） | 设置页「网络代理」面板 |
| `web/src/pages/settings/index.tsx` | 设置分区新增 `network`（Globe 图标） |
| `web/src/pages/settings/channel-settings-pane.tsx` | 渠道设置扩展（+279 行，账号池接口类型） |
| `web/src/constant/navigation-tools.ts` | 新增 `accounts` 导航项（IdCard 图标） |
| `web/src/router.tsx`、`web/src/lib/workspace-route-modules.ts`、`web/src/components/layout/workspace-command-palette.tsx` | 注册 `/accounts` 路由、懒加载与命令面板入口 |
| `web/src/services/api/doubao-accounts.ts`、`network-proxies.ts`、`webrelay-accounts.ts`、`video-provider-doubao.ts` 等 | 对应 API 模块（依赖本地后端 doubao/webrelay/代理接口） |

### D. 本地单机模式（桌面版）登录相关 UI

| 文件 | 说明 |
| --- | --- |
| `web/src/pages/auth/login.tsx` | localMode 下登录页替换为「正在进入影策工作台」加载态并轮询自动进入；冷启动重试（约 25s） |
| `web/src/components/auth/auth-session-hydrator.tsx`、`require-auth.tsx`、`web/src/lib/user-session.ts`、`web/src/stores/use-user-store.ts` | `localMode` 状态贯穿：会话重试、本地模式不跳登录、内置豆包/Dola 账号池渠道注入 |

---

## 三、与上游 v1.5.7.1 的冲突风险矩阵

两边（本地定制 & 上游 v1.5.7→v1.5.7.1）都改过的文件，迁移时需要人工合并：

| 文件 | 本地改动 | 上游改动 | 风险 |
| --- | --- | --- | --- |
| `web/src/components/model-picker.tsx` | 文案/后缀精简（~10 行） | 价格展示、标签（53 行） | **高** |
| `web/src/lib/model-picker-groups.ts` | 分组逻辑重写（38 行） | 小调整（9 行） | **中** |
| `web/test/model-picker-groups.test.ts` | 测试重写（44 行） | 新增价格用例（50 行） | **中** |
| `web/src/pages/admin/components/admin-shell.tsx` | admin 侧栏状态独立（8 行） | 密度组件/边框收敛（47 行） | **中** |
| `web/src/styles/globals.css` | 3 处 token（8 行） | 29 行（不同区域） | 中（同文件不同 hunk，大概率自动合并） |
| `web/src/pages/settings/index.tsx` | 新增 network 分区（19 行） | 7 行 | 低 |
| `web/src/lib/user-session.ts` | +160 行（豆包/Dola 渠道、localMode） | 仅 1 行 | 低 |
| `web/src/stores/use-config-store.ts` | +11 行 | +2 行 | 低 |
| `web/src/styles/workspace-product.css` | +11 行 | -1 行 | 低 |

**上游新增、本地未动的 UI 面（迁移后自动获得）：** Live2D 形象（`live2d-avatar.tsx`）、管理后台 `admin-density`、`admin-chrome.css`/`admin-tokens.css` 重构、模型标签（`model-tags`）、tooltip/floating dock 修复、素材批量删除工具条等。注意：上游在 v1.5.7.1 里也改了 `workspace-product.css`（删 1 行），与本地「顶栏隐藏」规则同文件，需确认保留。

---

## 四、迁移操作步骤

补丁存档（`.local/ui-archive/`，均为 `git diff v1.5.7 HEAD` 格式，可 `git apply -3`）：

| 文件 | 内容 | 大小 |
| --- | --- | --- |
| `00-full-web-ui.patch` | 全量前端差异（web/src + web/test） | 225 KB |
| `01-ui-shell.patch` | 壳层/样式/登录页/画布顶栏/admin 壳 | 40 KB |
| `02-model-picker.patch` | 模型选择器分组与能力展示 | 23 KB |
| `03-feature-pages-and-services.patch` | 账号池/网络代理页面与 services、stores、路由 | 163 KB |

**推荐路径（保留全部本地功能 + 复现 UI）：**

```powershell
$git = "C:\Users\Administrator\.workbuddy\binaries\PortableGit\versions\1.2.0\cmd\git.exe"
& $git -C <仓库> fetch upstream --tags     # 修复网络环境后执行，确认拿到 v1.5.7.1 之后的 main
& $git -C <仓库> checkout -b sync-v1.5.7.1 v1.5.7.1
& $git -C <仓库> merge main                # main = 本地定制基线；按第三节矩阵逐个解冲突
```

- 冲突集中在 `model-picker.tsx` / `model-picker-groups.ts`：以**上游新版价格与标签结构为骨架**，把本地「按渠道聚合、去后缀文案」的规则重新落进去，不要直接取任一整边。
- `globals.css` / `workspace-product.css`：解冲突后确认三个几何 token（64px 侧栏、48px 顶栏、1920px 内容宽）与「空扩展槽隐藏顶栏」规则仍在。
- `user-session.ts` / 账号池相关：整段本地代码照搬即可（上游几乎没动）。

**备选路径（只迁移 UI，不带本地功能）：** 在 v1.5.7.1 分支上 `git apply -3 .local/ui-archive/01-ui-shell.patch` + `02-model-picker.patch`，跳过 03。注意 03 里的 `/accounts`、网络代理面板依赖本地后端 doubao/webrelay API，不带后端时不要单独迁移。

**本机 git 环境注意：** 当前 PowerShell 里裸 `git fetch` 报 `'remote-https' is not a git command`，需设置 `GIT_EXEC_PATH=C:\Users\Administrator\.workbuddy\binaries\PortableGit\versions\1.2.0\mingw64\libexec\git-core` 后重试；若仍失败，可从 GitHub 下载 v1.5.7.1 tarball 解压后比对。

---

## 五、迁移后验证清单

```powershell
cd web
bun run build          # 类型检查 + 生产构建
bun test model-picker-groups model-picker-prices model-picker-style-source
bun run lint           # UI 退场规则
```

浏览器验收点（明暗两套主题）：
1. 侧栏收起后为 64px 图标轨，折叠态顶部是主题切换按钮，无登录/头像残留；
2. 列表页（短剧/画布/资产）顶栏整体消失，内容区宽度上限 1920px；
3. 模型选择器一级分组按渠道聚合，模型行无「N 个渠道」后缀、无重复渠道名；
4. 管理后台侧栏折叠状态与工作台互不影响；
5. 画布顶栏无积分 pill；管理后台新边框/密度（上游 v1.5.7.1 特性）正常；
6. 若迁移了 03：`/accounts` 页与设置页网络代理面板可打开、接口不 404。
