#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"
# shellcheck source=lib/evidence.sh
source "${SCRIPT_DIR}/lib/evidence.sh"
# shellcheck source=lib/package_provenance.sh
source "${SCRIPT_DIR}/lib/package_provenance.sh"

require_cmd env git grep id mktemp python3 systemctl timeout

EVIDENCE_FILE="${E2E_ARTIFACT_DIR}/remote-client-acceptance.txt"

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

assert_recovery_clean() {
  local output
  output="$(mktemp "${E2E_TMP_ROOT}/remote-client-recover.XXXXXX")"
  run_ordinary_podlaz 20s recover --json >"${output}" 2>/dev/null || fail "read-only recovery inspection failed"
  python3 - "${output}" <<'PY'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as handle:
    recovery = json.load(handle).get("recovery", {})
if recovery.get("candidates"):
    raise SystemExit("unexpected recovery candidates")
if recovery.get("warnings"):
    raise SystemExit("unexpected recovery warnings")
PY
  rm -f -- "${output}"
  write_evidence recovery_clean pass
}

assert_status_and_doctor() {
  local status_output doctor_output doctor_code=0
  status_output="${E2E_ARTIFACT_DIR}/remote-client-status.txt"
  doctor_output="${E2E_ARTIFACT_DIR}/remote-client-doctor.txt"

  run_ordinary_podlaz 20s status >"${status_output}" 2>&1 || fail "ordinary-user status failed"
  grep -Fx 'Connection: inactive' "${status_output}" >/dev/null || fail "ordinary-user status is not inactive"
  grep -Fx 'Stale state: none' "${status_output}" >/dev/null || fail "ordinary-user status reports stale state"

  set +e
  run_ordinary_podlaz 30s doctor >"${doctor_output}" 2>&1
  doctor_code=$?
  set -e
  if [[ "${doctor_code}" != "0" && "${doctor_code}" != "3" ]]; then
    fail "ordinary-user doctor failed with unexpected exit code ${doctor_code}"
  fi
  write_evidence status_doctor_readable pass
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
assert_recovery_clean
assert_status_and_doctor
assert_logs_36h_ordinary_user daemon 'podlaz daemon logs' daemon
assert_logs_36h_ordinary_user core 'podlaz core logs' core
write_evidence acceptance pass
