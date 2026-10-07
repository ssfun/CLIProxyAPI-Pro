# Other-provider inspection quota parity

Failure scenarios written before transport changes. No live credentials required.

1. Kimi with `domain=ai` or an `.ai` base URL must request only `api.kimi.ai/coding/v1/usages`; explicit `.com` wins over a filename containing `kimi-ai`. Arbitrary base URLs must never receive a bearer token.
2. Weekly 20% + monthly 95% must persist both rows and the monthly reset, and trigger a 90% quota threshold. Monthly-only payload must remain usable. Missing/invalid monthly input must not create a bogus row.
3. Claude active Fable window must replace the legacy iguana window, persist its reset, and participate in the quota decision. Absent/invalid Fable retains legacy behavior. Team-scoped active subscriptions win over personal flags; inactive Team falls back to personal flags.
4. xAI first inspection fetches user/settings plan metadata; later successful observations refresh it. One failed optional endpoint can still use the other. Both failing, malformed, timeout or unrecognized metadata do not alter billing/free-quota health and retain previously saved plan metadata, matching manual refresh fallback.
5. xAI optional requests are concurrent, bounded, and not retried. Cancellation or credential replacement while they are pending must not persist that observation. Official API accounts must not request CLI subscription endpoints. Existing quota failure/deep-probe behavior must remain unchanged.

Validation order: run targeted HTTP -> inspection -> SQLite fixtures red, implement, run targeted fixtures green with race detector and exported SQLite JSON. At completion replay durable patches on clean pinned upstream and execute the binary HTTP E2E suite once. Preserve logs, replay inputs and result JSON under the validation artifact directory.
