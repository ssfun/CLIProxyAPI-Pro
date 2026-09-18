#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"
pycache_root="${PYTHONPYCACHEPREFIX:-${TMPDIR:-/tmp}/cliproxyapi-pro-pycache}"

export PYTHONPYCACHEPREFIX="${pycache_root}"

python3 -m py_compile \
  "${repo_root}/cliproxyapi-pro-core/patches/apply_upstream_patches.py" \
  "${repo_root}/cliproxyapi-pro-management/apply_customizations.py" \
  "${repo_root}/scripts/validation/check_patch_surface.py" \
  "${repo_root}/scripts/validation/compare_go_test_results.py" \
  "${repo_root}/scripts/validation/classify_validation_changes.py" \
  "${repo_root}/scripts/validation/summarize_core_validation.py" \
  "${repo_root}/scripts/validation/check_workflow_actions.py" \
  "${repo_root}/scripts/validation/api_key_policy_runtime_smoke.py" \
  "${repo_root}/scripts/validation/run_api_key_policy_binary_smoke.py" \
  "${repo_root}/scripts/validation/test_api_key_policy_contract.py" \
  "${repo_root}/scripts/build/create_reproducible_archive.py"

python3 -m unittest discover \
  -s "${repo_root}/cliproxyapi-pro-management/tests" \
  -p 'test_*.py'

python3 -m unittest discover \
  -s "${repo_root}/scripts/validation" \
  -p 'test_*.py'

python3 -m unittest discover \
  -s "${repo_root}/scripts/build/tests" \
  -p 'test_*.py'

python3 -m json.tool \
  "${repo_root}/cliproxyapi-pro-management/monitoring-locales.json" \
  >/dev/null
python3 -m json.tool \
  "${repo_root}/cliproxyapi-pro-management/overlay-replacements.json" \
  >/dev/null

python3 "${repo_root}/scripts/validation/check_workflow_actions.py" \
  "${repo_root}/.github/workflows"

if grep -RIn --exclude='*_test.go' 'internal/embeddedusage' \
  "${repo_root}/cliproxyapi-pro-core/patches/sources/internal/pro"; then
  echo "internal/pro modules must not depend on the embeddedusage compatibility facade" >&2
  exit 1
fi

sh -n "${repo_root}/cliproxyapi-pro-core/entrypoint.sh"

if grep -Eq '^[[:space:]]*COPY[[:space:]]+plugins([[:space:]/]|$)' \
  "${repo_root}/cliproxyapi-pro-core/Dockerfile.runtime"; then
  echo "Dockerfile.runtime must not require removed bundled plugin artifacts" >&2
  exit 1
fi

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
