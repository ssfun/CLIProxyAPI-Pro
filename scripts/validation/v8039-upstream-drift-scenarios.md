# Core v8.0.39 generator failure scenarios (recorded before repair)
1. Replay against exact upstream ceb045a129d43f805e93a5e4aef3a59b108f34f7 must fail at usage_helpers field anchor because upstream adds effectiveServiceTier and gofmt-aligned spacing.
2. Replay must fail at the corresponding constructor/record anchors if they still assume pre-effectiveServiceTier layout.
3. A repaired durable generator must apply once, preserve effectiveServiceTier, and insert speed exactly once.
4. Generated tree must compile and focused usage HTTP E2E must produce a repeatable artifact.
5. Full replay also fails on redisqueue queued usage field/record anchors due to EffectiveServiceTier alignment. Preserve its normalization, record mapping and JSON field while adding policy attribution, speed and model audit fields.
6. Full replay next fails at MarkResult now/lock anchor: upstream inserts rejectsToken calculation between them. Preserve original 401 rejection expression and stale-result handling; insert pinned identity check under the same mutation and manager locks, only in markResult.
7. Existing redisqueue contract requires preserving native-key/access-provider identity fields and rejecting missing or duplicate blocks. Keep strict multi-field replace_all_exact; refresh only existing fixture whitespace to match the upstream layout.

## Exact-input reproduction

Upstream: `router-for-me/CLIProxyAPI@ceb045a129d43f805e93a5e4aef3a59b108f34f7` (v8.0.39).
Models: `router-for-me/models@a4f8f2f092fc2724a1118635dfbd6e332244d788`.
Use Go 1.26.10, required by this upstream's go.mod, and Staticcheck v0.7.0.

From this Pro checkout, with a clean upstream Git checkout and exact models.json:

```sh
GOTOOLCHAIN=go1.26.10 VALIDATION_STATICCHECK=1 VALIDATION_RACE=1 \
  VALIDATION_ARTIFACT_DIR=/tmp/core-v8039-validation \
  bash scripts/validation/core.sh /path/to/CLIProxyAPI /path/to/models.json
bash scripts/validation/repo.sh
GOTOOLCHAIN=go1.26.10 go -C /path/to/CLIProxyAPI build -buildvcs=false \
  -o /tmp/core-v8039-server ./cmd/server/
python3 scripts/validation/e2e/v8-usage-http/run.py \
  --binary /tmp/core-v8039-server --output /tmp/core-v8039-usage-http
```

Repair evidence is retained locally under `/tmp/core-v8039-repair`:
`before.log` reproduces the original usage-field failure; `replay.log` records
complete replay; `validation-final/` contains phase timings, JSON test events,
reapplication/late-anchor guard evidence, and the credits HTTP E2E receipt.
`usage-http-final/` contains real-process stream usage request/response, SQLite and
usage API receipts plus the built binary digest. These temporary paths are
validation outputs, not build dependencies.

## Newly exposed runtime compatibility failures (recorded before repair)

8. Final exact-input candidate tests fail `TestHandleAuthUpdatesFileSnapshotKeepsCooldown`
   (model and auth-level 429) and `TestHandleAuthUpdatesDiscardedSnapshotTriggersWatcherReread`.
   Clean upstream baseline passes them. Pro routes watcher commits through
   updateModeSnapshot, but upstream's new file-snapshot disk fence and runtime
   preservation only recognize updateModeReplace. Snapshot mode must retain
   the Pro base-input CAS and also participate in upstream file-snapshot handling.
9. Pro's legacy unversioned-file reread must not intercept a digest-bearing
   upstream file snapshot: its freshness must be decided under the mutation gate,
   and a discarded snapshot must reach the upstream watcher reread callback.
   Keep the legacy reread for file observations without HasFileSnapshot metadata.
10. Preserve existing management-write/source-first fences, new generation and
    credential versions, runtime cooldown state, model state and refresh state;
    successful commits clear file snapshot metadata as upstream already does.

The pre-repair candidate/baseline comparison is retained in
`/tmp/core-v8039-repair/runtime-before/`. Existing upstream service regression
cases cover these failures; no new unit tests are added after implementation.

## Final verification

All final commands exited 0 on the exact inputs above. `core.sh` passed its
atomic late-anchor/reapplication guards, patch-surface contract, Staticcheck,
go vet, existing candidate tests with `-race`, CGO/non-CGO builds, and credits
recovery ordering HTTP E2E. Candidate tests include both newly exposed upstream
file-snapshot cases and existing Pro watcher concurrency cases. `repo.sh` passed;
its existing redisqueue identity fixture changed only to the new upstream spacing.
The rebuilt final binary passed all five usage HTTP scenarios, with evidence in
`/tmp/core-v8039-repair/usage-http-final/receipt.json`.
