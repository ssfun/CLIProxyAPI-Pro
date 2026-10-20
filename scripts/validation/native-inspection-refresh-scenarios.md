# Native inspection refresh convergence

Failure scenarios written before implementation:

- Expired SIWC credentials with a fresh outer token must refresh; a fresh SIWC token must not conceal an expired outer token.
- Devin credential quota must revalidate after the native hourly interval, while a recent observation must wait.
- Ordinary disabled credentials still refresh; disabled invalid_grant credentials must not automatically refresh. Manual force remains an explicit bypass.
- Pending backoff and runtime evaluator denial must prevent automatic executor calls; evaluator approval must allow them.
- The supplied decision instant must control expiration evaluation, independent of wall clock.
- A successful but still-due refresh must reserve ineffective-refresh backoff using the same native decision.
- Existing credential identity fencing, newer error preservation, disabled status, and quota/retry cooldown preservation must remain intact.

Verification uses the real Manager registration, automatic/manual inspection entrypoints, refresh executor invocation, and committed credential state. Provider network execution is replaced by a deterministic executor. Existing identity/error matrix tests are retained.

Repeat against a freshly generated Core tree after copying both inspection_refresh Go files:

```sh
go test -race ./sdk/cliproxy/auth -run 'TestInspectionRefresh' -count=1 -v
```

Local red/green logs and replay script: `/tmp/pro-native-convergence-refresh/`.

The fresh Codex fixture uses seven days of validity because native outer refresh
lead is 96 hours. Corrected-fixture Go 1.26.0 red evidence is
`red-go126.log`: outer-due, Devin-due, disabled invalid_grant and supplied-instant
cases fail before delegation. `green-go126.log` is the corresponding race-enabled
full inspection matrix result; preliminary logs are not acceptance evidence.

## Refresh compatibility review: force context and Devin recovery

Failure scenarios recorded before the compatibility implementation:

- Automatic inspection must not add the native force context marker; manual
  inspection must pass it to the executor while retaining caller context values.
- With real Codex execution and both credentials refreshable, expire only outer,
  then only SIWC. Automatic inspection must call only the expired layer; manual
  inspection must call both OAuth endpoints and commit both returned tokens.
- For identical Devin credential-quota state and newer positive daily/weekly
  observations, native and inspection refresh must release account and propagated
  model restrictions and make the same scheduling decision.
- Exhausted, stale, missing-time, future, or partly exhausted observations cannot
  release the quota. A non-quota model cooldown and Pro inspection protection
  must survive recovery. Manual disable before or during refresh must survive.
- A restriction, error, quota observation, or model cooldown added after the base
  snapshot must not be cleared, even when the refresh observation is newer than
  that mutation. Credential replacement must reject the commit and preserve the
  replacement account exactly.
- A metadata-only concurrent edit must survive without preventing legitimate
  recovery. Refreshed quota signals must remain available after either outcome.

The targeted replay artifact is `/tmp/pro-refresh-compat-review/`: before/after
race logs plus `replay.sh`, using Go 1.26.0. The Codex fixture performs local HTTPS
OAuth requests through the real executor, Manager entrypoint and commit path.
- A newly recorded account or model 401 must survive even when the executor
  rotates the access token: native generic unauthorized cleanup runs after the
  inspection status reconciliation and therefore requires the same observation
  fence as Devin quota recovery.
