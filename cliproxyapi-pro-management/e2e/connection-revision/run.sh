#!/usr/bin/env bash
set -euo pipefail
# Run against a clean upstream v1.27.2 checkout; logs and hashes are the artifact.
repo=$(cd "$(dirname "$0")/../.." && pwd)
target=${1:?usage: run.sh UPSTREAM_CHECKOUT [ARTIFACT_DIR]}
artifact=${2:-/tmp/management-connection-revision-artifacts}
mkdir -p "$artifact"
target=$(cd "$target" && pwd)
artifact=$(cd "$artifact" && pwd)
test -z "$(git -C "$target" status --porcelain)"
expected=f154fa9380825c4fe94bc577a9abaa7e505f8875
test "$(git -C "$target" rev-parse HEAD)" = "$expected"
printf '%s\n' "$expected" > "$artifact/upstream-sha.txt"
python3 "$repo/apply_customizations.py" "$target" 2>&1 | tee "$artifact/customization.log"
python3 -m unittest discover -s "$repo/tests" -p 'test_api_client_connection_isolation_customization.py' 2>&1 | tee "$artifact/generator.log"
python3 "$repo/../scripts/validation/check_patch_surface.py" "$target" "$repo/../scripts/validation/contracts/management-upstream-modified-files.txt" 2>&1 | tee "$artifact/surface.log"
git -C "$target" diff --check
git -C "$target" add -N .
before=$(git -C "$target" diff --binary | git hash-object --stdin)
python3 "$repo/apply_customizations.py" "$target" 2>&1 | tee "$artifact/reapply.log"
after=$(git -C "$target" diff --binary | git hash-object --stdin)
test "$before" = "$after"
printf 'before=%s\nafter=%s\n' "$before" "$after" > "$artifact/idempotence.log"
cd "$target"
bun install --frozen-lockfile --cache-dir /tmp/bun-cache-management-revision 2>&1 | tee "$artifact/install.log"
bun test tests/apiClientConnectionIsolation.test.ts tests/proManagementTransport.test.ts tests/clientRequestMetadata.test.ts 2>&1 | tee "$artifact/transport.log"
printf 'AbortSignal.any = undefined;\n' > "$artifact/no-signal-any.ts"
bun test --preload "$artifact/no-signal-any.ts" tests/apiClientConnectionIsolation.test.ts 2>&1 | tee "$artifact/signal-fallback.log"
bun run test 2>&1 | tee "$artifact/all-tests.log"
bun run lint 2>&1 | tee "$artifact/lint.log"
bun run type-check 2>&1 | tee "$artifact/type-check.log"
bun run build 2>&1 | tee "$artifact/build.log"
shasum -a 256 src/services/api/client.ts dist/index.html > "$artifact/SHA256SUMS"
