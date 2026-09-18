#!/usr/bin/env bash

set -euo pipefail

mode="${RELEASE_PUBLISH_MODE:?RELEASE_PUBLISH_MODE is required}"
allow_stale_publish="${ALLOW_STALE_PUBLISH:-false}"

if [[ "${allow_stale_publish}" == "true" ]]; then
  echo "::warning::Stale publication protection was explicitly bypassed."
  exit 0
fi

current_repo="${CURRENT_REPO:?CURRENT_REPO is required}"
customization_sha="${CUSTOMIZATION_SHA:?CUSTOMIZATION_SHA is required}"
management_upstream_repo="${MANAGEMENT_UPSTREAM_REPO:?MANAGEMENT_UPSTREAM_REPO is required}"
target_release_tag="${TARGET_RELEASE_TAG:?TARGET_RELEASE_TAG is required}"
target_management_tag="${TARGET_MANAGEMENT_TAG:?TARGET_MANAGEMENT_TAG is required}"
target_management_sha="${TARGET_MANAGEMENT_SHA:?TARGET_MANAGEMENT_SHA is required}"

normalize_version() {
  local version="$1"
  version="${version#v}"
  version="${version%-pro}"
  printf '%s\n' "${version}"
}

version_gt() {
  local left="$1"
  local right="$2"
  [[ "$(printf '%s\n%s\n' "${right}" "${left}" | sort -V | tail -n 1)" == "${left}" && "${left}" != "${right}" ]]
}

reject_stale() {
  echo "::error::$1" >&2
  exit 1
}

release_value() {
  local prefix="$1"
  awk -v prefix="${prefix}" 'index($0, prefix) == 1 && value == "" { value = substr($0, length(prefix) + 1) } END { print value }'
}

ensure_not_behind_commit() {
  local repo="$1"
  local published_sha="$2"
  local target_sha="$3"
  local label="$4"
  local compare_status

  if [[ -z "${published_sha}" || "${published_sha}" == "${target_sha}" ]]; then
    return 0
  fi
  compare_status="$(gh api "repos/${repo}/compare/${published_sha}...${target_sha}" --jq '.status' 2>/dev/null || true)"
  case "${compare_status}" in
    ahead|identical)
      ;;
    *)
      reject_stale "${label} would move from ${published_sha} to ${target_sha} with compare status '${compare_status:-unknown}'."
      ;;
  esac
}

ensure_management_not_newer() {
  local published_tag="$1"
  local published_sha="$2"

  if [[ -n "${published_tag}" ]]; then
    if version_gt "$(normalize_version "${published_tag}")" "$(normalize_version "${target_management_tag}")"; then
      reject_stale "Published Management ${published_tag} is newer than target ${target_management_tag}."
    fi
    if version_gt "$(normalize_version "${target_management_tag}")" "$(normalize_version "${published_tag}")"; then
      return 0
    fi
  fi
  ensure_not_behind_commit "${management_upstream_repo}" "${published_sha}" "${target_management_sha}" "Management snapshot"
}

latest_customization_sha="$(gh api "repos/${current_repo}/commits/main" --jq '.sha')"
latest_release_tag="$(gh release view --repo "${current_repo}" --json tagName --jq '.tagName' 2>/dev/null || true)"
latest_management_tag="$(gh release view --repo "${management_upstream_repo}" --json tagName --jq '.tagName')"
latest_management_sha="$(gh api "repos/${management_upstream_repo}/commits/${latest_management_tag}" --jq '.sha')"

[[ "${customization_sha}" == "${latest_customization_sha}" ]] || reject_stale "Customization commit ${customization_sha} is no longer main (${latest_customization_sha})."
[[ "${target_management_tag}" == "${latest_management_tag}" ]] || reject_stale "Management release ${target_management_tag} is no longer latest (${latest_management_tag})."
[[ "${target_management_sha}" == "${latest_management_sha}" ]] || reject_stale "Management commit ${target_management_sha} no longer matches ${latest_management_tag} (${latest_management_sha})."

