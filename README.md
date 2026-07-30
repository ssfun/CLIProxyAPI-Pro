# CLIProxyAPI Pro

CLIProxyAPI Pro 是对两个 upstream 项目的最小化定制层集合：

- `cliproxyapi-pro-core/`：基于 `router-for-me/CLIProxyAPI` 的后端 Docker 构建定制。
- `cliproxyapi-pro-management/`：基于 `router-for-me/Cli-Proxy-API-Management-Center` 的前端管理中心定制。

本项目不维护 upstream 的完整 fork，而是维护可重复应用的 patch、overlay 和构建流程。发布时会拉取 upstream 最新 release，应用本项目定制层，再生成 Pro 版本产物。

## 核心特色

- 持久化保存请求数据，支持导入、导出、webdav 备份
- 账号巡检支持 Codex、Claude、Antigravity、Gemini CLI、Kimi、xAI
- 账号巡检结果（配额和账号异常状态）支持持久化到配额管理和认证文件
- 账号巡检支持自动化启用、禁用、删除、主动刷新令牌
- 账号巡检针对 Antigravity 软封禁和 xAI 可用性异常提供可选深度检测
- 路由策略页面统一管理 upstream 路由行为与按 provider 配置的请求状态保护
- 内置动态代理池插件，把多个 HTTP/SOCKS 节点汇聚为固定的本地 SOCKS5 地址，支持轮询、加权、健康隔离与故障转移
- 内置 OAuth 模型策略插件，可按多个提供商的账号套餐分别排除不可用模型，并同步约束模型列表和账号调度

## 项目结构

