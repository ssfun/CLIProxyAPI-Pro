# Request monitoring regression scenarios

Run against a generated Management checkout: copy this directory to its root as
`monitoring-review`, start Vite, and open `/monitoring-review/index.html`.
The page uses real React hooks with deterministic API responses, never a real server.
Click Run; save the JSON shown on the page as the repeatable verification artifact.

Before implementation, verify these failure cases:

1. Manual refresh succeeds: spinner clears, changed model price becomes visible,
   and a second refresh actually requests another snapshot.
2. Manual refresh fails: spinner clears and retry remains possible.
3. A refresh overlaps switching connections: old price response cannot overwrite
   the newly connected server's price or keep the new connection busy.
4. A log snapshot is empty (snapshot ID 0): first incoming request triggers follow.
5. After reaching page 3, a failed refresh preserves the page-2 cursor.
6. Follow disabled, details open, or non-first page must pause automatic refresh.

This is a browser integration fixture, not a backend E2E substitute.

## Review outcome (2026-09-26)

- `useUsageData`: manual refresh captured the stream generation before snapshot
  loading incremented it. This discarded refreshed prices and skipped releasing
  the refresh lock. The existing lock now carries a refresh identity, invalidated
  on connection cleanup, independently of snapshot/stream generations.
- `useRealtimeLogData`: snapshot ID zero is valid for an empty database. Follow
  now requires a loaded snapshot rather than a positive ID.
- `useRealtimeLogData`: refresh no longer destroys pagination history before its
  request succeeds; the existing success path already truncates cursor history.
- Removed the duplicate incremental-loading boolean lock (the shared promise
  already serializes calls), duplicate pending-payload cleanup, unused auth and
  channel metadata, and unused model-name collection.

## Evidence

`before.json` records the baseline; refresh-related failures cascade from the
first stuck lock, while the empty-snapshot and pagination cases are independent.
`after.json` records all six scenarios passing with real React in Ego Lite.

Management upstream tree: `4530da271ba2e89810d4dccebc57f3091afa590a`.
Final validation used a fresh archive of that tree and ran:

```sh
bash scripts/validation/management.sh /private/tmp/monitoring-review-final-20260926
```

Passed overlay application, patch-surface contract, idempotent reapplication,
1037 existing tests, ESLint, TypeScript and production build. Detailed local log:
`/private/tmp/monitoring-review-validation.log`.

No Core implementation changes or live backend E2E claims are made here.
