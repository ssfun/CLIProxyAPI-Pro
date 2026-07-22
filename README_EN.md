# CLIProxyAPI Pro

CLIProxyAPI Pro is a minimal customization-layer collection for two upstream projects:

- `cliproxyapi-pro-core/` — backend Docker build customization for `router-for-me/CLIProxyAPI`.
- `cliproxyapi-pro-management/` — frontend management-center customization for `router-for-me/Cli-Proxy-API-Management-Center`.

This project does not maintain a full fork of either upstream project. Instead, it keeps repeatable patches, overlays, and build workflows. Release workflows fetch the latest upstream release, apply the Pro customization layer, and publish the resulting artifacts.

## Repository layout

```text
.
├── cliproxyapi-pro-core/
│   ├── Dockerfile
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
└── .github/workflows/
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
- Supports usage JSONL/NDJSON import and export, including usage events, model prices, quota cache, account-inspection schedules, and the latest inspection-result snapshot.
- Supports WebDAV usage backup restore.
- Supports SQLite-backed quota cache.
- Supports model price persistence.
- Forces required upstream startup config: `usage-statistics-enabled=true` and the Pro management panel repository.
- Adds a backend account-inspection scheduler and executor with token refresh before probing.
- Optionally starts the Komari agent.
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

## Backend and frontend relationship

Some `cliproxyapi-pro-management` features depend on enhanced management APIs provided by `cliproxyapi-pro-core`.

Core dependent routes:

```text
/v0/management/usage
/v0/management/usage/status
/v0/management/usage/events
/v0/management/usage/aggregates
/v0/management/usage/stream
/v0/management/usage/export
/v0/management/usage/import
/v0/management/usage/quota-cache
/v0/management/usage/model-prices
/v0/management/usage/settings
/v0/management/account-inspection/schedule
/v0/management/account-inspection/status
/v0/management/account-inspection/logs
/v0/management/account-inspection/run
/v0/management/account-inspection/pause
/v0/management/account-inspection/resume
/v0/management/account-inspection/stop
/v0/management/account-inspection/actions
```

Request monitoring stores diagnostic fields such as TTFT, HTTP status code, structured error, reasoning effort, and service tier, and exposes the `/usage/aggregates` server-side aggregation API. The management UI deduplicates increments by event ID, receives SSE updates from SQLite commit notifications, catches up by cursor after disconnects, prefers server-side aggregates for trends and rankings, uses stable server-side paging for log filters and combined raw-text/account-metadata search, and pauses live rendering in background tabs. `/usage/status` returns recent dead-letter samples with sensitive fields redacted.

Account inspection is executed by the backend only. The management UI configures schedules, starts or controls runs, polls status/progress/results, streams logs and live status over WebSocket/WSS, and confirms manual actions. Backend automatic actions support consecutive-confirmation gating, and quota cache entries record parser version plus response-shape hashes to help diagnose upstream field changes.

During backend inspection, eligible auth records are refreshed before quota/account probing when they are already in their normal refresh window. The inspection refresh path skips API-key accounts, accounts not yet due for refresh, and accounts still blocked by `NextRefreshAfter`; disabled accounts are allowed to refresh. If refresh succeeds, probing uses the refreshed auth. If refresh fails, the account is kept and probing is skipped for that account.

The backend forces `usage-statistics-enabled=true` and `remote-management.panel-github-repository=https://github.com/ssfun/CLIProxyAPI-Pro` at startup, then writes those values back to `config.yaml` only when the loaded config differs.

If the management UI is used with the unmodified upstream backend, request monitoring, SQLite persistence, model prices, and backend account inspection will show errors or empty data.

## Release workflows

### Unified Pro release

Workflow:

```text
.github/workflows/release-core.yml
```

The GitHub Release version is based on the upstream core version with a `-pro` suffix.

Example:

```text
v7.1.18-pro
```

Overview:

1. Checks the latest upstream `router-for-me/CLIProxyAPI` release.
2. Computes the Pro release tag, for example `v7.1.18-pro`.
3. Checks out the latest upstream core and upstream management releases.
4. Applies core patches, then builds and pushes the Docker image.
5. Builds Pro binary assets: default desktop/Linux archives enable CGO for dynamic-library plugin support, while `_no-plugin` archives remain CGO-free portable builds.
6. Applies the management customization layer and builds the single-file `management.html`.
7. Creates or updates the current repository GitHub Release, then uploads binaries, `checksums.txt`, and `management.html`.
8. Includes both core upstream and management upstream version mapping and release notes in the release notes.
9. Runs WebDAV usage backup, Render deployment hooks, Telegram notification, and old workflow-run cleanup.

Docker image tags use the Pro release tag:

```text
latest
v7.1.18-pro
```

During Docker builds, `CLIPROXY_VERSION` selects the upstream core tag to download, while `CLIPROXY_BUILD_VERSION` sets the runtime version. This lets the image and binary report `v7.1.18-pro` while still building from upstream `v7.1.18` source.

Binary asset platforms and archive formats match upstream CLIProxyAPI. The version already carries the Pro release tag, so the asset prefix remains `CLIProxyAPI`. Default desktop/Linux archives support dynamic-library plugins; `_no-plugin` archives are for static or constrained environments. Docker images follow upstream with CGO-enabled Debian builds and dynamic-library plugin support:

```text
CLIProxyAPI_7.1.18-pro_linux_amd64.tar.gz
CLIProxyAPI_7.1.18-pro_linux_aarch64.tar.gz
CLIProxyAPI_7.1.18-pro_linux_amd64_no-plugin.tar.gz
CLIProxyAPI_7.1.18-pro_linux_aarch64_no-plugin.tar.gz
CLIProxyAPI_7.1.18-pro_darwin_amd64.tar.gz
CLIProxyAPI_7.1.18-pro_darwin_aarch64.tar.gz
CLIProxyAPI_7.1.18-pro_freebsd_amd64.tar.gz
CLIProxyAPI_7.1.18-pro_freebsd_amd64_no-plugin.tar.gz
CLIProxyAPI_7.1.18-pro_freebsd_aarch64_no-plugin.tar.gz
CLIProxyAPI_7.1.18-pro_windows_amd64.zip
CLIProxyAPI_7.1.18-pro_windows_aarch64.zip
checksums.txt
management.html
```

The binary archives include this repository's `README.md` and `README_EN.md`.

### Management asset update

Workflow:

```text
.github/workflows/release-management.yml
```

This workflow no longer creates a separate release. It only rebuilds `management.html` when the management upstream changes, then uploads it to the current repository latest release.

Overview:

1. Checks the latest upstream `router-for-me/Cli-Proxy-API-Management-Center` release.
2. Reads the management upstream version recorded in the current repository latest release notes.
3. If management upstream is newer, or the latest release has no `management.html`, checks out the latest management upstream release.
4. Applies the `cliproxyapi-pro-management` customization layer.
5. Runs `npm ci` and `npm run build`.
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
docker build -t cliproxyapi-pro ./cliproxyapi-pro-core
```

Build a specific upstream release:

```bash
docker build \
  --build-arg CLIPROXY_VERSION=v7.1.18 \
  --build-arg CLIPROXY_BUILD_VERSION=v7.1.18-pro \
  -t cliproxyapi-pro:v7.1.18-pro \
  ./cliproxyapi-pro-core
```

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
npm install
npm run type-check
npm run build
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

Usage export/import uses NDJSON metadata records for model prices, quota cache, monitoring settings, the account-inspection schedule, and the latest finished inspection-result snapshot, so WebDAV backup restore can recover the monitoring-related state together with usage events. Restored inspection snapshots are read-only for migration and troubleshooting; a new full inspection must run before rechecking accounts, refreshing tokens, or changing account state. Inspection logs are not included. Monitoring log retention runs daily at 02:00 server local time and also runs once immediately when settings are saved; WebDAV backups can use separate retention days, deleting expired `usage-export-*.jsonl` files after successful backups.

New exports include an integrity manifest. Import and automatic WebDAV restore reject manifest-free legacy backups by default; set `USAGE_ALLOW_LEGACY_RESTORE=true` only when one trusted legacy backup must be restored automatically.

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
USAGE_ALLOW_LEGACY_RESTORE
```

### Account inspection

```text
ACCOUNT_INSPECTION_SCHEDULE_PATH
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
