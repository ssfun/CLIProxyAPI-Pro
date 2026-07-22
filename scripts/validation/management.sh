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
git -C "${upstream_root}" diff --check

(
  cd "${upstream_root}"
  bun install --frozen-lockfile
  bun run test
  bun run lint
  bun run type-check
  VERSION="${VERSION:-review}" bun run build
)
