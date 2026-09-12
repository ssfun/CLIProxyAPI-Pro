# CLIProxyAPI Pro Core

Customized Docker build layer for upstream `router-for-me/CLIProxyAPI`.

This directory does not maintain a full fork of upstream. During Docker build it downloads an upstream release, copies in the local `embeddedusage/` package, applies the patch script in `patches/`, and builds a multi-arch image for the Pro deployment.

The proxy pool and OAuth account policy are linked directly into Core. Every Pro build, including `_no-plugin` assets, includes both features. Their settings are stored in the usage SQLite `pro_settings` table and are never written to `config.yaml` or auth files.

## What this customization adds

### Embedded usage service

`embeddedusage/` is copied into upstream as:

```text
internal/embeddedusage
```

The patch layer starts the service with the main API process, enables upstream usage statistics, and exposes the service under the management API prefix:

```text
/v0/management/usage
```

By default it stores SQLite data at:

```text
/CLIProxyAPI/usage/usage.sqlite
```

The image declares `/CLIProxyAPI/usage` as a Docker volume so usage data, quota cache, model prices, and account-inspection schedule state can survive container replacement.

At service startup the patch layer forces the upstream config values required by Pro:

- `usage-statistics-enabled: true`
- `remote-management.panel-github-repository: https://github.com/ssfun/CLIProxyAPI-Pro`

The loaded in-memory config is always corrected. Runtime writes may only update keys that already exist in `config.yaml`; Pro never adds a missing key.

### Usage API

The embedded service exposes these management routes:

- `GET /v0/management/usage` — aggregated usage payload for the management UI.
- `GET /v0/management/usage/events` — incremental usage events after a cursor.
- `GET /v0/management/usage/aggregates` — aggregate usage by time bucket and provider/model/endpoint/API key.
- `GET /v0/management/usage/account` — aggregate overview, breakdown, and quality metrics for one exact `auth_index`.
- `GET /v0/management/usage/stream` — SSE stream for live usage updates.
- `GET /v0/management/usage/export` — JSONL/NDJSON export.
- `POST /v0/management/usage/import` — JSONL/NDJSON import.
- `GET /v0/management/usage/webdav/backups` — list known `usage-export-*.jsonl` backups from the configured WebDAV directory.
- `POST /v0/management/usage/webdav/preview` — download one listed backup and run the same verified import preview used for local files.
- `POST /v0/management/usage/webdav/restore` — restore one listed backup through the same JSONL parser and atomic import pipeline.
- `POST /v0/management/usage/reset` — clear request events, derived statistics, and account scheduling/success/failure counters behind an exclusive runtime-state barrier while preserving routing cursors, monitoring settings, model prices, quota cache, and backups.
- `GET /v0/management/usage/status` — service status and record counts.
- `GET /v0/management/usage/quota-cache` — read quota cache entries or stats.
- `PUT /v0/management/usage/quota-cache` — write a quota cache entry.
- `DELETE /v0/management/usage/quota-cache` — delete quota cache entries.
- `GET /v0/management/usage/model-prices` — read model price settings.
- `PUT /v0/management/usage/model-prices` — write model price settings.
- `GET|PUT|DELETE /v0/management/usage/model-price-rules` — manage globally applied per-model rules and context tiers.
- `POST /v0/management/usage/model-prices/sync` — synchronize observed models from models.dev.
- `GET /v0/management/usage/model-prices/sync-status` — read synchronization status.
- `POST /v0/management/usage/model-prices/recalculate` — explicitly recalculate historical costs.
- `GET /v0/management/usage/settings` — read retention, WebDAV, and model-price synchronization settings.
- `PUT /v0/management/usage/settings` — write retention, WebDAV, and model-price synchronization settings.

Details returned by `/usage/events` and `/usage/stream` include a stable event `id`, which the management UI uses for incremental deduplication and cursor catch-up. Usage responses also include a persistent `generation`; manual resets and retention cleanup advance it, and SSE emits a `reset` event so open pages replace their complete snapshot. SSE connections are awakened by an in-process notification after SQLite commits, with only a low-frequency keepalive instead of one database poll per connection per second.

