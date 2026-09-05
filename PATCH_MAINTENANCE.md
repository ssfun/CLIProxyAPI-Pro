# Pro 定制维护边界

维护来源是 Core `patches/sources`、独立补丁文件，以及 Management `overlay`。生成后的 upstream 树仅用于验证。静态业务模块保留在当前进程中；不为目录拆分引入新的插件协议。

## 补丁用途与退出条件

精确文件清单由 `scripts/validation/contracts/*-upstream-modified-files.txt` 维护。以下分组覆盖该清单中的宿主注入职责；同一宿主文件可能承载多个分组。

| 分组 | 维护入口 | 保留原因 | 删除或缩小条件 |
| --- | --- | --- | --- |
| 启动、配置、管理 API | Core generator；`pro/app`、`pro_management_runtime.go` | 组合 Pro 生命周期并复用管理认证 | upstream 提供等价生命周期和管理路由注册点后，保留模块实现，只替换注入 |
| 请求执行、协议转换 | generator；`runtime/executor`、`translator` sources | 错误语义、取消、用量与协议兼容 | upstream 同一执行路径已包含等价行为，完整 executor / translator 测试证明可删 |
| 账号、调度和请求策略 | `auth_runtime_state.go`、`auth_account_policy.go`、`pro/apikeypolicy` | 状态恢复、请求授权及调度约束 | upstream 能覆盖请求、模型列表、热更新全部入口，并保持已有数据语义 |
| 配额与账号巡检 | `account_inspection_*.go`、`plugin_quota_*.go`、`pro/quota` | 内建账号操作和历史配额适配 | 新宿主钩子覆盖刷新、删除、状态更新和账号绑定 HTTP 后逐条迁移 |
| 存储、统计与备份 | `pro/observability`、`pro/storage`、`pro/backup`、`pro/state` | SQLite、NDJSON 和运行状态一致性 | 兼容读取、恢复事务和在途请求一致性通过验证后，才删除旧适配；不自动删除历史数据 |
| 插件宿主扩展 | generator；`pluginhost`、`pluginstore`、SDK ABI/API | 已有外部插件使用的通用宿主能力 | upstream ABI/API 提供等价能力且现有调用者验证通过 |
| 管理界面集成 | `apply_customizations.py`、模块 manifest、`overlay` | 路由、导航、认证卡片和原生 UI 扩展 | upstream 原生提供同等扩展点或功能后，删除对应定制与过期结构断言 |
| 产物与发布 | Dockerfile、entrypoint、workflows | 可重复构建、平台产物和面板更新 | 上游产物覆盖 Pro 功能及发布契约后逐项简化 |

## 修改与验证要求

1. 每项新补丁在提交说明中写明对应分组、具体触发问题和退出条件。业务逻辑优先进入现有模块，generator 只保留确有必要的注入；避免另建补丁注册框架。
2. 在最新 upstream release 的精确 SHA 上回放。更新文件清单的基线注释；范围变化必须逐文件解释，不能靠扩大白名单绕过漂移。
3. `scripts/validation/repo.sh` 检查结构契约；Management 完成测试、lint、类型检查、构建及重复应用验证；Core 完成受影响包测试、race、补丁失败原子性与构建。
4. Core executor 验证运行整个包。源码字符串断言只守护注入点，不能代替取消、错误、慢响应和恢复行为测试。
5. 前端后台轮询从请求完成后计时；旧响应不得覆盖新连接、手动刷新或成功写入后的状态。保留旧数据时显示更新时间和刷新失败状态。
6. 监控全历史/近期概览在当前连接内缓存最多 60 秒，显示独立更新时间；手动刷新、数据 generation 变化和跨日会重新查询。趋势保留短周期增量触发刷新。继续使用 SQLite，先测量查询成本再增加存储设施。
7. 账号策略内部消费版本化 `quota.PlanEvidence`；历史卡片缓存只在读取适配边界解码。现有缓存选择优先级、SQLite 内容与备份格式不变，新增业务字段须验证 legacy 与标准化证据得到相同套餐判定。
