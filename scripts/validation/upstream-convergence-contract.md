# 通用能力收敛契约

## 基线字段

本表仅覆盖本轮用量解析、连接版本及列出的退役边界，不代表重新审查了全部业务模块。

| 字段 | Core | Management |
| --- | --- | --- |
| `last_reviewed_upstream_ref` | v8.0.56 / `585f97deac9aa57c700301221fb1a7085e1827cc` | v1.27.2 / `f154fa9380825c4fe94bc577a9abaa7e505f8875` |
| `last_validated_upstream_ref` | v8.0.56 / `585f97deac9aa57c700301221fb1a7085e1827cc`（本地基线比较通过，仍有上游已知失败） | v1.27.2 / `f154fa9380825c4fe94bc577a9abaa7e505f8875`（本地验证，见下方证据） |
| `actual_build_upstream_sha` | 读取每次 Core 构建的 `CORE_SHA` / `CLIPROXY_COMMIT` | 读取每次 Management 构建的 `MANAGEMENT_SHA` |

`actual_build_upstream_sha` 是产物属性，不是仓库固定版本。CI 的
`validation-state.json` 保存 `head_sha`、`core_sha`、`management_sha` 及配对的
Management 资产摘要；只有对应验证成功的记录才是 hosted 验证证据。
本地验证必须保存精确输入 SHA、命令、退出码和 E2E receipt；不能据此宣称已经发布。
修改面清单中的版本仅说明清单核对输入；归属表的历史批次版本不是最新构建版本。

## 已收敛的算法和状态

| 能力 / 上游文件与符号 | Pro 剩余差异 | 退役后继续执行的验证 |
| --- | --- | --- |
| `internal/runtime/executor/helps/plugin_executor_usage.go`: `ParsePluginExecutorResponseUsage`, `ObservePluginExecutorStreamUsage` | 保留 `pluginhost/plugin_executor_usage.go` 的转换后发布时点、失败状态、响应头上下文、空流错误和模型观察；不维护协议分派、JSON 提取或字段枚举 | `plugin-responses-usage-scenarios.md`；生成树 `go test ./internal/pluginhost ./internal/runtime/executor/helps`；真实服务器 `e2e/v8-usage-http/run.py` |
| `internal/runtime/executor/helps/usage_helpers.go`: `StreamUsageBuffer.ObserveClaudeStream`, `ObserveMergedStreamUsage`, `MergeStreamUsageDetail` | 删除 Pro `ObserveClaude`；保留 `prepareUsageRecordForPublish` 同步结算和最终请求模型观察 | `go test ./internal/runtime/executor/helps ./internal/runtime/executor`；`e2e/v8-claude-usage-http/run.py`；缓存首事件保留、错误前用量和取消终态断言不能随方法删除 |
| `src/services/api/client.ts`: `connectionRevision`, `getConnectionRevision`, `captureConnection` | 删除第二个计数；保留 AbortController、调用方 signal 合并、旧成功/错误拒绝和 Pro 排队请求版本检查 | Management `tests/apiClientConnectionIsolation.test.ts` 和 `tests/proManagementTransport.test.ts`；地址、仅密钥、A→B→A、迟到 401/成功、主动取消、排队切换 |

非流式 Responses 同时存在两处用量时，选择顺序由上游解析器负责（有效嵌套用量优先）；不再承诺旧 Pro 的顶层优先。结构化字段也由上游识别。无用量请求仍必须完成一次成功或失败发布，不能因解析结果为空跳过同步结算和终态记录。
生产流式适配器此前已直接调用上游 helper，本轮删除的是残留辅助分支；该分支也交付完整 chunk，由上游遍历事件，不再截取第一条 JSON。
TTFT 继续调用原生 `ObservePluginExecutorStreamTTFT`，不得在收敛时覆盖上游直接插件路由的修正。

## 仍保留的通用补丁与退出门槛

下列验证必须先在**不含对应补丁的干净上游**通过，再删除补丁并在 Pro 生成树复验。
“存在同名 API”不构成等价证据。每次退役记录精确 SHA 和 red/green artifact。

