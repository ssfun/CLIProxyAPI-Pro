# CLIProxyAPI Pro Management 定制说明

这是 upstream `router-for-me/Cli-Proxy-API-Management-Center` 的定制层。

本目录不保存 upstream 应用完整源码，而是维护 overlay 文件和补丁脚本，在本地开发或 GitHub Actions 发布构建时应用到干净的 upstream checkout 上。

## 定制内容

### 代理池页面

新增 `/proxy-pool` 顶级页面，管理 Core 二进制内建代理池。页面可配置 HTTP/HTTPS/SOCKS5/SOCKS5H 节点、轮询策略、权重、健康检查、隔离和故障转移，并提供批量粘贴导入、节点搜索、筛选结果批量启停、快速复制、未保存草稿测试和手动解除隔离。运行区展示成功率、失败数、活动连接、最近成功/失败、配置代次和健康检查时间。

新增 `/oauth-policy` 顶级页面，管理 Core 二进制内建 OAuth 账号策略。页面按 xAI、Codex、Claude、Gemini CLI、Antigravity 和 Kimi 标签页编辑套餐模型排除、前缀、优先级与调度权重，支持自定义套餐、`_unknown`/`_default` 回退和账号有效策略预览。旧 `/oauth-model-policy` 地址自动重定向。

页面通过原生管理 API 开关运行时代理接管，不修改 `config.yaml` 或根 `proxy-url`；带独立 `proxy-url` 的凭证会作为绕过项明确列出。两项功能配置均持久化到 usage SQLite 的 `pro_settings`，健康状态和连接统计仍属于进程级运行数据。

轮换粒度是 SOCKS5 TCP tunnel，不保证每个复用的 HTTP 请求都切换节点。默认 `fail-open=false`，避免所有代理失效时静默直连。

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
- `GET|PUT|DELETE /usage/model-price-rules`（按 model 全局生效，不传 provider）
- `POST /usage/model-prices/sync`
- `GET /usage/model-prices/sync-status`
- `POST /usage/model-prices/recalculate`

如果后端还没有保存价格，页面可从旧 `localStorage` 价格设置做一次性迁移。之后正常读写都走 SQLite。

价格规则按 model ID 全局生效，同一 model 在不同 provider 下共享规则；规则支持 input、output、cache read、cache write、多个上下文长度阶梯和 service tier 覆盖。页面可手动从 models.dev 同步，也可在监控设置中启用定期同步；同步只保存请求历史中实际出现过的模型，手动规则默认锁定且不会被自动覆盖。

成本在后端按单次请求选择阶梯并固化到 usage event，费用明细中的 `pricingMode` 会明确区分基础价格、上下文阶梯和实际命中的 service tier 覆盖；请求 tier、生效 tier 和命中的价格 tier 会分别展示。OpenAI Fast 统一保存为 `fast`，并兼容请求或响应中的 `priority`；仅记录到请求 `service_tier` 不代表覆盖价格已经生效。聚合接口直接累加事件成本，模型价格和完整规则会作为 `model_prices` 元数据记录参与 JSONL 导入导出。

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
- Gemini CLI
- Kimi
- xAI

认证文件页的非运行时账号卡片还提供“测试连接”入口。弹窗复用上游 `GET /v0/management/auth-files/models` 读取测试模型；Pro 后端在账号被禁用、上游不再注册其模型时，会让该接口回落到对应提供商的静态模型定义。随后通过 `POST /v0/management/auth-files/test` 将一次最小真实生成请求固定到当前 `auth_index`；成功时展示模型输出和耗时，失败时展示上游 HTTP 状态、错误码与脱敏后的错误详情。被禁用或处于冷却状态的账号也可独立测试，测试不会改动用户设置的禁用开关，也不会产生请求监控记录。

当 `src/pro/modules/quota/features.ts` 中的特性开关启用时，配额卡片还会显示缓存时间戳，并支持成功状态下的单卡刷新。

### 账号巡检页面

新增顶级账号巡检路由：

```text
/account-inspection
```

页面负责控制和展示后端巡检，浏览器不再直接执行探测。认证文件页面还会在没有显式状态消息时显示巡检写入的 `last_error` 健康消息。后端可巡检：

- Antigravity
- Claude
- Codex
- Gemini CLI
- Kimi
- xAI

主要能力：

- 选择目标 provider
- 配置探测总并发 `workers`（`1–8`）、单 provider 并发 `providerWorkers`（`1–4`）、操作并发 `deleteWorkers`（`1–4`）、timeout、retries、用量阈值和抽样数量
- 后端巡检的运行、暂停、继续和停止控制
- 后端调度启用和间隔配置
- 通过后端状态轮询展示进度、摘要卡片和结果表
- 通过后端 WebSocket/WSS 流接收日志和实时状态
- 建议操作：保留、删除、禁用、启用
- 通过后端手动执行单个建议操作或全部建议操作
- 单账号重检 toast 显示真实业务结果，例如账号异常、额度耗尽或健康状态
- 针对额度不足暂停调度、到期定向复查恢复、账号错误禁用/删除的后端可选自动执行策略；额度保护不覆盖人工禁用，并显示预计复查时间
- 根据后端巡检结果刷新配额快照

探测总并发作用于所有 provider，单 provider 并发在总并发之内独立约束每个 provider；普通探测、深度探测、xAI 探测和探测前 token refresh 使用同一组限制。操作并发同时作用于自动操作和手动批量操作。页面通过账号巡检 schedule API 保存这些设置，不读取或修改 `config.yaml`。

页面依赖的后端调度/状态/控制接口：

