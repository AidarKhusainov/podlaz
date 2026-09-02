#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"
# shellcheck source=lib/exit_trap.sh
source "${SCRIPT_DIR}/lib/exit_trap.sh"
# shellcheck source=lib/evidence.sh
source "${SCRIPT_DIR}/lib/evidence.sh"
# shellcheck source=lib/package_provenance.sh
source "${SCRIPT_DIR}/lib/package_provenance.sh"
# shellcheck source=lib/profile_input.sh
source "${SCRIPT_DIR}/lib/profile_input.sh"

require_cmd awk env git grep id mktemp python3 sudo systemctl timeout

: "${PODLAZ_E2E_PROFILE_URI:=}"
: "${PODLAZ_E2E_PROFILE_URI_LIST:=}"
if [[ -z "${PODLAZ_E2E_PROFILE_URI}" && -z "${PODLAZ_E2E_PROFILE_URI_LIST}" ]]; then
  fail "PODLAZ_E2E_PROFILE_URI or PODLAZ_E2E_PROFILE_URI_LIST is required"
fi

EVIDENCE_FILE="${E2E_ARTIFACT_DIR}/remote-client-acceptance.txt"
PROFILE_ID=""
CONNECTED=false

write_evidence() {
  append_evidence_kv "${EVIDENCE_FILE}" "$1" "$2"
}

# Deliberately local. This preserves the self-hosted runner's normal login
# identity, including OS-managed supplementary groups; forcing a service-group
# override would manufacture a different journald permission scenario.
run_ordinary_podlaz() {
  local timeout_seconds="$1"
  shift
  timeout --signal=TERM --kill-after=5s "${timeout_seconds}" \
    env \
      LC_ALL=C \
      XDG_CONFIG_HOME="${XDG_CONFIG_HOME}" \
      XDG_STATE_HOME="${XDG_STATE_HOME}" \
      XDG_CACHE_HOME="${XDG_CACHE_HOME}" \
      /usr/bin/podlaz "$@"
}

# Deliberately local. Lifecycle setup is privileged and bounded, unlike the
# shared normal-user installed-client execution contract.
run_privileged_podlaz() {
  local timeout_seconds="$1"
  shift
  timeout --signal=TERM --kill-after=5s "${timeout_seconds}" \
    sudo -n env \
      LC_ALL=C \
      XDG_CONFIG_HOME="${XDG_CONFIG_HOME}" \
      XDG_STATE_HOME="${XDG_STATE_HOME}" \
      XDG_CACHE_HOME="${XDG_CACHE_HOME}" \
      /usr/bin/podlaz "$@"
}

cleanup() {
  local saved=$? cleanup_failed=0
  set +e
  if [[ "${CONNECTED}" == "true" ]]; then
    run_privileged_podlaz 60s disconnect >/dev/null 2>&1 || cleanup_failed=1
    CONNECTED=false
  fi
  if (( saved == 0 && cleanup_failed == 0 )); then
    write_evidence acceptance pass || cleanup_failed=1
  fi
  finish_exit_trap "${saved}" "${cleanup_failed}"
}
trap cleanup EXIT

verify_package_and_ordinary_identity() {
  local build_commit groups
  build_commit="${GITHUB_SHA:-$(git rev-parse HEAD)}"
  assert_installed_podlaz_commit "${build_commit}"
  assert_package_service_active podlazd.service

  if (( $(id -u) == 0 )); then
    fail "remote-client ordinary-user acceptance must not run as root"
  fi
  groups="$(id -nG)"
  if grep -qw -- podlaz <<<"${groups}"; then
    fail "remote-client ordinary-user fixture must not rely on membership in the private podlaz service group"
  fi
  write_evidence package_provenance pass
  write_evidence ordinary_user_without_podlaz_group pass
}

import_profile_privately() {
  local uri output error_output
  uri="$(first_configured_profile_uri)"
  assert_nonempty "${uri}" "remote-client profile URI"
  mask_value "${uri}"
  output="$(mktemp "${E2E_TMP_ROOT}/remote-client-profile-import.stdout.XXXXXX")"
  error_output="$(mktemp "${E2E_TMP_ROOT}/remote-client-profile-import.stderr.XXXXXX")"
  if ! run_ordinary_podlaz 30s profile import "${uri}" >"${output}" 2>"${error_output}"; then
    rm -f -- "${output}" "${error_output}"
    fail "remote-client profile import failed"
  fi
  PROFILE_ID="$(awk '/^Imported profile:/ {print $3}' "${output}")"
  rm -f -- "${output}" "${error_output}"
  assert_nonempty "${PROFILE_ID}" "remote-client imported profile id"
  mask_value "${PROFILE_ID}"
  write_evidence profile_import pass
}

