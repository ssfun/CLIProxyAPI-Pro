# CLIProxyAPI Pro Core

这是 upstream `router-for-me/CLIProxyAPI` 的定制 Docker 构建层。

本目录不维护 upstream 的完整 fork。Docker 构建时会下载指定 upstream release，复制本地 `embeddedusage/` 包，执行 `patches/` 中的补丁脚本，然后构建 Pro 部署使用的多架构镜像。

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

加载后的内存配置始终会被修正。只有当加载到的值不一致时才会更新 `config.yaml`，文件已经正确时不会重复落盘。

### Usage API

内嵌服务提供这些 management routes：

- `GET /v0/management/usage` — 管理页面使用的聚合 usage 数据。
- `GET /v0/management/usage/events` — cursor 之后的增量 usage events。
- `GET /v0/management/usage/aggregates` — 按时间桶和 provider/model/endpoint/API key 聚合 usage。
- `GET /v0/management/usage/stream` — usage 实时更新 SSE 流。
- `GET /v0/management/usage/export` — JSONL/NDJSON 导出。
- `POST /v0/management/usage/import` — JSONL/NDJSON 导入。
- `POST /v0/management/usage/reset` — 原子清空请求事件和派生统计，保留监控设置、模型价格、配额缓存和备份。
- `GET /v0/management/usage/status` — 服务状态和记录数量。
- `GET /v0/management/usage/quota-cache` — 读取配额缓存或统计信息。
- `PUT /v0/management/usage/quota-cache` — 写入配额缓存。
- `DELETE /v0/management/usage/quota-cache` — 删除配额缓存。
- `GET /v0/management/usage/model-prices` — 读取模型价格设置。
- `PUT /v0/management/usage/model-prices` — 写入模型价格设置。
- `GET|PUT|DELETE /v0/management/usage/model-price-rules` — 管理 provider/model 价格规则和上下文阶梯。
- `POST /v0/management/usage/model-prices/sync` — 从 models.dev 同步请求历史中出现过的模型。
- `GET /v0/management/usage/model-prices/sync-status` — 读取同步状态。
- `POST /v0/management/usage/model-prices/recalculate` — 显式重新估算历史成本。
- `GET /v0/management/usage/settings` — 读取监控日志保留、WebDAV 备份和模型价格同步设置。
- `PUT /v0/management/usage/settings` — 写入监控日志保留、WebDAV 备份和模型价格同步设置。

`/usage/events` 和 `/usage/stream` 的 detail 会携带稳定事件 `id`，管理端用它进行增量去重和断线追平。usage 响应还会返回持久化的 `generation`；手动重置或保留期清理推进版本后，SSE 会发送 `reset` 事件，已打开页面据此替换完整快照。SSE 在事件成功写入 SQLite 后由进程内通知立即唤醒，仅保留低频 keepalive，不再为每个连接每秒轮询数据库。

`/usage/aggregates` 支持 `from_ms`、`to_ms`、`interval=minute|hour|day|all`、`group_by=provider,model,endpoint,api_key_hash`、`api_key_hash` 和 `timezone_offset_minutes`。响应同时返回 `latest_id`、`snapshot_at_ms` 和逐事件累加的 `estimatedCost`，避免使用聚合 Token 错选上下文价格阶梯。

### JSONL usage 备份与恢复

`/usage/export` 返回 `application/x-ndjson`，一行一个 JSON 对象。新导出的第一行是 `backup_manifest`，记录后续行数和 SHA-256；导入会在任何数据库写入前校验完整文件，因此截断或篡改的备份会被整体拒绝。

导出内容包含 usage events，也可能包含元数据记录：

- `model_prices` — 基础价格兼容数据和完整 provider/model 价格规则。
- `quota_cache` — 配额卡片和账号级刷新使用的 SQLite-backed quota snapshots。
- `monitoring_settings` — 监控日志保留时间、WebDAV 备份配置和 models.dev 定期同步配置。
- `routing_cursor_state` — 账号路由轮转游标。
- `auth_runtime_stats` — 账号选择、成功/失败和近期请求桶统计。
- `account_inspection_schedule` — 后端账号巡检调度设置。
- `account_inspection_snapshot` — 最近一次已结束的账号巡检结果，包含运行设置、汇总、健康统计、完整结果和原始错误详情，不包含巡检日志。

`/usage/import` 接受同样的 JSONL 格式。导入时会先完整读取和校验请求，再导入 usage events，恢复模型价格、quota cache entries、运行时路由状态、监控设置、账号巡检调度和最近一次巡检结果快照。路由游标与账号运行统计在同一个 SQLite 事务中恢复。恢复的结果快照为只读；发起新的完整巡检后才允许重检、刷新令牌或执行账号变更。旧版无 manifest 的 event-only 或混合 JSONL 仍可导入，但无法获得文件级完整性校验。