```text
.
├── cliproxyapi-pro-core/
│   ├── Dockerfile
│   ├── Dockerfile.runtime
│   ├── QUOTA_PROVIDER.md
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
├── cliproxyapi-pro-plugins/
│   ├── proxy-pool/
│   └── oauth-model-policy/
│
├── scripts/validation/
└── .github/workflows/
    ├── ci.yml
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
- 支持 usage JSONL/NDJSON 导入导出，包含 usage events、模型价格、quota cache、Pro 设置、路由运行状态、账号巡检调度和最近一次巡检结果快照。
- 支持 WebDAV usage 备份恢复。
- 支持 SQLite-backed quota cache。
- 支持模型价格持久化。
- 支持 QuotaProvider 插件协议和 Gemini CLI legacy adapter。
- 支持通用 `AuthModelFilter` 插件协议；预打包的 `oauth-model-policy` 可按 xAI、Codex、Claude、Gemini CLI、Antigravity 和 Kimi OAuth 套餐排除模型。
- 启动时在内存中强制必要 upstream 配置；仅修改 YAML 中已存在的键，禁止自动新增键。
- 支持后端账号巡检调度器和执行器，巡检探测前可刷新 token。
- 支持统一路由策略与请求状态保护 API。
- 支持 Komari agent 可选启动。
- 标准 macOS、Windows amd64、Linux Release 和 Docker 镜像预打包 `proxy-pool` 与 `oauth-model-policy` 动态插件；Windows ARM64、FreeBSD 与 `_no-plugin` 产物暂不内置动态插件。
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
- 新增 `/routing` 路由策略页面。
- 新增 `/proxy-pool` 代理池页面，负责节点配置、连通性测试、运行统计和全局代理接管/恢复。
- 新增 `/oauth-model-policy` 可视化配置页，按提供商和 OAuth 套餐编辑模型排除规则、自定义套餐、回退策略和套餐探测缓存。
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

### 请求监控概览

![请求监控](assets/01.png)

### 请求监控完整视图

![请求监控全览](assets/02.png)

### 账号巡检概览

![账号巡检全览](assets/03.png)

### 账号巡检设置

![账号巡检设置](assets/04.png)

### 巡检策略详情

![巡检策略详情](assets/05.png)

</div>

更多预览请查看 assets 目录。

## 前后端关系

`cliproxyapi-pro-management` 的部分功能依赖 `cliproxyapi-pro-core` 提供的增强 management API。

核心依赖接口按稳定前缀分为：

```text
/v0/management/usage
/v0/management/usage/*
/v0/management/quota/fetch
/v0/management/account-inspection/*
/v0/management/routing-policy
/v0/management/routing-policy/*
```

完整 method/path 清单以 `cliproxyapi-pro-core/README.md` 为准。

请求监控会保存 TTFT、HTTP 状态码、结构化错误、reasoning effort 和 service tier 等诊断字段，并提供 `/usage/aggregates` 服务端聚合接口。管理端使用事件 ID 进行增量去重，通过 SQLite 写入通知驱动 SSE，断线后按 cursor 追平；趋势和排行优先使用服务端聚合，日志过滤及原始文本/账号元数据联合搜索使用稳定的服务端分页，并在后台标签页暂停实时渲染。`/usage/status` 会返回最近 dead letter 样本，样本会做敏感字段脱敏。

账号巡检只由后端执行。管理端负责配置调度、启动和控制巡检、轮询状态/进度/结果，通过 WebSocket/WSS 接收日志和实时状态，并确认手动操作。后端自动动作支持连续确认门槛，quota cache 会记录解析器版本和返回结构 hash，便于上游字段变化时排查。

管理端新增一级“路由策略”页面，统一配置 upstream 路由、会话粘性、重试、账号切换、冷却和配额回退，并为 Antigravity、xAI、Codex、Gemini CLI、Gemini、Gemini Interactions、Vertex AI、AI Studio、Claude 和 Kimi 提供按 HTTP 状态码触发的请求保护策略。提供商保护只显示当前已有 API 配置或凭据的提供商。保护功能默认关闭；可先使用 `observe` 模式观察命中情况，再切换到 `enforce` 自动禁用。自动解除只作用于本策略禁用的账号，不会覆盖用户手动禁用状态。

后端巡检时，如果认证记录本来已经进入正常刷新窗口，会在配额/账号探测前尝试刷新 token。巡检刷新路径会跳过 API key 账号、未到刷新窗口的账号，以及仍受 `NextRefreshAfter` 限制的账号；disabled 账号允许刷新。刷新成功后使用刷新后的 auth 继续探测；刷新失败时保留该账号，并跳过该账号本次探测。

后端启动时会在内存中强制 `usage-statistics-enabled=true` 和 Pro 管理面板仓库。只有对应 YAML 键已经存在且值不一致时才会修改；缺失键不会新增。请求保护设置保存在 `usage.sqlite`，不写入上游 `config.yaml`。

如果只使用 upstream 后端，管理端中的请求监控、SQLite 持久化、模型价格、后端账号巡检和路由保护等功能会显示错误或空数据。

## 发布流程

### 统一 Pro Release 发布

Workflow：

```text
.github/workflows/release-core.yml
```

Release 版本号以 upstream core 版本为准，并追加 `-pro` 后缀。

示例：

```text
v<core-version>-pro
```

流程概览：

1. 检查 upstream `router-for-me/CLIProxyAPI` 最新 release。
2. 计算 Pro release tag，例如 `v<core-version>-pro`。
3. checkout upstream core 和 upstream management 最新 release。
4. 应用 core patch 并构建 Pro 二进制资产：默认桌面/Linux 包启用 CGO 并支持动态库插件，`_no-plugin` 包保留 CGO-free 静态便携构建。
5. 复用已构建的 Linux 资产，通过 `Dockerfile.runtime` 组装并推送多架构 Docker 镜像。
6. 应用 management 定制层，构建单文件 `management.html`。
7. 创建或更新当前仓库的 GitHub Release，并上传二进制、`checksums.txt` 和 `management.html`。
8. release notes 同时包含 core upstream 和 management upstream 的版本映射与 release notes。
9. 执行 WebDAV usage 备份、Render 部署触发、Telegram 通知和 workflow run 清理。

Docker 镜像 tag 使用 Pro release tag：

```text
latest
v<core-version>-pro
```

Docker 构建参数中 `CLIPROXY_VERSION` 用于选择 upstream core tag，`CLIPROXY_BUILD_VERSION` 用于写入 Pro runtime 版本号。

二进制资产平台和压缩格式与 upstream CLIProxyAPI 保持一致，版本号使用 Pro release tag，因此资产名前缀保持为 `CLIProxyAPI`。默认桌面/Linux 包支持动态库插件；`_no-plugin` 包用于静态或受限环境。Docker 镜像对齐 upstream，使用 CGO-enabled Debian 构建并支持动态库插件：

```text
CLIProxyAPI_<core-version>-pro_<os>_<arch>.<archive>
CLIProxyAPI_<core-version>-pro_<os>_<arch>_no-plugin.<archive>
checksums.txt
management.html
```

归档内 README 使用本仓库的 `README.md` 和 `README_EN.md`。

### Management 资产更新

Workflow：

```text
.github/workflows/release-management.yml
```

该 workflow 不再创建独立 release。它会在 management upstream 更新、latest release 缺少 `management.html`，或手动触发时重建资产，并上传覆盖到当前仓库 latest release。

流程概览：

1. 检查 upstream `router-for-me/Cli-Proxy-API-Management-Center` 最新 release。
2. 读取当前仓库 latest release notes 中记录的 management upstream 版本。
3. 如果 management upstream 更新，或 latest release 缺少 `management.html`，则 checkout management upstream 最新 release。
4. 应用 `cliproxyapi-pro-management` 定制层。
5. 执行 `bun install --frozen-lockfile` 和 `bun run build`；Bun 版本读取 upstream `package.json`。
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
docker build -t cliproxyapi-pro -f cliproxyapi-pro-core/Dockerfile .
```

指定 upstream release：

```bash
UPSTREAM_TAG=vX.Y.Z
PRO_TAG="${UPSTREAM_TAG}-pro"
docker build \
  --build-arg CLIPROXY_VERSION="${UPSTREAM_TAG}" \
  --build-arg CLIPROXY_BUILD_VERSION="${PRO_TAG}" \
  -t "cliproxyapi-pro:${PRO_TAG}" \
  ./cliproxyapi-pro-core
```

Dockerfile 的 builder 和 runtime 基础镜像使用不可变 digest；Debian 软件仓库和外部工具链仍是滚动依赖，因此项目不承诺跨时间逐字节一致的完整镜像。本地需要确定的 source binary 时，还应传入不可变 `CLIPROXY_COMMIT` 和固定 `SOURCE_DATE_EPOCH`；release workflow 会从不可变输入提交推导该时间，并规范化全部 Core 归档。

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
bun install --frozen-lockfile
bun run test
bun run lint
bun run type-check
VERSION=review bun run build
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
- Pro settings

Usage 导入导出会使用 NDJSON 元数据记录保存模型价格、quota cache、监控设置、Pro 设置、账号巡检调度和最近一次已结束的巡检结果快照，因此 WebDAV 备份恢复可以随 usage events 一起恢复监控相关状态。恢复的巡检快照用于迁移和问题追溯，默认只读；发起新的完整巡检后才允许重检、刷新令牌或执行账号变更。巡检日志不进入快照。监控日志保留会在每天服务器本地时间 02:00 自动清理，保存设置时也会立即清理一次；WebDAV 备份可单独设置保留天数，成功备份后会删除过期的 `usage-export-*.jsonl` 文件。

新导出的备份包含完整性 manifest。管理 API 和页面默认拒绝或要求确认无 manifest 的旧版备份；Docker WebDAV 自动恢复在过渡阶段会强制启用旧版兼容导入，带 manifest 的新备份仍会严格校验完整性。

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
```

### 账号巡检

```text
ACCOUNT_INSPECTION_SCHEDULE_PATH
ACCOUNT_INSPECTION_SNAPSHOT_PATH
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
