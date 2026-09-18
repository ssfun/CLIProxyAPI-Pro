#!/usr/bin/env bash
set -euo pipefail

if [[ "$#" -lt 1 || "$#" -gt 2 ]]; then
  echo "Usage: $0 /path/to/CLIProxyAPI [/path/to/models.json]" >&2
  exit 2
fi

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../.." && pwd)"
upstream_root="$(cd "$1" && pwd)"
release_models_file=""
if [[ "$#" -eq 2 ]]; then
  release_models_file="$(cd "$(dirname "$2")" && pwd)/$(basename "$2")"
fi

if [[ ! -f "${upstream_root}/go.mod" || ! -d "${upstream_root}/cmd/server" ]]; then
  echo "Core upstream checkout is invalid: ${upstream_root}" >&2
  exit 1
fi

if [[ ! -d "${upstream_root}/.git" ]] && [[ ! -f "${upstream_root}/.git" ]]; then
  echo "Core upstream checkout must be a Git worktree: ${upstream_root}" >&2
  exit 1
fi

if [[ -n "$(git -C "${upstream_root}" status --porcelain)" ]]; then
  echo "Core upstream checkout must be clean before validation: ${upstream_root}" >&2
  exit 1
fi

if [[ -n "${release_models_file}" ]] && [[ ! -f "${release_models_file}" ]]; then
  echo "Release models data is invalid: ${release_models_file}" >&2
  exit 1
fi

export PYTHONPYCACHEPREFIX="${PYTHONPYCACHEPREFIX:-${TMPDIR:-/tmp}/cliproxyapi-pro-pycache}"
export SRC_ROOT="${upstream_root}"

cleanup_validation_tmp=0
if [[ -n "${VALIDATION_ARTIFACT_DIR:-}" ]]; then
  validation_tmp="${VALIDATION_ARTIFACT_DIR}"
  mkdir -p "${validation_tmp}"
else
  validation_tmp="$(mktemp -d "${TMPDIR:-/tmp}/cliproxyapi-pro-core-validation.XXXXXX")"
  cleanup_validation_tmp=1
fi

timings_log="${validation_tmp}/phase-timings.tsv"
baseline_test_log="${validation_tmp}/go-test-baseline.jsonl"
candidate_test_log="${validation_tmp}/go-test-candidate.jsonl"
summary_file="${validation_tmp}/summary.md"
build_tmp="$(mktemp -d "${TMPDIR:-/tmp}/cliproxyapi-pro-core-build.XXXXXX")"
: >"${timings_log}"
: >"${candidate_test_log}"
rm -f "${baseline_test_log}" "${summary_file}"

baseline_root=""
render_summary() {
  local args=(
    "${repo_root}/scripts/validation/summarize_core_validation.py"
    --timings "${timings_log}"
    --go-log "${candidate_test_log}"
  )
  if [[ -f "${baseline_test_log}" ]]; then
    args+=(--go-log "${baseline_test_log}")
  fi
  python3 "${args[@]}" >"${summary_file}" 2>/dev/null || true
  if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]] && [[ -s "${summary_file}" ]]; then
    cat "${summary_file}" >>"${GITHUB_STEP_SUMMARY}"
  fi
}

cleanup() {
  local status=$?
  trap - EXIT
  if [[ -n "${baseline_root}" ]]; then
    git -C "${upstream_root}" worktree remove --force "${baseline_root}" >/dev/null 2>&1 || true
  fi
  render_summary
  if [[ "${cleanup_validation_tmp}" == "1" ]]; then
    rm -rf "${validation_tmp}"
  fi
  rm -rf "${build_tmp}"
  exit "${status}"
}
trap cleanup EXIT