| 上游对应位置 / 符号 | Pro 剩余差异 / 无补丁验收条件 | 删除后保留的验证 |
| --- | --- | --- |
| `sdk/cliproxy/usage`: `Manager.Publish`, `RegisterNamed`；helps 用量发布 | 异步分发不替代发布前同步 API key 配额结算；注册者身份和旧 owner 恢复语义须一致 | `quota-retained-usage-patch-scenarios.md`、用量 HTTP receipt 与 API key policy runtime smoke；结算次数和失败记录均检查 |
| `internal/runtime/executor/codex_websockets_errors.go`: Retry-After 错误帧处理 | 连接数限制且含有效 Retry-After 时，header 优先于零秒 fallback；常规 HTTP、图片和握手已交还上游 | executor 包的 `TestCodexRetryAfter*`；有效/无效头、HTTP-date、原生 reset、非 429 |
| helps 请求模型观察与 executor 发出请求边界 | 最终实际 HTTP/帧模型审计，不以别名或响应 model 代替 | executor/helps 模型审计测试、插件模型测试及 HTTP usage receipt |
| `internal/pluginhost/host_model_stream_callbacks.go`: 流回调生命周期 | RPC 序列化失败必须清理注册流；成功返回不能提前关闭；保持实例归属与同步结算 | pluginhost 包和 `lifecycle-scheduler-scenarios.md` |
| `src/components/ui/Modal.tsx`、`ConfirmationModal.tsx`、scroll lock | 覆盖层栈、异步关闭生命周期、确认队列和嵌套锁；上游焦点/Escape 支持不等价 | Management 原生及 Pro modal/confirmation 测试，嵌套关闭、卸载和焦点恢复 |
| `internal/pluginhost/host_test.go`: `reconfigureOnce` 测试局部变量 | `pluginhost_unused_once.patch` 仅移除无用 copylock；v8.0.56 仍需要；上游移除或改正后退役 | `core.sh` 对 candidate/clean baseline 同样应用 fixture，反向检查和 `go vet`；漂移必须失败 |

额度持久化、认证身份隔离、账号/密钥策略、SQLite 统计备份与动态代理控制仍有独立业务语义；本轮不为这些模块声明新的退役完成状态。具体分组继续遵守 `PATCH_MAINTENANCE.md` 和 `v8-capability-ownership.md`。

## 已退役能力禁止重新接管

原生可视化配置核心、凭据基础 API、额度批量加载、完整 transport 构造、Claude 通用失败发布和常规 HTTP Retry-After 均由上游承担。修复新问题时先给出具体失败输入与上游缺口，再添加最小接线；不得重新复制整个算法或函数。

## 本轮验证证据（2026-11-05）

变更基于 Pro `66717c08191dd2246fde7b0dfb1ba0cfbd7eddee` 的未提交工作区；不能仅凭该提交号复现本轮变更，需同时保留工作区 diff / 文件摘要。

- 仓库：`bash scripts/validation/repo.sh`，189 项检查通过；本地日志 `/private/tmp/pro-convergence-repo-validation.log`。
- Management：精确 v1.27.2 重放，41 个原生文件修改面与重复应用 diff hash 一致；124 项 Python、2,268 项 Bun、18 项传输/元数据和 7 项 signal fallback 测试通过，lint、类型检查与构建通过。复现入口 `cliproxyapi-pro-management/e2e/connection-revision/run.sh`；本地日志和 SHA256SUMS 在 `/private/tmp/management-connection-revision-artifacts`。这是传输集成验证，不是浏览器 E2E。生成树 `add -N` 后暴露的 `maskSensitiveText.ts` 末尾空行是既有 overlay 内容，本轮未改动。
- Core 完整验证：`VALIDATION_STATICCHECK=1 VALIDATION_RACE=1 VALIDATION_INSPECTION_E2E=1 VALIDATION_RELEASE_HTTP_GATES=1 bash scripts/validation/core.sh <clean-upstream> <models.json>` 返回 0；补丁失败原子性、101 文件修改面、重复应用保护、staticcheck、vet、CGO/non-CGO 构建、积分恢复与 20 步巡检 E2E 通过。candidate 与独立 clean baseline 各 6 个相同失败事件（OAuth 测试清理 race、Claude allocation 阈值），新增失败为 0；没有降低阈值或跳过测试。models SHA `26f77650ca9f30630f244989ad47962c2ef02b33`。精确命令/源码摘要在 `/private/tmp/pro-core-parser-convergence/inputs.json`，完整摘要在 `final-validation/summary.md`。
- Core 实际服务器：`e2e/v8-usage-http/run.py` 5/5 和 `e2e/v8-claude-usage-http/run.py` 10/10；receipt、SQLite 行、请求响应及配置位于 `/private/tmp/pro-core-parser-convergence/{usage-http,claude-http}`，二进制 SHA256 为 `05f74d90bb83c2f07fa94a8d9870b1cdeef51a8220a09af6592489e8c0469731`。

本地临时目录是本次证据位置，不是版本库持久资产；复验使用仓库中的运行入口生成新的产物目录。本轮没有推送或发布，不推进 hosted validation-state。
