# CLIProxyAPI Pro Management 定制说明

这是 upstream `router-for-me/Cli-Proxy-API-Management-Center` 的定制层。

本目录不保存 upstream 应用完整源码，而是维护 overlay 文件和补丁脚本，在本地开发或 GitHub Actions 发布构建时应用到干净的 upstream checkout 上。

## 定制内容

### 请求监控页面

新增顶级监控路由：

```text
/monitoring
```

该页面消费 customized `cliproxyapi-pro-core` 后端 usage API。页面会加载初始 usage 快照，并通过增量事件轮询或 SSE usage 流跟进更新，提供：

- 请求总量和成功/失败统计
- 成功率和延迟摘要
- 输入、输出、缓存、reasoning 和总 token 汇总
- 基于可配置模型价格的成本估算
- 今日、7 天、14 天、30 天、全部数据的时间范围过滤
- 搜索，以及账号/provider/model/channel/status 过滤
- 自动刷新间隔选择和手动刷新
- 可排序的账号汇总表
- 可展开的账号行，展示模型花费明细
- 账号级配额刷新和配额展示
- 实时请求表，展示最近成功/失败模式条
- 对请求元数据中的敏感 token-like 文本进行遮罩

账号汇总表和实时请求表会在模块内部滚动，避免长历史记录把整个页面撑长。

### 模型价格持久化

模型价格设置通过后端 SQLite API 持久化，不再作为普通浏览器本地状态保存：

- `GET /usage/model-prices`
- `PUT /usage/model-prices`
- `GET|PUT|DELETE /usage/model-price-rules`
- `POST /usage/model-prices/sync`
- `GET /usage/model-prices/sync-status`
- `POST /usage/model-prices/recalculate`

如果后端还没有保存价格，页面可从旧 `localStorage` 价格设置做一次性迁移。之后正常读写都走 SQLite。

价格规则以 `(provider, model)` 为键，支持 input、output、cache read、cache write、多个上下文长度阶梯和 service tier 覆盖。页面可手动从 models.dev 同步，也可在监控设置中启用定期同步；同步只保存请求历史中实际出现过的模型，手动规则默认锁定且不会被自动覆盖。

成本在后端按单次请求选择阶梯并固化到 usage event，聚合接口直接累加事件成本。模型价格和完整规则会作为 `model_prices` 元数据记录参与 JSONL 导入导出。

### SQLite 配额持久化

配额快照通过后端 usage service 持久化：

- `GET /usage/quota-cache`
- `PUT /usage/quota-cache`
- `DELETE /usage/quota-cache`

UI 会在主布局中启动 `QuotaPersistenceBootstrap`，把已保存的配额快照预加载到 Zustand quota store，并把成功的配额检查同步回 SQLite。Quota cache entries 也会作为 `quota_cache` 元数据记录参与 usage JSONL 导入导出。

支持的配额 provider：

- Antigravity
- Claude
- Codex
- Kimi

当 `src/config/features.ts` 中的特性开关启用时，配额卡片还会显示缓存时间戳，并支持成功状态下的单卡刷新。

### 账号巡检页面

新增顶级账号巡检路由：

```text
/account-inspection
```

页面负责控制和展示后端巡检，浏览器不再直接执行探测。认证文件页面还会在没有显式状态消息时显示巡检写入的 `last_error` 健康消息。后端可巡检：

- Antigravity
- Claude
- Codex
- Kimi

主要能力：

- 选择目标 provider
- 配置 workers、delete workers、timeout、retries、用量阈值和抽样数量
- 后端巡检的运行、暂停、继续和停止控制
- 后端调度启用和间隔配置
- 通过后端状态轮询展示进度、摘要卡片和结果表
- 通过后端 WebSocket/WSS 流接收日志和实时状态
- 建议操作：保留、删除、禁用、启用
- 通过后端手动执行单个建议操作或全部建议操作
- 刷新令牌和单账号重检 toast 显示真实业务结果，例如刷新成功/失败、账号异常、额度耗尽或健康状态
- 针对额度耗尽禁用、额度恢复启用、账号错误禁用/删除的后端可选自动执行策略
- 根据后端巡检结果刷新配额快照

页面依赖的后端调度/状态/控制接口：

- `GET /account-inspection/schedule`
- `GET /account-inspection/status`
- `GET /account-inspection/logs`（WebSocket/WSS 日志和状态流）
- `PUT /account-inspection/schedule`
- `POST /account-inspection/run`
- `POST /account-inspection/pause`
- `POST /account-inspection/resume`
- `POST /account-inspection/stop`
- `POST /account-inspection/actions`

在完整 management API 前缀下，后端暴露为 `/v0/management/account-inspection/...`。

### 路由策略页面

新增顶级路由策略页面：

```text
/routing
```

页面集中管理 upstream 路由配置与 Pro 请求状态保护，包含三个视图：

- 全局路由：轮询/优先填满、会话粘性和 TTL、请求重试与账号切换、冷却和冷却状态持久化、瞬时错误冷却、配额回退、Codex 身份混淆。
- 提供商保护：只展示当前已有 API 配置或凭据的受支持提供商，并分别配置开关、HTTP 状态码、连续确认门槛、确认窗口、429 配额证据、自动解除和兜底禁用时长。
- 运行状态：查看当前由请求保护策略禁用的账号和最近事件；HTTP 状态以可点击徽章展示，完整原因和上下文收纳在详情弹窗中，并支持手动解除单个账号。

页面使用：

- `GET /routing-policy`
- `PUT /routing-policy`
- `POST /routing-policy/release`

状态码由用户配置，后端会过滤到 `100-599`、去重并排序。保护功能默认关闭；`observe` 只记录匹配事件，`enforce` 才会实际禁用账号。自动解除只处理带有 request-protection 归属元数据的账号。