run_timed() {
  local phase_name="$1"
  shift
  local started_at="${SECONDS}"
  local status=0
  if [[ "${GITHUB_ACTIONS:-false}" == "true" ]]; then
    echo "::group::${phase_name}"
  else
    echo "==> ${phase_name}"
  fi
  "$@" || status=$?
  local duration=$((SECONDS - started_at))
  printf '%s\t%s\t%s\n' "${phase_name}" "${duration}" "${status}" >>"${timings_log}"
  echo "<== ${phase_name}: ${duration}s (status ${status})"
  if [[ "${GITHUB_ACTIONS:-false}" == "true" ]]; then
    echo "::endgroup::"
  fi
  return "${status}"
}

if [[ -n "${release_models_file}" ]]; then
  python3 -m json.tool "${release_models_file}" >/dev/null
  cp "${release_models_file}" "${upstream_root}/internal/registry/models/models.json"
fi

upstream_commit="$(git -C "${upstream_root}" rev-parse HEAD)"

validate_late_patch_guard() {
  local late_guarded_source='sdk/cliproxy/auth/scheduler.go'
  python3 - "${upstream_root}/${late_guarded_source}" <<'PY' || return
from pathlib import Path
import sys

path = Path(sys.argv[1])
source = path.read_text()
anchor = '\t\ts.mixedCursors[cursorKey] = slot + 1\n\t\treturn picked, providerKey, nil\n'
if source.count(anchor) != 1:
    raise SystemExit(f'late Core preflight anchor count is {source.count(anchor)}, want 1')
path.write_text(source.replace(anchor, anchor.replace('return picked', 'return  picked'), 1))
PY
  local late_preflight_status
  local late_preflight_diff_hash
  local late_preflight_log
  late_preflight_status="$(git -C "${upstream_root}" status --porcelain=v1 -uall)" || return
  late_preflight_diff_hash="$(git -C "${upstream_root}" diff --binary | git hash-object --stdin)" || return
  late_preflight_log="${validation_tmp}/late-preflight.log"
  if python3 "${repo_root}/cliproxyapi-pro-core/patches/apply_upstream_patches.py" >"${late_preflight_log}" 2>&1; then
    echo "Core customization unexpectedly accepted a changed late patch anchor" >&2
    return 1
  fi
  if ! grep -Fq 'expected one pattern' "${late_preflight_log}"; then
    cat "${late_preflight_log}" >&2
    echo "Core customization did not fail with the expected late-anchor error" >&2
    return 1
  fi
  if [[ "$(git -C "${upstream_root}" status --porcelain=v1 -uall)" != "${late_preflight_status}" ]] || \
     [[ "$(git -C "${upstream_root}" diff --binary | git hash-object --stdin)" != "${late_preflight_diff_hash}" ]]; then
    echo "Core customization changed the source tree before validating every patch anchor" >&2
    return 1
  fi
  git -C "${upstream_root}" restore --worktree -- "${late_guarded_source}" || return
}

apply_candidate_customization() {
  python3 "${repo_root}/cliproxyapi-pro-core/patches/apply_upstream_patches.py" || return
  python3 "${repo_root}/scripts/validation/check_patch_surface.py" \
    "${upstream_root}" \
    "${repo_root}/scripts/validation/contracts/core-upstream-modified-files.txt" \
    --ignore internal/registry/models/models.json || return
  go -C "${upstream_root}" mod tidy || return
  git -C "${upstream_root}" diff --check || return
}

run_static_checks() {
  if ! command -v staticcheck >/dev/null 2>&1; then
    echo "VALIDATION_STATICCHECK=1 requires staticcheck" >&2
    return 1
  fi
}

run_staticcheck_sa4011() {
  (
    cd "${upstream_root}" || exit
    staticcheck -checks=SA4011 ./internal/api/handlers/management || exit
  ) || return
}

run_staticcheck_u1000() {
  (
    cd "${upstream_root}" || exit
    staticcheck -checks=U1000 ./internal/pro/... || exit
  ) || return
}

run_go_vet() {
  go -C "${upstream_root}" vet ./internal/pluginhost || return
}

