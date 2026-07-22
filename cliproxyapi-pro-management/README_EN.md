# CLIProxyAPI Pro Management customizations

Customization layer for upstream `router-for-me/Cli-Proxy-API-Management-Center`.

This directory does not vendor the upstream application. It keeps overlay files plus a patch script that can be applied to a clean upstream checkout during local development or GitHub Actions release builds.

## What this customization adds

### Request monitoring page

Adds a top-level monitoring route:

```text
/monitoring
```

The page consumes the customized `cliproxyapi-pro-core` backend usage API. It loads an initial usage snapshot, follows incremental event polling or the SSE usage stream, and provides:

- request totals and success/failure metrics
- success rate and latency summaries
- input, output, cached, reasoning, and total token summaries
- estimated cost based on configurable model prices
- time-range filtering for today, 7d, 14d, 30d, and all data
- search plus account/provider/model/channel/status filters
- auto refresh interval selection and manual refresh
- sortable account overview table
- expandable account rows with model spend details
- account-scoped quota refresh and quota display
- realtime request table with recent success/failure pattern bars
- masking for sensitive token-like text in request metadata

Large account and realtime tables scroll inside their panels, so long histories do not stretch the whole page.

### Model price persistence

Model price settings are persisted through the backend SQLite API instead of normal browser-only state:

- `GET /usage/model-prices`
- `PUT /usage/model-prices`
- `GET|PUT|DELETE /usage/model-price-rules`
- `POST /usage/model-prices/sync`
- `GET /usage/model-prices/sync-status`
- `POST /usage/model-prices/recalculate`

If the backend has no saved prices, the UI can migrate old `localStorage` price settings once. Normal reads and writes then use SQLite.

Rules are keyed by `(provider, model)` and support input, output, cache-read, cache-write, multiple context-size tiers, and service-tier overrides. Prices can be synchronized manually or periodically from models.dev; only models observed in request history are persisted, and locked manual rules are not overwritten.

The backend selects pricing per request and snapshots the estimated cost on each usage event. Aggregate APIs sum those event costs. JSONL export/import preserves both complete rules and cost snapshots.

### SQLite-backed quota persistence

Quota snapshots are persisted through the backend usage service:

- `GET /usage/quota-cache`
- `PUT /usage/quota-cache`
- `DELETE /usage/quota-cache`

The UI starts `QuotaPersistenceBootstrap` from the main layout. It preloads saved quota snapshots into the Zustand quota store and syncs successful quota checks back to SQLite. Quota cache entries are also included in usage JSONL export/import as a `quota_cache` metadata record.

Supported quota providers:

- Antigravity
- Claude
- Codex
- Kimi

Quota cards also show cache timestamps and support single-card refresh when the feature flags in `src/config/features.ts` are enabled.

### Account inspection page

Adds a top-level account inspection route:

```text
/account-inspection
```

The page controls and displays backend-run inspections. The browser does not execute probes directly. The auth files page also shows inspection-written `last_error` health messages when no explicit status message exists. The backend can inspect:

- Antigravity
- Claude
- Codex
- Kimi

Features include:

- target provider selection
- configurable workers, delete workers, timeout, retries, used-percent threshold, and sample size
- backend run, pause, resume, and stop controls
- backend schedule enablement and interval configuration
- progress, summary cards, and result table from backend status polling
- logs and live status from the backend WebSocket/WSS stream
- suggested actions: keep, delete, disable, enable
- manual execution for a single planned action or all planned actions through the backend
- business-result toast messages for token refresh and single-account recheck, such as refresh success/failure, account errors, quota exhaustion, or healthy state
- optional backend auto-execution policies for quota-limit disable, quota-recovery enable, and account-error disable/delete
- quota snapshot refresh from backend inspection results

Backend schedule/status/control routes expected by the page:

- `GET /account-inspection/schedule`
- `GET /account-inspection/status`
- `GET /account-inspection/logs` (WebSocket/WSS log and status stream)
- `PUT /account-inspection/schedule`
- `POST /account-inspection/run`
- `POST /account-inspection/pause`
- `POST /account-inspection/resume`
- `POST /account-inspection/stop`
- `POST /account-inspection/actions`

Under the full management API prefix these are exposed by the backend as `/v0/management/account-inspection/...`.

### Supporting API and type patches

`apply_customizations.py` also patches upstream files to add:

- `/monitoring` and `/account-inspection` routes.
- sidebar navigation labels and icon.
- locale entries from `monitoring-locales.json`.
- `usageStatisticsEnabled` and `clean` config types used by monitoring/account inspection.
- `authFilesApi.patchFile` and `setStatusWithFallback` helpers.
- `accountInspection` service export.
- `Select` `triggerClassName` and `dropdownClassName` props.
- `maskSensitiveText` utility.
- `cachedAt` fields for quota state types and success states.

