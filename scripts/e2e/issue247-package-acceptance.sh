#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"

require_cmd awk git grep ip mktemp runuser sudo systemctl timeout

: "${PODLAZ_E2E_PROFILE_URI:=}"
: "${PODLAZ_E2E_PROFILE_URI_LIST:=}"

if [[ -z "${PODLAZ_E2E_PROFILE_URI}" && -z "${PODLAZ_E2E_PROFILE_URI_LIST}" ]]; then
  fail "PODLAZ_E2E_PROFILE_URI or PODLAZ_E2E_PROFILE_URI_LIST is required"
fi

EVIDENCE_FILE="${E2E_ARTIFACT_DIR}/issue247-diagnostics-acceptance.txt"
PROFILE_ID=""
CONNECTED=false
STALE_LINK_CREATED=false
STALE_LINK_INDEX=""

write_evidence() {
  local key="$1" value="$2"
  case "${key}${value}" in
    *$'\n'*|*$'\r'*) fail "invalid normalized issue 247 evidence" ;;
  esac
  printf '%s=%s\n' "${key}" "${value}" >>"${EVIDENCE_FILE}"
}

run_installed_podlaz() {
  sudo -n runuser -u "$(id -un)" -g podlaz -- env \
    XDG_CONFIG_HOME="${XDG_CONFIG_HOME}" \
    XDG_STATE_HOME="${XDG_STATE_HOME}" \
    XDG_CACHE_HOME="${XDG_CACHE_HOME}" \
    /usr/bin/podlaz "$@"
}

run_installed_podlaz_bounded() {
  local timeout_seconds="$1"
  shift
  timeout --signal=TERM --kill-after=5s "${timeout_seconds}" \
    sudo -n runuser -u "$(id -un)" -g podlaz -- env \
      XDG_CONFIG_HOME="${XDG_CONFIG_HOME}" \
      XDG_STATE_HOME="${XDG_STATE_HOME}" \
      XDG_CACHE_HOME="${XDG_CACHE_HOME}" \
      /usr/bin/podlaz "$@"
}

remove_stale_test_link() {
  local current_index=""
  [[ "${STALE_LINK_CREATED}" == "true" ]] || return 0

  current_index="$(ip -o link show dev podlaz0 2>/dev/null | awk -F: 'NR == 1 {gsub(/[[:space:]]/, "", $1); print $1}')"
  if [[ -z "${current_index}" ]]; then
    STALE_LINK_CREATED=false
    STALE_LINK_INDEX=""
    return 0
  fi
  if [[ "${current_index}" != "${STALE_LINK_INDEX}" ]]; then
    printf 'issue 247 cleanup refused to delete a replaced podlaz0 identity\n' >&2
    return 1
  fi
  if ! ip tuntap show dev podlaz0 2>/dev/null | grep -Eq '^podlaz0:[[:space:]]+tun([[:space:]]|$)'; then
    printf 'issue 247 cleanup refused to delete a non-TUN podlaz0 identity\n' >&2
    return 1
  fi

  sudo -n ip link del dev podlaz0
  STALE_LINK_CREATED=false
  STALE_LINK_INDEX=""
}

cleanup() {
  local saved=$? cleanup_failed=0
  set +e
  if [[ "${CONNECTED}" == "true" ]]; then
    run_installed_podlaz_bounded 60s disconnect >/dev/null 2>&1 || cleanup_failed=1
    CONNECTED=false
  fi
  remove_stale_test_link || cleanup_failed=1
  set -e
  if (( saved == 0 && cleanup_failed != 0 )); then
    saved=1
  fi
  return "${saved}"
}
trap cleanup EXIT

first_profile_uri() {
  if [[ -n "${PODLAZ_E2E_PROFILE_URI}" ]]; then
    printf '%s\n' "${PODLAZ_E2E_PROFILE_URI}"
    return
  fi
  while IFS= read -r uri; do
    [[ -n "${uri}" ]] || continue
    printf '%s\n' "${uri}"
    return
  done <<<"${PODLAZ_E2E_PROFILE_URI_LIST}"
}

verify_package_provenance() {
  local build_commit version_output
  build_commit="${GITHUB_SHA:-$(git rev-parse HEAD)}"
  version_output="$(mktemp "${E2E_TMP_ROOT}/issue247-version.XXXXXX")"
  /usr/bin/podlaz version >"${version_output}" 2>/dev/null || fail "issue 247 installed CLI version failed"
  grep -F -- "${build_commit}" "${version_output}" >/dev/null || fail "issue 247 installed CLI does not identify the tested commit"
  rm -f -- "${version_output}"
  systemctl is-active --quiet podlazd.service || fail "issue 247 requires the packaged podlazd service"
  write_evidence package_provenance pass
}

