# CLIProxyAPI Pro

CLIProxyAPI Pro is a minimal customization-layer collection for two upstream projects:

- `cliproxyapi-pro-core/` — backend Docker build customization for `router-for-me/CLIProxyAPI`.
- `cliproxyapi-pro-management/` — frontend management-center customization for `router-for-me/Cli-Proxy-API-Management-Center`.

This project does not maintain a full fork of either upstream project. Instead, it keeps repeatable patches, overlays, and build workflows. Release workflows fetch the latest upstream release, apply the Pro customization layer, and publish the resulting artifacts.

## Key features

- Persistent request data with import, export, and WebDAV backup.
- Account inspection for Codex, Claude, Antigravity, Gemini CLI, Kimi, and xAI.
- Persistent inspection quota and account-error state for quota management and auth-file health views.
- Optional automatic enable, disable, delete, and token-refresh actions.
- Optional deep probes for Antigravity soft bans and xAI availability anomalies.
- A routing-policy page for upstream routing behavior and provider-scoped request-state protection.
- A bundled dynamic proxy-pool plugin that aggregates HTTP/SOCKS nodes behind one fixed loopback SOCKS5 endpoint with rotation, health isolation, and failover.
- A bundled OAuth model-policy plugin that removes unavailable models per provider and account plan, constraining both model listing and auth scheduling.

## Repository layout

```text
.
├── cliproxyapi-pro-core/
│   ├── Dockerfile
│   ├── Dockerfile.runtime
│   ├── QUOTA_PROVIDER.md
│   ├── entrypoint.sh
│   ├── embeddedusage/
│   └── patches/
│
├── cliproxyapi-pro-management/
│   ├── apply.sh
│   ├── apply_customizations.py
│   ├── monitoring-locales.json
│   └── overlay/
│
├── cliproxyapi-pro-plugins/
│   ├── proxy-pool/
│   └── oauth-model-policy/
│
├── scripts/validation/
└── .github/workflows/
    ├── ci.yml
    ├── release-core.yml
    └── release-management.yml
```

## Subprojects

### cliproxyapi-pro-core

Backend customization layer for building the Pro Docker image.

Main capabilities:

- Builds a multi-arch Docker image from an upstream CLIProxyAPI release.
- Builds Pro binary release assets using the same platform matrix and archive formats as upstream.
- Embeds a SQLite usage service.
- Exposes `/v0/management/usage` API routes, including status, incremental event polling, and SSE streaming.
- Supports usage JSONL/NDJSON import and export, including usage events, model prices, quota cache, Pro settings, routing runtime state, account-inspection schedules, and the latest inspection-result snapshot.
- Supports WebDAV usage backup restore.
- Supports SQLite-backed quota cache.
- Supports model price persistence.
- Supports the QuotaProvider plugin protocol and a Gemini CLI legacy adapter.
- Supports a generic `AuthModelFilter` plugin protocol; the bundled `oauth-model-policy` filters OAuth models by account plan for xAI, Codex, Claude, Gemini CLI, Antigravity, and Kimi.
- Forces required upstream startup config: `usage-statistics-enabled=true` and the Pro management panel repository.
- Adds a backend account-inspection scheduler and executor with token refresh before probing.
- Adds unified routing-policy and request-state-protection APIs.
- Optionally starts the Komari agent.
- Prebundles the `proxy-pool` and `oauth-model-policy` dynamic libraries in standard macOS, Windows amd64, and Linux releases plus Docker images; Windows ARM64, FreeBSD, and `_no-plugin` assets do not currently bundle dynamic plugins.
- Redirects `/` to `/management.html`.
- Enhances the `/healthz` response.

See:

- `cliproxyapi-pro-core/README.md`
- `cliproxyapi-pro-core/README_EN.md`

### cliproxyapi-pro-management

Frontend management-center customization layer for generating the single-file `management.html` artifact.

Main capabilities:

