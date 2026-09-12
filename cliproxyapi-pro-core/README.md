# CLIProxyAPI Pro Core

这是 upstream `router-for-me/CLIProxyAPI` 的定制 Docker 构建层。

本目录不维护 upstream 的完整 fork。Docker 构建时会下载指定 upstream release，复制本地 `embeddedusage/` 包，执行 `patches/` 中的补丁脚本，然后构建 Pro 部署使用的多架构镜像。

代理池和 OAuth 账号策略直接编译到 Core 二进制中，所有 Pro 构建（包括 `_no-plugin`）都具备这两项能力。配置保存在 usage SQLite 的 `pro_settings`，不会写入 `config.yaml` 或认证文件。

## 定制内容

### 内嵌 usage service

`embeddedusage/` 会复制到 upstream 源码中的：

```text
internal/embeddedusage
```

补丁层会随主 API 进程启动该服务，启用 upstream usage statistics，并把服务挂载到 management API 前缀下：

```text
/v0/management/usage
```

默认 SQLite 数据位置：

```text
/CLIProxyAPI/usage/usage.sqlite
```

镜像声明 `/CLIProxyAPI/usage` 为 Docker volume，用于在容器替换后保留 usage 数据、quota cache、模型价格和账号巡检调度状态。

服务启动时，补丁层会强制 Pro 依赖的 upstream 配置值：

- `usage-statistics-enabled: true`
- `remote-management.panel-github-repository: https://github.com/ssfun/CLIProxyAPI-Pro`

加载后的内存配置始终会被修正。运行时只允许修改 `config.yaml` 中已经存在的键；缺失键不会被 Pro 自动新增。

### Usage API

内嵌服务提供这些 management routes：

- `GET /v0/management/usage` — 管理页面使用的聚合 usage 数据。
- `GET /v0/management/usage/events` — cursor 之后的增量 usage events。
- `GET /v0/management/usage/aggregates` — 按时间桶和 provider/model/endpoint/API key 聚合 usage。
- `GET /v0/management/usage/account` — 按精确 `auth_index` 聚合单账号的概览、明细和质量指标。
- `GET /v0/management/usage/stream` — usage 实时更新 SSE 流。
- `GET /v0/management/usage/export` — JSONL/NDJSON 导出。
- `POST /v0/management/usage/import` — JSONL/NDJSON 导入。
- `GET /v0/management/usage/webdav/backups` — 列出已配置 WebDAV 目录中的 `usage-export-*.jsonl` 备份。
- `POST /v0/management/usage/webdav/preview` — 下载选定备份，并复用本地文件的完整性校验与导入预检。
- `POST /v0/management/usage/webdav/restore` — 通过同一 JSONL 解析器和原子导入管线恢复选定备份。
- `POST /v0/management/usage/reset` — 在独占运行态屏障内清空请求事件、派生统计和账号调度/成功/失败计数；保留路由轮询游标、监控设置、模型价格、配额缓存和备份。
- `GET /v0/management/usage/status` — 服务状态和记录数量。
- `GET /v0/management/usage/quota-cache` — 读取配额缓存或统计信息。
- `PUT /v0/management/usage/quota-cache` — 写入配额缓存。
- `DELETE /v0/management/usage/quota-cache` — 删除配额缓存。
- `GET /v0/management/usage/model-prices` — 读取模型价格设置。
- `PUT /v0/management/usage/model-prices` — 写入模型价格设置。
- `GET|PUT|DELETE /v0/management/usage/model-price-rules` — 管理按 model 全局生效的价格规则和上下文阶梯；公共规则 JSON 和删除参数均不包含 provider。
- `POST /v0/management/usage/model-prices/sync` — 从 models.dev 同步请求历史中出现过的模型。
- `GET /v0/management/usage/model-prices/sync-status` — 读取同步状态。
- `POST /v0/management/usage/model-prices/recalculate` — 显式重新估算历史成本。
- `GET /v0/management/usage/settings` — 读取监控日志保留、WebDAV 备份和模型价格同步设置。
- `PUT /v0/management/usage/settings` — 写入监控日志保留、WebDAV 备份和模型价格同步设置。

