# Batch current-state execution failure matrix (before implementation)

Batch commands must not require the selected observation to remain current.
They must retain account identity and the action the user confirmed.

- Selected resultRef becomes old after a recheck: explicit recheck, recovery,
  enable, disable, and delete still target the same registration.
- Normal access-token refresh keeps registration identity: use current credentials.
- Same filename/key is replaced by a new registration: reject that target only.
- Missing identity cannot be rebound to an arbitrary current account. Older
  clients may resolve identity from their archived resultRef, never by name alone.
- A suggested action or its effect changes: skip, including on retry; never
  silently substitute the new recommendation (especially deletion).
- A suggestion already processed is not executed again.
- Already enabled/disabled, absent delete target, or no active recovery restriction:
  successful no-op receipt, no unnecessary provider request, no whole-batch 409.
- Mixed actionable/no-op/conflicting targets: process eligible accounts independently.
- New inspection observations while work is queued do not cancel explicit commands.
- Changed recovery restriction revisions remain protected by routing CAS. Never
  clear a new restriction with an older recovery response.
- Existing durable intent, idempotency, restart interruption, audit, and provider
  concurrency behavior remain intact.

Validation: extend the process-level HTTP batch-history fixture, retain all
existing adversarial checks except assertions that intentionally required stale
observation equality, and write repeatable JSON receipts. Run focused scenarios
first and the complete HTTP suite after implementation.

## Repeatable acceptance

Build Core from a clean upstream checkout after applying the current patch
sources, then run these commands from the Pro repository (set
`INSPECTION_SERVER` to the resulting binary):

```sh
INSPECTION_E2E_OUTPUT=/tmp/inspection-current-state \
  python3 cliproxyapi-pro-core/e2e/account-inspection-batch-history/run.py --current-state-only
INSPECTION_E2E_OUTPUT=/tmp/inspection-batch-history \
  python3 cliproxyapi-pro-core/e2e/account-inspection-batch-history/run.py
```

Each output directory contains `result.json` with the binary SHA-256 and HTTP
receipts. The focused run covers archived references, identity-only requests,
already-satisfied states, no-request recovery, mixed conflicts, real same-name
replacement, changed suggestions, and repeated deletion. The full run covers
queued observation updates, administrative conflicts, retries, disconnection,
bounded concurrency, restart, and durable audit behavior.

The Management fixture and `inspection_receipt_smoke.mjs` additionally verify
four no-op account states through the rendered page, including mobile layout.
`inspection_batch_errors_smoke.mjs` verifies localized errors and legacy response
compatibility. See each runner's header for ego-browser configuration.

Limits: this HTTP provider fixture does not simulate OAuth token refresh or an
xAI suggested quota-recovery response. Existing Core recovery tests cover
inspection-only restriction scope; this is separate from full provider E2E.
