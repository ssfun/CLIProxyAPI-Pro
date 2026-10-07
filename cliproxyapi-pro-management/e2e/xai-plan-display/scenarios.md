# xAI plan display regression

Failure scenarios, written before the rendering change:

1. An inspection snapshot has `planType=x-premium-plus`, no `planLabel`, and zero
   monthly credits. Its card must show exactly one fallback plan.
2. Refresh uses the real quota action, Pro adapter, upstream billing parser, and
   background subscription enrichment. While subscription requests are pending,
   the fallback remains; after `planLabel=X Premium+` arrives, exactly one plan
   must remain. Before the fix this produces two plan labels.
3. Subscription endpoints fail: the fallback must remain and quota bars must
   still be usable. A subsequent successful refresh must recover without duplicates.
4. All JWT plans with a native subscription label must render one plan. Free must
   retain its token quota row, including the pending state when no observation exists.
5. Native monthly-limit plans (15000 / 150000 cents) must render once even when
   JWT metadata disagrees. Official API paid-health cards must also render once.
6. An empty native label without a native monthly plan must not suppress the Pro fallback.
7. A subscription result arriving after the quota store has been cleared must not
   repopulate the old account.

The browser fixture replaces only `apiCallApi.request` with deterministic,
non-secret responses. It runs production refresh/store/enrichment/render code;
it is not a live provider or live Core E2E. Core HTTP/SQLite coverage is separate.

Apply the overlay to a clean Management checkout, install locked dependencies,
copy this directory to `<checkout>/e2e/xai-plan-display`, and start Vite on loopback.
Reuse an Ego task space:

```sh
bash cliproxyapi-pro-management/e2e/xai-plan-display/run.sh \
  16 http://127.0.0.1:4173/e2e/xai-plan-display/ /private/tmp/xai-plan-evidence
```

The runner saves assertions, requested URLs, rendered text, and a screenshot.
It leaves the task space open for integration with other checks. Pass `before`
as argument 4 to reproduce the pre-fix duplicate instead of requiring a pass.