`/usage/events` 和 `/usage/stream` 的 detail 会携带稳定事件 `id`，管理端用它进行增量去重和断线追平。usage 响应还会返回持久化的 `generation`；手动重置或保留期清理推进版本后，SSE 会发送 `reset` 事件，已打开页面据此替换完整快照。SSE 在事件成功写入 SQLite 后由进程内通知立即唤醒，仅保留低频 keepalive，不再为每个连接每秒轮询数据库。

detail 还会保留 upstream `ClientRequestMetadata` 提供的 `client_ip`、`x_forwarded_for` 和 `user_agent`。其中 `client_ip` 是直连 peer 地址，`x_forwarded_for` 是未经可信代理校验的原始转发链，只用于请求诊断与搜索，不参与访问控制、路由或请求保护判断。这些字段受日志保留策略管理，也会进入 usage JSONL/WebDAV 备份。

历史 `/usage/events` 分页支持 `from_ms`、`to_ms`、`provider`、`model`、`auth_index`、`api_key_hash`、`status` 和 `search`。可选的逗号分隔 `search_auth_indexes` 会与原始事件文本 `search` 按 OR 联合，其他结构化过滤条件仍按 AND 叠加；首个响应返回的稳定快照 cursor 会在后续页面保留完整过滤范围。

`/usage/aggregates` 支持 `from_ms`、`to_ms`、`interval=minute|hour|day|all`、`group_by=provider,model,endpoint,api_key_hash`、`api_key_hash` 和 `timezone_offset_minutes`。响应同时返回 `latest_id`、`snapshot_at_ms` 和逐事件累加的 `estimatedCost`，避免使用聚合 Token 错选上下文价格阶梯。

`/usage/account` 必须传入精确 `auth_index`，支持 `days=7|30|90|0`（`0` 表示全部）和 `timezone_offset_minutes`。响应包含每日趋势、模型与 API key 分布、费用覆盖率、延迟/TTFT/P95、流式占比，以及基于真实零基 `attempt_index` 的重试次数和样本覆盖率；历史事件没有尝试序号时保持未知，不根据 `Retry-After` 推断。

### JSONL usage 备份与恢复

`/usage/export` 返回 `application/x-ndjson`，一行一个 JSON 对象。新导出的第一行是 `backup_manifest`，记录后续行数和 SHA-256；导入会在任何数据库写入前校验完整文件，因此截断或篡改的备份会被整体拒绝。

导出内容包含 usage events，也可能包含元数据记录：

- `model_prices` — 基础价格兼容数据和完整的全局 model 价格规则。
- `quota_cache` — 配额卡片和账号级刷新使用的 SQLite-backed quota snapshots。
- `monitoring_settings` — 监控日志保留时间、WebDAV 备份配置和 models.dev 定期同步配置。
- `pro_settings` — Pro 私有设置；当前包含请求状态保护、代理池和 OAuth 账号策略。账号策略 namespace 为 `oauth-policy`。
- `routing_cursor_state` — 账号路由轮转游标。
- `auth_runtime_stats` — 账号选择、成功/失败和近期请求桶统计。
- `account_inspection_schedule` — 后端账号巡检调度设置。
- `account_inspection_snapshot` — 最近一次已结束的账号巡检结果，包含运行设置、汇总、健康统计、完整结果和原始错误详情，不包含巡检日志。

`/usage/import` 接受同样的 JSONL 格式。导入时会先完整读取和校验请求，再在一个 SQLite 事务中导入 usage events、模型价格、quota cache entries、运行时路由状态、监控设置和 Pro 设置；Pro 设置的 live 配置会在提交前应用，应用失败会回滚数据库，提交失败则恢复导入前配置。提交成功后再按固定顺序恢复其余运行态、账号巡检调度和最近一次巡检结果快照。整个导入由独占写屏障保护；同步管理写会等待导入结束，高频路由/账号运行态快照会在导入窗口内丢弃，避免旧快照覆盖恢复结果。恢复的结果快照为只读；发起新的完整巡检后才允许重检、刷新令牌或执行账号变更。无 manifest 的旧版 event-only 或混合 JSONL 默认拒绝，因为它们无法获得文件级完整性校验；可信旧备份可显式使用 `?allow_legacy=1` 或 `X-CLIProxy-Allow-Legacy-Backup: true` 请求头导入，管理端会在启用兼容模式前要求确认。

