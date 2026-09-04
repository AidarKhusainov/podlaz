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

require_cmd awk env git grep id mktemp python3 sudo systemctl timeout

EVIDENCE_FILE="${E2E_ARTIFACT_DIR}/remote-client-acceptance.txt"
PROFILE_URI='vless://00000000-0000-0000-0000-000000000077@remote-client.example.net:443?type=tcp&security=tls&encryption=none#remote-client'
PROFILE_ID=""
CONNECTED=false

write_evidence() {
  append_evidence_kv "${EVIDENCE_FILE}" "$1" "$2"
}

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

import_profile() {
  local output error_output
  mask_value "${PROFILE_URI}"
  output="$(mktemp "${E2E_TMP_ROOT}/remote-client-profile-import.stdout.XXXXXX")"
  error_output="$(mktemp "${E2E_TMP_ROOT}/remote-client-profile-import.stderr.XXXXXX")"
  if ! run_ordinary_podlaz 30s profile import "${PROFILE_URI}" >"${output}" 2>"${error_output}"; then
    fail "remote-client profile import failed"
  fi
  PROFILE_ID="$(awk '/^Imported profile:/ {print $3}' "${output}")"
  assert_nonempty "${PROFILE_ID}" "remote-client imported profile id"
  mask_value "${PROFILE_ID}"
  rm -f -- "${output}" "${error_output}"
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
recovery = payload.get("recovery", {})
if recovery.get("candidates"):
    raise SystemExit("unexpected recovery candidates")
if recovery.get("warnings"):
    raise SystemExit("unexpected recovery warnings")
PY
  rm -f -- "${output}"
  write_evidence "recovery_clean_${phase}" pass
}

connect_packaged_runtime() {
  local output error_output
  output="${E2E_ARTIFACT_DIR}/remote-client-connect.stdout"
  error_output="${E2E_ARTIFACT_DIR}/remote-client-connect.stderr"
  if ! run_privileged_podlaz 90s connect --mode proxy-only "${PROFILE_ID}" >"${output}" 2>"${error_output}"; then
    sudo -n journalctl -u podlazd.service -n 200 --no-pager >"${E2E_ARTIFACT_DIR}/remote-client-connect.journal" 2>&1 || true
    fail "remote-client proxy-only connect failed"
  fi
  CONNECTED=true
  write_evidence packaged_proxy_connect pass
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
    fail "ordinary-user podlaz logs --${mode} --since 36h failed"
  fi
  grep -Fx "${header}" "${output}" >/dev/null || fail "ordinary-user podlaz logs --${mode} --since 36h did not render the expected header"
  rm -f -- "${output}" "${error_output}"
  write_evidence "logs_since_36h_${key}_ordinary_user" pass
}

: >"${EVIDENCE_FILE}"
setup_isolated_xdg remote-client-acceptance
verify_package_and_ordinary_identity
assert_recovery_clean baseline
import_profile
connect_packaged_runtime
assert_proxy_publication_consistent

run_privileged_podlaz 60s disconnect >/dev/null 2>&1 || fail "remote-client proxy-only disconnect failed"
CONNECTED=false
assert_recovery_clean after-proxy-disconnect

assert_logs_36h_ordinary_user daemon 'podlaz daemon logs' daemon
assert_logs_36h_ordinary_user core 'podlaz core logs' core