import_profile_privately() {
  local uri="$1" output error_output
  output="$(mktemp "${E2E_TMP_ROOT}/issue247-profile-import.stdout.XXXXXX")"
  error_output="$(mktemp "${E2E_TMP_ROOT}/issue247-profile-import.stderr.XXXXXX")"
  if ! run_installed_podlaz_bounded 30s profile import "${uri}" >"${output}" 2>"${error_output}"; then
    rm -f -- "${output}" "${error_output}"
    fail "issue 247 profile import failed"
  fi
  PROFILE_ID="$(awk '/^Imported profile:/ {print $3}' "${output}")"
  rm -f -- "${output}" "${error_output}"
  assert_nonempty "${PROFILE_ID}" "issue 247 imported profile id"
  mask_value "${PROFILE_ID}"
  write_evidence profile_import pass
}

assert_inactive_doctor_clean() {
  local phase="$1" output exit_code
  output="$(mktemp "${E2E_TMP_ROOT}/issue247-${phase}-inactive-doctor.XXXXXX")"
  set +e
  run_installed_podlaz_bounded 20s doctor >"${output}" 2>&1
  exit_code=$?
  set -e
  if (( exit_code != 0 )); then
    rm -f -- "${output}"
    fail "${phase}: inactive doctor returned non-zero"
  fi
  grep -Fx '[OK] stale-resources: no podlaz-owned resources found' "${output}" >/dev/null || {
    rm -f -- "${output}"
    fail "${phase}: inactive doctor does not report clean managed resources"
  }
  grep -Fx '[OK] startup-recovery-scan: startup recovery scan: clean inactive state' "${output}" >/dev/null || {
    rm -f -- "${output}"
    fail "${phase}: inactive doctor startup scan is not clean inactive"
  }
  if grep -F '[WARN] stale-resources:' "${output}" >/dev/null; then
    rm -f -- "${output}"
    fail "${phase}: inactive doctor unexpectedly reports stale resources"
  fi
  rm -f -- "${output}"
  write_evidence "inactive_doctor_${phase}" pass
}

assert_active_doctor_clean() {
  local output exit_code
  output="$(mktemp "${E2E_TMP_ROOT}/issue247-active-doctor.XXXXXX")"
  set +e
  run_installed_podlaz_bounded 30s doctor >"${output}" 2>&1
  exit_code=$?
  set -e
  if (( exit_code != 0 )); then
    rm -f -- "${output}"
    fail "active doctor returned non-zero"
  fi
  grep -Fx '[OK] stale-resources: managed resources match active lifecycle' "${output}" >/dev/null || {
    rm -f -- "${output}"
    fail "active doctor does not accept exact transaction-owned resources"
  }
  grep -Fx '[OK] startup-recovery-scan: startup recovery scan: clean for active connection' "${output}" >/dev/null || {
    rm -f -- "${output}"
    fail "active doctor startup scan does not use active lifecycle wording"
  }
  if grep -F 'clean inactive state' "${output}" >/dev/null || grep -F '[WARN] stale-resources:' "${output}" >/dev/null; then
    rm -f -- "${output}"
    fail "active doctor contains a false inactive/stale diagnostic"
  fi
  rm -f -- "${output}"
  write_evidence active_doctor pass
}

assert_active_status() {
  local output
  output="$(mktemp "${E2E_TMP_ROOT}/issue247-active-status.XXXXXX")"
  if ! run_installed_podlaz_bounded 20s status >"${output}" 2>&1; then
    rm -f -- "${output}"
    fail "issue 247 active status returned non-zero"
  fi
  grep -Fx 'Connection: active' "${output}" >/dev/null || fail "issue 247 connection is not active"
  grep -Fx 'Transaction: committed' "${output}" >/dev/null || fail "issue 247 TUN transaction is not committed"
  grep -Fx 'Stale state: none' "${output}" >/dev/null || fail "issue 247 active status reports stale state"
  rm -f -- "${output}"
  write_evidence active_status pass
}

