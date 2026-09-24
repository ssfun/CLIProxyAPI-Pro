# xAI official probe recovery: failure matrix

The repeatable integration cases live in `cliproxyapi-pro-core/patches/routing_policy_test.go` and `account_inspection_transport_test.go`. They use the real management check route, auth manager, and SQLite setting writer with a controlled xAI HTTP executor. No external xAI request is sent.

| Probe outcome or concurrent event | Required behavior |
| --- | --- |
| A newer native cooldown is recorded for the same model while an older probe waits, then the older probe succeeds | The newer model cooldown and result hook count remain unchanged by the stale success; the account-wide hold remains. The manual check reports a failed phase. A later background attempt supplies retry/backoff. |
| A result policy strips pin metadata from that stale success | The same stale result is rejected, with no hook call, cooldown change, or hold release. A policy cannot remove the identity and state guard. |
| The credential or registration epoch is replaced under the same auth ID while a probe waits, including a new registration with the same API key | The replacement's identity, model state, result hook count, and hold remain unchanged by the old probe. |
| The inspection hold revision changes while a probe waits | The old probe cannot remove the new hold. |
| An account-wide hold receives another 429 quota response | Retain account-wide model coverage and advance the retry time; a model-only native cooldown cannot replace the hold. |
| An account-wide hold receives a model/request error (400 or 404) | Retain the hold and retry plan; do not create a credential-wide native auth failure or report quota recovery. |
| An unchanged identity, state, and hold receives a valid 2xx probe response | Release the observed inspection hold; no unrelated native restriction changes. |
| Transport, limiter, probe timeout, or persistence fails | No unsafe release; background attempts use bounded backoff and do not monopolize the recovery queue. Parent lifecycle cancellation stops work without a new retry write. |

The concurrency tests pause the official `/chat/completions` HTTP request until the replacement cooldown, auth identity, or hold revision has been committed, then allow the old response to finish. Endpoint responses and live manager state are both checked.

This is an integration test of the management HTTP router, real auth manager, and SQLite-backed protection setting using a controlled provider executor. It is not a live xAI end-to-end test.

From a clean Core checkout after applying the durable patches, capture the repeatable result with:

```sh
GOCACHE=/private/tmp/recovery-boundaries-gocache go test -json ./internal/api/handlers/management -run 'TestXAIRoutingRecovery|TestQuotaRecoveryRunsOneOfficialXAIProbeWhenAutoRecheckDisabled' -count=1 > /private/tmp/xai-recovery-final.jsonl
```

The JSON log records named subtests and their pass/fail outcomes. Local SQLite data is created in each test's temporary directory; no external account is used.