API Key 策略备份记录包含稳定 SHA-256 Key 指纹、Profile 规则、active Profile 状态、策略审计历史、密钥禁用设置与并发上限，但绝不包含或恢复 `config.yaml api-keys`。指纹属于敏感标识；JSONL 与 WebDAV 备份必须沿用敏感配置导出的访问控制、传输加密和存储保留要求。WebDAV 恢复只接受列表中的 `usage-export-*.jsonl` 文件名，并复用本地导入的预检和原子恢复管线。

API 密钥工作区支持按 Key 设置并发请求上限（`0` 不限，最大 `1,000,000`），无需先创建 Profile。仅在策略接管开启时执行；超限请求立即返回 HTTP `429` / `api_key_concurrency_exceeded`，不进入执行或消耗请求配额。普通请求在处理结束后释放名额，流式响应和 WebSocket 连接持有名额直到结束；模型发现、公开密钥查询和管理接口不计入。WebRTC SDP 引导只计算其 HTTP 请求生命周期，不计算引导结束后的媒体会话。计数按当前服务实例独立维护，不跨实例共享，不写入备份；修改上限、停止/恢复接管或恢复备份不会清空在途请求。恢复旧策略备份时并发上限恢复为不限，预览会显示设置及生效范围变化。

并发设置接口为 `PUT /v0/management/api-key-policy-key-concurrency`，沿用管理认证和会话绑定的 `keyRef`，请求体为 `{ "keyRef": "...", "limit": 4, "expectedLimit": 0 }`。旧值不匹配返回 `409`，写入成功返回 `concurrencyLimit`；工作区复用配额的启用开关和统一保存：创建或更新策略时，通过可选的 `concurrency: { limit, expectedLimit }` 与配额、Profile 在同一事务内提交。旧客户端省略该字段会保留现有上限；面板仅在 Core 声明 `workspace_concurrency_limits` 能力时显示工作区设置。

导入响应示例字段：

```json
{
  "added": 100,
  "skipped": 5,
  "total": 105,
  "failed": 0,
  "modelPrices": 12,
  "modelPriceRecords": 1,
  "modelPriceRules": 12,
  "quotaCache": 8,
  "quotaCacheRecords": 1,
  "routingCursors": 4,
  "routingCursorRecords": 1,
  "authRuntimeStats": 8,
  "authRuntimeStatsRecords": 1,
  "accountInspectionSchedule": true,
  "accountInspectionScheduleRecords": 1,
  "accountInspectionSnapshot": true,
  "accountInspectionSnapshotRecords": 1,
  "monitoringSettings": true,
  "monitoringSettingsRecords": 1,
  "legacyBackup": false
}
```

### SQLite 配额缓存

内嵌服务会为以下 provider 保存配额快照：

- Antigravity
- Claude
- Codex
- Gemini CLI
- Kimi
- xAI

管理页面通过 `/usage/quota-cache` 读写该缓存，因此配额卡片可在页面刷新、浏览器切换和后端重启后恢复。

### QuotaProvider 插件协议

补丁层为 upstream 插件 SDK/ABI 增加可选 `QuotaProvider` 能力，并提供
`POST /v0/management/quota/fetch`。宿主负责回调生命周期、规范化快照、SQLite 持久化以及
套餐信息的 last-known-good 保留。当前 Gemini CLI 插件无需修改：Core 会通过插件已有的
`Executor.HttpRequest` 提供兼容适配；插件未来原生实现协议后会自动优先使用原生能力。
协议字段与兼容策略见 [QUOTA_PROVIDER.md](QUOTA_PROVIDER.md)。

### 认证文件连接测试

- `POST /v0/management/auth-files/test` — 传入 `name`、可选的 `auth_index` 和 `model`，将一次最小 OpenAI Chat 格式的真实文本生成请求固定到该认证记录。

Pro 扩展了上游 `GET /v0/management/auth-files/models`：优先使用该认证记录已注册的模型；上游因账号禁用而注销模型时，改用对应提供商的静态模型定义，Codex 会按账号套餐选择目录。因此模型查看和连接测试可以共用同一个接口。测试沿用 upstream 执行器的请求翻译、账号代理、401 刷新、模型别名和结果统计路径，但不会写入请求监控。诊断执行会绕过正常调度的 disabled、cooldown 和 unavailable 可用性门槛，因此可验证异常账号是否已经恢复；结果不会清除用户设置的 `disabled` 开关。响应包含 `success`、`model`、`latency_ms`、模型 `output`，或 `error`、`error_code`、`http_status`。