Request Monitoring uses an initial snapshot plus SSE increments and cursor catch-up, with event-ID deduplication. Trends, model rankings, and API-key rankings prefer server-side `/usage/aggregates` data and automatically fall back to local detail calculations when unavailable. Hidden tabs pause SSE and React incremental updates, then catch up by cursor when visible again; the page header shows live, reconnecting, background-paused, error, and latest-event states.

## Repository layout

- `overlay/` — files copied directly into the upstream checkout.
- `overlay/src/pages/MonitoringCenterPage.tsx` — request monitoring UI.
- `overlay/src/pages/AccountInspectionPage.tsx` — account inspection UI.
- `overlay/src/features/monitoring/` — monitoring and inspection logic.
- `overlay/src/extensions/quota/` — SQLite quota persistence integration.
- `overlay/src/services/api/` — added API clients.
- `overlay-replacements.json` — reviewed upstream and overlay SHA-256 pairs for the full-file replacements that intentionally collide with upstream paths.
- `monitoring-locales.json` — locale additions merged into upstream locale files.
- `apply_customizations.py` — applies all customizations to a target upstream checkout.
- `apply.sh` — shell wrapper around `apply_customizations.py`.
- `quota-persistence.patch` — legacy patch artifact kept for reference; current builds use `apply_customizations.py`.

Overlay collision preflight validates both sides of every reviewed replacement. An upstream file change or a local replacement change must update `overlay-replacements.json` explicitly; new unreviewed path collisions are rejected before any overlay file is copied.

## Applying locally

From this directory:

```bash
./apply.sh /path/to/Cli-Proxy-API-Management-Center
```

Equivalent direct command:

```bash
python3 apply_customizations.py /path/to/Cli-Proxy-API-Management-Center
```

The target directory must be an upstream checkout containing:

- `src/`
- `package.json`

## Local validation

After applying to an upstream checkout:

```bash
npm install
npm run type-check
npm run build
```

For clean validation without mutating the upstream working copy:

```bash
rm -rf /tmp/cpa-management-check
cp -R /path/to/Cli-Proxy-API-Management-Center /tmp/cpa-management-check
python3 /path/to/CLIProxyAPI-Pro/cliproxyapi-pro-management/apply_customizations.py /tmp/cpa-management-check
npm --prefix /tmp/cpa-management-check install
npm --prefix /tmp/cpa-management-check run type-check
npm --prefix /tmp/cpa-management-check run build
```

## GitHub Actions release workflow

Workflow:

```text
.github/workflows/release-management.yml
```

This workflow no longer creates a separate management release. It rebuilds and clobbers `management.html` on the current repository latest release when the management upstream changes, when the latest release is missing `management.html`, or when the workflow is triggered manually.

The workflow:

1. Checks the current repository latest release.
2. Checks the latest upstream `router-for-me/Cli-Proxy-API-Management-Center` release.
3. Reads the management upstream version recorded in the latest release notes.
4. If upstream is newer, the latest release has no `management.html`, or the workflow was triggered manually, checks out the latest upstream release tag.
5. Applies this customization layer from `cliproxyapi-pro-management/apply.sh`.
6. Runs `npm ci` and `npm run build`.
7. Renames `dist/index.html` to `management.html`.
8. Uploads and clobbers `management.html` on the current latest release.
9. Updates the management version mapping and upstream release notes in the release notes.
10. Deletes old workflow runs.

This keeps `remote-management.panel-github-repository=https://github.com/ssfun/CLIProxyAPI-Pro` able to fetch the latest `management.html` through GitHub `/releases/latest`.

## Backend expectations

These frontend customizations expect the customized `cliproxyapi-pro-core` backend to expose usage and account-inspection routes under the management API prefix:

- `/v0/management/usage`
- `/v0/management/usage/status`
- `/v0/management/usage/events`
- `/v0/management/usage/aggregates`
- `/v0/management/usage/stream`
- `/v0/management/usage/export`
- `/v0/management/usage/import`
- `/v0/management/usage/reset`
- `/v0/management/usage/quota-cache`
- `/v0/management/usage/model-prices`
- `/v0/management/usage/settings`
- `/v0/management/account-inspection/schedule`
- `/v0/management/account-inspection/status`
- `/v0/management/account-inspection/logs`
- `/v0/management/account-inspection/run`
- `/v0/management/account-inspection/pause`
- `/v0/management/account-inspection/resume`
- `/v0/management/account-inspection/stop`
- `/v0/management/account-inspection/actions`

Without the customized backend, monitoring, SQLite-backed persistence, model prices, and backend account inspection will show errors or empty data.
