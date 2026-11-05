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

## Runner provenance failure scenarios

Recorded before provenance implementation; verified with an isolated temporary Git repository:

- A clean checkout must identify exact Pro/upstream commits and Python/Bun versions.
- Staged and unstaged customization edits must have distinct hashes and preserved binary diffs.
- Nonignored untracked customization files must be hashed and snapshotted; unrelated ignored secrets must never be read.
- An artifact directory inside the repository must not contaminate dirty status or recursively snapshot itself.
- Spaces and shell metacharacters in invocation and command arguments must be preserved without reinterpretation.
- `inputs.json` must exist before generation starts, including when generation fails.

`inputs.json` records substantive executed commands as argv arrays with their working directories. The dirty flag covers the whole Pro repository, excluding the artifact directory. Diff hashes and snapshots intentionally cover only `cliproxyapi-pro-management` and `scripts/validation`; `pro_worktree_diff_hash_scope` makes this scope explicit. `pro-inputs/` preserves staged, unstaged, and combined tracked diffs plus nonignored untracked files in those scopes. Symlinks preserve their target text without reading target contents. Ignored files are excluded from untracked snapshots.

The artifact directory must be new or empty before capture. Occupied directories (including source directories and prior receipts) are rejected before snapshot writes, preventing source inputs from being excluded as output. Untracked snapshots also record file permission modes.

## Fallback listener lifetime follow-up

Failure cases recorded before implementation: repeated successful requests, Axios errors, plain adapter errors, request-transform errors and stale queued Pro requests must release both source listeners. Caller/connection abort must release both synchronously; an already-aborted caller must attach none. Abort and finalization cleanup must be idempotent and request-local.

The new behavior tests first reproduced seven failures on the old implementation. The regular `scripts/validation/management.sh` and this runner both execute the shared `disableAbortSignalAny.ts` preload, so fallback cleanup cannot silently remain untested on modern runtimes.
