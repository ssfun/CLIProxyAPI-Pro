# Backup size limit HTTP E2E

Failure cases specified before implementation:

1. A valid JSONL backup above the old 96 MiB upload / 64 MiB WebDAV limits must preview and restore with the new 256 MiB default.
2. A positive `PRO_BACKUP_MAX_BYTES` must apply to data-management upload, WebDAV, and legacy usage preview/restore. Exactly the limit is accepted; one byte more returns HTTP 413 with the byte limit and environment variable in the error.
3. Chunked uploads and WebDAV responses without Content-Length must not bypass the limit. A rejected restore must not import its model-price record.
4. Missing, empty, zero, negative, nonnumeric, or overflowing configuration must fall back to 268435456 bytes, never unlimited. A larger positive value must be honored after restart.
5. Encrypted exports must still preview/restore with a passphrase; an oversized encrypted envelope gets the same 413. Invalid manifests and wrong passphrases remain 400; WebDAV upstream errors remain 502.
6. Oversized rejection must leave the service usable for a subsequent valid restore. Startup restoration uses the tested data-management upload endpoint.

Build a clean upstream checkout with the current patch generator, then run:

```sh
python3 cliproxyapi-pro-core/e2e/backup-size-limit/run.py \
  --binary /absolute/path/to/cli-proxy-api \
  --output /private/tmp/backup-size-limit-e2e
```

Only disposable local Core instances and a local WebDAV fixture are used. The script creates a new run directory under `--output`, retaining `result.json`, server logs, config, and SQLite files. The receipt includes the binary and script SHA256, fixture digests, expected/actual statuses, and persistence checks. The large case contains a real model-price record and legal whitespace padding to exercise total bytes without millions of records; it does not benchmark production-scale parsing or memory usage. No full repository E2E suite is required.
