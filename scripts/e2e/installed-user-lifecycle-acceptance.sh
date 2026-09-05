#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"
# shellcheck source=lib/exit_trap.sh
source "${SCRIPT_DIR}/lib/exit_trap.sh"
# shellcheck source=lib/recovery_json.sh
source "${SCRIPT_DIR}/lib/recovery_json.sh"

require_cmd awk env grep id install mktemp pgrep python3 readlink ss stat sudo systemctl timeout

DAEMON_SOCKET="/run/podlaz/podlazd.sock"
FIXTURE_DIR="/run/podlaz-e2e-installed-user"
MODE_FILE="${FIXTURE_DIR}/pkcheck-mode"
DROPIN_DIR="/run/systemd/system/podlazd.service.d"
DROPIN_PATH="${DROPIN_DIR}/installed-user-lifecycle-acceptance.conf"
PROFILE_URI="$(vless_uri installed-user)"
PROFILE_ID=""
CONNECTED=false
FIXTURE_TOUCHED=false

run_user_podlaz() {
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

set_pkcheck_mode() {
  local mode="$1"
  printf '%s\n' "${mode}" | sudo -n tee "${MODE_FILE}" >/dev/null
}

cleanup() {
  local saved=$? cleanup_failed=0
  set +e
  if [[ "${FIXTURE_TOUCHED}" == "true" ]]; then
    set_pkcheck_mode allow >/dev/null 2>&1 || cleanup_failed=1
    if [[ "${CONNECTED}" == "true" ]]; then
      run_user_podlaz 30s disconnect >/dev/null 2>&1 || cleanup_failed=1
      CONNECTED=false
    fi
    sudo -n rm -f -- "${DROPIN_PATH}" >/dev/null 2>&1 || cleanup_failed=1
    sudo -n rm -rf -- "${FIXTURE_DIR}" >/dev/null 2>&1 || cleanup_failed=1
    sudo -n systemctl daemon-reload >/dev/null 2>&1 || cleanup_failed=1
    sudo -n systemctl restart podlazd.service >/dev/null 2>&1 || cleanup_failed=1
  fi
  finish_exit_trap "${saved}" "${cleanup_failed}"
}
trap cleanup EXIT

assert_ordinary_user_socket_boundary() {
  local groups socket_metadata
  (( $(id -u) != 0 )) || fail "installed-user acceptance must not run as root"
  groups="$(id -nG)"
  if grep -qw -- podlaz <<<"${groups}"; then
    fail "installed-user acceptance must not belong to the podlaz group"
  fi

  [[ -S "${DAEMON_SOCKET}" ]] || fail "installed daemon filesystem socket is missing"
  socket_metadata="$(stat -c '%U:%G:%a' /run/podlaz/podlazd.sock)"
  [[ "${socket_metadata}" == "root:podlaz:660" ]] || \
    fail "installed daemon socket metadata is ${socket_metadata}, want root:podlaz:660"

  python3 - "${DAEMON_SOCKET}" <<'PY'
import errno
import socket
import sys

sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
try:
    sock.connect(sys.argv[1])
except OSError as exc:
    if exc.errno not in {errno.EACCES, errno.EPERM}:
        raise
else:
    raise SystemExit("filesystem socket unexpectedly reachable")
finally:
    sock.close()
PY
}

install_authorization_fixture() {
  local pkcheck_tmp dropin_tmp
  pkcheck_tmp="$(mktemp "${E2E_TMP_ROOT}/installed-user-pkcheck.XXXXXX")"
  cat >"${pkcheck_tmp}" <<'SH'
#!/usr/bin/env bash
set -Eeuo pipefail
mode="$(tr -d '[:space:]' <"${PODLAZ_E2E_PKCHECK_MODE_FILE:?}")"
case "${mode}" in
  allow) exit 0 ;;
  deny) exit 1 ;;
  unavailable) exit 2 ;;
  *) exit 2 ;;
esac
SH
  chmod 0755 "${pkcheck_tmp}"

  sudo -n mkdir -p "${FIXTURE_DIR}" "${DROPIN_DIR}"
  sudo -n install -m 0755 "${pkcheck_tmp}" "${FIXTURE_DIR}/pkcheck"
  rm -f -- "${pkcheck_tmp}"
  set_pkcheck_mode unavailable

  dropin_tmp="$(mktemp "${E2E_TMP_ROOT}/installed-user-dropin.XXXXXX")"
  cat >"${dropin_tmp}" <<EOF
