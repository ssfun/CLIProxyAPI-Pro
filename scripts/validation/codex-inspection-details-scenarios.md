# Codex 巡检配额详情失败矩阵（实现前记录）

验证路径为本地 HTTPS 提供方 → 真实 Codex 巡检 → SQLite quota-cache；主额度判断仅使用本次 wham/usage。开发期只运行新增集成场景，最终运行完整巡检 HTTP E2E。

1. 认证文件没有套餐到期日期；subscriptions 实时返回 active_until，缓存展示该日期。
2. usage 返回 credits.balance 数字、合法字符串或 unlimited；缓存与手动刷新字段一致。
3. reset-credits 返回 available_count、applicable_available_count、可用 Codex reset credits；筛除其他类型和已消费条目；保存 id/status/grantedAt/expiresAt。
4. usage 的 applicable count 优先于详情端点；详情 count 优先于 usage count；只有非空详情时才用详情长度推导 count；显式 0 不能被回退覆盖。
5. 辅助请求的 401/403/500、无效 JSON、无效 schema、请求超时不能把健康账号变成异常/删除，也不能解除本次明确耗尽。
6. 辅助失败时只保留可信、未超过 15 分钟且不早于当前 auth.UpdatedAt 的订阅到期日期；独立 subscriptionObservedAt 不得随失败或整体 cachedAt 刷新。reset credits 仍按自身 observation 判断，并写入错误标记。
7. reset credits 成功返回空列表/0 必须清空旧详情；辅助失败不能无限沿用旧 count；超过 15 分钟或已过期的详情不能用于恢复操作。
8. 当前 usage 明确 count=0 时，即使详情请求失败也清空旧可操作详情。
9. 手动刷新写入的既有缓存没有新增凭据字段，仍可在 AuthIndex 一致、缓存晚于当前认证注册/更新且在 15 分钟内时安全保留；不修改数据库 schema。
10. 认证被替换、重注册或 token 在辅助请求期间变化，不写入旧账号结果；新认证缓存不继承旧凭据详情。
11. 父 context 取消时迅速终止请求，不覆盖缓存；每个辅助请求受 settings.Timeout 和 8 秒上限约束；辅助调用不重试。
12. 缺少 account id 或主 usage 失败/不完整时，不运行详情请求，不覆盖旧缓存。
13. account_id 查询参数正确转义，三个端点使用同一观察 token/account-id；reset endpoint 带 Accept/OpenAI-Beta/Originator。

14. 旧版本已有凭据绑定却缺少 subscriptionObservedAt 的缓存无法证明日期来源，辅助失败时回退当前认证日期；不得用已被旧代码续期的整体 cachedAt 代替。
15. 当前认证日期已更新，早于 auth.UpdatedAt 的旧订阅日期不能覆盖它，即使凭据绑定一致；超过 TTL 或未来时间的订阅观测同样回退认证值。
16. UI 未绑定的新缓存仅可从 entry.ObservedAt 补足订阅观测时间，并受认证更新时间/TTL 限制；没有可信 observation 的订阅值不能继续继承。
17. 连续 subscriptions 失败或 HTTP 200 active_until=null 不能延长原观测时间；模拟原观测超过 TTL 后必须回退当前 auth 日期。subscriptions 恢复成功后保存新的实时日期及新观测时间。
18. 仅 subscriptions 失败时，当前 usage 主额度判断与成功 reset-credits 明细/count/观测时间保持正确；订阅观测与 reset-credits 观测互不续期。

重复验证：在干净上游重放 apply_upstream_patches.py，运行 `go test -race -json ./internal/api/handlers/management -run '^TestCodexInspectionQuotaDetailsHTTPAndSQLite$' -count=1` 保存 JSONL；设置 `CODEX_INSPECTION_EVIDENCE_DIR` 保存各场景实际 SQLite 缓存 JSON。最终构建真实 Core 二进制，设置 `INSPECTION_SERVER` 与 `INSPECTION_E2E_OUTPUT` 后运行 `python3 cliproxyapi-pro-core/e2e/account-inspection-batch-history/run.py --ci-fast`。具体树与完整命令见本次验证产物。

验证界限：请求期间发生身份替换或取消时拦截旧结果；提交前身份检查与 SQLite 写入沿用既有分离步骤，本次不引入跨系统原子 CAS，也不声称消除了二者之间的所有竞态窗口。UI 未记录 AuthIndex/凭据绑定的旧缓存只在文件对应且 observation 不早于当前认证更新时间时回退；无法证明归属的旧缓存保守舍弃。

## Nested JWT renewal fallback regression

Before implementation: extend HTTP -> inspection -> SQLite fixtures with the real `https://api.openai.com/auth` claim namespace. Cover JWT text, JSON text and already-decoded objects; nested-vs-top-level conflicts follow Management's nested precedence; flat JWT compatibility; missing/malformed claims remain unknown. On subscription failure with no eligible old cache, persist the credential renewal date without a live observation timestamp. Verify nested account ID selection, live API precedence, repeated failure and recovery, and rerun existing cancellation/credential replacement scenarios.
