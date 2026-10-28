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

### 额度保护恢复

巡检额度限制由 `pro/routing/quota_protection.go` 定义；`auth/pro_quota_protection.go` 负责 SQLite 状态和按来源版本校验，Management `quota_recovery.go` 负责定向复查。`routing_policy.go` 合成上游冷却和巡检保护，并提供定向检查与按来源、模型、版本解除的恢复入口；不写入 `routing:` 平行保护或接管 `disabled`。固定账号检查复用 upstream 结果处理，`pinned_execution.go` 的结果身份保护防止旧探测覆盖替换账号。复用 upstream selector/scheduler 的阻塞评估、冷却到期重新选择以及现有账号策略刷新入口，不修改优先级、权重或轮询算法。退出条件是 upstream 提供带来源隔离、持久化、阈值复查及凭据版本保护的等价接口；届时迁移状态后删除对应接入点。旧的无归属禁用状态不得批量自动启用。

巡检错误仅作为观测保存，不写入原生调度错误；行政启用不解除额度保护或已有不可用状态。手动解除只更新限制字段，健康结论必须来自新探测。后台恢复失败推进退避，持久化失败时用按保护版本绑定的临时退避避免队列饥饿。管理端检查共享 55 秒总时限，返回阶段结果和最新限制；前端无论成功、失败或超时都重新读取看板。定向真实请求携带管理层观察的账号身份，并在准备和发送前复核，继续保留结果回写的身份与限制指纹保护。

Core 验证对 candidate 和 clean baseline 同样应用 `codex_live_media_loopback.patch`：仅将同进程 WebRTC 桥接测试的四个节点限制到回环 ICE 候选，保留真实音频、文本和二进制通道断言；不修改生产网络配置、不跳过测试。上游测试自行隔离主机网络接口后移除该 fixture；补丁漂移必须显式失败。

### Management v1.25.0 compatibility

This compatibility change was introduced against v1.25.0
`b87b9487f63e08ad97b1fb4e7c17b4adb811b922`.
Retire the legacy visual-layout adapter: upstream now edits v8 paths natively.
`src/hooks/useVisualConfig.ts` stays byte-for-byte upstream, including recovered
payload AST lineage and concurrent-list conflict checks. The historical
`e2e/v8-visual-config` harness has been removed; its legacy/mixed-layout scenarios
are not the current contract;
upstream `visualConfigV8`, `visualConfigRebase`, `visualConfigPayloadAst`, and
`visualConfigConcurrency` tests validate the native behavior. In particular, do
not promise legacy fallback or byte-identical no-op serialization.

Keep native credential operations and normalization on upstream `/credentials`
endpoints. Pro connection tests and identity-aware connection-test model lookups
live in `pro/authFiles/connectionTestApi.ts`. Attach cancellation to the native
connection revision increment without replacing its ABA guard. Inspection WebSocket URLs accept a bare base or
an explicit v0/v8 prefix and use the Pro v0 route described below.

The cooldown hint test fixture compares HTML-escaped translated text, preserving
its assertion. Clean upstream's full test suite also fails on the unescaped
English apostrophe; remove this fixture when upstream escapes the expectation.
The reviewed patch surface removes `src/hooks/useVisualConfig.ts` and adds only
`tests/authFileCooldowns.test.ts`; all other modified upstream paths are unchanged.
Credential exports reuse upstream `authFilesApi.download`, avoiding a second
hard-coded route that can drift independently of the API client.

Native Management requests retain `/v8/management`. Pro extensions use the
explicit `proApiClient` namespace on the same authenticated API client, selecting
`/v0/management` only at dispatch. This includes legacy configuration reads used
by Pro features, quota cache, data management, policy, inspection, model catalog,
and panel update routes. Do not globally downgrade the native client or depend on
new Core aliases: management.html updates independently of the released Core.
Pro SSE/WebSocket URLs use the same v0 URL helper, preserving reverse-proxy path
prefixes and accepting bare, v0-suffixed, or v8-suffixed server addresses.
Route regression tests must exercise the real HTTP transport, not only mocked
apiClient methods. The release contract harness additionally checks the existing
Core binary and quota persistence across a process restart.

### 第一批补丁收敛

Core v8.0.4 上的纯新增声明保留在原 Go 包的 Pro 文件中：插件自动安装配置访问器、布尔标量保存辅助函数、LRU `Purge` 和插件 executor provider 查询。保留原签名、私有字段访问及锁/回调语义，不增加包装层。`config_yaml.go`、`config_normalization.go`、`bounded_lru.go`、`executor_route.go` 恢复为原生文件；布尔保存函数直接生成最终实现，删除先插入再替换的流程。原生修改文件数由 101 降至 97。

Management v1.25.0 的 `maskSensitiveText` 进入 `pro/shared`，继续复用原生 `maskApiKey`，脱敏规则不变；`utils/format.ts` 恢复为原生文件，原生修改文件数由 41 降至 40。登出复用上游已有连接清空，保留 Pro 请求取消和缓存清理；移除未使用的旧 Gemini payload 类型，保留实际配额状态类型。此前 cooldown 成功后刷新凭据缓存的修复继续保留。