assert_recovery_clean() {
  local phase="$1" output
  output="$(mktemp "${E2E_TMP_ROOT}/remote-client-${phase}-recover.XXXXXX")"
  run_ordinary_podlaz 20s recover --json >"${output}" 2>/dev/null || fail "${phase}: read-only recovery inspection failed"
  python3 - "${output}" <<'PY'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as handle:
    payload = json.load(handle)
if payload.get("recovery", {}).get("candidates"):
    raise SystemExit("unexpected recovery candidates")
PY
  rm -f -- "${output}"
  write_evidence "recovery_clean_${phase}" pass
}

assert_proxy_publication_consistent() {
  local status_output doctor_output
  status_output="$(mktemp "${E2E_TMP_ROOT}/remote-client-proxy-status.XXXXXX")"
  doctor_output="$(mktemp "${E2E_TMP_ROOT}/remote-client-proxy-doctor.XXXXXX")"

  run_ordinary_podlaz 20s status >"${status_output}" 2>&1 || fail "active proxy-only status returned non-zero"
  grep -Fx 'Connection: active' "${status_output}" >/dev/null || fail "proxy-only connection is not active"
  grep -Fx 'Mode: proxy-only' "${status_output}" >/dev/null || fail "proxy-only mode was not published"
  grep -Fx 'Stale state: none' "${status_output}" >/dev/null || fail "active proxy-only status reports stale state"
  grep -Fx 'Startup recovery scan: clean for active connection' "${status_output}" >/dev/null || fail "active proxy-only startup scan is not clean"

  run_ordinary_podlaz 30s doctor >"${doctor_output}" 2>&1 || fail "active proxy-only doctor returned non-zero"
  grep -F 'managed resources match active lifecycle' "${doctor_output}" >/dev/null || fail "active proxy-only doctor does not accept exact owned resources"
  grep -F 'startup recovery scan: clean for active connection' "${doctor_output}" >/dev/null || fail "active proxy-only doctor startup scan is not clean"
  if grep -F '[WARN] stale-resources:' "${doctor_output}" >/dev/null; then
    fail "active proxy-only doctor reports false stale resources"
  fi

  rm -f -- "${status_output}" "${doctor_output}"
  assert_recovery_clean active-proxy
  write_evidence proxy_status_doctor_recover_consistent pass
}

assert_logs_36h_ordinary_user() {
  local mode="$1" header="$2" key="$3" output error_output
  output="$(mktemp "${E2E_TMP_ROOT}/remote-client-logs-${mode}.stdout.XXXXXX")"
  error_output="$(mktemp "${E2E_TMP_ROOT}/remote-client-logs-${mode}.stderr.XXXXXX")"
  if ! run_ordinary_podlaz 30s logs "--${mode}" --since 36h >"${output}" 2>"${error_output}"; then
    rm -f -- "${output}" "${error_output}"
    fail "ordinary-user podlaz logs --${mode} --since 36h failed"
  fi
  grep -Fx "${header}" "${output}" >/dev/null || {
    rm -f -- "${output}" "${error_output}"
    fail "ordinary-user podlaz logs --${mode} --since 36h did not render the expected header"
  }
  rm -f -- "${output}" "${error_output}"
  write_evidence "logs_since_36h_${key}_ordinary_user" pass
}

: >"${EVIDENCE_FILE}"
setup_isolated_xdg remote-client-acceptance
verify_package_and_ordinary_identity
assert_recovery_clean baseline
import_profile_privately

# Lifecycle setup is privileged so this headless self-hosted acceptance does not
# accidentally test polkit active-session policy. All read-only client paths
# below use the runner's unchanged ordinary login identity.
if ! run_privileged_podlaz 90s connect --mode proxy-only "${PROFILE_ID}" >/dev/null 2>&1; then
  fail "remote-client proxy-only connect failed"
fi
CONNECTED=true
assert_proxy_publication_consistent

if ! run_privileged_podlaz 60s disconnect >/dev/null 2>&1; then
  fail "remote-client proxy-only disconnect failed"
fi
CONNECTED=false
assert_recovery_clean after-proxy-disconnect

assert_logs_36h_ordinary_user daemon 'podlaz daemon logs' daemon
assert_logs_36h_ordinary_user core 'podlaz core logs' core
