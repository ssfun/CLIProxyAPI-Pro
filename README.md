# CLIProxyAPI Pro

CLIProxyAPI Pro 是对两个 upstream 项目的最小化定制层集合：

- `cliproxyapi-pro-core/`：基于 `router-for-me/CLIProxyAPI` 的后端 Docker 构建定制。
- `cliproxyapi-pro-management/`：基于 `router-for-me/Cli-Proxy-API-Management-Center` 的前端管理中心定制。

本项目不维护 upstream 的完整 fork，而是维护可重复应用的 patch、overlay 和构建流程。发布时会拉取 upstream 最新 release，应用本项目定制层，再生成 Pro 版本产物。

## 核心特色

- 持久化保存请求数据，支持导入、导出、webdav 备份
- 账号巡检支持 Codex、Claude、Antigravity、Kimi、xAI
- 账号巡检结果（配额和账号异常状态）支持持久化到配额管理和认证文件
- 账号巡检支持自动化启用、禁用、删除、主动刷新令牌
- 账号巡检针对 Antigravity 软封禁（有配额，但是无法请求）提供深度检测

## 项目结构

```text
.
├── cliproxyapi-pro-core/
│   ├── Dockerfile
│   ├── entrypoint.sh
│   ├── embeddedusage/
│   └── patches/
│
├── cliproxyapi-pro-management/
│   ├── apply.sh
│   ├── apply_customizations.py
│   ├── monitoring-locales.json
│   └── overlay/
│
└── .github/workflows/
    ├── release-core.yml
    └── release-management.yml
```

## 子项目说明

### cliproxyapi-pro-core

后端定制层，用于构建 Pro Docker 镜像。

主要能力：

- 构建 upstream CLIProxyAPI release 的多架构 Docker 镜像。
- 构建与 upstream 平台和打包格式一致的 Pro 二进制 release 资产。
- 内嵌 SQLite usage service。
- 暴露 `/v0/management/usage` 系列 API，包括状态、增量事件轮询和 SSE 流。
- 支持 usage JSONL/NDJSON 导入导出，包含 usage events、模型价格、quota cache、账号巡检调度和最近一次巡检结果快照。
- 支持 WebDAV usage 备份恢复。
- 支持 SQLite-backed quota cache。
- 支持模型价格持久化。
- 启动时强制写入必要 upstream 配置：`usage-statistics-enabled=true` 和 Pro 管理面板仓库。
- 支持后端账号巡检调度器和执行器，巡检探测前可刷新 token。
- 支持 Komari agent 可选启动。
- 将 `/` 跳转到 `/management.html`。
- 增强 `/healthz` 返回信息。

详见：

- `cliproxyapi-pro-core/README.md`
- `cliproxyapi-pro-core/README_EN.md`

### cliproxyapi-pro-management

前端管理中心定制层，用于生成单文件 `management.html`。

主要能力：

- 新增 `/monitoring` 请求监控页面。
- 新增 `/account-inspection` 账号巡检页面。
- 请求量、成功率、延迟、token 和成本统计。
- 模型价格 SQLite 持久化。
- quota cache SQLite 持久化。
- 配额卡片缓存时间显示和单卡刷新。
- 对接后端账号巡检，负责运行控制、状态轮询、结果展示和操作确认。
- 认证文件页面可显示巡检写入的 `last_error` 健康消息。
- 账号巡检结果表格的刷新/重检操作会反馈令牌刷新结果或重检后的业务判定。
- 账号禁用、启用、删除建议与执行。
- 多语言文案补丁。
- 最小化 overlay + patch 应用流程。

详见：

- `cliproxyapi-pro-management/README.md`
- `cliproxyapi-pro-management/README_EN.md`

## 界面预览

<div align="center">

### 请求监控
![请求监控](assets/01.png)

### 请求监控
![请求监控全览](assets/02.png)

### 账号巡检
![账号巡检全览](assets/03.png)

</div>

更多预览请查看 assets 目录。

## 前后端关系

