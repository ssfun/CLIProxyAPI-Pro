# Routing board inline recovery actions

This harness renders the real `RoutingPolicyPage` from a patched Management
checkout. It replaces only the scheduling-board and auth-file API calls with
controlled browser responses.

The scenario freezes these product requirements before implementation:

1. Every scheduling-board row exposes `重检` and `恢复` without opening
   the restriction details dialog.
2. The restriction details dialog remains informational and does not render the
   standalone `调度恢复` module.
3. Recheck posts the current account identity and refreshes the board.
4. Resume-calls requires explicit confirmation and releases every current
   releasable restriction with its exact source, model, and revision.
5. The desktop table and narrow card list reuse the request-log panel's
   `min(560px, 64vh)` constrained internal scrolling, leaving pagination outside.
6. Desktop and narrow layouts keep the actions usable.

To run it, first apply the overlay to a clean Management checkout, copy this
directory to `<checkout>/e2e/routing-board-actions`, and start Vite in that
checkout. Then run:

```sh
bash cliproxyapi-pro-management/e2e/routing-board-actions/run.sh \
  http://127.0.0.1:4173/e2e/routing-board-actions/ \
  /private/tmp/routing-board-actions-evidence \
  fixed
```

Use `before` against the old UI to preserve the failing evidence. The run writes
`result.json`, `desktop.png`, and `mobile.png`.

## Connection lifecycle review

Failure scenarios frozen before the fix:

- Switching API address/key while still connected leaves account A on server B.
- A pending recheck from A refreshes/notifies the new connection after completion.
- A delayed post-recheck board refresh notifies a new connection.
- A confirmation opened on A sends release requests after switching to B.

`lifecycle.mjs` runs these against the real page with controlled API promises,
and writes `lifecycle.json`. Run through `ego-browser nodejs` with `config`
containing `url`, `artifactDir`, and optionally `spaceId` / `expectBefore`.

Example (reuse the same `spaceId` for before/after runs):

```sh
{
  printf '%s\n' 'const config = {url:"http://127.0.0.1:4186/e2e/routing-board-actions/",artifactDir:"/private/tmp/scheduling-review-after"};'
  cat cliproxyapi-pro-management/e2e/routing-board-actions/lifecycle.mjs
} | ego-browser nodejs
```

The lifecycle runner retains its task space for subsequent layout checks.
Finish that space after the last run, or pass it to `run.sh` as argument four.