导入响应示例字段：

```json
{
  "added": 100,
  "skipped": 5,
  "total": 105,
  "failed": 0,
  "modelPrices": 12,
  "modelPriceRecords": 1,
  "quotaCache": 8,
  "quotaCacheRecords": 1,
  "accountInspectionSchedule": true,
  "accountInspectionScheduleRecords": 1,
  "accountInspectionSnapshot": true,
  "accountInspectionSnapshotRecords": 1,
  "monitoringSettings": true,
  "monitoringSettingsRecords": 1
}
```

### SQLite 配额缓存

内嵌服务会为以下 provider 保存配额快照：

- Antigravity
- Claude
- Codex
- Gemini CLI
- Kimi

管理页面通过 `/usage/quota-cache` 读写该缓存，因此配额卡片可在页面刷新、浏览器切换和后端重启后恢复。

### QuotaProvider 插件协议

补丁层为 upstream 插件 SDK/ABI 增加可选 `QuotaProvider` 能力，并提供
`POST /v0/management/quota/fetch`。宿主负责回调生命周期、规范化快照、SQLite 持久化以及
套餐信息的 last-known-good 保留。当前 Gemini CLI 插件无需修改：Core 会通过插件已有的
`Executor.HttpRequest` 提供兼容适配；插件未来原生实现协议后会自动优先使用原生能力。
协议字段与兼容策略见 [QUOTA_PROVIDER.md](QUOTA_PROVIDER.md)。

### 后端账号巡检调度器

补丁层在 management API 下增加账号巡检路由：

请求监控会额外保存 TTFT、HTTP 状态码、结构化错误、reasoning effort 和 service tier；`/usage/status` 会返回最近 dead letter 样本并对敏感字段脱敏。账号巡检自动动作支持连续确认门槛，quota cache 会记录解析器版本和返回结构 hash。

- `GET /v0/management/account-inspection/schedule`
- `GET /v0/management/account-inspection/status`
- `GET /v0/management/account-inspection/logs`（WebSocket/WSS 日志和状态流）
- `PUT /v0/management/account-inspection/schedule`
- `POST /v0/management/account-inspection/run`
- `POST /v0/management/account-inspection/pause`
- `POST /v0/management/account-inspection/resume`
- `POST /v0/management/account-inspection/stop`
- `POST /v0/management/account-inspection/actions`

调度器支持巡检：

- Antigravity
- Claude
- Codex
- Kimi

能力包括 provider 过滤、worker 数量限制、重试/超时、抽样、按用量阈值判断、进度/状态/日志/结果快照、暂停/继续/停止控制、手动操作，以及对额度耗尽、额度恢复、账号错误的可选自动操作。

探测账号前，调度器会在认证记录本来已经进入 upstream 正常刷新窗口时尝试刷新 auth。巡检刷新路径复用 upstream provider 刷新逻辑和持久化逻辑，允许 disabled 账号，跳过 API key 账号、未到刷新窗口的账号，并遵守 `NextRefreshAfter`。刷新成功后使用刷新后的 auth 探测；刷新失败时保留账号，并跳过该账号本次探测。

调度文件默认位置：

```text
/CLIProxyAPI/usage/account-inspection-schedule.json
```

如需自定义，可设置 `ACCOUNT_INSPECTION_SCHEDULE_PATH`。

最近一次已结束的巡检结果会单独持久化到 `/CLIProxyAPI/usage/account-inspection-snapshot.json`，文件权限为 `0600`。进程重启或 usage 导入恢复后，该快照会标记为只读；下一次完整巡检结束时覆盖。可通过 `ACCOUNT_INSPECTION_SNAPSHOT_PATH` 自定义路径。

### 路由策略与请求状态保护

补丁层在 management API 下增加统一路由策略接口：

- `GET /v0/management/routing-policy`
- `PUT|PATCH /v0/management/routing-policy`
- `POST /v0/management/routing-policy/release`

接口聚合 upstream 的路由策略、会话粘性、请求重试、账号切换、冷却、配额回退和 Codex 身份混淆配置，并增加 `routing.request-protection` 请求状态保护配置。内置 provider 支持 Antigravity、xAI、Codex、Gemini CLI、Gemini、Gemini Interactions、Vertex AI、AI Studio、Claude 和 Kimi。

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

### 运行时辅助进程

当以下变量同时配置时，`entrypoint.sh` 会在主 API 进程前启动内置 Komari agent：

- `KOMARI_SERVER`
- `KOMARI_SECRET`

