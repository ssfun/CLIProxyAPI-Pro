# Retained Claude quota customization regression

Recorded before implementation for Management v1.26.4
(`13f94b47f3400e681dfdede65acf1a1242ee3fce`), failed Actions run
`38650799796`.

1. Both `QuotaCard.tsx` and `AuthFileQuotaSection.tsx` now render normal quotas
   under `quota && !retainedClaudeUsage`, and retained Claude usage under a
   separate guard. The old whole-ternary anchor fails before dependencies/tests.
2. Patching only the first component leaves the second failing. Patching only
   the normal body loses the Pro timestamp for successful retained Claude usage.
3. Preserve both upstream guards, loading/error messages and body renderers;
   insert one timestamp alongside each rendered body. Keep the timestamp's
   existing success-only policy, so loading/error observations do not appear fresh.
4. The earlier single-body layout must still apply. Missing or duplicate normal
   body anchors must fail explicitly, without flushing partial edits to disk.
5. Applying twice must produce identical source, without duplicate imports,
   timestamps or quota bodies. Full patch-surface validation must remain unchanged.
6. At final validation, run the complete Management validator on the exact clean
   upstream, including upstream Claude retained-usage rendering tests, lint,
   type checking and production build. Run the generated frontend against the
   repaired Core using `e2e/pro-route-contract/run.py` and retain its receipt and
   HTTP transcript. The HTTP E2E covers route/cache persistence, not external
   providers or visual browser layout.

Focused reproduction: `python3 -m unittest discover -s cliproxyapi-pro-management/tests -p test_quota_card_customization.py`.
Final commands and exact inputs are retained with the task's validation logs.

## Additional failure exposed by the complete validator

The upstream `claudeOAuthUsage.test.ts` raw API-call equality assertion passes
on clean v1.26.4, but fails on the generated tree because Pro already adds the
required `ApiCallResult.hasStatusCode` field. Its typed `result(statusCode, body)`
fixture must include `hasStatusCode: true` for its explicit numeric statuses.
Keep strict equality and the authenticated route, status, body and error-message
assertions; do not drop the production metadata or weaken the test to a subset.
This fixture adaptation adds one reviewed upstream test file to the patch surface.

## v1.26.6 multiline Claude plan regression

Recorded before implementation for upstream
`603539ab5c5343f32c3945c6e0cdb8333991c493`, Actions `39480689073`.

1. QuotaCard now passes `planItems={claudePlanItems}` and
   `afterPlan={claudeAfterPlan}` in multiline bodies in both ordinary and retained
   branches. The retained branch is parenthesized. Exact single-line anchors fail.
2. Preserve all body properties, guards and intervening loading/error markup;
   add exactly one timestamp to each branch, with identical reapplication output.
3. Continue supporting both legacy single-line renderers and the legacy single
   ordinary branch. Exercise the multiline form for both renderer configurations.
4. Reject missing/duplicate multiline bodies, changed properties and changed
   retained guards instead of silently dropping upstream behavior. Do not flush
   any buffered edits on failure.
5. Reproduce with the focused Python regression command above before the fix;
   finish with clean exact-upstream Management validation and retained logs.

### Next v1.26.6 replay failure: translated API client comments

The complete replay reaches connection isolation next and fails because upstream
translated interceptor JSDoc and the baseURL comment into English. Comments must
remain intact in either language, while connection cancellation, request-generation
metadata, stale success/error rejection and idempotence remain unchanged. Match
executable interceptor boundaries instead of translated prose; reject absent or
duplicate executable anchors. Add the English fixture regression before changing
the generator and replay the entire generator on another clean upstream copy.
