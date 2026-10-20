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
