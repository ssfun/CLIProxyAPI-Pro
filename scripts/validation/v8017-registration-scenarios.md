# v8.0.17 registration compatibility validation

## Inputs and failure scenarios (recorded before implementation)

- Failed Actions run: 37563038843; Pro commit: f3e836e1fd15e9a835f0043627734e92e5832183.
- Core: v8.0.17, 57bde35179ecbdca176ca8923d39cc18805774f2. The CI Core validator uses the models bundled in that upstream revision.
- The unchanged generator fails because Register now initializes CredentialVersion between RegistrationEpoch and Generation. Reproduction: `/tmp/ci-v8017-evidence/before.log`.
- Removing or overwriting the new CredentialVersion block would regress upstream protection against stale refreshes overwriting replaced credentials.
- Moving quota-protection restoration after persistence or publication would store/publish an auth without its retained protection state.
- Matching Generation alone could select the initial defaulting block or Load instead of Register's final initialization; require the adjacent persistence boundary and exactly one match.
- Omitting restoration or injecting it twice could lose protection state or repeat restoration during a single registration.
- Repairing the first anchor can uncover subsequent upstream drift; replay the entire generator before starting final validation.
- Full replay exposed MarkResult anchor drift: upstream now rejects stale execution results before selecting the model key. Preserve its unlock, mutation-gate release, hook, and error-event behavior; Pro's boolean markResult must return false on this branch. Removing the guard could apply an old credential's failure to a replacement account; retaining a bare return fails compilation.
- The scheduler ownership guard found two new direct scheduler writes: rejected access tokens without expiry and canceled refresh retries. Both must preserve upstream state changes and refresh the policy-aware scheduler after releasing m.mu; invoking RefreshSchedulerEntry under that lock deadlocks, while retaining direct upserts can bypass account policy.
- Pro diagnostics construct Result directly, bypassing upstream recordExecutionResult's new version binding. After a token refresh raises CredentialVersion above one, a correctly bound diagnostic with version zero can be incorrectly rejected. Reproduce via Register, token-changing Update, BindPinnedResult, and MarkPinnedResult using only public manager APIs; require the current result to be accepted and an old result after replacement to be rejected.
- Inspection refresh's custom identity check must reject a credential-version change even if token values later return to the original values (ABA), while allowing unrelated metadata updates. Reproduce with a saved base, two token-changing Updates, and CommitInspectionRefresh; a stale refresh must not overwrite the current credentials.

## Verification

1. Run the unchanged generator against the exact upstream source and retain its failure log.
2. Replay the repaired generator, inspect Register for the intact CredentialVersion block and exactly one restoreQuotaProtection call before persistLocked/publication, and verify rejected reapplication does not mutate the tree.
3. Run the existing Core validator on a fresh clean checkout with race detection, staticcheck, vet, both builds, and inspection HTTP E2E. Retain JSONL, phase timings, summary, and E2E receipts; do not add unit tests after implementation.
4. Run repository validation and inspect the final diff. This repair does not authorize a commit, push, or release.

```sh
PATH="/Users/jiegto/go/bin:$PATH" \
GOTOOLCHAIN=go1.26.0 GOCACHE=/tmp/pro-v8-go-build-cache \
VALIDATION_RACE=1 VALIDATION_STATICCHECK=1 VALIDATION_INSPECTION_E2E=1 \
VALIDATION_ARTIFACT_DIR=/tmp/ci-v8017-evidence/final-go126 \
bash scripts/validation/core.sh /tmp/ci-v8017-verified-go126-core
```

## Verified results (2026-10-07)

- Full clean replay, 97-file upstream patch surface, rejected reapplication, Staticcheck SA4011/U1000, and go vet passed.
- Existing race regression suite: 10,923 test/subtest passes across 45 packages, zero failures. CGO and non-CGO builds passed.
- CI inspection HTTP E2E: 20 receipts passed, no reported coverage gaps within the `ci-fast` subset. Receipt: `/tmp/ci-v8017-evidence/final-go126/inspection-batch-history-e2e/result.json`.
- Public-API credential probe failed before the semantic repair (`boundVersion=0`, current diagnostic rejected, ABA refresh accepted), then passed afterward (`boundVersion=2`, current diagnostic accepted, old diagnostic and ABA refresh rejected). Reproducer source and before/after JSON are retained in `/tmp/ci-v8017-evidence/`.
- Repository validation passed all 180 existing tests plus actionlint/shellcheck and diff checks. Independent read-only review found no actionable issue.
- The initial local Staticcheck attempt used Go 1.27 and failed to decode its newer export format. The final complete run used CI's Go 1.26.0 and Staticcheck v0.7.0; no project workaround was added.
- Inputs/source hashes: `/tmp/ci-v8017-evidence/inputs.json`. The retained binary `/tmp/ci-v8017-evidence/cli-proxy-api-verified` matches the HTTP E2E receipt's SHA-256.
- No unit tests were added after implementation. No commit, push, release, or hosted Actions rerun was performed.

The checkout used above is now patched. For repeat validation, use the retained script, which creates a fresh checkout/output directory:

```sh
bash /tmp/ci-v8017-evidence/repeat-core-validation.sh
bash /tmp/ci-v8017-evidence/repeat-http-e2e.sh
```
