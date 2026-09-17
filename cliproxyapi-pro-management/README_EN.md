# CLIProxyAPI Pro Management customizations

Customization layer for upstream `router-for-me/Cli-Proxy-API-Management-Center`.

This directory does not vendor the upstream application. It keeps overlay files plus a patch script that can be applied to a clean upstream checkout during local development or GitHub Actions release builds.

## What this customization adds

### Proxy pool page

Adds a top-level `/proxy-pool` page for Core's statically linked proxy pool. It manages HTTP/HTTPS/SOCKS5/SOCKS5H nodes, selection strategy, weights, health checks, isolation, and failover. It also supports batch paste, node search, filtered enable/disable, quick duplication, unsaved-draft tests, and manual isolation recovery. Runtime details include success rate, failures, active tunnels, last success/failure, config generation, and health-cycle timestamps.

Adds a top-level `/oauth-policy` page for Core's statically linked OAuth account policy. Provider tabs edit plan-specific model exclusions, prefixes, priorities, and scheduler weights for xAI, Codex, Claude, Gemini CLI, Antigravity, and Kimi, including custom plans, `_unknown`/`_default` fallbacks, and effective runtime previews. The legacy `/oauth-model-policy` route redirects to the new page.

The page toggles runtime proxy takeover through native management APIs without changing `config.yaml` or the root `proxy-url`. Credentials with their own `proxy-url` are listed explicitly as bypasses. Both feature configurations persist in the usage SQLite `pro_settings` table, while health state and connection counters remain process-local runtime data.

Rotation is per SOCKS5 TCP tunnel, not necessarily per multiplexed HTTP request. `fail-open=false` is the default to prevent silent direct traffic leakage.

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

Auth-file account cards also expose a connection test. The dialog reuses upstream `GET /v0/management/auth-files/models`; when upstream no longer registers models for a disabled credential, the Pro backend makes that endpoint fall back to the matching provider's static definitions. It then calls `POST /v0/management/auth-files/test` with the exact `auth_index`. The dialog shows model output and latency on success, or the upstream HTTP status, error code, and masked failure details on failure. Disabled and cooling-down credentials can be tested without changing the operator-controlled disabled switch or creating request-monitoring records.

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

Rules apply globally by model ID, so the same model shares one rule across providers. They support input, output, cache-read, cache-write, multiple context-size tiers, and service-tier overrides. Prices can be synchronized manually or periodically from models.dev; only models observed in request history are persisted, and locked manual rules are not overwritten.

The backend selects pricing per request and snapshots the estimated cost on each usage event. The cost breakdown `pricingMode` distinguishes base pricing, context tiers, and a service-tier override that actually matched, while the UI shows requested, effective, and matched pricing tiers separately. OpenAI Fast rules are stored as `fast` and accept `priority` in requests or responses as a compatibility alias; merely recording a requested `service_tier` does not mean an override was applied. Aggregate APIs sum those event costs. JSONL export/import preserves both complete rules and cost snapshots.

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
- Gemini CLI
- Kimi
- xAI

Quota cards also show cache timestamps and support single-card refresh when the feature flags in `src/pro/modules/quota/features.ts` are enabled.

### Account inspection page

Adds a top-level account inspection route:

```text
/account-inspection
```

The page controls and displays backend-run inspections. The browser does not execute probes directly. The auth files page also shows inspection-written `last_error` health messages when no explicit status message exists. The backend can inspect:

- Antigravity
- Claude
- Codex
- Gemini CLI
- Kimi
- xAI

Features include:

- target provider selection
- configurable global probe concurrency `workers` (`1–8`), per-provider concurrency `providerWorkers` (`1–4`), action concurrency `deleteWorkers` (`1–4`), timeout, retries, used-percent threshold, and sample size
- backend run, pause, resume, and stop controls
- backend schedule enablement and interval configuration
- progress, summary cards, and result table from backend status polling
- logs and live status from the backend WebSocket/WSS stream
- suggested actions: keep, delete, disable, enable
- manual execution for a single planned action or all planned actions through the backend
- business-result toast messages for single-account rechecks, such as account errors, quota exhaustion, or healthy state
- optional backend auto-execution policies for quota-limit disable, quota-recovery enable, and account-error disable/delete
- quota snapshot refresh from backend inspection results

Global probe concurrency applies across all providers, while per-provider concurrency independently caps each provider within that global limit. Regular probes, deep probes, xAI probes, and pre-probe token refreshes use the same limits. Action concurrency applies to both automatic actions and manual bulk actions. The page persists these values through the account-inspection schedule API and neither reads nor modifies `config.yaml`.

Backend schedule/status/control routes expected by the page:

- `GET /account-inspection/schedule`
- `GET /account-inspection/status`
- `GET /account-inspection/logs` (WebSocket/WSS log and status stream)
- `PUT|PATCH /account-inspection/schedule`
- `POST /account-inspection/run`
- `POST /account-inspection/inspect-one`
- `POST /account-inspection/pause`
- `POST /account-inspection/resume`
- `POST /account-inspection/stop`
- `POST /account-inspection/actions`

Under the full management API prefix these are exposed by the backend as `/v0/management/account-inspection/...`.

### Scheduling board page

Adds a top-level scheduling-board route:

```text
/routing
```

The page is a read-only view of accounts the selector will not pick right now. It never reads or edits global routing values in `config.yaml`. Buckets cover quota, auth/transient failures, pending recheck, and overlap. The only action is jumping to Account Inspection; upstream cooldowns are not cleared from this page.

The page uses:

- `GET /routing-policy`

### Supporting API and type patches

`apply_customizations.py` also patches upstream files to add:

- `/monitoring`, `/account-inspection`, and `/routing` routes.
- sidebar navigation labels and icon.
- locale entries from `monitoring-locales.json`.
- the `usageStatisticsEnabled` config type used by monitoring; account-inspection settings are managed only through the schedule API.
- `authFilesApi.patchFile` and `setStatusWithFallback` helpers.
- `accountInspection` service export.
- `Select` `triggerClassName` and `dropdownClassName` props.
- `maskSensitiveText` utility.
- `cachedAt` fields for quota state types and success states.
- a “Check for updates” action on the Management Center version tile; it calls `POST /management-panel/check-update`, replaces the panel only when the latest-release asset hash changes, and reloads only after an actual update.

Request Monitoring uses an initial snapshot plus SSE increments and cursor catch-up, with event-ID deduplication. Trends, model rankings, and API-key rankings prefer server-side `/usage/aggregates` data and automatically fall back to local detail calculations when unavailable. Hidden tabs pause SSE and React incremental updates, then catch up by cursor when visible again; the page header shows live, reconnecting, background-paused, error, and latest-event states.

## Repository layout

- `overlay/` — files copied directly into the upstream checkout.
- `overlay/src/pro/modules/monitoring/` — request monitoring, usage analytics, and backup UI.
- `overlay/src/pro/modules/inspection/` — account inspection page, state, and actions.
- `overlay/src/pro/modules/routing/` — scheduling board UI.
- `overlay/src/pro/modules/proxyPool/` and `oauthPolicy/` — independent module pages and APIs.
- `overlay/src/pro/modules/quota/` — SQLite quota persistence, sorting, and provider extensions.
- `overlay/src/pro/modules/*/manifest.tsx` — each business module declares its route, navigation, and startup effects; `registry.tsx` keeps only the module list and derives host projections, while `ProBootstrap.tsx` mounts them after authentication.
- `overlay/src/pro/shared/` — domain-neutral shared UI models; business modules depend on another module only through its `index.ts` public surface and never through internal `features/` or style files.
- `overlay/src/services/api/` — added API clients.
- `overlay-replacements.json` — reviewed upstream SHA-256 values and reasons for full-file replacements that intentionally collide with upstream paths.
- `monitoring-locales.json` — locale additions merged into upstream locale files.
- `apply_customizations.py` — applies all customizations to a target upstream checkout.
- `apply.sh` — shell wrapper around `apply_customizations.py`.
Overlay collision preflight validates the upstream side of every reviewed replacement. Upstream file changes must update `overlay-replacements.json` explicitly; local replacements are reviewed through normal PR diffs and behavior tests, and new unreviewed path collisions are rejected before any overlay file is copied.

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
bun install --frozen-lockfile
bun run test
bun run lint
bun run type-check
VERSION=review bun run build
```

Use the repository validation script with a disposable clean upstream checkout to verify overlay preflight, reapplication, tests, lint, type checking, and build:

```bash
bash scripts/validation/management.sh /path/to/disposable/clean-management-checkout
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
6. Runs `bun install --frozen-lockfile` and `bun run build`; the Bun version comes from upstream `package.json`.
7. Renames `dist/index.html` to `management.html`.
8. Uploads and clobbers `management.html` on the current latest release.
9. Updates the management version mapping and upstream release notes in the release notes.
10. Deletes old workflow runs.

This keeps `remote-management.panel-github-repository=https://github.com/ssfun/CLIProxyAPI-Pro` able to fetch the latest `management.html` through GitHub `/releases/latest`.

## Backend expectations

These frontend customizations expect the customized `cliproxyapi-pro-core` backend to expose these stable route groups under the management API prefix:

- `/v0/management/usage`
- `/v0/management/usage/*`
- `/v0/management/quota/fetch`
- `/v0/management/account-inspection/*`
- `/v0/management/routing-policy`
- `/v0/management/routing-policy/*`

See the Core README for the complete method/path list. Without the customized backend, monitoring, SQLite-backed persistence, model prices, backend account inspection, and routing protection will show errors or empty data.