随后启动 `CLIProxyAPI`，并按需从 WebDAV 恢复最新 usage 备份。

## 目录结构

- `Dockerfile` — 下载 upstream CLIProxyAPI，应用定制层，并构建最终镜像。
- `entrypoint.sh` — 启动 Komari、主 API 和 WebDAV usage 恢复逻辑。
- `embeddedusage/` — 内嵌 SQLite usage service 和 management routes。
- `patches/apply_upstream_patches.py` — Docker build 阶段 patch upstream 源码。
- `patches/account_inspection_scheduler.go` — 注入 upstream management handlers 的后端账号巡检调度器。
- `patches/routing_policy.go` — 注入统一路由配置和请求状态保护 handlers、usage plugin 与自动解除任务。
- `patches/routing_protection_config.go` — 注入 `routing.request-protection` 配置类型。
- `.github/workflows/release-core.yml` — 镜像发布、Pro 二进制资产、management.html 发布、usage 备份、Render 部署触发、Telegram 通知和 workflow 清理。

## Docker 构建

已发布镜像：

```bash
docker pull sfun/cliproxyapi-pro:latest
```

构建 upstream 最新 release：

```bash
docker build -t cliproxyapi-pro ./cliproxyapi-pro-core
```

构建指定 upstream release，并写入 Pro runtime 版本：

```bash
docker build \
  --build-arg CLIPROXY_VERSION=v7.1.18 \
  --build-arg CLIPROXY_BUILD_VERSION=v7.1.18-pro \
  -t cliproxyapi-pro:v7.1.18-pro \
  ./cliproxyapi-pro-core
```

`CLIPROXY_VERSION` 用于下载 upstream 源码，`CLIPROXY_BUILD_VERSION` 用于写入运行时版本号。

可用 build args：

- `CLIPROXY_REPO` — upstream 仓库，默认 `router-for-me/CLIProxyAPI`。
- `CLIPROXY_VERSION` — upstream release tag。为空时 Dockerfile 自动解析 latest release。
- `CLIPROXY_COMMIT` — 可选 upstream commit SHA；设置后按该提交下载源码，同时保留 `CLIPROXY_VERSION` 作为版本标识。
- `CLIPROXY_BUILD_VERSION` — 可选 runtime 版本号。为空时使用 `CLIPROXY_VERSION` 解析到的 upstream 版本。
- `GITHUB_TOKEN` — 可选 GitHub API token。

## 运行时环境变量

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

1. 检查 upstream CLIProxyAPI 最新 release，并计算当前 Pro release tag，例如 `v7.1.18-pro`。
2. 检查 upstream management 最新 release。
3. 构建并推送 `linux/amd64` 和 `linux/arm64` Docker 镜像，tag 包括 `latest` 和 Pro release tag。
4. 构建与 upstream 平台和压缩格式一致的 Pro 二进制资产，资产名前缀保持为 `CLIProxyAPI`；默认桌面/Linux 包启用 CGO 以支持动态库插件，`_no-plugin` 包保留 CGO-free 静态便携构建。
5. 应用 management 定制层并构建 `management.html`。
6. 创建或更新当前仓库 GitHub Release，上传二进制资产、`checksums.txt` 和 `management.html`。
7. Release notes 写入 core upstream 与 management upstream 的版本映射和 release notes。
8. 从一个或多个正在运行的 CPA 实例导出 usage statistics 到 WebDAV。
9. 触发一个或多个 Render 部署。
10. 发送 Telegram 通知。
11. 清理旧 workflow runs。

### Docker 发布 secrets

- `DOCKER_USERNAME`
- `DOCKER_PASSWORD`

### 多实例 usage 备份

workflow 使用一个可选 JSON secret 配置全部 WebDAV 备份目标：

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

每个目标会从自己的 CPA API 导出 usage，并上传到自己的 WebDAV 目录，文件名为：

```text
usage-export-YYYYMMDD_HHMMSS.jsonl
```

workflow 会在每个 WebDAV 目录内保留最近 7 个备份，并同时清理 `.jsonl` 和历史 `.json` 文件。如果 secret 未配置、格式无效或某个目标失败，workflow 会记录警告并继续执行。

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

在 upstream checkout 中验证 embedded usage 包：

```bash
cp -R /path/to/CLIProxyAPI /tmp/cliproxy-check
SRC_ROOT=/tmp/cliproxy-check python3 cliproxyapi-pro-core/patches/apply_upstream_patches.py
go -C /tmp/cliproxy-check mod tidy
go -C /tmp/cliproxy-check test ./internal/embeddedusage/...
```

验证 entrypoint 语法：

```bash
sh -n cliproxyapi-pro-core/entrypoint.sh
```