[Service]
Environment=PATH=${FIXTURE_DIR}:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
Environment=PODLAZ_E2E_PKCHECK_MODE_FILE=${MODE_FILE}
EOF
  sudo -n install -m 0644 "${dropin_tmp}" "${DROPIN_PATH}"
  rm -f -- "${dropin_tmp}"
  FIXTURE_TOUCHED=true

  sudo -n systemctl daemon-reload
  sudo -n systemctl restart podlazd.service
  sudo -n systemctl is-active --quiet podlazd.service || fail "podlazd.service is not active with the authorization fixture"
}

import_profile_privately() {
  local output error_output
  mask_value "${PROFILE_URI}"
  output="$(mktemp "${E2E_TMP_ROOT}/installed-user-import.stdout.XXXXXX")"
  error_output="$(mktemp "${E2E_TMP_ROOT}/installed-user-import.stderr.XXXXXX")"
  if ! run_user_podlaz 30s profile import "${PROFILE_URI}" >"${output}" 2>"${error_output}"; then
    rm -f -- "${output}" "${error_output}"
    fail "ordinary-user profile import failed"
  fi
  PROFILE_ID="$(awk '/^Imported profile:/ {print $3}' "${output}")"
  rm -f -- "${output}" "${error_output}"
  assert_nonempty "${PROFILE_ID}" "ordinary-user imported profile id"
  mask_value "${PROFILE_ID}"
}

assert_status_disconnected() {
  local output
  output="$(mktemp "${E2E_TMP_ROOT}/installed-user-status-disconnected.XXXXXX")"
  run_user_podlaz 20s status >"${output}" 2>&1 || fail "ordinary-user disconnected status failed"
  grep -Fx 'Status: Disconnected' "${output}" >/dev/null || fail "ordinary-user status is not disconnected"
  rm -f -- "${output}"
}

assert_authorization_unavailable() {
  local output error_output code
  output="$(mktemp "${E2E_TMP_ROOT}/installed-user-connect-unavailable.stdout.XXXXXX")"
  error_output="$(mktemp "${E2E_TMP_ROOT}/installed-user-connect-unavailable.stderr.XXXXXX")"
  set +e
  run_user_podlaz 30s connect --mode proxy-only "${PROFILE_ID}" >"${output}" 2>"${error_output}"
  code=$?
  set -e
  [[ "${code}" == "1" ]] || fail "authorization-unavailable connect returned ${code}, want 1"
  grep -F 'authorization unavailable' "${error_output}" >/dev/null || fail "authorization-unavailable connect did not explain the authorization failure"
  rm -f -- "${output}" "${error_output}"
  assert_status_disconnected
}

listener_present() {
  local endpoint="$1"
  ss -H -ltn | awk '{print $4}' | grep -Fx -- "${endpoint}" >/dev/null
}

wait_for_listener_state() {
  local want="$1" endpoint="$2" attempt
  for attempt in $(seq 1 50); do
    if [[ "${want}" == "present" ]] && listener_present "${endpoint}"; then
      return 0
    fi
    if [[ "${want}" == "absent" ]] && ! listener_present "${endpoint}"; then
      return 0
    fi
    sleep 0.2
  done
  fail "proxy listener ${endpoint} did not become ${want}"
}

assert_connected_proxy() {
  local output attempt code
  output="$(mktemp "${E2E_TMP_ROOT}/installed-user-status-connected.XXXXXX")"
  for attempt in $(seq 1 50); do
    set +e
    run_user_podlaz 20s status >"${output}" 2>&1
    code=$?
    set -e
    if [[ "${code}" == "0" ]] && grep -Fx 'Status: Connected' "${output}" >/dev/null && grep -Fx 'Mode: proxy-only' "${output}" >/dev/null; then
      rm -f -- "${output}"
      return 0
    fi
    sleep 0.2
  done
  cat "${output}" >&2 || true
  rm -f -- "${output}"
  fail "ordinary-user proxy-only connection did not become connected"
}