`cliproxyapi-pro-management` 的部分功能依赖 `cliproxyapi-pro-core` 提供的增强 management API。

核心依赖接口包括：

```text
/v0/management/usage
/v0/management/usage/status
/v0/management/usage/events
/v0/management/usage/aggregates
/v0/management/usage/stream
/v0/management/usage/export
/v0/management/usage/import
/v0/management/usage/reset
/v0/management/usage/quota-cache
/v0/management/usage/model-prices
/v0/management/usage/settings
/v0/management/account-inspection/schedule
/v0/management/account-inspection/status
/v0/management/account-inspection/logs
/v0/management/account-inspection/run
/v0/management/account-inspection/pause
/v0/management/account-inspection/resume
/v0/management/account-inspection/stop
/v0/management/account-inspection/actions
```

请求监控会保存 TTFT、HTTP 状态码、结构化错误、reasoning effort 和 service tier 等诊断字段，并提供 `/usage/aggregates` 服务端聚合接口。管理端使用事件 ID 进行增量去重，通过 SQLite 写入通知驱动 SSE，断线后按 cursor 追平；趋势和排行优先使用服务端聚合，日志过滤及原始文本/账号元数据联合搜索使用稳定的服务端分页，并在后台标签页暂停实时渲染。`/usage/status` 会返回最近 dead letter 样本，样本会做敏感字段脱敏。

账号巡检只由后端执行。管理端负责配置调度、启动和控制巡检、轮询状态/进度/结果，通过 WebSocket/WSS 接收日志和实时状态，并确认手动操作。后端自动动作支持连续确认门槛，quota cache 会记录解析器版本和返回结构 hash，便于上游字段变化时排查。

管理端新增一级“路由策略”页面，统一配置 upstream 路由、会话粘性、重试、账号切换、冷却和配额回退，并为 Antigravity、xAI、Codex、Gemini CLI、Gemini、Gemini Interactions、Vertex AI、AI Studio、Claude 和 Kimi 提供按 HTTP 状态码触发的请求保护策略。提供商保护只显示当前已有 API 配置或凭据的提供商。保护功能默认关闭；可先使用 `observe` 模式观察命中情况，再切换到 `enforce` 自动禁用。自动解除只作用于本策略禁用的账号，不会覆盖用户手动禁用状态。

后端巡检时，如果认证记录本来已经进入正常刷新窗口，会在配额/账号探测前尝试刷新 token。巡检刷新路径会跳过 API key 账号、未到刷新窗口的账号，以及仍受 `NextRefreshAfter` 限制的账号；disabled 账号允许刷新。刷新成功后使用刷新后的 auth 继续探测；刷新失败时保留该账号，并跳过该账号本次探测。

后端启动时会强制 `usage-statistics-enabled=true` 和 `remote-management.panel-github-repository=https://github.com/ssfun/CLIProxyAPI-Pro`，并且只在加载到的配置不一致时同步回写 `config.yaml`。

如果只使用 upstream 后端，管理端中的请求监控、SQLite 持久化、模型价格和后端账号巡检等功能会显示错误或空数据。

## 发布流程

### 统一 Pro Release 发布

Workflow：

```text
.github/workflows/release-core.yml
```

Release 版本号以 upstream core 版本为准，并追加 `-pro` 后缀。

示例：

```text
v7.1.18-pro
```

流程概览：

1. 检查 upstream `router-for-me/CLIProxyAPI` 最新 release。
2. 计算 Pro release tag，例如 `v7.1.18-pro`。
3. checkout upstream core 和 upstream management 最新 release。
4. 应用 core patch，构建并推送 Docker 镜像。
5. 构建 Pro 二进制资产：默认桌面/Linux 包启用 CGO 并支持动态库插件，`_no-plugin` 包保留 CGO-free 静态便携构建。
6. 应用 management 定制层，构建单文件 `management.html`。
7. 创建或更新当前仓库的 GitHub Release，并上传二进制、`checksums.txt` 和 `management.html`。
8. release notes 同时包含 core upstream 和 management upstream 的版本映射与 release notes。
9. 执行 WebDAV usage 备份、Render 部署触发、Telegram 通知和 workflow run 清理。

