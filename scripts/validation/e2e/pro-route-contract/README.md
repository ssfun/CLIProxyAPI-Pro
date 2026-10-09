# Real Core / frontend Pro route regression

This harness runs the generated Management TypeScript against a real Core executable.
It is intentionally independent of mocked Axios responses and source-string tests.

## Run

Requirements: Python 3.11+, Bun 1.3.14, an executable Core release/build, and a
generated patched Management tree with its locked dependencies already installed.
The harness neither downloads software nor modifies the generated frontend.

```sh
python3 scripts/validation/e2e/pro-route-contract/run.py \
  --binary /absolute/path/to/cli-proxy-api \
  --management /absolute/path/to/generated-management \
  --bun /absolute/path/to/bun \
  --output /tmp/pro-route-contract-evidence \
  --require-v8-pro-404
```

The output directory must not exist. Omit `--require-v8-pro-404` when testing a
future Core that adds native aliases; negative-control statuses are still recorded.
The frontend is always required to use the declared namespaces without fallback.

## Assertions

- Native configuration uses `/v8/management/config` and retains the v8 layout
- Native credential inventory uses `/v8/management/credentials`
- Pro credential connection testing uses `/v0/management/auth-files/test`; an
  intentionally empty request receives validation HTTP 400 without a provider call
- Data overview, proxy-pool status, and SQLite quota list/stats/write use their
  declared `/v0/management` routes
- The real shared client supplies bearer authentication and server-version events
- Bare, explicit `/v8/management/`, and old `/v0/management/` connection bases
  normalize correctly while native operations always stay on v8
- A synthetic Codex quota is written by the real SQLite adapter and read back
- A synthetic plugin quota travels from the upstream store through the real middleware,
  HTTP and SQLite; its groups, subscription, summary and timestamps survive restart
- A persisted plugin row missing its required summary is ignored during hydration
- Core terminates gracefully and restarts with the same isolated SQLite database
- A new Bun process starts with an empty real Zustand store and hydrates it via
  the actual persistence middleware; data, revision and generation survive restart
- Hydration does not write the loaded record back to Core

A loopback recording proxy forwards unchanged request paths and response bodies
to Core, then asserts the routes actually observed on the wire. It is only a
recorder, never a backend mock. The frontend's only shim is browser event delivery;
its transport, domain APIs, cache adapter, and middleware are not mocked.

Core uses a fresh synthetic config, empty auth directory, isolated HOME, and
`USAGE_DB_PATH`. It binds loopback only and runs with `-local-model` to avoid model
catalog downloads. No real account or credential is used; provider tests and proxy
probes are never executed. Existing databases and running services are untouched.

## Evidence and failures

`receipt.json` includes binary/client hashes, Bun version, negative-control HTTP
statuses, per-phase assertions, and any failure traceback. `http-transcript.json`
records phase, method, exact path, status, response hash and an authentication
boolean, never the bearer value or config response. Individual phase JSON/logs,
the synthetic config/database and `server.log` are retained in the output directory.
All Core and recorder processes are stopped on success or handled failure.

This checks frontend API transport and restart hydration, not browser rendering,
WebSocket framing, real provider refreshes, or all Pro mutations. Existing unit and
browser tests cover those separate contracts.
