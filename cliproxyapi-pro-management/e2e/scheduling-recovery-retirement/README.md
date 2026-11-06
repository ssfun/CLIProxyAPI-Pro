# Scheduling board retirement verification

Failure modes recorded before implementation:

- Removing the Pro `/routing` manifest accidentally removes or shadows upstream routing orchestration.
- Recovery loses source, credential/model scope, retry time, reason, HTTP status, or resume information when the board disappears.
- A credential-wide restriction is incorrectly presented as limited to a model.
- Old account sessions or a changed registration epoch/restriction revision allow stale recovery actions.
- Successful checks/releases clear unrelated sources, or a failed refresh presents an old result as current.
- Deleted board imports, navigation labels, tests, or documentation remain in generated Management.
- Narrow screens cannot read long model names or reasons.

Final verification scenarios:

1. Generate Management from the pinned upstream revision and verify its native `/routing` route remains, with no Pro routing manifest.
2. Open recovery from inspection with simultaneous upstream model cooldown and inspection credential hold. Verify every detail's source, scope, retry time, reason, status, and recovery method, including missing timestamps and unknown reason values.
3. Exercise check and targeted release; verify request identity, registration epoch, source, model, revision and refresh behavior. Reject stale actions and preserve the other restriction.
4. Run the existing recovery-dialog-race harness for account switching, close/reopen, and delayed responses.
5. Inspect recovery at desktop and mobile widths. Save browser screenshots and a machine-readable result with exact upstream/generator revisions so the run is repeatable.

The main session performs final browser/E2E verification; development checks are limited to existing contracts and static compilation.

Entry-point P2 failure cases (recorded before implementation):

- Zero inspection history hides recovery for upstream-only restrictions.
- API-key credentials and providers unsupported by inspection cannot be selected.
- Account labels expose raw credential IDs or secret key material.
- Refresh failures leave stale selectable rows; empty results have no explanation.
- Closing/reopening or changing the Management connection lets an old request populate the new picker.
- Busy inspection/batch operations or disconnected sessions allow recovery writes.
- Targeted release removes an unrelated inspection/upstream restriction.

Entry-point browser verification must mount the real AccountInspectionPage, click its always-visible Restrictions and recovery action with zero history, search/select an unsupported API-key credential, and release only one source while asserting the remaining restriction and exact revision/identity request.