Details also preserve `client_ip`, `x_forwarded_for`, and `user_agent` from upstream `ClientRequestMetadata`. `client_ip` is the direct peer address, while `x_forwarded_for` is the raw forwarding chain without trusted-proxy validation. These fields are for diagnostics and search only and never participate in access control, routing, or request protection. They follow the usage retention policy and are included in usage JSONL/WebDAV backups.

Historical `/usage/events` paging accepts `from_ms`, `to_ms`, `provider`, `model`, `auth_index`, `api_key_hash`, `status`, and `search`. The optional comma-separated `search_auth_indexes` is ORed with raw event-text `search`, while the other structured filters remain AND conditions. The first response returns a stable snapshot cursor that carries the complete filter scope across later pages.

`/usage/aggregates` supports `from_ms`, `to_ms`, `interval=minute|hour|day|all`, `group_by=provider,model,endpoint,api_key_hash`, `api_key_hash`, and `timezone_offset_minutes`. Responses include `latest_id`, `snapshot_at_ms`, and event-level `estimatedCost` sums so context tiers are never selected from aggregated token totals.

`/usage/account` requires an exact `auth_index` and supports `days=7|30|90|0` (`0` means all history) plus `timezone_offset_minutes`. It returns daily history, model and API-key breakdowns, pricing coverage, latency/TTFT/P95, streaming share, and retry counts with sample coverage from real zero-based `attempt_index` instrumentation. Historical events without attempt indexes remain unknown and are never inferred from `Retry-After`.

### JSONL usage backup and restore

`/usage/export` returns `application/x-ndjson`, one JSON object per line. New exports start with a `backup_manifest` that records the following line count and SHA-256. Import verifies the complete file before any database write, so truncated or modified backups are rejected as a unit.

The export contains usage events and may also include metadata records:

- `model_prices` — legacy base prices plus complete global per-model pricing rules.
- `quota_cache` — SQLite-backed quota snapshots used by quota cards and account-scoped refresh.
- `monitoring_settings` — retention, WebDAV backup, and scheduled models.dev synchronization settings.
- `pro_settings` — Pro-owned settings, currently including request-state protection, proxy-pool settings, and the `oauth-policy` account policy.
- `routing_cursor_state` — account-routing rotation cursors.
- `auth_runtime_stats` — account selection, success/failure, and recent-request-bucket statistics.
- `account_inspection_schedule` — persisted backend account-inspection schedule.
- `account_inspection_snapshot` — the latest finished inspection result, including run settings, summary, health counts, complete results, and raw error details, but excluding inspection logs.

`/usage/import` accepts the same JSONL format. It reads and verifies the complete request before writing, then imports usage events, model prices, quota cache entries, routing runtime state, monitoring settings, and Pro settings in one SQLite transaction. Imported Pro settings are applied to live configuration before commit, so an apply failure rolls back the database and a commit failure restores the pre-import configuration. After commit, the remaining runtime state, account-inspection schedule, and latest inspection-result snapshot are restored in a fixed order. An exclusive write barrier covers the complete import: synchronous management writes wait, while high-frequency routing and auth-runtime snapshots are dropped during the import window so stale state cannot overwrite the restore. A restored result snapshot is read-only until a new full inspection runs. Manifest-free event-only and mixed JSONL files are rejected by default because they cannot receive file-level integrity verification. A trusted legacy backup can be imported explicitly with `?allow_legacy=1` or the `X-CLIProxy-Allow-Legacy-Backup: true` header; the management UI asks for confirmation before using this compatibility mode.

API-key policy backup records include stable SHA-256 key fingerprints, Profile rules, active Profile state, and policy audit history. They never include or restore `config.yaml api-keys`. Treat the fingerprints as sensitive identifiers: protect JSONL and WebDAV backups with the same access control, transport security, and storage retention used for other sensitive configuration exports. WebDAV restore accepts only listed `usage-export-*.jsonl` file names and reuses the local import preview and atomic restore pipeline.