### 支撑性 API 与类型补丁

`apply_customizations.py` 还会 patch upstream 文件以增加：

- `/monitoring`、`/account-inspection` 和 `/routing` 路由。
- 侧边栏导航文案和图标。
- 从 `monitoring-locales.json` 合并的多语言文案。
- monitoring/account inspection 使用的 `usageStatisticsEnabled` 和 `clean` 配置类型。
- `authFilesApi.patchFile`、`setStatusWithFallback` helper。
- `accountInspection` service export。
- `Select` 的 `triggerClassName` 和 `dropdownClassName` props。
- `maskSensitiveText` 工具函数。
- quota state 类型和 success state 中的 `cachedAt` 字段。

请求监控采用“首屏快照 + SSE 增量 + cursor 追平”同步链路，并按事件 ID 去重。趋势图、模型排行和 API Key 排行优先使用 `/usage/aggregates` 服务端聚合，接口不可用时自动回退到本地明细计算。页面隐藏时会暂停 SSE 和 React 增量刷新，回到前台后再按 cursor 补齐；标题区会展示实时、重连、后台暂停、异常和最近事件时间。

## 目录结构

- `overlay/` — 直接复制到 upstream checkout 的新增/覆盖文件。
- `overlay/src/pages/MonitoringCenterPage.tsx` — 请求监控页面。
- `overlay/src/pages/AccountInspectionPage.tsx` — 账号巡检页面。
- `overlay/src/pages/RoutingPolicyPage.tsx` — 路由策略和请求状态保护页面。
- `overlay/src/features/monitoring/` — 监控与巡检逻辑。
- `overlay/src/extensions/quota/` — SQLite 配额持久化集成。
- `overlay/src/services/api/` — 新增 API clients。
- `overlay-replacements.json` — 对有意覆盖 upstream 同路径文件的 full-file replacements，同时记录已审阅的 upstream 与 overlay SHA-256。
- `monitoring-locales.json` — 合并进 upstream locale 文件的多语言文案。
- `apply_customizations.py` — 将全部定制应用到目标 upstream checkout。
- `apply.sh` — `apply_customizations.py` 的 shell 包装脚本。
- `quota-persistence.patch` — 历史补丁文件，保留用于参考；当前构建使用 `apply_customizations.py`。

Overlay collision 预检会同时校验每个已审阅替换的两侧内容。upstream 文件或本地替换文件发生变化时，都必须显式更新 `overlay-replacements.json`；新的未审阅路径冲突会在复制任何 overlay 文件前被拒绝。

## 本地应用

在本目录中执行：

```bash
./apply.sh /path/to/Cli-Proxy-API-Management-Center
```

等价命令：

```bash
python3 apply_customizations.py /path/to/Cli-Proxy-API-Management-Center
```

目标目录必须是 upstream checkout，并包含：

- `src/`
- `package.json`

## 本地验证

应用到 upstream checkout 后执行：

```bash
npm install
npm run type-check
npm run build
```

如需不污染 upstream 工作目录，可复制到临时目录验证：

```bash
rm -rf /tmp/cpa-management-check
cp -R /path/to/Cli-Proxy-API-Management-Center /tmp/cpa-management-check
python3 /path/to/CLIProxyAPI-Pro/cliproxyapi-pro-management/apply_customizations.py /tmp/cpa-management-check
npm --prefix /tmp/cpa-management-check install
npm --prefix /tmp/cpa-management-check run type-check
npm --prefix /tmp/cpa-management-check run build
```

## GitHub Actions 发布流程

Workflow：

```text
.github/workflows/release-management.yml
```

该 workflow 不再创建独立 management release。它只在 management upstream 更新、当前 latest release 缺少 `management.html`，或手动触发时，重建并覆盖当前仓库 latest release 中的 `management.html`。

流程：

1. 检查当前仓库 latest release。
2. 检查 upstream `router-for-me/Cli-Proxy-API-Management-Center` 最新 release。
3. 读取 latest release notes 中记录的 management upstream 版本。
4. 如果 upstream 更新、latest release 缺少 `management.html`，或 workflow 手动触发，则 checkout upstream 最新 release tag。
5. 从 `cliproxyapi-pro-management/apply.sh` 应用本目录定制层。
6. 执行 `npm ci` 和 `npm run build`。
7. 将 `dist/index.html` 重命名为 `management.html`。
8. 上传并覆盖当前 latest release 中的 `management.html`。
9. 更新 release notes 中的 management 版本映射和 upstream release notes。
10. 清理旧 workflow runs。

这样 `remote-management.panel-github-repository=https://github.com/ssfun/CLIProxyAPI-Pro` 始终可以通过 GitHub `/releases/latest` 获取最新 `management.html`。

## 后端依赖

这些前端定制依赖 customized `cliproxyapi-pro-core` 后端在 management API 前缀下暴露 usage 和账号巡检接口：

- `/v0/management/usage`
- `/v0/management/usage/status`
- `/v0/management/usage/events`
- `/v0/management/usage/aggregates`
- `/v0/management/usage/stream`
- `/v0/management/usage/export`
- `/v0/management/usage/import`
- `/v0/management/usage/reset`
- `/v0/management/usage/quota-cache`
- `/v0/management/usage/model-prices`
- `/v0/management/usage/settings`
- `/v0/management/account-inspection/schedule`
- `/v0/management/account-inspection/status`
- `/v0/management/account-inspection/logs`
- `/v0/management/account-inspection/run`
- `/v0/management/account-inspection/pause`
- `/v0/management/account-inspection/resume`
- `/v0/management/account-inspection/stop`
- `/v0/management/account-inspection/actions`

如果未使用 customized 后端，请求监控、SQLite 持久化、模型价格和后端账号巡检相关功能会显示错误或空数据。