Docker 镜像 tag 使用 Pro release tag：

```text
latest
v7.1.18-pro
```

Docker 构建参数中 `CLIPROXY_VERSION` 用于下载 upstream core tag，`CLIPROXY_BUILD_VERSION` 用于写入运行时版本号，因此镜像和二进制显示的版本是 `v7.1.18-pro`，但源码仍来自 upstream `v7.1.18`。

二进制资产平台和压缩格式与 upstream CLIProxyAPI 保持一致，版本号使用 Pro release tag，因此资产名前缀保持为 `CLIProxyAPI`。默认桌面/Linux 包支持动态库插件；`_no-plugin` 包用于静态或受限环境。Docker 镜像对齐 upstream，使用 CGO-enabled Debian 构建并支持动态库插件：

```text
CLIProxyAPI_7.1.18-pro_linux_amd64.tar.gz
CLIProxyAPI_7.1.18-pro_linux_aarch64.tar.gz
CLIProxyAPI_7.1.18-pro_linux_amd64_no-plugin.tar.gz
CLIProxyAPI_7.1.18-pro_linux_aarch64_no-plugin.tar.gz
CLIProxyAPI_7.1.18-pro_darwin_amd64.tar.gz
CLIProxyAPI_7.1.18-pro_darwin_aarch64.tar.gz
CLIProxyAPI_7.1.18-pro_freebsd_amd64.tar.gz
CLIProxyAPI_7.1.18-pro_freebsd_amd64_no-plugin.tar.gz
CLIProxyAPI_7.1.18-pro_freebsd_aarch64_no-plugin.tar.gz
CLIProxyAPI_7.1.18-pro_windows_amd64.zip
CLIProxyAPI_7.1.18-pro_windows_aarch64.zip
checksums.txt
management.html
```

归档内 README 使用本仓库的 `README.md` 和 `README_EN.md`。

### Management 资产更新

Workflow：

```text
.github/workflows/release-management.yml
```

该 workflow 不再创建独立 release。它只负责在 management upstream 更新时重建 `management.html`，并上传覆盖到当前仓库 latest release。

流程概览：

1. 检查 upstream `router-for-me/Cli-Proxy-API-Management-Center` 最新 release。
2. 读取当前仓库 latest release notes 中记录的 management upstream 版本。
3. 如果 management upstream 更新，或 latest release 缺少 `management.html`，则 checkout management upstream 最新 release。
4. 应用 `cliproxyapi-pro-management` 定制层。
5. 执行 `npm ci` 和 `npm run build`。
6. 将 `dist/index.html` 重命名为 `management.html`。
7. 上传覆盖当前 latest release 中的 `management.html`。
8. 更新 release notes 中的 management 版本映射和 release notes。

这样 `remote-management.panel-github-repository=https://github.com/ssfun/CLIProxyAPI-Pro` 仍然可以通过 GitHub `/releases/latest` 获取到最新 `management.html`。

## 本地构建

### 构建 core Docker 镜像

已发布镜像：

```bash
docker pull sfun/cliproxyapi-pro:latest
```

本地构建：

```bash
docker build -t cliproxyapi-pro ./cliproxyapi-pro-core
```

指定 upstream release：

```bash
docker build \
  --build-arg CLIPROXY_VERSION=v7.1.18 \
  --build-arg CLIPROXY_BUILD_VERSION=v7.1.18-pro \
  -t cliproxyapi-pro:v7.1.18-pro \
  ./cliproxyapi-pro-core
```

### 应用 management 定制层

```bash
./cliproxyapi-pro-management/apply.sh /path/to/Cli-Proxy-API-Management-Center
```

或：

```bash
python3 ./cliproxyapi-pro-management/apply_customizations.py /path/to/Cli-Proxy-API-Management-Center
```

目标目录必须是 upstream management center checkout，并包含：

- `src/`
- `package.json`

应用后可在目标目录执行：