Example import response fields:

```json
{
  "added": 100,
  "skipped": 5,
  "total": 105,
  "failed": 0,
  "modelPrices": 12,
  "modelPriceRecords": 1,
  "modelPriceRules": 12,
  "quotaCache": 8,
  "quotaCacheRecords": 1,
  "routingCursors": 4,
  "routingCursorRecords": 1,
  "authRuntimeStats": 8,
  "authRuntimeStatsRecords": 1,
  "accountInspectionSchedule": true,
  "accountInspectionScheduleRecords": 1,
  "accountInspectionSnapshot": true,
  "accountInspectionSnapshotRecords": 1,
  "monitoringSettings": true,
  "monitoringSettingsRecords": 1,
  "legacyBackup": false
}
```

### SQLite-backed quota cache

The embedded service stores quota snapshots in SQLite for these providers:

- Antigravity
- Claude
- Codex
- Gemini CLI
- Kimi
- xAI

The management UI reads and writes this cache through `/usage/quota-cache`, so quota cards can be restored after page refreshes, browser changes, and backend restarts.

### QuotaProvider plugin protocol

The patch layer adds an optional `QuotaProvider` capability to the upstream plugin SDK/ABI and
exposes `POST /v0/management/quota/fetch`. The host owns callback lifecycle, normalized snapshots,
SQLite persistence, and last-known-good plan retention. The current Gemini CLI plugin needs no
changes: Core adapts its existing `Executor.HttpRequest`; a future native implementation takes
priority automatically. See [QUOTA_PROVIDER.md](QUOTA_PROVIDER.md) for the schema and compatibility
rules.

### Auth-file connection test

- `POST /v0/management/auth-files/test` — accepts `name`, optional `auth_index`, and `model`, then pins one minimal real OpenAI Chat-format text-generation request to that auth record.

Pro extends upstream `GET /v0/management/auth-files/models`: it prefers models registered for the selected auth, then uses the matching provider's static definitions when upstream unregisters a disabled credential; Codex fallback models are selected by account plan. The model viewer and connection test therefore share the same endpoint. The test reuses upstream request translation, credential proxying, 401 refresh, model aliasing, and result accounting, but does not write a request-monitoring event. Diagnostic execution bypasses normal disabled, cooldown, and unavailable eligibility gates so an unhealthy credential can be rechecked, while preserving the operator-controlled `disabled` switch. The response contains `success`, `model`, `latency_ms`, and model `output`, or `error`, `error_code`, and `http_status`.

### Built-in proxy pool and OAuth plan account policy

Core includes a loopback SOCKS5 proxy pool and OAuth account policies for xAI, Codex, Claude, Gemini CLI, Antigravity, and Kimi. Plan detection reuses auth-bound upstream execution, reads plugin-quota and account-inspection snapshots from SQLite in time order, and selects the newest evidence containing a usable plan. Plan rules support `excluded-models`, `prefix`, `priority`, and `weight` as runtime-only overlays; neither `config.yaml` nor auth files are rewritten. The result constrains both `/v1/models` aggregation and scheduler candidates.

On first startup, Core reads legacy `plugins.configs.proxy-pool` and `plugins.configs.oauth-model-policy`, validates and stores them in SQLite, verifies the stored bytes, and only then atomically removes the old YAML. If legacy takeover was active, the root `proxy-url` is restored from the old `restore-proxy-url`; unrelated third-party plugin configuration is preserved.

### Backend account inspection scheduler

The patch layer adds backend account-inspection routes under the management API:

Request monitoring also stores TTFT, HTTP status code, structured error, reasoning effort, the requested service tier, and the effective tier reported by upstream. Fast pricing treats `priority` as a compatibility alias for `fast` and prefers the response tier for billing, so requests downgraded to `default` do not use Fast rates. `/usage/status` returns recent dead-letter samples with sensitive fields redacted. Account-inspection automatic actions support consecutive-confirmation gating, and quota cache entries include parser version plus response-shape hashes.