find_packaged_xray_child() {
  local daemon_pid pid exe
  local matches=()
  daemon_pid="$(systemctl show -p MainPID --value podlazd.service)"
  [[ "${daemon_pid}" =~ ^[1-9][0-9]*$ ]] || fail "podlazd.service has no running MainPID"
  while IFS= read -r pid; do
    [[ "${pid}" =~ ^[1-9][0-9]*$ ]] || continue
    exe="$(sudo -n readlink -f "/proc/${pid}/exe" 2>/dev/null || true)"
    if [[ "${exe}" == "/usr/lib/podlaz/xray" ]]; then
      matches+=("${pid}")
    fi
  done < <(pgrep -P "${daemon_pid}" || true)
  [[ "${#matches[@]}" -eq 1 ]] || fail "expected exactly one supervised packaged Xray child, found ${#matches[@]}"
  printf '%s\n' "${matches[0]}"
}

assert_core_crash_visible() {
  local status_output doctor_output attempt status_code doctor_code observed=false
  status_output="${E2E_ARTIFACT_DIR}/installed-user-core-crash-status.txt"
  doctor_output="${E2E_ARTIFACT_DIR}/installed-user-core-crash-doctor.txt"
  for attempt in $(seq 1 50); do
    set +e
    run_user_podlaz 20s status >"${status_output}" 2>&1
    status_code=$?
    run_user_podlaz 20s doctor >"${doctor_output}" 2>&1
    doctor_code=$?
    set -e
    if [[ "${status_code}" == "3" && "${doctor_code}" == "3" ]] && \
        grep -F 'core exited unexpectedly; inspect podlaz logs --core' "${doctor_output}" >/dev/null; then
      observed=true
      break
    fi
    sleep 0.2
  done
  [[ "${observed}" == "true" ]] || fail "Xray child crash was not published through status/doctor"
  grep -Fx 'Status: Unknown' "${status_output}" >/dev/null || fail "core crash status is not Unknown"
}

assert_recovery_clean() {
  local output
  output="$(mktemp "${E2E_TMP_ROOT}/installed-user-recover.XXXXXX")"
  if ! run_user_podlaz 20s recover --json >"${output}" 2>/dev/null; then
    rm -f -- "${output}"
    fail "ordinary-user recovery inspection failed"
  fi
  if ! assert_clean_recovery_json_file "${output}"; then
    rm -f -- "${output}"
    fail "ordinary-user recovery inspection is not clean"
  fi
  rm -f -- "${output}"
}

setup_isolated_xdg installed-user-lifecycle
assert_ordinary_user_socket_boundary
install_authorization_fixture
import_profile_privately

log "verify packaged ordinary-user authorization boundary"
assert_authorization_unavailable
set_pkcheck_mode allow
wait_for_listener_state absent '127.0.0.1:1080'
wait_for_listener_state absent '127.0.0.1:8080'
run_user_podlaz 60s connect --mode proxy-only "${PROFILE_ID}" >/dev/null 2>&1 || fail "authorized ordinary-user connect failed"
CONNECTED=true
assert_connected_proxy
wait_for_listener_state present '127.0.0.1:1080'
wait_for_listener_state present '127.0.0.1:8080'

log "verify supervised packaged Xray crash convergence"
xray_pid="$(find_packaged_xray_child)"
sudo -n kill -KILL "${xray_pid}"
assert_core_crash_visible
sudo -n systemctl is-active --quiet podlazd.service || fail "podlazd.service stopped after Xray child crash"
wait_for_listener_state absent '127.0.0.1:1080'
wait_for_listener_state absent '127.0.0.1:8080'
run_user_podlaz 60s disconnect >/dev/null 2>&1 || fail "ordinary-user disconnect after Xray crash failed"
CONNECTED=false
assert_status_disconnected
assert_recovery_clean
wait_for_listener_state absent '127.0.0.1:1080'
wait_for_listener_state absent '127.0.0.1:8080'
if sudo -n test -e /run/podlaz/generated/xray.json; then
  fail "generated Xray runtime config remained after crash disconnect"
fi
assert_artifacts_do_not_contain_sensitive_values "installed-user-lifecycle" "${PROFILE_URI}" "${PROFILE_ID}"

log "installed-user lifecycle acceptance completed"