- Adds the `/monitoring` request monitoring page.
- Adds the `/account-inspection` account inspection page.
- Adds the `/routing` routing-policy page.
- Adds the `/proxy-pool` page for node configuration, connectivity tests, runtime statistics, and global-proxy takeover/restoration.
- Adds the `/oauth-model-policy` visual editor for provider-specific OAuth plan rules, custom plans, fallback policies, and plan-discovery caching.
- Shows request count, success rate, latency, token, and cost metrics.
- Persists model prices through SQLite.
- Persists quota cache through SQLite.
- Shows quota-card cache timestamps and supports single-card refresh.
- Integrates with backend account inspection for run control, polling, results, and actions.
- Shows inspection-written `last_error` messages on the auth files page.
- Shows business-result toast messages for account-inspection refresh and recheck actions.
- Supports suggested account disable, enable, and delete actions.
- Adds locale patches.
- Uses a minimal overlay + patch application flow.

See:

- `cliproxyapi-pro-management/README.md`
- `cliproxyapi-pro-management/README_EN.md`

## Interface preview

### Request monitoring overview

![Request monitoring overview](assets/01.png)

### Complete request monitoring view

![Complete request monitoring view](assets/02.png)

### Account inspection overview

![Account inspection overview](assets/03.png)

### Account inspection settings

![Account inspection settings](assets/04.png)

### Inspection policy details

![Inspection policy details](assets/05.png)

## Backend and frontend relationship

Some `cliproxyapi-pro-management` features depend on enhanced management APIs provided by `cliproxyapi-pro-core`.

Core dependencies are grouped under these stable prefixes:

```text
/v0/management/usage
/v0/management/usage/*
/v0/management/quota/fetch
/v0/management/account-inspection/*
/v0/management/routing-policy
/v0/management/routing-policy/*
```

See `cliproxyapi-pro-core/README_EN.md` for the complete method/path list.

Request monitoring stores diagnostic fields such as TTFT, HTTP status code, structured error, reasoning effort, and service tier, and exposes the `/usage/aggregates` server-side aggregation API. The management UI deduplicates increments by event ID, receives SSE updates from SQLite commit notifications, catches up by cursor after disconnects, prefers server-side aggregates for trends and rankings, uses stable server-side paging for log filters and combined raw-text/account-metadata search, and pauses live rendering in background tabs. `/usage/status` returns recent dead-letter samples with sensitive fields redacted.

Account inspection is executed by the backend only. The management UI configures schedules, starts or controls runs, polls status/progress/results, streams logs and live status over WebSocket/WSS, and confirms manual actions. Backend automatic actions support consecutive-confirmation gating, and quota cache entries record parser version plus response-shape hashes to help diagnose upstream field changes.

The top-level Routing Policy page combines upstream routing, session stickiness, retry, account switching, cooldown, and quota-fallback settings with request-state protection for Antigravity, xAI, Codex, Gemini CLI, Gemini, Gemini Interactions, Vertex AI, AI Studio, Claude, and Kimi. Protection is disabled by default; `observe` records matches and `enforce` can disable accounts. Automatic and manual release affect only accounts owned by this protection policy.

During backend inspection, eligible auth records are refreshed before quota/account probing when they are already in their normal refresh window. The inspection refresh path skips API-key accounts, accounts not yet due for refresh, and accounts still blocked by `NextRefreshAfter`; disabled accounts are allowed to refresh. If refresh succeeds, probing uses the refreshed auth. If refresh fails, the account is kept and probing is skipped for that account.

The backend forces `usage-statistics-enabled=true` and the Pro management-panel repository in memory at startup. It only changes a YAML key when that key already exists and differs; missing keys are never added. Request-protection settings live in `usage.sqlite`, not upstream `config.yaml`.

If the management UI is used with the unmodified upstream backend, request monitoring, SQLite persistence, model prices, backend account inspection, and routing protection will show errors or empty data.

## Release workflows

### Unified Pro release

Workflow:

```text
.github/workflows/release-core.yml
```

The GitHub Release version is based on the upstream core version with a `-pro` suffix.

Example:

```text
v<core-version>-pro
```

Overview:

1. Checks the latest upstream `router-for-me/CLIProxyAPI` release.
2. Computes the Pro release tag, for example `v<core-version>-pro`.
3. Checks out the latest upstream core and upstream management releases.
4. Applies core patches and builds Pro binary assets: default desktop/Linux archives enable CGO for dynamic-library plugin support, while `_no-plugin` archives remain CGO-free portable builds.
5. Reuses the built Linux assets to assemble and push the multi-architecture image through `Dockerfile.runtime`.
6. Applies the management customization layer and builds the single-file `management.html`.
7. Creates or updates the current repository GitHub Release, then uploads binaries, `checksums.txt`, and `management.html`.
8. Includes both core upstream and management upstream version mapping and release notes in the release notes.
9. Runs WebDAV usage backup, Render deployment hooks, Telegram notification, and old workflow-run cleanup.

Docker image tags use the Pro release tag:

```text
latest
v<core-version>-pro
```

During Docker builds, `CLIPROXY_VERSION` selects the upstream core tag, while `CLIPROXY_BUILD_VERSION` sets the Pro runtime version.

Binary asset platforms and archive formats match upstream CLIProxyAPI. The version already carries the Pro release tag, so the asset prefix remains `CLIProxyAPI`. Default desktop/Linux archives support dynamic-library plugins; `_no-plugin` archives are for static or constrained environments. Docker images follow upstream with CGO-enabled Debian builds and dynamic-library plugin support:

```text
CLIProxyAPI_<core-version>-pro_<os>_<arch>.<archive>
CLIProxyAPI_<core-version>-pro_<os>_<arch>_no-plugin.<archive>
checksums.txt
management.html
```

The binary archives include this repository's `README.md` and `README_EN.md`.

### Management asset update

Workflow:

```text
.github/workflows/release-management.yml
```

This workflow no longer creates a separate release. It rebuilds `management.html` when management upstream changes, when the latest release is missing the asset, or when manually triggered, then uploads it to the current repository latest release.

Overview:

1. Checks the latest upstream `router-for-me/Cli-Proxy-API-Management-Center` release.
2. Reads the management upstream version recorded in the current repository latest release notes.
3. If management upstream is newer, or the latest release has no `management.html`, checks out the latest management upstream release.
4. Applies the `cliproxyapi-pro-management` customization layer.
5. Runs `bun install --frozen-lockfile` and `bun run build`; the Bun version comes from upstream `package.json`.
6. Renames `dist/index.html` to `management.html`.
7. Uploads and clobbers `management.html` on the current latest release.
8. Updates the management version mapping and release notes section.

This keeps `remote-management.panel-github-repository=https://github.com/ssfun/CLIProxyAPI-Pro` compatible with GitHub `/releases/latest`, because the latest release always carries `management.html`.

## Local build

### Build the core Docker image

Published image:

```bash
docker pull sfun/cliproxyapi-pro:latest
```

Build locally:

```bash
docker build -t cliproxyapi-pro -f cliproxyapi-pro-core/Dockerfile .
```

Build a specific upstream release:

```bash
UPSTREAM_TAG=vX.Y.Z
PRO_TAG="${UPSTREAM_TAG}-pro"
docker build \
  --build-arg CLIPROXY_VERSION="${UPSTREAM_TAG}" \
  --build-arg CLIPROXY_BUILD_VERSION="${PRO_TAG}" \
  -t "cliproxyapi-pro:${PRO_TAG}" \
  ./cliproxyapi-pro-core
```

The Dockerfiles pin builder and runtime base images by immutable digest. Debian package repositories and external toolchains remain rolling dependencies, so the project does not claim a cross-time byte-for-byte guarantee for complete images. For a deterministic local source binary, also pass an immutable `CLIPROXY_COMMIT` and a fixed `SOURCE_DATE_EPOCH`; release workflows derive that epoch from immutable source commits and normalize all core archives.

### Apply the management customization layer

```bash
./cliproxyapi-pro-management/apply.sh /path/to/Cli-Proxy-API-Management-Center
```

Or:

```bash
python3 ./cliproxyapi-pro-management/apply_customizations.py /path/to/Cli-Proxy-API-Management-Center
```