本批仅调整声明归属和清理冗余。凭据 TTL 缓存、Pro API 提取、配额时间戳和共享 transport 进一步收敛需要单独的行为验证，不在本批扩大改动。

### 第二批补丁收敛

Management 的凭据列表使用原生无 TTL 缓存实现；删除全局 2 秒缓存和七类写操作失效包装，`services/api/authFiles.ts` 恢复上游原文。成功 cooldown reset 后的读取直接请求当前凭据，不再依赖第一批失效修复。移除缓存解决热缓存跨服务器复用和外部写入后刷新仍返回旧状态的问题；代价是重复读取恢复到每次一次 HTTP 请求，局部测试不代表生产网络延迟。

连接测试 DTO 和 API 位于 `pro/authFiles/connectionTestApi.ts`。测试执行仍经共享认证 transport 使用 Pro v0 路径；模型查询保留原生 v8 凭据路径、`auth_index` 身份和 `purpose=connection-test` 静态回退。原生普通模型查询、上传、导出、刷新和批量删除继续直接复用上游实现。

配额成功时间戳统一在已有 adapter registry 装饰 `buildSuccessState`；复用 `withQuotaCachedAt`，已有数字时间戳保留，未提供时才记录当前时间。`useQuotaActions.ts` 和 `useQuotaBatchLoader.ts` 恢复上游原文；单卡、批量、Devin 自动读取及账号卡片使用同一入口，不装饰 loading/error 或 SQLite hydration，也不改变 store、generation/revision 和连接隔离。原生修改文件数由 40 降至 37，Core 保持第一批的 97。

### 第三批补丁收敛

Core 的 13 处代理/transport 整函数替换改为局部锚点，仅修改有效代理解析、代际 cache key 和构造接入点。五份同包 Pro 文件承接已有 key 类型和解析一次的薄封装；上游 TLS、HTTP/1.1、超时、压缩、fallback 与连接池主体留在原生文件。独立 transport 收窄后的 119 个声明与第二批基线 AST 一致；随后三个代际同步函数复用 `internal/cache.SyncGeneration`，仍使用各自的原子状态、cache 和 Purge 回调，保持 Load/CAS/Purge 顺序与现有锁语义。

WebSocket 与 WebRTC sideband 共享同包 `realtime_usage_pro.go` 的终态识别、Codex/OpenAI usage fallback、total fallback 和 quota delta 转换。active turn 清空、response/payload event ID、重复事件处理、准入与结算失败处理仍由原 relay 负责；WS 的取消上下文与 sideband 的 `WithoutCancel` 不合并。空 usage 在两条 relay 的既有不同处理也保留。

本批 Core 原生修改文件仍为 97，Management 保持 37。代理部分整函数替换由 13 降至 0，生成器嵌入的替换 Go 文本由 539 降至 260 行；局部替换由 9 增至 36、旧锚点文本由 44 增至 191 行。收益是上游主体可以自然跟随，代价是仍须维护必要局部锚点；不以预设文件数下降替代语义验证。新增七份源码均登记碰撞检查、queue 与 gofmt。退出条件仍是上游提供等价的运行时代理与持久化结算边界。

### 持续发布验收

增量 CI 的成功检查点使用显式取消状态条件，避免可选验证任务跳过后连带跳过 `validation-state`；summary 失败、取消或手动全量运行不写入检查点。

Core 发布复用本次 Linux amd64 发布 archive，运行既有配置、流式用量及真实前端路由/SQLite 重启 E2E。配额 provider E2E 使用同一精确 Core、Pro 和 models 输入构建的独立进程 fixture；其哈希与输入另存，不把它当作产品可执行文件。新面板再与冻结的当前已发布 Core 配对；两个二进制哈希相同则只运行一组，没有已发布版本时仅验证候选。Management 单独发布必须与目标 Release 的实际二进制配对，不重编一个新 Core 替代。

配对最多包含候选和当前发布两组，不重建历史 Core、不重复前端完整测试与构建。下载核验 archive checksum 和可用的 GitHub asset digest；GitHub API 错误不能被当作首发。所有 E2E 成功后才上传供发布消费的面板产物，失败回执与日志独立归档。门禁范围是本地真实 HTTP、TypeScript transport 和 SQLite；浏览器渲染、外部供应商及插件 ABI 保留各自验证边界。

### 最新上游重复适配清理（2026-10-20）

当前审查基线为 Core v8.0.35（`ae3ee3be303fb07fb8173279923413005bf32177`）和 Management v1.26.5（`fe2963330f32a933f562282165f62ae52cf57145`）；文件清单与 Core 归属表同步标注该基线。前述各批次版本和文件数量为历史记录。

删除 Core 两处替换前后相同的空操作，以及 Management 已由上游提供的越南语配额文案、搜索状态和搜索控件补齐分支。保留 Pro 配额元数据搜索和流式终态用量修复；删除不再属于当前契约的旧配置编辑 E2E 目录，继续使用上游原生 v8 配置编辑验证。清理前后生成源码逐字节一致，实际原生修改范围仍为 Core 101 个文件、Management 41 个文件。

