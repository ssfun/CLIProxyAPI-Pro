#!/usr/bin/env bash
set -euo pipefail

if [[ "$#" -ne 1 ]]; then
  echo "Usage: $0 /path/to/Cli-Proxy-API-Management-Center" >&2
  exit 2
fi

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"
upstream_root="$(cd "$1" && pwd)"

if [[ ! -f "${upstream_root}/package.json" || ! -d "${upstream_root}/src" ]]; then
  echo "Management upstream checkout is invalid: ${upstream_root}" >&2
  exit 1
fi

if [[ -d "${upstream_root}/.git" ]] && [[ -n "$(git -C "${upstream_root}" status --porcelain)" ]]; then
  echo "Management upstream checkout must be clean before validation: ${upstream_root}" >&2
  exit 1
fi

bash "${repo_root}/cliproxyapi-pro-management/apply.sh" "${upstream_root}"
python3 "${repo_root}/scripts/validation/check_patch_surface.py" \
  "${upstream_root}" \
  "${repo_root}/scripts/validation/contracts/management-upstream-modified-files.txt"
git -C "${upstream_root}" diff --check

git -C "${upstream_root}" add -N .
patched_diff_hash="$(git -C "${upstream_root}" diff --binary | git hash-object --stdin)"
bash "${repo_root}/cliproxyapi-pro-management/apply.sh" "${upstream_root}"
reapplied_diff_hash="$(git -C "${upstream_root}" diff --binary | git hash-object --stdin)"
if [[ "${patched_diff_hash}" != "${reapplied_diff_hash}" ]]; then
  echo "Management customization is not idempotent" >&2
  exit 1
fi

(
  cd "${upstream_root}"
  bun install --frozen-lockfile
  bun run test
  bun test --preload ./tests/disableAbortSignalAny.ts tests/apiClientConnectionIsolation.test.ts
  bun run lint
  # The upstream production build runs tsc. Keep an explicit check for builds that omit it.
  if ! bun -e 'process.exit(require("./package.json").scripts.build.split("&&")[0].trim() === "tsc" ? 0 : 1)'; then
    bun run type-check
  fi
  VERSION="${VERSION:-review}" bun run build
)
