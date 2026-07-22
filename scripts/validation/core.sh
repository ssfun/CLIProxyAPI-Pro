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

guarded_source='internal/logging/requestid.go'
preflight_log="$(mktemp "${TMPDIR:-/tmp}/cliproxyapi-pro-preflight.XXXXXX")"
printf '\n' >> "${upstream_root}/${guarded_source}"
if python3 "${repo_root}/cliproxyapi-pro-core/patches/apply_upstream_patches.py" >"${preflight_log}" 2>&1; then
  echo "Core customization unexpectedly accepted changed guarded upstream source" >&2
  exit 1
fi
if ! grep -Fq 'upstream source changed before full-file replacement' "${preflight_log}"; then
  cat "${preflight_log}" >&2
  echo "Core customization did not fail with the expected upstream-drift error" >&2
  exit 1
fi
if [[ -e "${upstream_root}/internal/embeddedusage" ]]; then
  echo "Core customization wrote files before completing preflight" >&2
  exit 1
fi
git -C "${upstream_root}" restore --worktree -- "${guarded_source}"
rm -f "${preflight_log}"
if [[ -n "$(git -C "${upstream_root}" status --porcelain)" ]]; then
  echo "Core preflight regression did not restore a clean checkout" >&2
  exit 1
fi

python3 "${repo_root}/cliproxyapi-pro-core/patches/apply_upstream_patches.py"
git -C "${upstream_root}" diff --check

git -C "${upstream_root}" add -N .
patched_diff_hash="$(git -C "${upstream_root}" diff --binary | git hash-object --stdin)"
reapply_log="$(mktemp "${TMPDIR:-/tmp}/cliproxyapi-pro-reapply.XXXXXX")"
if python3 "${repo_root}/cliproxyapi-pro-core/patches/apply_upstream_patches.py" >"${reapply_log}" 2>&1; then
  echo "Core customization unexpectedly allowed a second application" >&2
  exit 1
fi
if ! grep -Fq 'target already contains CLIProxyAPI Pro customizations' "${reapply_log}"; then
  cat "${reapply_log}" >&2
  echo "Core customization did not fail with the expected already-applied error" >&2
  exit 1
fi
rm -f "${reapply_log}"
reapplied_diff_hash="$(git -C "${upstream_root}" diff --binary | git hash-object --stdin)"
if [[ "${patched_diff_hash}" != "${reapplied_diff_hash}" ]]; then
  echo "Core customization changed the source tree during rejected reapplication" >&2
  exit 1
fi

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
