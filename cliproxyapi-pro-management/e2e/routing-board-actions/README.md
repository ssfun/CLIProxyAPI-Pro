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