assert_inactive_foreign_link_warns() {
  local output exit_code
  if ip link show dev podlaz0 >/dev/null 2>&1; then
    fail "issue 247 stale-resource acceptance requires podlaz0 to be absent before creating the test link"
  fi

  sudo -n ip tuntap add dev podlaz0 mode tun
  STALE_LINK_CREATED=true
  STALE_LINK_INDEX="$(ip -o link show dev podlaz0 | awk -F: 'NR == 1 {gsub(/[[:space:]]/, "", $1); print $1}')"
  assert_nonempty "${STALE_LINK_INDEX}" "issue 247 stale test link index"

  output="$(mktemp "${E2E_TMP_ROOT}/issue247-inactive-stale-doctor.XXXXXX")"
  set +e
  run_installed_podlaz_bounded 20s doctor >"${output}" 2>&1
  exit_code=$?
  set -e
  if (( exit_code != 0 )); then
    rm -f -- "${output}"
    fail "inactive stale-resource doctor returned unexpected non-zero"
  fi
  grep -F '[WARN] stale-resources: found interface podlaz0 exists' "${output}" >/dev/null || {
    rm -f -- "${output}"
    fail "inactive foreign-looking podlaz0 was not reported as stale"
  }
  rm -f -- "${output}"

  remove_stale_test_link || fail "issue 247 could not safely remove its exact stale test link"
  write_evidence inactive_stale_resource pass
}

assert_logs_since_valid() {
  local output error_output exit_code
  output="$(mktemp "${E2E_TMP_ROOT}/issue247-logs-since.stdout.XXXXXX")"
  error_output="$(mktemp "${E2E_TMP_ROOT}/issue247-logs-since.stderr.XXXXXX")"
  set +e
  run_installed_podlaz_bounded 20s logs --since 36h >"${output}" 2>"${error_output}"
  exit_code=$?
  set -e
  if (( exit_code != 0 )); then
    rm -f -- "${output}" "${error_output}"
    fail "installed podlaz logs --since 36h failed"
  fi
  grep -Fx 'podlaz daemon logs' "${output}" >/dev/null || {
    rm -f -- "${output}" "${error_output}"
    fail "installed podlaz logs --since 36h did not render the daemon log header"
  }
  rm -f -- "${output}" "${error_output}"
  write_evidence logs_since_36h pass
}

assert_logs_since_invalid() {
  local value="$1" key="$2" output error_output exit_code
  output="$(mktemp "${E2E_TMP_ROOT}/issue247-invalid-since.stdout.XXXXXX")"
  error_output="$(mktemp "${E2E_TMP_ROOT}/issue247-invalid-since.stderr.XXXXXX")"
  set +e
  run_installed_podlaz_bounded 10s logs --since "${value}" >"${output}" 2>"${error_output}"
  exit_code=$?
  set -e
  if (( exit_code != 2 )); then
    rm -f -- "${output}" "${error_output}"
    fail "invalid logs --since value did not return usage exit code 2"
  fi
  if [[ -s "${output}" ]] || ! grep -F 'invalid logs --since duration' "${error_output}" >/dev/null; then
    rm -f -- "${output}" "${error_output}"
    fail "invalid logs --since value did not fail clearly before journal output"
  fi
  rm -f -- "${output}" "${error_output}"
  write_evidence "logs_since_invalid_${key}" pass
}

: >"${EVIDENCE_FILE}"
setup_isolated_xdg issue247-package-acceptance
verify_package_provenance
assert_inactive_doctor_clean initial

PROFILE_URI="$(first_profile_uri)"
assert_nonempty "${PROFILE_URI}" "issue 247 profile URI"
mask_value "${PROFILE_URI}"
import_profile_privately "${PROFILE_URI}"
unset PROFILE_URI

if ! run_installed_podlaz_bounded 90s connect --mode tun "${PROFILE_ID}" >/dev/null 2>&1; then
  fail "issue 247 TUN connect failed"
fi
CONNECTED=true
assert_active_status
assert_active_doctor_clean

if ! run_installed_podlaz_bounded 60s disconnect >/dev/null 2>&1; then
  fail "issue 247 disconnect failed"
fi
CONNECTED=false
assert_inactive_doctor_clean after-disconnect

assert_inactive_foreign_link_warns
assert_logs_since_valid
assert_logs_since_invalid '1h30m' compound
assert_logs_since_invalid '-1h' signed
assert_logs_since_invalid '0h' zero
assert_logs_since_invalid '721h' excessive

write_evidence acceptance pass