### 内建代理池与 OAuth 套餐账号策略

Core 内建回环 SOCKS5 代理池以及 xAI、Codex、Claude、Gemini CLI、Antigravity、Kimi 的 OAuth 套餐账号策略。套餐识别复用认证绑定的上游请求，并按时间读取 SQLite 中的插件配额和账号巡检快照，选择最新一份包含有效套餐的证据；配额更新会触发受 auth generation 保护的模型重注册，也可通过 `POST /v0/management/pro/oauth-policy/refresh` 手动重新识别。账号规则可配置 `excluded-models`、`prefix`、`priority` 和 `weight`；策略仅生成运行时覆盖，不改写 `config.yaml` 或认证文件。模型处理顺序为 upstream `excluded_models`、内建套餐过滤、OAuth alias、套餐 prefix、模型注册，最终结果同时约束 `/v1/models` 聚合和请求调度候选账号。

首次启动会读取旧 `plugins.configs.proxy-pool` 和 `plugins.configs.oauth-model-policy`，校验并写入 SQLite，回读确认成功后再原子清除旧 YAML。旧代理接管若处于启用状态，会先把根 `proxy-url` 恢复为旧 `restore-proxy-url`；其他第三方插件配置保持不变。

### 后端账号巡检调度器

补丁层在 management API 下增加账号巡检路由：

请求监控会额外保存 TTFT、HTTP 状态码、结构化错误、reasoning effort、请求 service tier 和上游响应的生效 service tier；`/usage/status` 会返回最近 dead letter 样本并对敏感字段脱敏。Fast 定价将 `priority` 作为 `fast` 的兼容别名，并优先依据响应 tier 计费，避免已降级为 `default` 的请求误用 Fast 单价。账号巡检自动动作支持连续确认门槛，quota cache 会记录解析器版本和返回结构 hash。

- `GET /v0/management/account-inspection/schedule`
- `GET /v0/management/account-inspection/status`
- `GET /v0/management/account-inspection/logs`（WebSocket/WSS 日志和状态流）
- `PUT|PATCH /v0/management/account-inspection/schedule`
- `POST /v0/management/account-inspection/run`
- `POST /v0/management/account-inspection/inspect-one`
- `POST /v0/management/account-inspection/refresh-token`
- `POST /v0/management/account-inspection/inspect-many`：后端限流批量重检并返回逐账号结果。
- `POST /v0/management/account-inspection/pause`
- `POST /v0/management/account-inspection/resume`
- `POST /v0/management/account-inspection/stop`
- `POST /v0/management/account-inspection/actions`

调度器支持巡检：

- Antigravity
- Claude
- Codex
- Gemini CLI
- Kimi
- xAI

能力包括 provider 过滤、两级探测并发、重试/超时、抽样、按用量阈值判断、进度/状态/日志/结果快照、暂停/继续/停止控制、手动操作，以及对额度耗尽、额度恢复、账号错误的可选自动操作。Antigravity 和 xAI 还支持可选深度探测。

巡检设置中的 `workers` 是所有 provider 合计的探测总并发，范围 `1–8`、默认 `4`；`providerWorkers` 是单个 provider 的探测并发，范围 `1–4`、默认 `2`。普通探测、深度探测、xAI 探测和探测前 token refresh 共用这两个限制，不再使用单独的串行闸门。`deleteWorkers` 范围 `1–4`、默认 `4`，同时约束自动操作和管理端手动批量操作。调度设置保存在账号巡检调度 JSON 中，不读取或修改 `config.yaml`。

探测账号前，调度器会在认证记录本来已经进入 upstream 正常刷新窗口时尝试刷新 auth。巡检刷新路径复用 upstream provider 刷新逻辑和持久化逻辑，允许 disabled 账号，跳过 API key 账号、未到刷新窗口的账号，并遵守 `NextRefreshAfter`。刷新成功后使用刷新后的 auth 探测；刷新失败时保留账号，并跳过该账号本次探测。

调度文件默认位置：

```text
/CLIProxyAPI/usage/account-inspection-schedule.json
```

如需自定义，可设置 `ACCOUNT_INSPECTION_SCHEDULE_PATH`。

