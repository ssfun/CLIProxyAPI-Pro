#!/usr/bin/env bash
set -euo pipefail
# Run against a clean upstream v1.27.2 checkout; logs and hashes are the artifact.
invocation=("$0" "$@")
repo=$(cd "$(dirname "$0")/../.." && pwd)
target=${1:?usage: run.sh UPSTREAM_CHECKOUT [ARTIFACT_DIR]}
artifact=${2:-/tmp/management-connection-revision-artifacts}
mkdir -p "$artifact"
target=$(cd "$target" && pwd)
artifact=$(cd "$artifact" && pwd)
test -z "$(git -C "$target" status --porcelain)"
expected=f154fa9380825c4fe94bc577a9abaa7e505f8875
test "$(git -C "$target" rev-parse HEAD)" = "$expected"
provenance="$repo/e2e/connection-revision/provenance.py"
python3 "$provenance" capture "$artifact" "$repo" "$target" "${invocation[@]}"
run() {
  python3 "$provenance" record "$artifact" "$@"
  "$@"
}
printf '%s\n' "$expected" > "$artifact/upstream-sha.txt"
run python3 "$repo/apply_customizations.py" "$target" 2>&1 | tee "$artifact/customization.log"
run python3 -m unittest discover -s "$repo/tests" -p 'test_api_client_connection_isolation_customization.py' 2>&1 | tee "$artifact/generator.log"
run python3 "$repo/../scripts/validation/check_patch_surface.py" "$target" "$repo/../scripts/validation/contracts/management-upstream-modified-files.txt" 2>&1 | tee "$artifact/surface.log"
run git -C "$target" diff --check
run git -C "$target" add -N .
python3 "$provenance" record "$artifact" git hash-object --stdin
before=$(run git -C "$target" diff --binary | git hash-object --stdin)
run python3 "$repo/apply_customizations.py" "$target" 2>&1 | tee "$artifact/reapply.log"
python3 "$provenance" record "$artifact" git hash-object --stdin
after=$(run git -C "$target" diff --binary | git hash-object --stdin)
test "$before" = "$after"
printf 'before=%s\nafter=%s\n' "$before" "$after" > "$artifact/idempotence.log"
cd "$target"
run bun install --frozen-lockfile --cache-dir /tmp/bun-cache-management-revision 2>&1 | tee "$artifact/install.log"
run bun test tests/apiClientConnectionIsolation.test.ts tests/proManagementTransport.test.ts tests/clientRequestMetadata.test.ts 2>&1 | tee "$artifact/transport.log"
run bun test --preload ./tests/disableAbortSignalAny.ts tests/apiClientConnectionIsolation.test.ts 2>&1 | tee "$artifact/signal-fallback.log"
run bun run test 2>&1 | tee "$artifact/all-tests.log"
run bun run lint 2>&1 | tee "$artifact/lint.log"
run bun run type-check 2>&1 | tee "$artifact/type-check.log"
run bun run build 2>&1 | tee "$artifact/build.log"
run shasum -a 256 src/services/api/client.ts dist/index.html > "$artifact/SHA256SUMS"
