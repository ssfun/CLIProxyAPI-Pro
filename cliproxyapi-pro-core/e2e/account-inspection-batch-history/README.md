# Account inspection batch and history HTTP E2E

Build the server from a clean Core upstream checkout with this repository's current patch generator. Then run:

```sh
INSPECTION_SERVER=/absolute/path/to/server python3 cliproxyapi-pro-core/e2e/account-inspection-batch-history/run.py
```

The script uses four disposable xAI auth files and a local fake upstream. It allocates local ports, starts and stops Core, resets only its own output directory's `auth/` and `usage/`, and writes a repeatable receipt to `/private/tmp/inspection-batch-history-e2e/result.json`. Set `INSPECTION_E2E_OUTPUT` to use another output directory. It requires no live provider credentials.

## Failure cases checked

1. An old `ResultRef` is stale at preflight.
2. Reinspection between preflight and execution makes the fixed item stale without disabling the account; retry creates another operation ID bound to the new result and can execute.
3. A suggested quota disable resolves to `quota_protection`, and a disconnected execute request still completes in the background with a queryable receipt.
4. A later suggested quota enable resolves to `quota_recovery` if the fixture produces quota usage data; the standard official xAI success response does not, so the receipt records this coverage gap.
5. An explicit disable resolves to `admin_disable`; a direct `/actions` operation keeps the same diagnostic fields in `before` and `after` while recording the execution effect.
6. A batch delete retains its diagnostic observation and operation evidence.
7. Killing Core during a blocked provider probe and restarting marks the running batch `interrupted`; the same operation cannot replay.
8. Making the evidence journal unwritable prevents a direct delete; restoring it permits exactly one delete and one operation record.
9. The persisted evidence omits test credentials and uses mode `0600`.

The output directory contains `server.log`, the generated fixture, and `result.json`. The result records step receipts, operation IDs, the binary SHA256, and any coverage gaps. Tests do not depend on the browser or the full repository E2E suite.

Audit **final** write failure after a destructive side effect is not fault-injected here: the production HTTP interface has no deterministic pause or write-failure hook between mutation and the final journal write. The script does verify that failed audit intent persistence prevents the mutation, plus durable receipts, restart handling, and the complete direct-action audit delta.