case "${mode}" in
  core)
    core_upstream_repo="${CORE_UPSTREAM_REPO:?CORE_UPSTREAM_REPO is required}"
    models_repo="${MODELS_REPO:?MODELS_REPO is required}"
    target_core_tag="${TARGET_CORE_TAG:?TARGET_CORE_TAG is required}"
    target_core_sha="${TARGET_CORE_SHA:?TARGET_CORE_SHA is required}"
    target_models_sha="${TARGET_MODELS_SHA:?TARGET_MODELS_SHA is required}"

    latest_core_tag="$(gh release view --repo "${core_upstream_repo}" --json tagName --jq '.tagName')"
    latest_core_sha="$(gh api "repos/${core_upstream_repo}/commits/${latest_core_tag}" --jq '.sha')"
    latest_models_sha="$(gh api "repos/${models_repo}/commits/main" --jq '.sha')"

    [[ "${target_core_tag}" == "${latest_core_tag}" ]] || reject_stale "Core release ${target_core_tag} is no longer latest (${latest_core_tag})."
    [[ "${target_core_sha}" == "${latest_core_sha}" ]] || reject_stale "Core commit ${target_core_sha} no longer matches ${latest_core_tag} (${latest_core_sha})."
    [[ "${target_models_sha}" == "${latest_models_sha}" ]] || reject_stale "Models commit ${target_models_sha} is no longer main (${latest_models_sha})."

    if [[ -n "${latest_release_tag}" ]]; then
      if version_gt "$(normalize_version "${latest_release_tag}")" "$(normalize_version "${target_release_tag}")"; then
        reject_stale "Published release ${latest_release_tag} is newer than target ${target_release_tag}."
      fi

      current_body="$(gh release view "${latest_release_tag}" --repo "${current_repo}" --json body --jq '.body // ""')"
      published_core_tag="$(printf '%s\n' "${current_body}" | release_value '- Core upstream release: ')"
      published_management_tag="$(printf '%s\n' "${current_body}" | release_value '- Management upstream release: ')"
      published_management_sha="$(printf '%s\n' "${current_body}" | release_value '- Management upstream commit: ')"
      published_models_sha="$(printf '%s\n' "${current_body}" | release_value '- Models data commit: ')"
      published_customization_sha="$(printf '%s\n' "${current_body}" | release_value '- Customization commit: ')"

      if [[ "${latest_release_tag}" == "${target_release_tag}" && -n "${published_core_tag}" && "${published_core_tag}" != "${target_core_tag}" ]]; then
        reject_stale "Existing ${target_release_tag} records Core ${published_core_tag}, not ${target_core_tag}."
      fi
      ensure_management_not_newer "${published_management_tag}" "${published_management_sha}"
      ensure_not_behind_commit "${current_repo}" "${published_customization_sha}" "${customization_sha}" "Customization snapshot"
      ensure_not_behind_commit "${models_repo}" "${published_models_sha}" "${target_models_sha}" "Models snapshot"
    fi
    ;;
  management)
    [[ "${target_release_tag}" == "${latest_release_tag}" ]] || reject_stale "Target release ${target_release_tag} is no longer latest (${latest_release_tag:-none})."

    current_body="$(gh release view "${target_release_tag}" --repo "${current_repo}" --json body --jq '.body // ""')"
    published_management_tag="$(printf '%s\n' "${current_body}" | release_value '- Management upstream release: ')"
    published_management_sha="$(printf '%s\n' "${current_body}" | release_value '- Management upstream commit: ')"
    published_customization_sha="$(printf '%s\n' "${current_body}" | release_value '- Customization commit: ')"

    ensure_management_not_newer "${published_management_tag}" "${published_management_sha}"
    ensure_not_behind_commit "${current_repo}" "${published_customization_sha}" "${customization_sha}" "Customization snapshot"
    ;;
  *)
    echo "Unsupported RELEASE_PUBLISH_MODE: ${mode}" >&2
    exit 2
    ;;
esac

echo "${mode} publication freshness checks passed for ${target_release_tag}."
