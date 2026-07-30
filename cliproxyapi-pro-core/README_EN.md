# CLIProxyAPI Pro Core

Customized Docker build layer for upstream `router-for-me/CLIProxyAPI`.

This directory does not maintain a full fork of upstream. During Docker build it downloads an upstream release, copies in the local `embeddedusage/` package, applies the patch script in `patches/`, and builds a multi-arch image for the Pro deployment.

Standard macOS, Windows amd64, and Linux Pro releases plus Docker images prebundle the `proxy-pool` and `oauth-model-policy` dynamic plugins. The former exposes a fixed loopback SOCKS5 endpoint; the latter removes models unavailable to OAuth plans across supported providers. Windows ARM64, FreeBSD, and `_no-plugin` assets do not currently bundle dynamic plugins.

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
- `POST /v0/management/usage/reset` — atomically clear request events and derived statistics while preserving monitoring settings, model prices, quota cache, and backups.
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
- `pro_settings` — Pro-owned settings, currently including request-state protection.
- `routing_cursor_state` — account-routing rotation cursors.
- `auth_runtime_stats` — account selection, success/failure, and recent-request-bucket statistics.
- `account_inspection_schedule` — persisted backend account-inspection schedule.
- `account_inspection_snapshot` — the latest finished inspection result, including run settings, summary, health counts, complete results, and raw error details, but excluding inspection logs.

`/usage/import` accepts the same JSONL format. It reads and verifies the complete request before writing, then imports usage events and restores model prices, quota cache entries, routing runtime state, monitoring settings, the account-inspection schedule, and the latest inspection-result snapshot when present. Routing cursors and account runtime statistics are restored in one SQLite transaction. A restored result snapshot is read-only until a new full inspection runs. Manifest-free event-only and mixed JSONL files are rejected by default because they cannot receive file-level integrity verification. A trusted legacy backup can be imported explicitly with `?allow_legacy=1` or the `X-CLIProxy-Allow-Legacy-Backup: true` header; the management UI asks for confirmation before using this compatibility mode.

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

### OAuth plan model policy plugin

The patch layer adds a generic `AuthModelFilter` capability to the upstream plugin SDK/ABI. Core provides the current auth, its native model set, and a controlled HTTP callback, while enforcing that a plugin may only subtract existing models. Plan discovery and policy rules stay in the bundled `oauth-model-policy` plugin.

The plugin supports xAI, Codex, Claude, Gemini CLI, Antigravity, and Kimi OAuth, with `_unknown`, `_default`, and custom plan rules for every provider. Processing order is upstream `excluded_models`, plugin plan filtering, OAuth alias/prefix, then model registration. The final registration constrains both `/v1/models` aggregation and scheduler candidates. See `cliproxyapi-pro-plugins/oauth-model-policy/README.md` for configuration and discovery details.

### Backend account inspection scheduler

The patch layer adds backend account-inspection routes under the management API:

Request monitoring also stores TTFT, HTTP status code, structured error, reasoning effort, and service tier. `/usage/status` returns recent dead-letter samples with sensitive fields redacted. Account-inspection automatic actions support consecutive-confirmation gating, and quota cache entries include parser version plus response-shape hashes.

- `GET /v0/management/account-inspection/schedule`
- `GET /v0/management/account-inspection/status`
- `GET /v0/management/account-inspection/logs` (WebSocket/WSS log and status stream)
- `PUT|PATCH /v0/management/account-inspection/schedule`
- `POST /v0/management/account-inspection/run`
- `POST /v0/management/account-inspection/inspect-one`
- `POST /v0/management/account-inspection/refresh-token`
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

It supports provider filtering, worker limits, retry/timeout settings, sampling, usage-threshold decisions, progress/status/log/result snapshots, pause/resume/stop controls, manual actions, and optional automatic actions for quota exhaustion, quota recovery, and account errors. Antigravity and xAI also support optional deep probes.

Before probing an account, the scheduler can refresh its auth record when it is already in the normal upstream refresh window. This inspection refresh path reuses upstream provider refresh logic and persistence, allows disabled accounts, skips API-key accounts, skips accounts not yet due, and respects `NextRefreshAfter`. If refresh succeeds, probing uses the refreshed auth; if refresh fails, the scheduler keeps the account and skips probing it for that run.

The schedule file defaults to:

```text
/CLIProxyAPI/usage/account-inspection-schedule.json
```

Override it with `ACCOUNT_INSPECTION_SCHEDULE_PATH` if needed.