validate_reapplication_guard() {
  git -C "${upstream_root}" add -N . || return
  local patched_diff_hash
  local reapplied_diff_hash
  local reapply_log
  patched_diff_hash="$(git -C "${upstream_root}" diff --binary | git hash-object --stdin)" || return
  reapply_log="${validation_tmp}/reapply.log"
  if python3 "${repo_root}/cliproxyapi-pro-core/patches/apply_upstream_patches.py" >"${reapply_log}" 2>&1; then
    echo "Core customization unexpectedly allowed a second application" >&2
    return 1
  fi
  if ! grep -Fq 'target already contains CLIProxyAPI Pro customizations' "${reapply_log}"; then
    cat "${reapply_log}" >&2
    echo "Core customization did not fail with the expected already-applied error" >&2
    return 1
  fi
  reapplied_diff_hash="$(git -C "${upstream_root}" diff --binary | git hash-object --stdin)" || return
  if [[ "${patched_diff_hash}" != "${reapplied_diff_hash}" ]]; then
    echo "Core customization changed the source tree during rejected reapplication" >&2
    return 1
  fi
}

test_flags=(-count=1)
if [[ "${VALIDATION_RACE:-0}" == "1" ]]; then
  test_flags+=(-race)
fi

run_go_test_group() {
  local source_root="$1"
  local aggregate_log="$2"
  local group_name="$3"
  shift 3
  local group_log="${aggregate_log%.jsonl}.${group_name}.jsonl"
  local status=0
  : >"${group_log}"
  go -C "${source_root}" test "${test_flags[@]}" -json "$@" >"${group_log}" 2>&1 || status=$?
  cat "${group_log}" >>"${aggregate_log}"
  if [[ "${status}" -eq 0 ]]; then
    rm -f "${group_log}"
    return 0
  fi
  if python3 - "${group_log}" <<'PY'
import json
from pathlib import Path
import sys

for raw_line in Path(sys.argv[1]).read_text(encoding="utf-8", errors="replace").splitlines():
    try:
        event = json.loads(raw_line)
    except json.JSONDecodeError:
        continue
    if event.get("Action") == "fail" and event.get("Package"):
        raise SystemExit(0)
raise SystemExit(1)
PY
  then
    rm -f "${group_log}"
    return 1
  fi
  printf '%s\n' "{\"Action\":\"fail\",\"Package\":\"[command:${group_name}]\"}" >>"${aggregate_log}"
  rm -f "${group_log}"
  return 2
}

apply_validation_fixture_adjustments() {
  local source_root="$1"
  local fixture_patch="${repo_root}/scripts/validation/fixtures/antigravity_models_timeout_cleanup.patch"
  if git -C "${source_root}" apply --unidiff-zero --reverse --check "${fixture_patch}" >/dev/null 2>&1; then
    return 0
  fi
  if ! git -C "${source_root}" apply --unidiff-zero --check "${fixture_patch}"; then
    echo "Antigravity timeout cleanup fixture no longer matches the selected upstream" >&2
    return 1
  fi
  git -C "${source_root}" apply --unidiff-zero "${fixture_patch}" || return
}

