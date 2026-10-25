# v8.0.43 repair scenarios (written before implementation)

Final repair: retire only the absorbed xAI upload and Responses terminal-error patches; preserve Codex upload synchronization, Pro cancellation priority for buffered chunks, and inspection-refresh fencing. Reviewed surface: 101 files; upstream-generic-fix: 29. Scenarios below record the order in which failures and review findings were established before their corresponding changes.

Inputs: Core 307803510bce87f986fcf7b1a84190f105c7b4aa; models 6e82c3999c895ef83ca9087e6944008658fabe78; Pro base a0b7564.

1. Full generator rejects the old xAI upload reader before writes although upstream now waits for payloadReceived. Retire this redundant patch, keeping upstream's sync.Once and payload ordering unchanged.
2. Broad deletion could remove the still-needed Codex synchronization. All three Codex keepalive-upload variants must retain requestReadCh and a wait before terminal response.
3. Retired file could remain generator-owned via gofmt, inventory, or ownership counts. xAI test must be byte-identical to upstream; reviewed files must be 101 and upstream-generic-fix 29.
4. Initial repair may uncover other stale anchors. Full clean-source preflight and replay must pass, including rejection before writes and rejection of reapplication.
5. Upload/Pong races or other regressions may appear at runtime. Run existing race-enabled Core validation, baseline comparison on failure, CGO/non-CGO builds, staticcheck, and final HTTP E2E; retain JSONL, summaries, and E2E receipts.

Verification script preservation.py and existing ownership assertions are prepared before implementation. No new unit tests will be added after code changes.

6. Full replay after xAI retirement exposed the absorbed Responses WebSocket terminal-error patch: upstream now shares handleErr and drains PendingStreamError when duplex or not completed. Remove the redundant handleError injection and two replacements, preserving upstream completed-response semantics and Pro interrupt lifecycle changes. Keep existing terminal-order regression coverage (both duplex modes, 401/429, EOF without errors) in final validation.

7. **Superseded by scenario 9; not the final implementation.** The next replay exposed the absorbed nextStreamChunk cancellation/closed-channel race fix. Upstream checks !ok && ctx.Err()!=nil; retain that distinction (ordinary buffered chunks are not discarded solely due to cancellation), retire the redundant patch, and retain the existing canceled-EOF regression test. Its removal reduces the final reviewed surface to 100 and upstream-generic-fix count to 28. The Responses WebSocket file remains owned for Pro interrupt lifecycle changes.

8. Devin inspection-refresh extension still required: upstream now captures now once for cooldown reconciliation and passes it to applyDevinQuotaRestoration. Match that call and preserve the shared timestamp while retaining the inspectionRefreshRestrictionsMatch fence; do not revert to a fresh time.Now(). Existing stale-inspection/Devin recovery coverage must continue passing.

9. Review found that scenario 7's retirement would narrow Pro cancellation priority: a canceled context plus a buffered normal/error chunk could process that chunk and report Failed rather than Canceled. Before the correction, extend the existing cancellation regression to cover closed, buffered data, and buffered non-cancellation errors, then reproduce against the intermediate generated source. Retain a narrow Pro patch removing only !ok from upstream's ctx.Err() check. Final surface remains 101 / upstream-generic-fix 29.