最近一次已结束的巡检结果会单独持久化到 `/CLIProxyAPI/usage/account-inspection-snapshot.json`，文件权限为 `0600`。进程重启或 usage 导入恢复后，该快照会标记为只读；下一次完整巡检结束时覆盖。可通过 `ACCOUNT_INSPECTION_SNAPSHOT_PATH` 自定义路径。

### 请求状态保护

补丁层在 management API 下增加请求状态保护接口：

- `GET /v0/management/routing-policy`
- `PUT /v0/management/routing-policy/request-protection`
- `PUT|PATCH /v0/management/routing-policy`（旧管理端兼容入口，仅处理 `requestProtection`）
- `POST /v0/management/routing-policy/release`

接口只管理 Pro 请求状态保护，不读取或修改 `config.yaml` 的全局路由配置。请求保护保存在 `usage.sqlite` 的 `pro_settings`。如果 SQLite 尚无设置，旧版 `routing.request-protection` 可作为一次性迁移来源；迁移后 SQLite 优先，原 YAML 保持不变。内置 provider 支持 Antigravity、xAI、Codex、Gemini CLI、Gemini、Gemini Interactions、Vertex AI、AI Studio、Claude 和 Kimi。

请求状态保护默认关闭，模式默认为 `observe`。接口通过 `availableProviders` 返回当前已有 API 配置或凭据的受支持 provider。启用后可按 provider 配置 HTTP 状态码、连续确认次数、确认窗口、429 配额证据、自动解除和兜底禁用时长。`enforce` 模式达到门槛后会禁用对应认证记录，并写入 `request_protection` 归属元数据；自动解除和管理端手动解除只处理由该策略禁用的账号，不会重新启用用户手动禁用或由其他模块禁用的账号。

自动解除时间优先读取 `Retry-After`、Codex reset headers、响应体 `resets_at` / `resets_in_seconds`，无法解析时使用 provider 的兜底禁用时长。运行状态接口同时返回当前受保护账号和进程内最近事件。

### 根路径跳转和 health 响应

补丁层还修改了 upstream API 行为：

- `/` 跳转到 `/management.html`。
- `/healthz` 返回更完整的 CLIProxyAPI 状态信息，同时保留 `HEAD /healthz`。

### 管理面板默认仓库

补丁层会将 upstream 的远程管理面板默认仓库改为：

```text
https://github.com/ssfun/CLIProxyAPI-Pro
```

该修改会同时影响内置默认配置、`config.example.yaml`，以及 management asset updater 的默认 latest-release API 地址。

发布流水线会把同一次构建生成的 Pro `management.html` 放入 Docker 镜像的 `/CLIProxyAPI/static/management.html`，并通过 `MANAGEMENT_STATIC_PATH` 固定为本地面板。GitHub Release API 或制品下载失败时，上游 updater 会保留并继续使用该本地文件。Core 二进制及非 Docker 发行包不再内嵌 management，也不改变 upstream 原有回退实现。

设置 `GITSTORE_GIT_TOKEN` 后，token 会自动用于 `api.github.com` 上的 management 和插件 GitHub Release 元数据、API 制品下载，以及启动时插件自动安装。匹配仅限 HTTPS GitHub API release 路径；显式的 `plugins.store-auth` 规则优先，其中 `type: none` 可禁止指定范围使用该环境变量。启动时插件自动安装和单次 HTTP 请求都有 2 分钟上限，注册表或制品服务异常不会无限阻塞主程序启动。

管理中心的“检查更新”按钮调用 `POST /v0/management/management-panel/check-update`。该接口复用 updater 的 30 秒节流、远端摘要校验和本地 SHA-256 比较；只有 latest release 的 `management.html` 与本地文件哈希不同才原子替换。因此既能处理新版本，也能处理同一 release 下重新上传但内容不同的面板文件；哈希相同不会重复下载。

### 运行时辅助进程

当以下变量同时配置时，`entrypoint.sh` 会在主 API 进程前启动内置 Komari agent：

- `KOMARI_SERVER`
- `KOMARI_SECRET`

随后启动 `CLIProxyAPI`，并按需从 WebDAV 恢复最新 usage 备份。容器收到 `TERM` 或 `INT` 时，entrypoint 会把信号转发给主程序和 Komari agent，等待它们退出，并保留主程序退出码。

## 目录结构

