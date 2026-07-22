#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"
pycache_root="${PYTHONPYCACHEPREFIX:-${TMPDIR:-/tmp}/cliproxyapi-pro-pycache}"

export PYTHONPYCACHEPREFIX="${pycache_root}"

python3 -m py_compile \
  "${repo_root}/cliproxyapi-pro-core/patches/apply_upstream_patches.py" \
  "${repo_root}/cliproxyapi-pro-management/apply_customizations.py" \
  "${repo_root}/scripts/validation/check_workflow_actions.py"

python3 -m unittest discover \
  -s "${repo_root}/cliproxyapi-pro-management/tests" \
  -p 'test_*.py'

python3 -m json.tool \
  "${repo_root}/cliproxyapi-pro-management/monitoring-locales.json" \
  >/dev/null

python3 "${repo_root}/scripts/validation/check_workflow_actions.py" \
  "${repo_root}/.github/workflows"

sh -n "${repo_root}/cliproxyapi-pro-core/entrypoint.sh"
bash -n \
  "${repo_root}/cliproxyapi-pro-management/apply.sh" \
  "${repo_root}/scripts/validation/repo.sh" \
  "${repo_root}/scripts/validation/core.sh" \
  "${repo_root}/scripts/validation/management.sh"

if command -v shellcheck >/dev/null 2>&1; then
  shellcheck \
    "${repo_root}/cliproxyapi-pro-core/entrypoint.sh" \
    "${repo_root}/cliproxyapi-pro-management/apply.sh" \
    "${repo_root}/scripts/validation/repo.sh" \
    "${repo_root}/scripts/validation/core.sh" \
    "${repo_root}/scripts/validation/management.sh"
elif [[ "${VALIDATION_REQUIRE_TOOLS:-0}" == "1" ]]; then
  echo "shellcheck is required but was not found" >&2
  exit 1
else
  echo "SKIP: shellcheck is not installed"
fi

if command -v actionlint >/dev/null 2>&1; then
  actionlint -color -ignore 'SC2129'
elif [[ "${VALIDATION_REQUIRE_TOOLS:-0}" == "1" ]]; then
  echo "actionlint is required but was not found" >&2
  exit 1
else
  echo "SKIP: actionlint is not installed"
fi

git -C "${repo_root}" diff --check