- `GET /v0/management/account-inspection/schedule`
- `GET /v0/management/account-inspection/status`
- `GET /v0/management/account-inspection/logs` (WebSocket/WSS log and status stream)
- `PUT|PATCH /v0/management/account-inspection/schedule`
- `POST /v0/management/account-inspection/run`
- `POST /v0/management/account-inspection/inspect-one`
- `POST /v0/management/account-inspection/refresh-token`
- `POST /v0/management/account-inspection/inspect-many` — backend-limited bulk recheck with per-account outcomes.
- `POST /v0/management/account-inspection/pause`
- `POST /v0/management/account-inspection/resume`
- `POST /v0/management/account-inspection/stop`
- `POST /v0/management/account-inspection/actions`

The scheduler can inspect accounts for:

- Antigravity
- Claude
- Codex
- Gemini CLI
- Kimi
- xAI

It supports provider filtering, two-level probe concurrency, retry/timeout settings, sampling, usage-threshold decisions, progress/status/log/result snapshots, pause/resume/stop controls, manual actions, and optional automatic actions for quota exhaustion, quota recovery, and account errors. Antigravity and xAI also support optional deep probes.

In the inspection settings, `workers` is the global probe concurrency across all providers (`1–8`, default `4`), while `providerWorkers` is the per-provider probe concurrency (`1–4`, default `2`). Regular probes, deep probes, xAI probes, and pre-probe token refreshes share these two limits and have no separate serialization gate. `deleteWorkers` (`1–4`, default `4`) limits both automatic actions and manual bulk actions from the management UI. These settings are stored in the account-inspection schedule JSON; they neither read nor modify `config.yaml`.

Before probing an account, the scheduler can refresh its auth record when it is already in the normal upstream refresh window. This inspection refresh path reuses upstream provider refresh logic and persistence, allows disabled accounts, skips API-key accounts, skips accounts not yet due, and respects `NextRefreshAfter`. If refresh succeeds, probing uses the refreshed auth; if refresh fails, the scheduler keeps the account and skips probing it for that run.

The schedule file defaults to:

```text
/CLIProxyAPI/usage/account-inspection-schedule.json
```

Override it with `ACCOUNT_INSPECTION_SCHEDULE_PATH` if needed.

The latest finished inspection result is persisted separately at `/CLIProxyAPI/usage/account-inspection-snapshot.json` with mode `0600`. A snapshot restored after process restart or usage import is read-only and is replaced when the next full inspection finishes. Override its path with `ACCOUNT_INSPECTION_SNAPSHOT_PATH` if needed.

### Request-state protection

The patch layer exposes request-state protection under the management prefix:

- `GET /v0/management/routing-policy`
- `PUT /v0/management/routing-policy/request-protection`
- `PUT|PATCH /v0/management/routing-policy` (legacy compatibility; only `requestProtection` is handled)
- `POST /v0/management/routing-policy/release`

The API manages only Pro request-state protection and never reads or edits global routing values in `config.yaml`. Protection is stored in the `pro_settings` table in `usage.sqlite`. When SQLite has no setting yet, a legacy `routing.request-protection` node can be used as a one-time migration source; SQLite takes precedence afterward and the original YAML remains unchanged. Built-in protection supports Antigravity, xAI, Codex, Gemini CLI, Gemini, Gemini Interactions, Vertex AI, AI Studio, Claude, and Kimi.

Protection is disabled by default and starts in `observe` mode. Per-provider settings cover HTTP statuses, consecutive-confirmation thresholds, confirmation windows, 429 quota evidence, automatic release, and fallback disable duration. `enforce` can disable matching auth records and records `request_protection` ownership; automatic or manual release affects only records owned by this policy, never user-disabled or differently owned accounts.

Release time prefers `Retry-After`, Codex reset headers, and response-body `resets_at` / `resets_in_seconds`, then falls back to the configured provider duration. Runtime status includes currently protected accounts and recent in-process events.