- `Dockerfile` — 下载 upstream CLIProxyAPI，应用定制层，并构建最终镜像。
- `Dockerfile.runtime` — GitHub Actions 使用预构建 Linux 二进制组装运行时镜像。
- `QUOTA_PROVIDER.md` — QuotaProvider 插件协议和兼容策略。
- `patches/sources/internal/pro/app/` — 静态 Pro 模块的 composition root、生命周期和旧配置迁移。
- `patches/sources/internal/pro/host/` — 最终代理传输、模型注册和认证对象等 upstream 易变边界适配。
- `patches/sources/internal/pro/proxypool/` — 独立的代理池配置、运行服务、节点池和 SOCKS5 实现。
- `patches/sources/internal/pro/oauthpolicy/` — 独立的 OAuth 套餐识别、模型过滤和配置服务。
- `patches/sources/internal/pro/settings/` — 模块使用的版本化设置持久化端口。
- `patches/sources/internal/pro/storage/` — 单一 SQLite 生命周期、幂等 schema、领域仓储和事务边界。
- `patches/sources/internal/pro/state/` — 路由游标、账号运行统计的稳定契约及合并写入器。
- `patches/sources/internal/pro/observability/` — usage、留存、价格同步、WebDAV 后台任务，以及普通状态写入的备份协调适配。
- `patches/sources/internal/pro/quota/` — Quota snapshot 规范化/最大使用率、cache 成功态与响应 shape 指纹、Gemini CLI/xAI billing、plan、request-path 配额解析与合并策略。
- `patches/sources/internal/pro/routing/` — 稳定选路游标和 request-protection 所有权规则。
- `patches/sources/internal/pro/inspection/` — 巡检配置、候选过滤/抽样/两级 worker 策略、状态/日志/流与手动操作 DTO、结果分类/过滤/分页/汇总与合并状态机、provider 决策与错误码、操作去重/汇总、结果快照 schema/codec、自动操作决策、Antigravity/Claude/Codex/Kimi 响应解析，以及 Antigravity/xAI deep-probe 请求与响应协议；provider 探测 transport、Gin/WebSocket、快照/quota cache/observation I/O 与 Auth 写回仍位于 Management host adapter。
- `patches/sources/internal/pro/backup/` — JSONL 导出、导入独占/普通写共享屏障，以及“暂停、flush、导入、恢复运行态、恢复巡检、清理旧缓存、resume”的跨模块协调器。
- `entrypoint.sh` — 启动 Komari、主 API 和 WebDAV usage 恢复逻辑。
- `embeddedusage/` — 保留 upstream 导入路径、公开类型和函数签名的薄兼容 façade；实现位于 `pro/observability`。
- `patches/apply_upstream_patches.py` — Docker build 阶段 patch upstream 源码。
- `patches/account_inspection_{runtime,http,accounts,transport,quota}.go` — 按生命周期/API、账号宿主能力、auth-bound transport 和 quota 状态边界拆分，并注入 upstream management handlers 的后端账号巡检 adapter；测试按相同边界拆分。
- `patches/account_inspection_host.go`、`patches/pro_auth_mutation.go` — Inspection quota port 与共享 Auth mutation/file persistence host adapter。
- `patches/pro_management_runtime.go` — 组合随 Management Handler 启停的 inspection、routing 后台生命周期。
- 生成后的 API Server 会在 `Stop` 时关闭 management Handler；直接通过 SDK 创建 Handler 的嵌入方也必须调用其 `Shutdown()`，以释放巡检、路由保护、登录清理及全局回调。
- `patches/routing_policy.go` — 注入统一路由配置和请求状态保护 handlers、usage plugin 与自动解除任务。
- 核心不变量：账号巡检状态优先于 request protection；导入的 `routing_cursor_state` 和 `auth_runtime_stats` 必须立即应用到 live manager；原 DB 表、JSONL record type 和 `/v0/management/usage*` API 保持兼容。

静态模块按实际宿主生命周期组合：`pro/app` 管理请求路径上的 proxy-pool 与 oauth-policy 服务；`pro/observability` 随进程 context 启停；inspection 与 routing 控制器随 Management Handler 启停。跨生命周期备份端口使用 owner-scoped 注册和逆序注销，旧 Handler 或旧 Service 关闭时不会清除新实例的回调。`internal/embeddedusage` 只允许出现在 upstream/SDK 兼容边界，`internal/pro` 业务模块不反向依赖该 façade。
- `patches/config_existing_updates.go` — 只修改已存在 YAML 标量、禁止补键的配置写入辅助层。
- `.github/workflows/release-core.yml` — 镜像发布、Pro 二进制资产、management.html 发布、usage 备份、Render 部署触发、Telegram 通知和 workflow 清理。

