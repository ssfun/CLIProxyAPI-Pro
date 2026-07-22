#!/usr/bin/env bash
set -euo pipefail

if [[ "$#" -ne 1 ]]; then
  echo "Usage: $0 /path/to/CLIProxyAPI" >&2
  exit 2
fi

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"
upstream_root="$(cd "$1" && pwd)"

if [[ ! -f "${upstream_root}/go.mod" || ! -d "${upstream_root}/cmd/server" ]]; then
  echo "Core upstream checkout is invalid: ${upstream_root}" >&2
  exit 1
fi

if [[ -d "${upstream_root}/.git" ]] && [[ -n "$(git -C "${upstream_root}" status --porcelain)" ]]; then
  echo "Core upstream checkout must be clean before validation: ${upstream_root}" >&2
  exit 1
fi

export PYTHONPYCACHEPREFIX="${PYTHONPYCACHEPREFIX:-${TMPDIR:-/tmp}/cliproxyapi-pro-pycache}"
export SRC_ROOT="${upstream_root}"

python3 "${repo_root}/cliproxyapi-pro-core/patches/apply_upstream_patches.py"
git -C "${upstream_root}" diff --check

test_flags=(-count=1)
if [[ "${VALIDATION_RACE:-0}" == "1" ]]; then
  test_flags+=(-race)
fi

go -C "${upstream_root}" test "${test_flags[@]}" ./internal/embeddedusage/...
go -C "${upstream_root}" test "${test_flags[@]}" \
  ./internal/api/handlers/management \
  ./internal/pluginhost \
  ./internal/pluginstore \
  ./internal/redisqueue \
  ./sdk/cliproxy/auth

build_dir="$(mktemp -d "${TMPDIR:-/tmp}/cliproxyapi-pro-build.XXXXXX")"
trap 'rm -rf "${build_dir}"' EXIT
go -C "${upstream_root}" build -buildvcs=false -o "${build_dir}/cli-proxy-api" ./cmd/server/