### Root redirect and health response

The patch layer also changes upstream API behavior:

- `/` redirects to `/management.html`.
- `/healthz` returns a richer CLIProxyAPI status payload while preserving `HEAD /healthz`.

### Management panel defaults

The patch layer changes upstream's default remote management panel repository to:

```text
https://github.com/ssfun/CLIProxyAPI-Pro
```

This affects the built-in default config, `config.example.yaml`, and the management asset updater's default latest-release API URL.

The release workflow places the Pro `management.html` produced by the same build at `/CLIProxyAPI/static/management.html` in the Docker image and pins it with `MANAGEMENT_STATIC_PATH`. If the GitHub Release API or asset download fails, upstream's updater keeps using this local file. Core binaries and non-Docker archives no longer embed management or change upstream's fallback implementation.

When `GITSTORE_GIT_TOKEN` is set, it is automatically used for management and plugin GitHub Release metadata, authenticated API asset downloads, and startup plugin auto-install. Matching is restricted to HTTPS GitHub API release paths. Explicit `plugins.store-auth` rules take precedence, and a matching `type: none` rule can suppress this environment fallback. Startup auto-install and its HTTP requests are bounded to two minutes, so an unhealthy registry or asset service cannot block process startup indefinitely.

The Management Center's “Check for updates” action calls `POST /v0/management/management-panel/check-update`. The endpoint keeps the updater's 30-second throttle, remote digest verification, and local SHA-256 comparison; it atomically replaces `management.html` only when the latest-release asset differs. This covers both a new release and a same-release asset replacement without re-downloading identical content.

### Runtime helper process

`entrypoint.sh` can start the bundled Komari agent before the main API process when both variables are configured:

- `KOMARI_SERVER`
- `KOMARI_SECRET`

It then starts `CLIProxyAPI` and optionally restores the latest usage backup from WebDAV. On `TERM` or `INT`, the entrypoint forwards the signal to the main process and Komari agent, waits for both, and preserves the main process exit status.

## Repository layout

- `Dockerfile` — downloads upstream CLIProxyAPI, applies this customization layer, and builds the final image.
- `Dockerfile.runtime` — assembles the Actions runtime image from prebuilt Linux binaries.
- `QUOTA_PROVIDER.md` — QuotaProvider plugin protocol and compatibility rules.
- `patches/sources/internal/pro/app/` — composition root, lifecycle, and legacy configuration migration for static Pro modules.
- `patches/sources/internal/pro/host/` — adapters around volatile upstream transport, model-registration, and auth boundaries.
- `patches/sources/internal/pro/proxypool/` — independent proxy-pool configuration, runtime service, node pool, and SOCKS5 implementation.
- `patches/sources/internal/pro/oauthpolicy/` — independent OAuth plan detection, model filtering, and configuration service.
- `patches/sources/internal/pro/settings/` — versioned settings persistence port consumed by modules.
- `patches/sources/internal/pro/storage/` — single SQLite lifecycle, idempotent schema, domain repositories, and transaction boundary.
- `patches/sources/internal/pro/state/` — stable routing/runtime contracts and the coalescing state writer.
- `patches/sources/internal/pro/observability/` — backup-coordination adapters for usage, retention, price sync, WebDAV jobs, and ordinary state writes.
- `patches/sources/internal/pro/quota/` — quota snapshot normalization/max-use calculation, cache success state and response-shape fingerprints, plus Gemini CLI/xAI billing, plan, request-path parsing, and merge policy.
- `patches/sources/internal/pro/routing/` — durable selection cursors and request-protection ownership policy.
- `patches/sources/internal/pro/inspection/` — inspection configuration, candidate filtering/sampling/two-level worker policy, status/log/stream/manual-action DTOs, result classification/filtering/pagination/summaries and merge transitions, provider decisions/error codes, action deduplication/summaries, result-snapshot schema/codec, automatic-action decisions, Antigravity/Claude/Codex/Kimi response parsing, and Antigravity/xAI deep-probe request/response protocols; provider probe transport, Gin/WebSocket, snapshot/quota-cache/observation I/O, and Auth mutation remain Management host adapters.
- `patches/sources/internal/pro/backup/` — JSONL export, the import-exclusive/ordinary-write shared barrier, and the cross-module pause, flush, import, live-state restore, inspection restore, legacy cleanup, and resume sequence.
- `entrypoint.sh` — starts Komari, starts the main API, and restores WebDAV usage backups.
- `embeddedusage/` — thin compatibility façade preserving upstream import paths, public types, and function signatures; implementation lives in `pro/observability`.
- `patches/apply_upstream_patches.py` — patches upstream source during Docker build.
- `patches/account_inspection_{runtime,http,accounts,transport,quota}.go` — backend account-inspection adapters split by lifecycle/API, account host capabilities, auth-bound transport, and quota-state boundaries before injection into upstream management handlers; tests follow the same split.
- `patches/account_inspection_host.go` and `patches/pro_auth_mutation.go` — host adapters for the inspection quota port and shared Auth mutation/file persistence.
- `patches/pro_management_runtime.go` — composes inspection and routing background lifecycles owned by one Management Handler.
- The generated API Server shuts down its management Handler from `Stop`; embedders that create a Handler directly through the SDK must also call `Shutdown()` to release inspection, routing-protection, login-cleanup, and global callback ownership.
- `patches/routing_policy.go` — unified routing configuration, request-state-protection handlers, usage plugin, and automatic release task.