### 原生能力复用与后续退出清单

巡检自动刷新使用原生 `Manager.shouldRefresh` 判定到期，不再维护刷新算法副本；disabled 且 invalid-grant 的账号遵循上游自动刷新拦截，手动强制刷新仍可尝试，并向执行器传递原生强制上下文。巡检刷新提交复用原生 Devin 配额恢复判定，但仅在凭据身份和刷新前观测的限制仍有效时解除对应冷却；刷新期间新增的账号、模型错误或限制不能被成功刷新抹除。巡检的结果合并和其他来源保护继续保留。

普通账号修改使用原生 `Manager.Modify` 的账号变更门加载最新状态，只修改业务涉及的字段；仍须在变更门内验证原操作绑定的身份，并检查 `committed`。虚拟账号的同源文件同步和受管理元数据持久化不能因引入 `Modify` 而缩减为单个运行态身份。原生普通持久化失败仍可能只记录警告并提交运行态，不能把 `committed` 当作磁盘写入成功证明。

虚拟账号共享源修改采用源文件优先：先保存受管理字段，再逐个同步同源运行态身份。文件保存失败时不修改运行态；文件保存成功后遇到身份替换、取消或其他同步失败，保留已保存的目标状态和已完成的更新，明确报告未完全同步，允许再次操作重试。不回滚旧凭据快照，不新增后台自动重试，也不承诺多个身份瞬间共同生效。身份局部巡检错误不写入共享源文件。源文件替换必须保留 Token、项目列表和其他非管理字段，拒绝空白、已删除、身份被替换或提交前再次变化的源文件；空白不能作为外部写入中间状态被采纳。临时文件替换和最后检查不能被描述为对任意外部写入者的原子比较交换。

调度看板继续提供只读状态及定向恢复。退休的策略写入路由保留 `410 Gone`，不恢复旧请求保护引擎；删除无调用者的策略编辑文案。

下表以 Core v8.0.35 和 Management v1.26.5 为未满足退出条件的审查基线，不表示已经提交上游或取得上游等价实现。

| 通用补丁 | 上游吸收后的退出条件 | 必须保留的验证 |
| --- | --- | --- |
| Antigravity 积分恢复状态清理 | 上游在成功请求清理旧积分提示时提供原子条件更新，不覆盖并发刷新的余额；本地缓存和 Home 存储均保留该保证 | 读取旧状态后完成真实积分刷新，再恢复清理；新可用和新不足余额均保留，无并发时正常清理，Home 冲突或不支持条件更新时不退回无条件写入 |
| Codex 标准 `Retry-After` | HTTP、图片、WebSocket 握手和错误帧均传播标准响应头；原生错误体的重试时间优先级不变 | 秒数、HTTP-date、无效/越界头、原生错误体优先级及向客户端返回的安全重试时间 |
| 流式终态与失败用量 | 上游正确处理取消、终态错误与 EOF 顺序，失败保留已解析用量且只发布一次 | 延迟终态、取消与 EOF 同时就绪、转换失败、提前断流及完整流成功 |
| 最终模型归属 | 上游在实际 HTTP/WebSocket 请求与响应边界覆盖所需协议格式，不以输入别名替代最终模型 | 别名和路由改写、完整/分块响应、错误及无模型信息的回退 |
| 公共弹窗与滚动锁 | 上游具备确认队列、去重、重复提交保护、异步关闭推进及嵌套遮罩锁释放 | 连续确认、双击提交、异步完成、嵌套关闭、卸载和焦点恢复 |

每项退出前固定包含等价实现的上游 tag/SHA，在该版本移除本地补丁并重放相关行为验证；不能仅凭功能名称或发布说明删除。管理端独立发布仍须与冻结的已发布 Core 配对验证。

### Core v8.0.45 漂移适配

本次固定上游 `ad8bdd3f20bef44f0684b8a6096a216b2c76040d`。HTTP、图片和 WebSocket 握手直接复用原生 `newCodexStatusErrWithHeadersAndCooling`，删除重复包装与调用替换；`codex_openai_images.go` 恢复原生并退出修改清单。原生错误体 reset 优先级和 model-level cooling 保持不变，标准头解析遵循上游：零值与过去日期返回零，超出 duration 范围的整数钳制为最大值。Pro 严格头解析仅保留于 WebSocket 错误帧的连接数限制分支，以保留有效头优先于零秒 fallback 的既有行为；上游覆盖该顺序后退出此剩余补丁。

认证中间件只修改签名和策略接入，保留上游新加入的未消费请求关闭、认证后 body capture 及 abort 检查。Usage 发布只替换 Pro 字段准备行，保留上游 captured-identity 上下文传播。这两处局部锚点不再要求相邻原生函数或完整发布函数逐字一致。

Safe response header adaptation retains the v8.0.45 native precedence for aggregate retry rounds, trusted retry deadlines, concurrency busy errors, dispatch cooldowns, model/auth cooldowns, and generic safe-header carriers. The Pro layer adds only the legacy `StatusCode()` plus `RetryAfter()` fallback after those native carriers, preserving compatibility for status errors without overriding native deadlines.
