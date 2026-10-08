# xAI quota write identity fence

Failure scenarios written before the implementation:

1. A request captures auth A, waits at a real HTTP upstream, and completes after a same-file/same-index auth B replacement and B quota write. Its completion timestamp is newer, but SQLite must retain B's identity and quota.
2. Removing an account while its request is pending must prevent the response from recreating that account's cache.
3. Removing and registering the same subject again advances the registration epoch. The old registration's response must not change the new registration's quota.
4. Ordinary token/status refresh preserving the subject and registration epoch must still accept observations and preserve billing fields.
5. Inspection free-quota observation and inspection billing persistence from an old auth snapshot must fail the same fence as executor observations.
6. A manual PUT passes middleware identity validation, pauses before the Store write, and races a replacement. The Store must reject it and retain the new cache; client-supplied registration epochs must never override the server binding.
7. Holding the guard across the SQLite write must serialize auth mutation; reading current auth and later committing without the lock is insufficient.
8. Standalone Stores without a runtime manager retain existing identity replacement/merge behavior. Guard attachment belongs to a specific service Store so replacing the service cannot reuse an old manager.
9. An uploaded account with an opaque access token and an email only in id_token (map, JSON string, or JWT) must use the same identity for inspection, request observations, manual PUT, and the runtime guard. An email fallback supported by inspection must never be rejected by the guard or hidden by GET.

Focused regression command (after generator replay):

```sh
GOTOOLCHAIN=go1.26.0 GOCACHE=/private/tmp/pro-v8-go-build-cache go test -mod=mod -race ./internal/runtime/executor ./internal/api/handlers/management ./sdk/cliproxy/auth ./internal/pro/observability -run 'TestXAIQuotaLateHTTPResponseFence|TestXAIQuotaInspectionAndManualWriteFence|TestXAIQuotaCommitSerializesReplacement|TestXAIQuotaIDTokenEmailHTTPAndSQLite|TestXAIUploadQuotaIdentityHTTPAndSQLite|TestXAIRequestObserverHTTPAndSQLiteIdentity|TestStoreMergeXAIQuotaCache|TestProQuotaEndpointPersists' -count=1 -v -json > xai-quota-fence.jsonl
```

The JSONL log records the authoritative SQLite identity/quota after each delayed writer, with channel synchronization instead of completion-order assumptions. The tests also print SQLite snapshots suitable for verification from repeated runs.
