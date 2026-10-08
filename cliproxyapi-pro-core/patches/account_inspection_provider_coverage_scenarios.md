# Provider coverage integration scenarios

Written before the production changes. Run `TestInspectionProviderCoverageHTTPAndSQLite` in a generated Core checkout with `PROVIDER_COVERAGE_EVIDENCE_DIR` set to retain each decision and SQLite payload.

1. A real `kimi-ai` or `kimi.ai` credential must be selected by both `all` and `kimi`, call the `.ai` usage endpoint, and persist quota under canonical `kimi` for manual UI parity. Runtime authentication retains its original provider identity.
2. Gemini CLI without a plugin quota provider must execute its credential-local `quota_probe`, normalize the same groups as manual refresh, and persist the snapshot. The fallback must neither change credentials nor run when a plugin already handled the request.
3. Devin must POST Connect-RPC user status using its own token in `metadata.apiKey`, parse daily/weekly remaining percentages and reset times, persist the manual UI shape, and protect the account when either window is exhausted.
4. Meta must POST the Muse key endpoint using only persisted `dca_token`, never the LLM API key. Only allowlisted quota fields may enter SQLite or error text. Missing/invalid DCA tokens must fail before any request.
5. Meta with an object but no usage is an unknown observation, never zero utilization or a recovery recommendation. Malformed Devin or Meta data cannot overwrite a previous successful observation.
6. Devin and Meta 401/403 responses retain account-auth classification; unrelated 429/5xx responses retain transient classification and cannot establish quota protection.
7. Quota reset times from exhausted daily/weekly windows feed account-wide routing protection. Requests and decision/persistence evidence are emitted as repeatable artifacts.
8. Devin records created by the real OAuth service include `api_key` and `session_token` attributes but explicitly declare OAuth. Initial registration and the auth-files API must not misclassify them as config API-key credentials; genuine config API keys remain excluded.
9. Devin reset strings containing decimal points or exponents are invalid, matching the manual parser's integer-string contract. Invalid reset times must not enter SQLite or become protection deadlines.
10. Cancellation at response EOF must prevent persistence; same-index credential replacement before completion must fail the identity fence. Malformed subsequent data must preserve the previous successful SQLite payload.
11. Persisted Devin/Meta snapshots must be accepted by the real account-policy plan snapshot reader, and Kimi aliases must query canonical Kimi cache rows without changing the policy's provider identity.
12. Rotating only Meta's DCA token through an in-place Manager update leaves the LLM key and registration epoch unchanged. A quota response authorized with the previous DCA token must not enter the new credential's cache.
