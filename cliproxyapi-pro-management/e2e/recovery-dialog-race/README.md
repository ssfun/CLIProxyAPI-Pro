# Recovery dialog browser race

This harness imports the real `SchedulingRecoveryDialog` and `SchedulingRecoveryActions` from a patched Management checkout. Only `routingPolicyApi.get/check` are replaced with controlled in-browser responses; no Core service or real account is used.

1. Clone a clean Management upstream checkout, apply `cliproxyapi-pro-management/apply.sh`, and install its locked Bun dependencies.
2. Copy this directory to `<checkout>/e2e/recovery-dialog-race`.
3. Run `bun run dev -- --host 127.0.0.1 --port 4173` in that checkout.
4. Run `bash cliproxyapi-pro-management/e2e/recovery-dialog-race/run.sh [URL] [ARTIFACT_DIR] [fixed|before]` from this repository. The default mode asserts the fix; `before` documents the original failure.

The Ego Browser script checks A→B, a same-account reopen, a closed dialog, the parent callback log, and the next check's account ID. It writes `result.json` to the artifact directory. This proves the React session behavior under controlled API timing; it does not test a live Core response.
