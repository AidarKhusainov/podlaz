#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"

require_cmd awk git grep mktemp python3 resolvectl runuser sudo systemctl

: "${PODLAZ_E2E_PROFILE_URI:=}"
: "${PODLAZ_E2E_PROFILE_URI_LIST:=}"

if [[ -z "${PODLAZ_E2E_PROFILE_URI}" && -z "${PODLAZ_E2E_PROFILE_URI_LIST}" ]]; then
  fail "PODLAZ_E2E_PROFILE_URI or PODLAZ_E2E_PROFILE_URI_LIST is required"
fi

EVIDENCE_FILE="${E2E_ARTIFACT_DIR}/issue243-acceptance.txt"
PROFILE_ID=""
CONNECTED=false

write_evidence() {
  local key="$1" value="$2"
  case "${key}${value}" in
    *$'\n'*|*$'\r'*) fail "invalid normalized issue 243 evidence" ;;
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

cleanup() {
  local saved=$?
  set +e
  if [[ "${CONNECTED}" == "true" ]]; then
    run_installed_podlaz disconnect >/dev/null 2>&1
  fi
  set -e
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
  version_output="$(mktemp "${E2E_TMP_ROOT}/issue243-version.XXXXXX")"
  /usr/bin/podlaz version >"${version_output}" 2>/dev/null || fail "issue 243 installed CLI version failed"
  grep -F -- "${build_commit}" "${version_output}" >/dev/null || fail "issue 243 installed CLI does not identify the tested commit"
  rm -f -- "${version_output}"
  systemctl is-active --quiet podlazd.service || fail "issue 243 requires the packaged podlazd service"
  write_evidence package_provenance pass
}

import_profile_privately() {
  local uri="$1" output error_output
  output="$(mktemp "${E2E_TMP_ROOT}/issue243-profile-import.stdout.XXXXXX")"
  error_output="$(mktemp "${E2E_TMP_ROOT}/issue243-profile-import.stderr.XXXXXX")"
  if ! run_installed_podlaz profile import "${uri}" >"${output}" 2>"${error_output}"; then
    rm -f -- "${output}" "${error_output}"
    fail "issue 243 profile import failed"
  fi
  PROFILE_ID="$(awk '/^Imported profile:/ {print $3}' "${output}")"
  rm -f -- "${output}" "${error_output}"
  assert_nonempty "${PROFILE_ID}" "issue 243 imported profile id"
  mask_value "${PROFILE_ID}"
  write_evidence profile_import pass
}

assert_active_status() {
  local phase="$1" output
  output="$(mktemp "${E2E_TMP_ROOT}/issue243-${phase}-active-status.XXXXXX")"
  if ! run_installed_podlaz status >"${output}" 2>&1; then
    rm -f -- "${output}"
    fail "${phase}: active status returned non-zero"
  fi
  grep -Fx "Connection: active" "${output}" >/dev/null || fail "${phase}: connection is not active"
  grep -Fx "Transaction: committed" "${output}" >/dev/null || fail "${phase}: TUN transaction is not committed"
  grep -Fx "Stale state: none" "${output}" >/dev/null || fail "${phase}: active status reports stale state"
  grep -Fx "Startup recovery scan: clean for active connection" "${output}" >/dev/null || fail "${phase}: active startup scan is not clean"
  if grep -F "Inspection warnings:" "${output}" >/dev/null; then
    fail "${phase}: active status contains inspection warnings"
  fi
  rm -f -- "${output}"
  write_evidence "active_status_${phase}" pass
}

