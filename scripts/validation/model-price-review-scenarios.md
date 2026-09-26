# Model price review scenarios

Freeze these failure cases before implementation. Price conversion is checked in
isolation because rounding boundaries are not practical to exercise through the UI;
Core checks use the real SQLite store and public pricing settlement entry point.

- A valid context threshold `2e5` must persist as 200000, not 2.
- Valid hexadecimal numeric input must have the same value at validation and save.
- Unsafe integer thresholds must be rejected before JSON serialization.
- Partial numeric strings must not silently become a different valid price.
- A cost rounding to 2^63 microdollars must return an error, never negative credit.
- Ordinary rounding, missing-price errors, locked overrides, global model lookup,
  provider migration, Fast/speed pricing, and historical recalculation must still work.
- Manual saves continue creating versions; remove the unreachable raw JSON equality
  branch rather than inventing a second deduplication policy. Sync retains its existing
  semantic comparison.

Validation: run modelPricePresentation.test.ts with Bun; replay Core patches on
v7.3.17 and run the observability package tests, then build cmd/server. Preserve
commands, revisions, and red/green logs in /tmp/model-price-review-20260926.

HTTP acceptance (isolated database, local binary, two server starts):

```sh
python3 scripts/validation/model_price_runtime_smoke.py \
  --binary /tmp/model-price-review-20260926/server \
  --artifact /tmp/model-price-review-20260926/http-smoke.json
```

This verifies global rule identity, locking, priority-to-fast canonicalization,
manual-save version increments, readback, restart persistence, and deletion.