Static modules follow their actual host lifecycles: `pro/app` owns the proxy-pool and oauth-policy services on the request path; `pro/observability` follows the process context; inspection and routing controllers follow the Management Handler. OAuth plan detection reuses auth-bound upstream execution and the newest usable SQLite plugin-quota or inspection snapshot. Quota updates trigger auth-generation-safe model re-registration, while `POST /v0/management/pro/oauth-policy/refresh` starts an explicit re-detection. Cross-lifecycle backup ports use owner-scoped registration and reverse-order unregistration, so stopping an older Handler or Service cannot clear callbacks owned by a newer instance. `internal/embeddedusage` is restricted to upstream/SDK compatibility boundaries; `internal/pro` business modules do not depend back on that façade.
- Core invariants: account inspection takes precedence over request protection; imported `routing_cursor_state` and `auth_runtime_stats` are applied to the live manager immediately; existing DB tables, JSONL record types, and `/v0/management/usage*` APIs remain compatible.
- `patches/config_existing_updates.go` — existing-scalar-only YAML updates that never create missing keys.
- `.github/workflows/release-core.yml` — image publish, Pro binary assets, `management.html` publish, usage backup, Render deployment trigger, Telegram notification, and run cleanup.

## Docker build

Published image:

```bash
docker pull sfun/cliproxyapi-pro:latest
```

Build latest upstream release:

```bash
docker build -t cliproxyapi-pro -f cliproxyapi-pro-core/Dockerfile .
```

Build a specific upstream release while writing the Pro runtime version:

```bash
docker build \
  --build-arg CLIPROXY_VERSION=vX.Y.Z \
  --build-arg CLIPROXY_BUILD_VERSION=vX.Y.Z-pro \
  -t cliproxyapi-pro:vX.Y.Z-pro \
  ./cliproxyapi-pro-core
```

`CLIPROXY_VERSION` selects the upstream source tag, while `CLIPROXY_BUILD_VERSION` sets the runtime version.

Build args:

- `CLIPROXY_REPO` — upstream repository, default `router-for-me/CLIProxyAPI`.
- `CLIPROXY_VERSION` — upstream release tag. If empty, the Dockerfile resolves the latest release.
- `CLIPROXY_COMMIT` — optional upstream commit SHA; when set, source is downloaded from that commit while `CLIPROXY_VERSION` remains the version label.
- `CLIPROXY_BUILD_VERSION` — optional runtime version. If empty, it uses the upstream version resolved from `CLIPROXY_VERSION`.
- `PRO_MANAGEMENT_REPO` — repository used by the source Docker build to obtain the Pro management asset packaged into the image; defaults to `ssfun/CLIProxyAPI-Pro`.
- `SOURCE_DATE_EPOCH` — optional Unix timestamp used for the embedded build date. Set it together with an immutable upstream commit for a deterministic source binary.
- `GITHUB_TOKEN` — optional token for GitHub API requests.