capture_exact_exit_zero_missing_status() {
  local phase="$1" stdout_file stderr_file exit_code
  stdout_file="$(mktemp "${E2E_TMP_ROOT}/issue243-${phase}-resolved.stdout.XXXXXX")"
  stderr_file="$(mktemp "${E2E_TMP_ROOT}/issue243-${phase}-resolved.stderr.XXXXXX")"

  set +e
  resolvectl status podlaz0 --no-pager >"${stdout_file}" 2>"${stderr_file}"
  exit_code=$?
  set -e

  if ! python3 - "${exit_code}" "${stdout_file}" "${stderr_file}" <<'PY'
import sys

exit_code = int(sys.argv[1])
stdout_path = sys.argv[2]
stderr_path = sys.argv[3]
expected = b'Failed to resolve interface "podlaz0", ignoring: No such device'

with open(stdout_path, "rb") as handle:
    stdout = handle.read()
with open(stderr_path, "rb") as handle:
    stderr = handle.read()

if exit_code != 0:
    raise SystemExit("read-only resolvectl status did not return the required exit-0 envelope")
if stdout != b"":
    raise SystemExit("read-only resolvectl status produced unexpected stdout")
if stderr not in (expected + b"\n", expected + b"\r\n"):
    raise SystemExit("read-only resolvectl status stderr did not match the supported exact missing-device envelope")
PY
  then
    rm -f -- "${stdout_file}" "${stderr_file}"
    fail "${phase}: real resolvectl status did not match the issue 243 byte contract"
  fi

  rm -f -- "${stdout_file}" "${stderr_file}"
  write_evidence "resolved_exit0_missing_${phase}" pass
}

assert_inactive_status() {
  local phase="$1" output
  output="$(mktemp "${E2E_TMP_ROOT}/issue243-${phase}-inactive-status.XXXXXX")"
  if ! run_installed_podlaz status >"${output}" 2>&1; then
    rm -f -- "${output}"
    fail "${phase}: inactive status returned non-zero"
  fi
  grep -Fx "Connection: inactive" "${output}" >/dev/null || fail "${phase}: status is not inactive"
  grep -Fx "Stale state: none" "${output}" >/dev/null || fail "${phase}: stale state is not clean"
  grep -Fx "Startup recovery scan: clean inactive state" "${output}" >/dev/null || fail "${phase}: startup recovery scan is not clean inactive"
  if grep -F "Recovery candidates:" "${output}" >/dev/null || grep -F "Inspection warnings:" "${output}" >/dev/null; then
    fail "${phase}: inactive status still publishes recovery or inspection evidence"
  fi
  rm -f -- "${output}"
  write_evidence "inactive_status_${phase}" pass
}

assert_recover_json_clean() {
  local phase="$1" output
  output="$(mktemp "${E2E_TMP_ROOT}/issue243-${phase}-recover.XXXXXX")"
  if ! run_installed_podlaz recover --json >"${output}" 2>/dev/null; then
    rm -f -- "${output}"
    fail "${phase}: recover --json returned non-zero"
  fi
  if ! python3 - "${output}" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    payload = json.load(handle)
if payload.get("status") != "ok":
    raise SystemExit("recover JSON status is not ok")
if payload.get("warnings"):
    raise SystemExit("top-level recover JSON warnings remain")
recovery = payload.get("recovery")
if not isinstance(recovery, dict):
    raise SystemExit("recover JSON payload is missing")
if recovery.get("candidates"):
    raise SystemExit("recover JSON candidates remain")
if recovery.get("warnings"):
    raise SystemExit("recover JSON inspection warnings remain")
PY
  then
    rm -f -- "${output}"
    fail "${phase}: recover --json is not clean"
  fi
  rm -f -- "${output}"
  write_evidence "recover_json_${phase}" pass
}

run_cycle() {
  local phase="$1"
  if ! run_installed_podlaz connect --mode tun "${PROFILE_ID}" >/dev/null 2>&1; then
    fail "${phase}: TUN connect failed"
  fi
  CONNECTED=true
  assert_active_status "${phase}"

  if ! run_installed_podlaz disconnect >/dev/null 2>&1; then
    fail "${phase}: disconnect failed"
  fi
  CONNECTED=false

  capture_exact_exit_zero_missing_status "${phase}"
  assert_inactive_status "${phase}"
  assert_recover_json_clean "${phase}"
  write_evidence "cycle_${phase}" pass
}

: >"${EVIDENCE_FILE}"
setup_isolated_xdg issue243-package-acceptance
verify_package_provenance

PROFILE_URI="$(first_profile_uri)"
assert_nonempty "${PROFILE_URI}" "issue 243 profile URI"
mask_value "${PROFILE_URI}"
import_profile_privately "${PROFILE_URI}"
unset PROFILE_URI

run_cycle first
run_cycle immediate-reconnect

write_evidence immediate_reconnect pass
write_evidence acceptance pass