## Docker 构建

已发布镜像：

```bash
docker pull sfun/cliproxyapi-pro:latest
```

构建 upstream 最新 release：

```bash
docker build -t cliproxyapi-pro -f cliproxyapi-pro-core/Dockerfile .
```

构建指定 upstream release，并写入 Pro runtime 版本：

```bash
docker build \
  --build-arg CLIPROXY_VERSION=vX.Y.Z \
  --build-arg CLIPROXY_BUILD_VERSION=vX.Y.Z-pro \
  -t cliproxyapi-pro:vX.Y.Z-pro \
  ./cliproxyapi-pro-core
```

`CLIPROXY_VERSION` 用于下载 upstream 源码，`CLIPROXY_BUILD_VERSION` 用于写入运行时版本号。

可用 build args：

- `CLIPROXY_REPO` — upstream 仓库，默认 `router-for-me/CLIProxyAPI`。
- `CLIPROXY_VERSION` — upstream release tag。为空时 Dockerfile 自动解析 latest release。
- `CLIPROXY_COMMIT` — 可选 upstream commit SHA；设置后按该提交下载源码，同时保留 `CLIPROXY_VERSION` 作为版本标识。
- `CLIPROXY_BUILD_VERSION` — 可选 runtime 版本号。为空时使用 `CLIPROXY_VERSION` 解析到的 upstream 版本。
- `PRO_MANAGEMENT_REPO` — Source Docker 构建用于取得镜像内 Pro management 的仓库，默认 `ssfun/CLIProxyAPI-Pro`。
- `SOURCE_DATE_EPOCH` — 可选 Unix 时间戳，用于写入确定的构建时间；与不可变 upstream commit 一起设置可获得确定的 source binary。
- `GITHUB_TOKEN` — 可选 GitHub API token。

Release workflow 会从 Core、models 和定制层三个不可变提交中取最新时间作为 `SOURCE_DATE_EPOCH`。Core 归档统一规范文件顺序、时间戳、属主和权限，Go 构建同时启用 `-trimpath`。

## 运行时环境变量

- `GITSTORE_GIT_TOKEN` — 可选 GitHub token；用于 management 和插件的 GitHub Release API 元数据及 API 制品下载，可避免匿名 API 限流导致的 403。
- `MANAGEMENT_STATIC_PATH` — Docker 镜像固定为 `/CLIProxyAPI/static/management.html`，指向镜像打包的 Pro 面板。

### Usage service

- `USAGE_SERVICE_ENABLED` — 默认 `true`；设为 `false`/`0`/`no`/`off` 可禁用内嵌服务。
- `USAGE_DATA_DIR` — 默认 `/CLIProxyAPI/usage`。
- `USAGE_DB_PATH` — 默认 `/CLIProxyAPI/usage/usage.sqlite`。
- `USAGE_BATCH_SIZE` — 默认 `100`。
- `USAGE_POLL_INTERVAL_MS` — 默认 `500`。
- `USAGE_QUERY_LIMIT` — 默认 `50000`。

### 账号巡检

- `ACCOUNT_INSPECTION_SCHEDULE_PATH` — 可选调度 JSON 路径。默认 `USAGE_DATA_DIR/account-inspection-schedule.json`。
- `ACCOUNT_INSPECTION_SNAPSHOT_PATH` — 可选最近一次巡检结果快照 JSON 路径。默认 `USAGE_DATA_DIR/account-inspection-snapshot.json`。

### WebDAV usage 恢复

当以下变量全部配置时，`entrypoint.sh` 会等待本地 API 就绪，从 WebDAV 下载最新备份，并导入到 `/v0/management/usage/import`：

- `WEBDAV_URL`
- `WEBDAV_USERNAME`
- `WEBDAV_PASSWORD`
- `MANAGEMENT_PASSWORD`

恢复文件查找同时支持：

```text
usage-export-YYYYMMDD_HHMMSS.json
usage-export-YYYYMMDD_HHMMSS.jsonl
```