Release workflows derive `SOURCE_DATE_EPOCH` from the newest immutable Core, models, and customization commit. Core archives use normalized ordering, timestamps, ownership, and permissions; Go builds also use `-trimpath`.

## Runtime environment variables

- `GITSTORE_GIT_TOKEN` — optional GitHub token used for management and plugin GitHub Release metadata and authenticated API asset downloads, avoiding anonymous API rate-limit 403 responses.
- `MANAGEMENT_STATIC_PATH` — fixed to `/CLIProxyAPI/static/management.html` in Docker images and points to the packaged Pro panel.

### Usage service

- `USAGE_SERVICE_ENABLED` — default `true`; set to `false`/`0`/`no`/`off` to disable the embedded service.
- `USAGE_DATA_DIR` — default `/CLIProxyAPI/usage`.
- `USAGE_DB_PATH` — default `/CLIProxyAPI/usage/usage.sqlite`.
- `USAGE_BATCH_SIZE` — default `100`.
- `USAGE_POLL_INTERVAL_MS` — default `500`.
- `USAGE_QUERY_LIMIT` — default `50000`.

### Account inspection

- `ACCOUNT_INSPECTION_SCHEDULE_PATH` — optional schedule JSON path. Defaults to `USAGE_DATA_DIR/account-inspection-schedule.json`.
- `ACCOUNT_INSPECTION_SNAPSHOT_PATH` — optional latest inspection-result snapshot JSON path. Defaults to `USAGE_DATA_DIR/account-inspection-snapshot.json`.

### WebDAV usage restore

When all variables below are configured, `entrypoint.sh` waits for the local API to become ready, downloads the latest backup from WebDAV, and imports it into `/v0/management/usage/import`:

- `WEBDAV_URL`
- `WEBDAV_USERNAME`
- `WEBDAV_PASSWORD`
- `MANAGEMENT_PASSWORD`

Restore lookup supports both backup names:

```text
usage-export-YYYYMMDD_HHMMSS.json
usage-export-YYYYMMDD_HHMMSS.jsonl
```

During the compatibility transition, Docker WebDAV restore always calls `/usage/import?allow_legacy=1`. Manifest-backed backups are still verified strictly; manifest-free legacy backups are imported with an explicit warning that integrity cannot be verified. Normal management API imports still reject manifest-free files by default.

The service's scheduled WebDAV upload, directory listing, and retention deletion requests have a two-minute total timeout, preventing an unhealthy endpoint from permanently holding the backup lifecycle or the usage-import pause barrier.

The import request uses:

```text
Content-Type: application/x-ndjson
```

### Komari agent

- `KOMARI_SERVER`
- `KOMARI_SECRET`

## GitHub Actions

Workflow:

```text
.github/workflows/release-core.yml
```

The workflow:

1. Checks the latest upstream CLIProxyAPI release and computes the Pro release tag, for example `v<core-version>-pro`.
2. Checks the latest upstream management release.
3. Builds Pro binary assets with the same platform matrix and archive formats as upstream, with the `CLIProxyAPI` asset prefix; default desktop/Linux archives enable CGO for dynamic-library plugin support, while `_no-plugin` archives remain CGO-free portable builds.
4. Reuses the Linux amd64/arm64 assets to assemble and push a multi-architecture image through `Dockerfile.runtime`, tagged with `latest` and the Pro release tag.
5. Applies the management customization layer and builds `management.html`.
6. Creates or updates the current repository GitHub Release, then uploads binary assets, `checksums.txt`, and `management.html`.
7. Writes core upstream and management upstream version mappings plus release notes into the GitHub Release notes.
8. Requests complete Pro WebDAV backups through the data-management pipeline on one or more running CPA instances, with a legacy usage-export fallback for old Cores returning 404.
9. Triggers one or more Render deployments.
10. Sends a Telegram notification.
11. Deletes old workflow runs.