The target must be an upstream management-center checkout containing:

- `src/`
- `package.json`

After applying customizations, run in the target directory:

```bash
bun install --frozen-lockfile
bun run test
bun run lint
bun run type-check
VERSION=review bun run build
```

## Runtime data directory

The core image uses this directory by default:

```text
/CLIProxyAPI/usage
```

It stores:

- usage SQLite database: `usage.sqlite`
- account-inspection schedule file: `account-inspection-schedule.json`
- latest account-inspection result snapshot: `account-inspection-snapshot.json`
- quota cache
- model prices
- monitoring settings
- Pro settings

Usage export/import uses NDJSON metadata records for model prices, quota cache, monitoring settings, Pro settings, the account-inspection schedule, and the latest finished inspection-result snapshot, so WebDAV backup restore can recover the monitoring-related state together with usage events. Restored inspection snapshots are read-only for migration and troubleshooting; a new full inspection must run before rechecking accounts, refreshing tokens, or changing account state. Inspection logs are not included. Monitoring log retention runs daily at 02:00 server local time and also runs once immediately when settings are saved; WebDAV backups can use separate retention days, deleting expired `usage-export-*.jsonl` files after successful backups.

New exports include an integrity manifest. Management API and UI imports reject or require confirmation for manifest-free legacy backups; during the compatibility transition, Docker WebDAV restore force-enables legacy import while continuing to verify manifest-backed backups strictly.

Configure a persistent volume for this directory in production.

## Key environment variables

### Usage service

```text
USAGE_SERVICE_ENABLED
USAGE_DATA_DIR
USAGE_DB_PATH
USAGE_BATCH_SIZE
USAGE_POLL_INTERVAL_MS
USAGE_QUERY_LIMIT
```

### WebDAV restore

```text
WEBDAV_URL
WEBDAV_USERNAME
WEBDAV_PASSWORD
MANAGEMENT_PASSWORD
```

### Account inspection

```text
ACCOUNT_INSPECTION_SCHEDULE_PATH
ACCOUNT_INSPECTION_SNAPSHOT_PATH
```

### Komari agent

```text
KOMARI_SERVER
KOMARI_SECRET
```

For full details, see `cliproxyapi-pro-core/README.md`.

## Design principles

This project follows a minimal customization approach:

- Do not vendor full upstream source code.
- Prefer overlays and patches for customization.
- Reapply the customization layer when upstream updates.
- Keep documentation, scripts, and workflows verifiable and repeatable.

## Copyright and acknowledgements

This repository is a customization layer and release workflow for upstream projects. It does not claim ownership of upstream code, names, or assets. Upstream code and artifacts retain their original copyright notices and licenses.

- `router-for-me/CLIProxyAPI` is licensed under the MIT License. Its upstream `LICENSE` currently states:
  - Copyright (c) 2025-2005.9 Luis Pater
  - Copyright (c) 2025.9-present Router-For.ME
- `router-for-me/Cli-Proxy-API-Management-Center` is licensed under the MIT License. Its upstream `LICENSE` currently states:
  - Copyright (c) 2026 Router-For.ME

Special thanks to:

- [router-for-me/CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) — the upstream backend project this core customization layer builds on.
- [router-for-me/Cli-Proxy-API-Management-Center](https://github.com/router-for-me/Cli-Proxy-API-Management-Center) — the upstream management UI this frontend customization layer builds on.
- [seakee/CPA-Manager](https://github.com/seakee/CPA-Manager) — an important CLIProxyAPI management and monitoring project that inspired the Pro usage, monitoring, and account-inspection direction.
- Thanks to the [Linux.do](https://linux.do/) community for project promotion and feedback.

## Documentation

- Core English README: `cliproxyapi-pro-core/README_EN.md`
- Core Chinese README: `cliproxyapi-pro-core/README.md`
- Management English README: `cliproxyapi-pro-management/README_EN.md`
- Management Chinese README: `cliproxyapi-pro-management/README.md`
- Chinese project overview: `README.md`