Docker WebDAV 自动恢复在过渡阶段固定调用 `/usage/import?allow_legacy=1`。带 manifest 的新备份仍会严格校验完整性；无 manifest 的旧版备份会强制导入，并在日志中明确记录正在使用未经完整性校验的兼容路径。管理 API 的普通导入仍默认拒绝无 manifest 文件。

服务内置的 WebDAV 定时备份、目录读取和旧文件删除请求均有 2 分钟总超时，异常端点不会永久占用备份生命周期或阻塞 usage 导入暂停屏障。

导入请求使用：

```text
Content-Type: application/x-ndjson
```

### Komari agent

- `KOMARI_SERVER`
- `KOMARI_SECRET`

## GitHub Actions

Workflow：

```text
.github/workflows/release-core.yml
```

流程：

1. 检查 upstream CLIProxyAPI 最新 release，并计算当前 Pro release tag，例如 `v<core-version>-pro`。
2. 检查 upstream management 最新 release。
3. 构建与 upstream 平台和压缩格式一致的 Pro 二进制资产，资产名前缀保持为 `CLIProxyAPI`；默认桌面/Linux 包启用 CGO 以支持动态库插件，`_no-plugin` 包保留 CGO-free 静态便携构建。
4. 复用 Linux amd64/arm64 二进制资产，通过 `Dockerfile.runtime` 组装并推送带 `latest` 和 Pro release tag 的多架构镜像。
5. 应用 management 定制层并构建 `management.html`。
6. 创建或更新当前仓库 GitHub Release，上传二进制资产、`checksums.txt` 和 `management.html`。
7. Release notes 写入 core upstream 与 management upstream 的版本映射和 release notes。
8. 请求一个或多个正在运行的 CPA 实例通过数据管理管线创建完整 Pro WebDAV 备份；旧 Core 404 时回退 usage 导出。
9. 触发一个或多个 Render 部署。
10. 发送 Telegram 通知。
11. 清理旧 workflow runs。

### Docker 发布 secrets

- `DOCKER_USERNAME`
- `DOCKER_PASSWORD`

### 多实例 Pro 数据备份

workflow 使用一个可选 JSON secret 配置全部备份目标。新 Core 主路径只需要 `api_url` 和 `management_password`；示例中的 `webdav_*` 字段仅供尚未提供数据管理备份端点的旧 Core 404 回退使用：

```text
CLIPROXY_USAGE_BACKUP_TARGETS
```

示例：

```json
[
  {
    "name": "cpa-main",
    "api_url": "https://cpa-main.example.com",
    "management_password": "management-password-1",
    "webdav_url": "https://webdav.example.com/cpa-main",
    "webdav_username": "webdav-user-1",
    "webdav_password": "webdav-password-1"
  }
]
```

每个目标优先调用 `/v0/management/data/backups/now`。WebDAV 地址、凭证、文件命名、保留期和操作记录均由该 CPA 实例的数据管理设置负责，完整备份文件名为：

```text
cliproxy-pro-backup-YYYYMMDD_HHMMSS_000.jsonl
```

仅当目标返回 404 时，workflow 才使用目标中的 `webdav_*` 字段回退到旧 `/v0/management/usage/export` 流程，生成 `usage-export-YYYYMMDD_HHMMSS.jsonl` 并保留最近 7 个旧备份。如果 secret 未配置、格式无效、旧 Core 缺少回退字段或某个目标失败，workflow 会记录警告并继续执行。

### 多 Render 部署 hook

workflow 使用一个可选 JSON secret 配置全部 Render deploy hooks：

```text
CLIPROXY_RENDER_DEPLOY_HOOKS
```

示例：

```json
[
  {
    "name": "cpa-main",
    "hook_url": "https://api.render.com/deploy/srv-xxx?key=xxx"
  }
]
```

`url` 也可作为 `hook_url` 的别名。如果 secret 未配置、格式无效或某个目标失败，workflow 会记录警告并继续执行。

### Telegram 通知 secrets

- `TELEGRAM_CHAT_ID`
- `TELEGRAM_BOT_TOKEN`

## 本地验证

使用仓库验证脚本检查干净的 upstream checkout。脚本会验证 source hash 预检、拒绝重复应用、`internal/pluginhost` 的 `go vet`、相关 Go packages 和 server build：

```bash
bash scripts/validation/core.sh /path/to/clean/CLIProxyAPI
```

仅验证 entrypoint 语法：

```bash
sh -n cliproxyapi-pro-core/entrypoint.sh
```