### Required Docker secrets

- `DOCKER_USERNAME`
- `DOCKER_PASSWORD`

### Multi-instance Pro data backup

The workflow uses one optional JSON secret for all backup targets. The primary path on a current Core requires only `api_url` and `management_password`; the `webdav_*` fields in the example are used only for the 404 fallback when an old Core does not expose the data-management backup endpoint:

```text
CLIPROXY_USAGE_BACKUP_TARGETS
```

Example:

```json
[
  {
    "name": "cpa-main",
    "api_url": "https://cpa-main.example.com",
    "management_password": "management-password-1",
    "webdav_url": "https://webdav.example.com/cpa-main",
    "webdav_username": "webdav-user-1",
    "webdav_password": "webdav-password-1"
  }
]
```

Each target first calls `/v0/management/data/backups/now`. The CPA instance's data-management settings own the WebDAV URL, credentials, file naming, retention, and operation record. Complete backups are named:

```text
cliproxy-pro-backup-YYYYMMDD_HHMMSS_000.jsonl
```

Only when the target returns 404 does the workflow use the target's `webdav_*` fields to fall back to `/v0/management/usage/export`, producing `usage-export-YYYYMMDD_HHMMSS.jsonl` and retaining the latest 7 legacy backups. If the secret is missing or invalid, an old Core lacks fallback fields, or a target fails, the workflow logs a warning and continues.

### Multi-target Render deploy hooks

The workflow uses one optional JSON secret for all Render deploy hooks:

```text
CLIPROXY_RENDER_DEPLOY_HOOKS
```

Example:

```json
[
  {
    "name": "cpa-main",
    "hook_url": "https://api.render.com/deploy/srv-xxx?key=xxx"
  }
]
```

`url` is also accepted as an alias for `hook_url`. If the secret is missing, invalid, or a target fails, the workflow logs a warning and continues.

### Telegram notification secrets

- `TELEGRAM_CHAT_ID`
- `TELEGRAM_BOT_TOKEN`

## Local validation

Validate a clean upstream checkout with the repository script. It checks guarded-source preflight, rejected reapplication, `go vet` for `internal/pluginhost`, the relevant Go packages, and the server build:

```bash
bash scripts/validation/core.sh /path/to/clean/CLIProxyAPI
```

Validate only entrypoint syntax:

```bash
sh -n cliproxyapi-pro-core/entrypoint.sh
```

### API key concurrency limits

Each API key workspace can set a concurrent request limit without creating a Profile. `0` means unlimited; values up to `1,000,000` are accepted. Limits apply only while policy takeover is enabled. Excess requests return HTTP `429` with `api_key_concurrency_exceeded` before execution or request-quota consumption. A slot lasts until the HTTP handler finishes, including streaming responses and the entire WebSocket connection. Model discovery, public key queries, and management calls are excluded. WebRTC SDP bootstrap counts only its HTTP request, not the subsequent media session.

Counts are local to one server instance, never backed up, and retained across limit changes, takeover changes, and backup restores. Saved limits are persisted independently of Profiles and included in policy backups and restore previews. Restoring a policy backup from before this feature clears the saved limits to unlimited.

`PUT /v0/management/api-key-policy-key-concurrency` uses Management authentication and a session-bound `keyRef`: `{ "keyRef": "...", "limit": 4, "expectedLimit": 0 }`. A stale prior value returns `409`; success returns `concurrencyLimit`.

The concurrency workspace uses the same enable toggle and Save action as API Key quota. Policy create/update accepts optional `concurrency: { limit, expectedLimit }` and commits it atomically with quota and Profile changes. Omitting this field preserves the saved limit. Workspace controls require the `workspace_concurrency_limits` capability.
