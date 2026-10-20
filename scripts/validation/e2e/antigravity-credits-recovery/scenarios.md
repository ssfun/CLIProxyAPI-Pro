# Antigravity credits recovery publication ordering

Written before the production fix. The CI failure leaves an unknown hint after a
successful Manager credit recovery, although loadCodeAssist returned 2516 credits.

Failure cases to verify:

1. Recovery reads an expired unavailable hint; an HTTP balance refresh publishes
   2516 credits before recovery invalidates its old observation. Preserve 2516.
2. Recovery removes an explicit failure and reads a fresh unavailable hint; the
   same concurrent publication must survive.
3. A newer unavailable balance publication must also survive old invalidation.
4. With no concurrent publication, expired hints and explicit failure hints still
   become unknown; fresh unavailable hints without an explicit failure survive.
5. Missing/blank IDs must remain harmless. Home mode must compare the exact stored
   value and use the existing server CAS; a CAS failure must not fall back to SET.
6. Run the unchanged real Manager Execute and ExecuteStream top-up recovery test.

The isolated concurrency reproducer uses a Go overlay containing only a scheduling
barrier after recovery reads its hint. A real HTTP loadCodeAssist response completes
in another goroutine before the recovery continues. No production hook or delay is
installed. The same fixture and barrier run against both unmodified and repaired
sources. `events.jsonl` and `result.json` make each run auditable.

Run:

    python3 scripts/validation/e2e/antigravity-credits-recovery/run.py \
      --src /path/to/generated/core --output /tmp/credits-recovery-proof

The runner only runs its bounded integration scenarios and the existing Manager
regression. It does not run the full E2E suite.