```bash
npm install
npm run type-check
npm run build
```

## Runtime 数据目录

core 镜像默认使用：

```text
/CLIProxyAPI/usage
```

该目录保存：

- usage SQLite 数据库：`usage.sqlite`
- 账号巡检调度文件：`account-inspection-schedule.json`
- 最近一次账号巡检结果快照：`account-inspection-snapshot.json`
- quota cache
- model prices
- monitoring settings

Usage 导入导出会使用 NDJSON 元数据记录保存模型价格、quota cache、监控设置、账号巡检调度和最近一次已结束的巡检结果快照，因此 WebDAV 备份恢复可以随 usage events 一起恢复监控相关状态。恢复的巡检快照用于迁移和问题追溯，默认只读；发起新的完整巡检后才允许重检、刷新令牌或执行账号变更。巡检日志不进入快照。监控日志保留会在每天服务器本地时间 02:00 自动清理，保存设置时也会立即清理一次；WebDAV 备份可单独设置保留天数，成功备份后会删除过期的 `usage-export-*.jsonl` 文件。

新导出的备份包含完整性 manifest。导入和 WebDAV 自动恢复默认拒绝无 manifest 的旧版备份；只有需要自动恢复某个可信旧备份时，才设置 `USAGE_ALLOW_LEGACY_RESTORE=true`。

建议在生产环境中为该目录配置持久化 volume。

## 关键环境变量

### Usage service

```text
USAGE_SERVICE_ENABLED
USAGE_DATA_DIR
USAGE_DB_PATH
USAGE_BATCH_SIZE
USAGE_POLL_INTERVAL_MS
USAGE_QUERY_LIMIT
```

### WebDAV 恢复

```text
WEBDAV_URL
WEBDAV_USERNAME
WEBDAV_PASSWORD
MANAGEMENT_PASSWORD
USAGE_ALLOW_LEGACY_RESTORE
```

### 账号巡检

```text
ACCOUNT_INSPECTION_SCHEDULE_PATH
```

### Komari agent

```text
KOMARI_SERVER
KOMARI_SECRET
```

完整说明见 `cliproxyapi-pro-core/README.md`。

## 设计原则

本项目遵循最小化定制原则：

- 不复制 upstream 完整源码。
- 尽量通过 overlay 和 patch 注入功能。
- upstream 更新时重新应用定制层。
- 文档、脚本和 workflow 尽量保持可验证、可重复。

## 版权与鸣谢

本仓库是围绕 upstream 项目的定制层和发布流程，不声明拥有 upstream 代码、名称或资源的版权。upstream 代码和产物仍保留其原始版权声明和许可证。

- `router-for-me/CLIProxyAPI` 使用 MIT License。其 upstream `LICENSE` 当前声明：
  - Copyright (c) 2025-2005.9 Luis Pater
  - Copyright (c) 2025.9-present Router-For.ME
- `router-for-me/Cli-Proxy-API-Management-Center` 使用 MIT License。其 upstream `LICENSE` 当前声明：
  - Copyright (c) 2026 Router-For.ME

特别鸣谢：

- [router-for-me/CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) — 本项目 core 定制层所基于的 upstream 后端项目。
- [router-for-me/Cli-Proxy-API-Management-Center](https://github.com/router-for-me/Cli-Proxy-API-Management-Center) — 本项目 management 定制层所基于的 upstream 管理 UI 项目。
- [seakee/CPA-Manager](https://github.com/seakee/CPA-Manager) — 重要的 CLIProxyAPI 管理与监控项目，对 Pro usage、monitoring 和账号巡检方向提供了参考。
- 感谢 [Linux.do](https://linux.do/) 社区对项目推广与反馈的支持。

## 参考文档

- Core 中文文档：`cliproxyapi-pro-core/README.md`
- Core English README：`cliproxyapi-pro-core/README_EN.md`
- Management 中文文档：`cliproxyapi-pro-management/README.md`
- Management English README：`cliproxyapi-pro-management/README_EN.md`
- English project overview：`README_EN.md`
