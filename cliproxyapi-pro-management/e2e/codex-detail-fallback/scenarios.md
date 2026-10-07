# Codex retained-detail display regression

Failure scenarios written before the fix:

1. SQLite inspection state contains both retained reset-credit details and
   `rateLimitResetCreditsError=HTTP 503`: both the details and one failure message
   must be visible. Before the fix, the details hide the message.
2. Failed details with no retained rows: show one failure message, no detail rows.
3. A successful subsequent result removes the message and displays fresh details.
4. Successful empty details display neither rows nor an error.
5. Both the quota-page and auth-file compact style bindings display the warning;
   the shared body must work at desktop and mobile widths.

The fixture renders the production Codex body with production Chinese translations
and both host style maps. It supplies normalized quota snapshots, not live accounts.
Core HTTP/SQLite tests separately validate production snapshot generation.

Copy this directory to a clean patched Management checkout at
`e2e/codex-detail-fallback`, install locked dependencies, start Vite on loopback:

```sh
bash cliproxyapi-pro-management/e2e/codex-detail-fallback/run.sh \
  SPACE_ID http://127.0.0.1:4175/e2e/codex-detail-fallback/ /private/tmp/codex-detail-display
```

Pass `before` as a fourth argument to confirm the old hidden-error bug. The runner
preserves JSON assertions and DOM snapshots even if screenshot capture is unavailable.