The latest finished inspection result is persisted separately at `/CLIProxyAPI/usage/account-inspection-snapshot.json` with mode `0600`. A snapshot restored after process restart or usage import is read-only and is replaced when the next full inspection finishes. Override its path with `ACCOUNT_INSPECTION_SNAPSHOT_PATH` if needed.

### Routing policy and request-state protection

The patch layer exposes a unified routing-policy API under the management prefix:

- `GET /v0/management/routing-policy`
- `PATCH /v0/management/routing-policy/upstream`
- `PUT /v0/management/routing-policy/request-protection`
- `PUT|PATCH /v0/management/routing-policy` (legacy management-client compatibility)
- `POST /v0/management/routing-policy/release`

The API combines upstream routing mode, session stickiness, request retry, account switching, cooldown, quota fallback, and Codex identity-cloaking settings with Pro request protection. Upstream values can only update keys already present in `config.yaml`; request protection is stored in the `pro_settings` table in `usage.sqlite`. A legacy `routing.request-protection` node is migrated to SQLite and removed from YAML on first startup. Built-in protection supports Antigravity, xAI, Codex, Gemini CLI, Gemini, Gemini Interactions, Vertex AI, AI Studio, Claude, and Kimi.

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

The Management Center's “Check for updates” action calls `POST /v0/management/management-panel/check-update`. The endpoint keeps the updater's 30-second throttle, remote digest verification, and local SHA-256 comparison; it atomically replaces `management.html` only when the latest-release asset differs. This covers both a new release and a same-release asset replacement without re-downloading identical content.

### Runtime helper process

`entrypoint.sh` can start the bundled Komari agent before the main API process when both variables are configured:

- `KOMARI_SERVER`
- `KOMARI_SECRET`

It then starts `CLIProxyAPI` and optionally restores the latest usage backup from WebDAV.

## Repository layout

- `Dockerfile` — downloads upstream CLIProxyAPI, applies this customization layer, and builds the final image.
- `Dockerfile.runtime` — assembles the Actions runtime image from prebuilt Linux binaries.
- `QUOTA_PROVIDER.md` — QuotaProvider plugin protocol and compatibility rules.
- `../cliproxyapi-pro-plugins/oauth-model-policy/` — dynamic plugin for filtering auth models by OAuth plan.
- `entrypoint.sh` — starts Komari, starts the main API, and restores WebDAV usage backups.
- `embeddedusage/` — embedded SQLite usage service and management routes.
- `patches/apply_upstream_patches.py` — patches upstream source during Docker build.
- `patches/account_inspection_scheduler.go` — backend account-inspection scheduler injected into upstream management handlers.
- The generated API Server shuts down its management Handler from `Stop`; embedders that create a Handler directly through the SDK must also call `Shutdown()` to release inspection, routing-protection, login-cleanup, and global callback ownership.
- `patches/routing_policy.go` — unified routing configuration, request-state-protection handlers, usage plugin, and automatic release task.
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
- `SOURCE_DATE_EPOCH` — optional Unix timestamp used for the embedded build date. Set it together with an immutable upstream commit for a deterministic source binary.
- `GITHUB_TOKEN` — optional token for GitHub API requests.

Release workflows derive `SOURCE_DATE_EPOCH` from the newest immutable Core, models, and customization commit. Core archives use normalized ordering, timestamps, ownership, and permissions; Go builds also use `-trimpath`.

## Runtime environment variables

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
8. Exports usage statistics from one or more running CPA instances to WebDAV.
9. Triggers one or more Render deployments.
10. Sends a Telegram notification.
11. Deletes old workflow runs.

### Required Docker secrets

- `DOCKER_USERNAME`
- `DOCKER_PASSWORD`

### Multi-instance usage backup

The workflow uses one optional JSON secret for all WebDAV backup targets:

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

Each target is exported from its own CPA API and uploaded to its own WebDAV directory as:

```text
usage-export-YYYYMMDD_HHMMSS.jsonl
```

The workflow keeps the latest 7 backups per WebDAV directory and cleans both `.jsonl` and legacy `.json` files. If the secret is missing, invalid, or a target fails, the workflow logs a warning and continues.

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

Validate a clean upstream checkout with the repository script. It checks guarded-source preflight, rejected reapplication, the relevant Go packages, and the server build:

```bash
bash scripts/validation/core.sh /path/to/clean/CLIProxyAPI
```

Validate only entrypoint syntax:

```bash
sh -n cliproxyapi-pro-core/entrypoint.sh
```