run_upstream_test_groups() {
  local source_root="$1"
  local aggregate_log="$2"
  local include_pro_packages="$3"
  local result=0
  local saw_test_failure=0
  local saw_command_failure=0
  local packages=(
    ./cmd/server
    ./internal/api
    ./internal/api/handlers/management
    ./internal/auth/claude
    ./internal/client/codex/live
    ./internal/cmd
    ./internal/config
    ./internal/managementasset
    ./internal/pluginhost
    ./internal/pluginstore
    ./internal/redisqueue
    ./internal/requestmeta
    ./internal/runtime/executor/helps
    ./internal/translator/codex/openai/chat-completions
    ./internal/translator/codex/openai/responses
    ./sdk/api/handlers
    ./sdk/api/handlers/claude
    ./sdk/api/handlers/gemini
    ./sdk/api/handlers/openai
    ./sdk/auth
    ./sdk/cliproxy/auth
    ./sdk/cliproxy/executor
    ./sdk/cliproxy/usage
    ./sdk/pluginapi
    ./sdk/proxyutil
  )
  : >"${aggregate_log}"
  apply_validation_fixture_adjustments "${source_root}" || return 2

  # Keep this list scoped to packages containing a modified upstream file or
  # an injected Pro compatibility/test source. Generic upstream-only packages
  # belong in upstream CI, not in this customization comparison.
  if [[ "${include_pro_packages}" == "1" ]]; then
    packages+=(./internal/pro/... ./internal/embeddedusage/...)
  fi
  run_go_test_group "${source_root}" "${aggregate_log}" patch-and-pro-packages \
    "${packages[@]}" || result=$?
  if [[ "${result}" -eq 2 ]]; then saw_command_failure=1; elif [[ "${result}" -ne 0 ]]; then saw_test_failure=1; fi

  # These two packages must remain isolated. Combining them with the broad
  # group has triggered grouping-dependent WebSocket races in remote CI.
  result=0
  run_go_test_group "${source_root}" "${aggregate_log}" runtime-executor ./internal/runtime/executor || result=$?
  if [[ "${result}" -eq 2 ]]; then saw_command_failure=1; elif [[ "${result}" -ne 0 ]]; then saw_test_failure=1; fi
  result=0
  run_go_test_group "${source_root}" "${aggregate_log}" sdk-cliproxy ./sdk/cliproxy || result=$?
  if [[ "${result}" -eq 2 ]]; then saw_command_failure=1; elif [[ "${result}" -ne 0 ]]; then saw_test_failure=1; fi

  if [[ "${saw_command_failure}" -ne 0 ]]; then return 2; fi
  if [[ "${saw_test_failure}" -ne 0 ]]; then return 1; fi
  return 0
}

prepare_baseline_worktree() {
  baseline_root="${validation_tmp}/baseline-upstream"
  git -C "${upstream_root}" worktree add --detach "${baseline_root}" "${upstream_commit}" || return
  if [[ -n "${release_models_file}" ]]; then
    cp "${release_models_file}" "${baseline_root}/internal/registry/models/models.json" || return
  fi
}

build_candidate() {
  go -C "${upstream_root}" build -buildvcs=false -o "${build_tmp}/cli-proxy-api" ./cmd/server/ || return
  CGO_ENABLED=0 go -C "${upstream_root}" build -buildvcs=false -o "${build_tmp}/cli-proxy-api-no-plugin" ./cmd/server/ || return
}

run_timed "patch preflight guard" validate_late_patch_guard
run_timed "apply customization and dependencies" apply_candidate_customization
if [[ "${VALIDATION_STATICCHECK:-0}" == "1" ]]; then
  run_static_checks
  run_timed "staticcheck SA4011" run_staticcheck_sa4011
  run_timed "staticcheck U1000" run_staticcheck_u1000
fi
run_timed "go vet" run_go_vet
run_timed "reapplication guard" validate_reapplication_guard

candidate_status=0
run_timed "candidate tests" run_upstream_test_groups \
  "${upstream_root}" "${candidate_test_log}" 1 || candidate_status=$?

if [[ "${candidate_status}" -eq 0 ]]; then
  echo "OK: customized Core candidate passed; clean upstream baseline is not required"
else
  echo "NOTICE: customized Core tests failed; preparing an independent clean upstream baseline" >&2
  run_timed "prepare clean baseline" prepare_baseline_worktree
  baseline_status=0
  run_timed "clean upstream baseline tests" run_upstream_test_groups \
    "${baseline_root}" "${baseline_test_log}" 0 || baseline_status=$?
  if [[ "${baseline_status}" -eq 2 ]]; then
    echo "Clean upstream baseline could not be executed reliably" >&2
    exit 1
  fi
  if [[ "${baseline_status}" -ne 0 ]]; then
    echo "NOTICE: clean upstream has test failures; they will be tolerated only if the same failures remain after customization" >&2
  fi
  run_timed "compare candidate with baseline" \
    python3 "${repo_root}/scripts/validation/compare_go_test_results.py" \
    "${baseline_test_log}" "${candidate_test_log}"
fi

run_timed "CGO and non-CGO builds" build_candidate
