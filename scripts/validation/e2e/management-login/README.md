# Management v1.27.0 login compatibility acceptance

Exact upstream: `4e4aeff23f9f82b3ccdbe48b2e774d5dc918234e`.

Run `bash scripts/validation/management.sh /path/to/clean-upstream` and retain
its output. This checks the patch surface, repeat application, existing tests,
lint, types, and production build. Serve that build with
`bun run preview --host 127.0.0.1 --port 4179` in the upstream directory.

Run `ego-browser nodejs < scripts/validation/e2e/management-login/check.mjs`.
Set `spaceId` in a temporary copy when resuming an existing browser task; edit
`url` and `output` if needed. The runner writes `receipt.json` and desktop,
mobile, and public usage page snapshots to `/tmp/management-login-e2e`.
It checks a unique login link, empty management-key validation, anonymous
navigation and reload of `/usage`, and an actionable link at 390px width.
No real credentials or backend writes are needed. This does not validate
management authentication success or usage accounting.

Compatibility changes retain the extracted upstream LoginForm, preserve native
Select dropdown styling, and add only the Pro trigger styling extension.
Turkish Pro copy currently uses explicit English resources, except the translated
usage entry and upstream show/hide-key labels. It is not a complete Turkish
translation. Existing translated language bundles remain unchanged.
