# v8 capability ownership

Reviewed against Core v8.0.4 (`d33f63f8`) and Management v1.25.0 (`b87b9487`).
The surface manifests list modified upstream files, not all added Pro sources.

| Area | Upstream owns | Pro retains |
| --- | --- | --- |
| Configuration | YAML migration, canonical schema validation, effective runtime Config, v0 compatibility, native v8 visual editing | Existing-key startup overrides; Pro SQLite migration |
| Usage | Claude nested usage parsing, PublishFailureWithDetail, stream response-model fallback | Speed/pricing metadata, final outcome ordering, independent persistence sink and synchronous policy settlement |
| Quota | Provider discovery and native plugin dispatch, panic/fuse/HTTP lifecycle | Previous/normalized snapshots, auth-bound context, last-known-good plan, SQLite persistence, legacy adapter, selection and stale-result guards |
| Management quota | Provider state types and tab order | Persistent card snapshots, including Devin and Meta; these do not automatically add inspection or account-policy support |
| Codex identity | Persisted subscription plan and token refresh | Cross-provider policy freshness and execution-only overlays |
| Model observation | ResponseModel and stream observer | Final outbound model audit and plugin integration |
| Request completion | Asynchronous lifecycle notifications | Raw response capture and synchronous quota settlement, which cannot be replaced by an asynchronous notification |
| Proxy transport | Static proxy settings and per-request proxy support | Pool takeover and generation-aware transport invalidation |
| Auth and scheduling | Credential registration epoch, weighted selector, catalog cooldown projection | Batch identity fencing, revision-bound inspection holds, routing cursors and runtime-state persistence |

## Retired replacements

The generator no longer reimplements upstream-owned Claude `message.usage`
parsing or `UsageReporter.PublishFailureWithDetail`, and no longer replaces
`StreamUsageBuffer.PublishFailure`. Pro failure branches still call these native
helpers when needed. The native function preserves the buffered
response model and publishes failures even before token usage is available.

The existing isolated helper regression was updated before the replacement was
removed, because an executor's own model observer can hide a broken buffer
fallback. Process-level stream receipts verify the persisted outcome separately.

## Deliberately retained boundaries

- Upstream v8.0.52 owns model-stream startup cancellation, instance isolation,
  and callback-scope cleanup. The retained guard only closes a registered stream
  if serializing its RPC response fails; successful RPC return leaves it open.
- Auth model registration still needs a commit-time fence after asynchronous
  discovery; a pre-work generation check is insufficient.
- Standard Retry-After header fallback and metadata read synchronization still
  differ from upstream and are not removed by this convergence.
- Existing Pro v0 routes remain supported. Only quota fetch is composed into the
  new v8 route; reset operations do not acquire inspection-recovery semantics.
- Dynamic-plugin migration is not a prerequisite for this change. Static Pro
  features remain available in no-plugin builds.
- Management uses native v8 visual editing and credential APIs. Pro connection
  tests retain a small feature API, and authenticated Pro v0 requests continue
  to share the native client; changing URL versions is not a convergence goal.
- Runtime proxy patches alter only resolution, cache identity and construction
  boundaries. The three generation owners share the existing CAS/Purge algorithm,
  while cache locks and callbacks remain local. Realtime relays share usage
  decoding, retaining their distinct event identity and settlement lifecycle.

## Validation

Failure cases are recorded in `v8-capability-convergence-scenarios.md` and the
subsystem E2E scenario files. Replay into a fresh upstream checkout; never apply
a changed overlay onto a previous generated tree. Final validation must include
Core and Management checks plus browser/config HTTP, quota HTTP/persistence and
stream usage receipts. Keep the red and green artifacts separately.
