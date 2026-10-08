# Inspection provider UI coverage failure scenarios

Before implementation, reproduce these failures against the clean selected Management revision:

1. Devin and Meta credential files are omitted from the inspection asset selector and target settings despite upstream manual quota support. API-key-only records must remain excluded.
2. `kimi-ai`, `kimi.ai`, and `kimi` auth files must share the Kimi selector and cached plan lookup; restoring either alias as a saved target must retain Kimi scope.
3. A manual or inspection Devin cache stores `remainingPercent` windows and `plan` at top level. A 95%-used observation must affect availability at threshold 90; a healthy, null, loading, or failed observation must not be declared quota-low.
4. A manual or inspection Meta cache stores windows and `planName` inside `data`. It must affect availability and plan display using the same representation, without displaying credential-bearing response fields.
5. Gemini CLI `remainingFraction` windows must affect the same quota-low threshold as inspection results.
6. SQLite cache selection and hydration must accept both manual and inspection snapshots for Devin/Meta, selecting the freshest compatible state. Incompatible newer plugin payloads must not replace them.
7. Devin's real auth-files listing contains `provider/type: devin`, `account_type: oauth`, label `Devin (username)`, and source `memory` before a file path exists; it deliberately omits `api_key` and `session_token`. That OAuth identity must remain inspectable even if a compatibility response additionally retains `api_key`; explicit `source: config:*` API-key records must remain excluded.

Repeatable focused artifact: run `bun test tests/inspectionProviderCoverage.test.ts` after replay into a private clean upstream tree and save its output. Final HTTP/browser E2E must inspect mixed-provider accounts, select Devin/Meta/Kimi, refresh manual quota, run inspection, and verify provider counts, plans and quota status after reload. Do not run the full E2E suite until final integration.
