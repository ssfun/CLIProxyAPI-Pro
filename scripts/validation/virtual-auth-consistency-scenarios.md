# Virtual Auth mutation consistency failure scenarios

These scenarios are recorded before implementation. They exercise the real
Management Handler, native Auth Manager, and local source files in an isolated
copy of the v8.0.35 generated Core. The user subsequently confirmed the
source-file-first policy below; the initial failure evidence was collected
before production implementation.

## Failure scenarios

1. **Missing source file or parent.** Register two virtual runtime identities
   backed by one source path, remove the file or its parent, then disable the
   shared account. The operation must expose whether a source-write error occurs
   after runtime publication. Record both identities and file existence before
   and after. Permission bits are not a deterministic failure injector.
2. **Concurrent source refresh.** Pause the mutation after its source JSON was
   read, replace the JSON with refreshed credentials and an unrelated field,
   then resume. Detect whether an old full-file snapshot overwrites the refreshed
   credentials or fields. The Pro mutation should only own its managed fields.
3. **Stale sibling snapshot.** Pause after the Manager's sibling List snapshot,
   commit a native Modify on a sibling, then continue the Pro mutation. Detect
   whether List plus Update restores the sibling's old fields and loses the
   native commit. Use channels and the mutation callback to control ordering.
4. **Cancellation between siblings.** Cancel after the first sibling commits,
   before another sibling updates. Record the returned error, how many siblings
   changed, and whether the shared source changed. This determines whether the
   current operation reports partial completion or silently succeeds.
5. **Sibling registration changes between List and Update.** Replace or remove a
   sibling while its copied Auth is awaiting Update. Record registration epoch,
   error, surviving/new identity, and source state; never assume a Manager method
   rejects stale writes without running the real path.

## Required evidence and policy decision

Keep the fixture source, replay script, exact Go/Core versions, race-enabled
targeted test JSON, and before/after state receipts under
`/tmp/pro-virtual-consistency`. Expected-red safety assertions are deliberate.
Run only this bounded integration group; do not run the full E2E suite during
development.

## Confirmed acceptance policy

- Save managed fields into the current existing source before any runtime update.
  A source-write failure must leave every virtual runtime identity unchanged.
- After source commit, use native Modify for each captured identity and epoch,
  attempt every still-eligible sibling, preserve completed changes, and report
  partial errors explicitly. Manual retry reconciles the remaining identities.
  Do not roll back or add background retry.
- Preserve unmanaged source fields and native token/metadata updates. A mixed
  nonvirtual representative must not perform another whole-source Store.Save.
- Reject removed or replaced source/identities. Stage a complete 0600 temporary
  file, sync and close it, check the existing source identity/content, and rename.
  Clean up temporary files on create/chmod/write/sync/close/conflict/rename/cancel
  failures. Final path checks plus rename are not an OS compare-and-swap.
- Keep identity-local errors local; do not propagate them or rewrite the source.
- Verify the actual Management HTTP response includes partial failure outcomes.

## Review-discovered boundaries, recorded before their correction

1. After source commit and before the trigger's native Modify, refresh the
   triggering identity's token without changing its registration epoch. Its
   original caller guard must run again inside Modify; a stale result becomes a
   source-saved partial failure, while other eligible siblings still run.
2. After the first sibling publishes, rewrite the source in place with conflicting
   managed fields. Inode identity alone is insufficient: later siblings must reject
   the changed desired state and report partial failure. Unmanaged token changes
   remain allowed.

The two additional boundaries are reproduced through the real Handler/Manager
and local files in `/tmp/pro-virtual-consistency/reviewer_boundary_e2e_test.go`
before their fixes. Keep red/green JSON and receipts alongside the initial evidence.

## Blank source rejection (P1, recorded before correction)

- An existing empty or whitespace-only source is invalid input, never an empty
  metadata object. The real Handler/Manager mutation must fail before publishing
  any runtime changes, preserving the exact source bytes.
- An external writer opens the source with O_TRUNC before the first source read,
  keeps that descriptor open through the Pro mutation, then finishes writing
  refreshed credentials. Pro must reject the transient empty source; the visible
  path must still refer to the writer's file and contain all refreshed fields.
- Repeat that interleaving inside the preparation mutation callback, after the
  initial source read but before the second read. Reject before rename/runtime
  publication, and preserve the external writer's eventual visible credentials.
- Compare serialized real Manager snapshots before/after and emit source/runtime
  receipts. This covers observed blank reads, not arbitrary external writes after
  the final check and before rename (which remains a documented boundary).