- `GET /account-inspection/schedule`
- `GET /account-inspection/status`
- `GET /account-inspection/logs`（WebSocket/WSS 日志和状态流）
- `PUT|PATCH /account-inspection/schedule`
- `POST /account-inspection/run`
- `POST /account-inspection/inspect-one`
- `POST /account-inspection/pause`
- `POST /account-inspection/resume`
- `POST /account-inspection/stop`
- `POST /account-inspection/actions`

在完整 management API 前缀下，后端暴露为 `/v0/management/account-inspection/...`。

### 调度看板页面

新增顶级调度看板页面：

```text
/routing
```

页面只读展示选择器当前不会选中的账号，不读取或修改 `config.yaml` 的全局路由配置。分桶包括额度、认证与瞬时、待复查和重叠。动作只有跳转到账号巡检；上游冷却不会从本页清除。

页面使用：

- `GET /routing-policy`

### 支撑性 API 与类型补丁

`apply_customizations.py` 还会 patch upstream 文件以增加：

- `/monitoring`、`/account-inspection` 和 `/routing` 路由。
- 侧边栏导航文案和图标。
- 从 `monitoring-locales.json` 合并的多语言文案。
- monitoring 使用的 `usageStatisticsEnabled` 配置类型；账号巡检设置仅通过 schedule API 管理。
- `authFilesApi.patchFile`、`setStatusWithFallback` helper。
- `accountInspection` service export。
- `Select` 的 `triggerClassName` 和 `dropdownClassName` props。
- `maskSensitiveText` 工具函数。
- quota state 类型和 success state 中的 `cachedAt` 字段。
- 管理中心版本卡片的“检查更新”按钮；调用后端 `POST /management-panel/check-update`，仅在 latest release 资源哈希变化时替换面板，并在实际更新后重新加载页面。

请求监控采用“首屏快照 + SSE 增量 + cursor 追平”同步链路，并按事件 ID 去重。趋势图、模型排行和 API Key 排行优先使用 `/usage/aggregates` 服务端聚合，接口不可用时自动回退到本地明细计算。页面隐藏时会暂停 SSE 和 React 增量刷新，回到前台后再按 cursor 补齐；标题区会展示实时、重连、后台暂停、异常和最近事件时间。

## 目录结构

- `overlay/` — 直接复制到 upstream checkout 的新增/覆盖文件。
- `overlay/src/pro/modules/monitoring/` — 请求监控、用量分析与备份 UI。
- `overlay/src/pro/modules/inspection/` — 账号巡检页面、状态与操作逻辑。
- `overlay/src/pro/modules/routing/` — 调度看板 UI。
- `overlay/src/pro/modules/proxyPool/` 与 `oauthPolicy/` — 独立业务模块页面及 API。
- `overlay/src/pro/modules/quota/` — SQLite 配额持久化、排序与 provider 扩展。
- `overlay/src/pro/modules/*/manifest.tsx` — 各业务模块声明自己的路由、导航和启动副作用；`registry.tsx` 只维护模块清单并派生宿主投影，`ProBootstrap.tsx` 负责认证后挂载。
- `overlay/src/pro/shared/` — 与业务域无关的共享 UI 模型；业务模块只能通过其他模块的 `index.ts` 公开面依赖，禁止 monitoring/inspection 等模块互相引用内部 `features/` 或样式文件。
- `overlay/src/services/api/` — 新增 API clients。
- `overlay-replacements.json` — 对有意覆盖 upstream 同路径文件的 full-file replacements，记录已审阅的 upstream SHA-256 与替换原因。
- `monitoring-locales.json` — 合并进 upstream locale 文件的多语言文案。
- `apply_customizations.py` — 将全部定制应用到目标 upstream checkout。
- `apply.sh` — `apply_customizations.py` 的 shell 包装脚本。
Overlay collision 预检会校验每个已审阅替换的 upstream 内容。upstream 文件变化时必须显式更新 `overlay-replacements.json`；本地替换通过正常 PR diff 和行为测试审查，新的未审阅路径冲突会在复制任何 overlay 文件前被拒绝。

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
bun install --frozen-lockfile
bun run test
bun run lint
bun run type-check
VERSION=review bun run build
```

也可直接使用仓库验证脚本检查一个可丢弃的干净 upstream checkout；脚本会验证 overlay 预检、重复应用、测试、lint、type-check 和 build：

```bash
bash scripts/validation/management.sh /path/to/disposable/clean-management-checkout
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
6. 执行 `bun install --frozen-lockfile` 和 `bun run build`；Bun 版本读取 upstream `package.json`。
7. 将 `dist/index.html` 重命名为 `management.html`。
8. 上传并覆盖当前 latest release 中的 `management.html`。
9. 更新 release notes 中的 management 版本映射和 upstream release notes。
10. 清理旧 workflow runs。

这样 `remote-management.panel-github-repository=https://github.com/ssfun/CLIProxyAPI-Pro` 始终可以通过 GitHub `/releases/latest` 获取最新 `management.html`。

## 后端依赖

这些前端定制依赖 customized `cliproxyapi-pro-core` 后端在 management API 前缀下暴露以下稳定接口分组：

- `/v0/management/usage`
- `/v0/management/usage/*`
- `/v0/management/quota/fetch`
- `/v0/management/account-inspection/*`
- `/v0/management/routing-policy`
- `/v0/management/routing-policy/*`

完整 method/path 清单以 Core README 为准。如果未使用 customized 后端，请求监控、SQLite 持久化、模型价格、后端账号巡检和路由保护相关功能会显示错误或空数据。
