# Connection revision convergence: failure scenarios (before implementation)

- A second Pro counter can diverge from the upstream connection revision and disagree with queued Pro request fencing.
- Changing only the server address or only the management key must increment the native revision and physically abort in-flight requests.
- A → B → A must invalidate both in-flight responses and queued Pro writes even though the final credentials match.
- Reapplying the same normalized address/key (including a timeout-only update) must not abort a request or advance revision.
- A delayed success must not update server metadata after switching connections; a stale 401 must not dispatch unauthorized/logout events.
- Caller cancellation must still reach the adapter when combined with the connection abort signal, including the AbortSignal.any fallback.
- Pro requests queued before a switch must never dispatch against the new server; fresh Pro requests must retain v0 namespace, credentials, and request options.

Validation uses the generated v1.27.2 client and real Axios pipeline with controlled delayed adapters, plus existing generator idempotence tests, Pro transport tests, typecheck and build. This is isolated transport integration coverage, not browser E2E; server/browser lifecycle is outside this counter-only change.

## Reproduction and result (2026-11-05)

Run `./cliproxyapi-pro-management/e2e/connection-revision/run.sh /absolute/path/to/clean-v1.27.2 /absolute/path/to/artifacts`. Dependencies require registry access; full HTTP integration tests require localhost binding permission.

Verified upstream: `f154fa9380825c4fe94bc577a9abaa7e505f8875` (v1.27.2). Full replay and repeat replay hashes matched; patch surface matched 41 modified upstream files. Python customization suite: 124 passed. Targeted transport/metadata: 18 passed. AbortSignal.any fallback: 7 passed. Full Bun suite: 2268 passed (unrestricted localhost run). Lint exited 0; a final type-only fixture cleanup removed the two new explicit-any warnings. Typecheck and production build passed.

Local receipt directory: `/tmp/management-connection-revision-artifacts`; includes command logs, upstream SHA, source/build SHA256SUMS, and repeat replay hashes. The first sandbox full-suite attempt failed 13 localhost-bind cases; the same full suite passed outside the sandbox. A post-`git add -N` whitespace check exposed the existing `src/pro/shared/maskSensitiveText.ts:14` blank line at EOF; it is unrelated to this change. No browser E2E was run.
