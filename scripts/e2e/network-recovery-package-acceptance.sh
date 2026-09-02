#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"

require_cmd apt awk curl dpkg find getent grep mktemp python3 runuser sed sleep sudo systemctl timeout

: "${PODLAZ_E2E_BASE_DEB:=}"
: "${PODLAZ_E2E_PROFILE_URI:=}"
: "${PODLAZ_E2E_PROFILE_URI_LIST:=}"
: "${PODLAZ_E2E_DNS_CHECK_HOST:=github.com}"
: "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL:=https://api.ipify.org}"
: "${PODLAZ_DEB_ARCH:=$(dpkg --print-architecture)}"

[[ -n "${PODLAZ_E2E_BASE_DEB}" ]] || fail "PODLAZ_E2E_BASE_DEB is required"
[[ -f "${PODLAZ_E2E_BASE_DEB}" ]] || fail "released baseline package is missing"
if [[ -z "${PODLAZ_E2E_PROFILE_URI}" && -z "${PODLAZ_E2E_PROFILE_URI_LIST}" ]]; then
  fail "PODLAZ_E2E_PROFILE_URI or PODLAZ_E2E_PROFILE_URI_LIST is required"
fi
if [[ "${PODLAZ_DEB_ARCH}" != "$(dpkg --print-architecture)" ]]; then
  fail "issue 259 package acceptance requires a native .deb"
fi

DEV_DEB="dist/podlaz_0.0.0~dev-1_linux_${PODLAZ_DEB_ARCH}.deb"
DAEMON_SOCKET="/run/podlaz/podlazd.sock"
CONTINUATION_PATH="/run/podlaz/network-session-continuation.json"
TRANSACTION_DIR="/run/podlaz/transactions"
HOOK_DIR="/run/podlaz/issue259-e2e"
OVERRIDE_DIR="/etc/systemd/system/podlazd.service.d"
OVERRIDE_PATH="${OVERRIDE_DIR}/99-issue259-e2e.conf"
EVIDENCE="${E2E_ARTIFACT_DIR}/issue259-acceptance.txt"

ACTIVE_CONNECTION=0
CANDIDATE_INSTALLED=0
PROFILE_ID=""

mask_multiline_sensitive() {
  local value="${1:-}"
  [[ -n "${value}" ]] || return 0
  mask_value "${value}"
  while IFS= read -r line; do
    [[ -n "${line}" ]] && mask_value "${line}"
  done <<<"${value}"
}
for sensitive in "${PODLAZ_E2E_PROFILE_URI}" "${PODLAZ_E2E_PROFILE_URI_LIST}"; do
  mask_multiline_sensitive "${sensitive}"
done

write_evidence() {
  local key="$1"
  case "${key}" in
    *[!A-Za-z0-9_.-]*) fail "invalid normalized issue 259 evidence key" ;;
  esac
  printf '%s=pass\n' "${key}" >>"${EVIDENCE}"
}

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

run_installed_podlaz() {
  sudo -n runuser -u "$(id -un)" -g podlaz -- env \
    XDG_CONFIG_HOME="${XDG_CONFIG_HOME}" \
    XDG_STATE_HOME="${XDG_STATE_HOME}" \
    XDG_CACHE_HOME="${XDG_CACHE_HOME}" \
    /usr/bin/podlaz "$@"
}

wait_for_daemon_socket() {
  local attempt
  for attempt in $(seq 1 200); do
    if [[ -S "${DAEMON_SOCKET}" ]] && sudo -n systemctl is-active --quiet podlazd.service; then
      return 0
    fi
    sleep 0.1
  done
  fail "podlazd.service did not become ready"
}

daemon_status_matches() {
  local expected_connection="$1" expected_tun="$2" output
  output="$(mktemp "${E2E_TMP_ROOT}/issue259-status.XXXXXX")"
  if ! sudo -n curl --fail --silent --show-error --max-time 5 \
      --unix-socket "${DAEMON_SOCKET}" http://localhost/v1/status >"${output}" 2>/dev/null; then
    rm -f -- "${output}"
    return 1
  fi
  if python3 - "${output}" "${expected_connection}" "${expected_tun}" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    status = json.load(handle)
if status.get("connection") != sys.argv[2]:
    raise SystemExit(1)
if status.get("tun") != sys.argv[3]:
    raise SystemExit(1)
PY
  then
    rm -f -- "${output}"
    return 0
  fi
  rm -f -- "${output}"
  return 1
}

wait_for_active_tun() {
  local phase="$1" attempt
  wait_for_daemon_socket
  for attempt in $(seq 1 300); do
    if daemon_status_matches active active; then
      write_evidence "${phase}"
      return 0
    fi
    sleep 0.2
  done
  fail "${phase}: TUN session did not return active"
}

wait_for_inactive() {
  local phase="$1" attempt
  wait_for_daemon_socket
  for attempt in $(seq 1 150); do
    if daemon_status_matches inactive disabled; then
      write_evidence "${phase}"
      return 0
    fi
    sleep 0.2
  done
  fail "${phase}: daemon did not remain disconnected"
}

run_private_profile_import() {
  local uri out err
  uri="$(first_profile_uri)"
  [[ -n "${uri}" ]] || fail "no profile URI available"
  out="$(mktemp "${E2E_TMP_ROOT}/issue259-import.stdout.XXXXXX")"
  err="$(mktemp "${E2E_TMP_ROOT}/issue259-import.stderr.XXXXXX")"
  if ! run_installed_podlaz profile import "${uri}" >"${out}" 2>"${err}"; then
    rm -f -- "${out}" "${err}"
    fail "released-package profile import failed"
  fi
  PROFILE_ID="$(awk '/^Imported profile:/ {print $3}' "${out}")"
  [[ -n "${PROFILE_ID}" ]] || {
    rm -f -- "${out}" "${err}"
    fail "released-package profile import returned no profile ID"
  }
  mask_multiline_sensitive "${PROFILE_ID}"
  rm -f -- "${out}" "${err}"
}

connect_once_on_released_package() {
  local out err
  out="$(mktemp "${E2E_TMP_ROOT}/issue259-connect.stdout.XXXXXX")"
  err="$(mktemp "${E2E_TMP_ROOT}/issue259-connect.stderr.XXXXXX")"
  if ! run_installed_podlaz connect --mode tun "${PROFILE_ID}" >"${out}" 2>"${err}"; then
    rm -f -- "${out}" "${err}"
    fail "released-package TUN connect failed"
  fi
  rm -f -- "${out}" "${err}"
  ACTIVE_CONNECTION=1
  wait_for_active_tun released_package_connected
}

# Setup and cleanup may explicitly start the service because they are creating or
# restoring the test fixture, not validating candidate package replacement.
install_setup_package() {
  local package_path="$1"
  sudo -n apt install --allow-downgrades -y "${package_path}" >/dev/null
  sudo -n systemctl daemon-reload >/dev/null
  sudo -n systemctl start podlazd.service >/dev/null
  wait_for_daemon_socket
}

# Candidate installation must stand on the package's own service lifecycle. No
# daemon-reload/start/restart repair is allowed here: postinstall must replace
# the active daemon and leave the service usable itself.
install_candidate_package() {
  local package_path="$1" previous_pid="$2" current_pid
  sudo -n apt install --allow-downgrades -y "${package_path}" >/dev/null
  CANDIDATE_INSTALLED=1
  if ! sudo -n systemctl is-active --quiet podlazd.service; then
    fail "candidate package installation did not leave podlazd.service active"
  fi
  current_pid="$(main_pid)"
  if ! [[ "${current_pid}" =~ ^[0-9]+$ ]] || (( current_pid <= 1 )); then
    fail "candidate package installation left an invalid daemon MainPID"
  fi
  if [[ "${current_pid}" == "${previous_pid}" ]]; then
    fail "candidate package installation did not replace the released daemon process"
  fi
  wait_for_daemon_socket
  write_evidence candidate_package_replaced_daemon
}

main_pid() {
  sudo -n systemctl show -p MainPID --value podlazd.service | tr -d '[:space:]'
}

wait_for_new_main_pid() {
  local previous="$1" attempt current
  for attempt in $(seq 1 300); do
    current="$(main_pid)"
    if [[ "${current}" =~ ^[0-9]+$ ]] && (( current > 1 )) && [[ "${current}" != "${previous}" ]] && sudo -n systemctl is-active --quiet podlazd.service; then
      return 0
    fi
    sleep 0.1
  done
  fail "podlazd.service did not replace the daemon process"
}

assert_exact_rolling_back_authority() {
  sudo -n python3 - "${TRANSACTION_DIR}" <<'PY'
import glob
import json
import os
import sys

paths = glob.glob(os.path.join(sys.argv[1], "*.json"))
found = False
for path in paths:
    with open(path, encoding="utf-8") as handle:
        tx = json.load(handle)
    if tx.get("owner") != "podlaz" or tx.get("state") != "rolling_back":
        continue
    rollback = tx.get("rollback") or {}
    owned = False
    for key in (
        "tun_addresses", "routes", "policy_rules", "dns", "nftables",
        "generated_configs", "child_processes",
    ):
        values = rollback.get(key) or []
        if any(isinstance(item, dict) and item.get("owner") == "podlaz" for item in values):
            owned = True
            break
    if owned:
        found = True
        break
if not found:
    raise SystemExit("no exact transaction-owned rolling_back authority found")
PY
  write_evidence forced_rollback_authority_persisted
}

wait_for_file() {
  local path="$1" phase="$2" attempt
  for attempt in $(seq 1 400); do
    if sudo -n test -f "${path}"; then
      return 0
    fi
    sleep 0.1
  done
  fail "${phase}: marker did not appear"
}

install_rollback_pause_override() {
  sudo -n mkdir -p "${OVERRIDE_DIR}"
  sudo -n tee "${OVERRIDE_PATH}" >/dev/null <<EOF
[Service]
Environment=PODLAZ_E2E_TUN_ROLLBACK_PAUSE=true
Environment=PODLAZ_E2E_TUN_ROLLBACK_PAUSE_DIR=${HOOK_DIR}
Environment=PODLAZ_E2E_TUN_ROLLBACK_PAUSE_TIMEOUT_SECONDS=120
EOF
  sudo -n systemctl daemon-reload >/dev/null
}

remove_rollback_pause_override() {
  sudo -n rm -f -- "${OVERRIDE_PATH}"
  sudo -n systemctl daemon-reload >/dev/null 2>&1 || true
}

force_kill_inside_durable_rollback() {
  local old_pid restart_log restart_pid
  install_rollback_pause_override

  # Restart once so the replacement daemon inherits the gated E2E hook. This
  # restart itself remains an ordinary continuation scenario because the old
  # daemon does not have the hook environment.
  sudo -n systemctl restart podlazd.service >/dev/null
  wait_for_active_tun hook_enabled_restart_reconnected

  sudo -n rm -rf -- "${HOOK_DIR}"
  sudo -n mkdir -m 0700 "${HOOK_DIR}"
  sudo -n sh -c 'umask 077; printf "%s\n" armed > "$1"' sh "${HOOK_DIR}/rollback-pause.arm"

  old_pid="$(main_pid)"
  if ! [[ "${old_pid}" =~ ^[0-9]+$ ]] || (( old_pid <= 1 )); then
    fail "invalid daemon PID before forced rollback interruption"
  fi
  restart_log="$(mktemp "${E2E_TMP_ROOT}/issue259-forced-restart.XXXXXX")"
  sudo -n systemctl restart podlazd.service >"${restart_log}" 2>&1 &
  restart_pid=$!

  wait_for_file "${HOOK_DIR}/rollback-pause.ready" forced_rollback_pause
  assert_exact_rolling_back_authority
  sudo -n kill -KILL "${old_pid}"

  set +e
  wait "${restart_pid}"
  local restart_code=$?
  set -e
  rm -f -- "${restart_log}"
  if [[ "${restart_code}" != "0" ]]; then
    # A concurrent SIGKILL may make the original systemctl restart client
    # observe a failed stop job even though Restart=on-failure starts the next
    # daemon. Product success is determined by the converged service/session.
    sudo -n systemctl reset-failed podlazd.service >/dev/null 2>&1 || true
    sudo -n systemctl start podlazd.service >/dev/null 2>&1 || true
  fi

  wait_for_new_main_pid "${old_pid}"
  wait_for_active_tun forced_rollback_crash_recovered
  remove_rollback_pause_override
}

check_direct_connectivity() {
  getent hosts "${PODLAZ_E2E_DNS_CHECK_HOST}" >/dev/null 2>&1 || fail "ordinary DNS did not recover after explicit service stop"
  curl -4 -fsS --max-time 30 "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL}" >/dev/null || fail "ordinary IPv4 egress did not recover after explicit service stop"
  write_evidence ordinary_network_after_explicit_stop
}

restore_candidate() {
  [[ -f "${DEV_DEB}" ]] || return 1
  install_setup_package "./${DEV_DEB}" || return 1
  CANDIDATE_INSTALLED=1
  if daemon_status_matches active active; then
    run_installed_podlaz disconnect >/dev/null 2>&1 || return 1
  fi
  return 0
}

cleanup() {
  local code=$? cleanup_code=0
  remove_rollback_pause_override
  sudo -n rm -rf -- "${HOOK_DIR}" >/dev/null 2>&1 || true
  if [[ "${ACTIVE_CONNECTION}" == "1" ]] && [[ -x /usr/bin/podlaz ]]; then
    run_installed_podlaz disconnect >/dev/null 2>&1 || true
    ACTIVE_CONNECTION=0
  fi
  if [[ "${CANDIDATE_INSTALLED}" != "1" ]]; then
    restore_candidate || cleanup_code=1
  fi
  if [[ "${code}" == "0" && "${cleanup_code}" != "0" ]]; then
    code="${cleanup_code}"
  fi
  exit "${code}"
}
trap cleanup EXIT

setup_isolated_xdg "issue259-package"
: >"${EVIDENCE}"

[[ -f "${DEV_DEB}" ]] || fail "candidate package is missing: ${DEV_DEB}"

# Enter the lower released package from a deliberately disconnected service
# boundary so the only connection action in this acceptance belongs to it.
sudo -n systemctl stop podlazd.service >/dev/null 2>&1 || true
install_setup_package "${PODLAZ_E2E_BASE_DEB}"
run_private_profile_import
connect_once_on_released_package

# Upgrade to the PR package while the released TUN session is active. No CLI
# connect or service-start repair is issued after this point.
candidate_previous_pid="$(main_pid)"
if ! [[ "${candidate_previous_pid}" =~ ^[0-9]+$ ]] || (( candidate_previous_pid <= 1 )); then
  fail "invalid released daemon MainPID before candidate package upgrade"
fi
install_candidate_package "./${DEV_DEB}" "${candidate_previous_pid}"
wait_for_active_tun released_to_candidate_upgrade_reconnected

# Graceful restart must preserve intent and automatically converge/reconnect.
sudo -n systemctl restart podlazd.service >/dev/null
wait_for_active_tun graceful_restart_reconnected

# Unexpected main-process death must be recovered by Restart=on-failure.
old_pid="$(main_pid)"
if ! [[ "${old_pid}" =~ ^[0-9]+$ ]] || (( old_pid <= 1 )); then
  fail "invalid daemon PID before crash test"
fi
sudo -n kill -KILL "${old_pid}"
wait_for_new_main_pid "${old_pid}"
wait_for_active_tun daemon_crash_reconnected

# Kill the daemon after it has durably recorded rolling_back but before host
# cleanup begins. The next daemon must use that exact authority and reconnect.
force_kill_inside_durable_rollback

# Explicit service stop is a different intent: disarm before teardown, restore
# ordinary networking, and remain disconnected after a later manual start.
sudo -n systemctl stop podlazd.service >/dev/null
ACTIVE_CONNECTION=0
if sudo -n test -e "${CONTINUATION_PATH}"; then
  fail "explicit service stop left reconnect continuation armed"
fi
write_evidence explicit_stop_disarmed_continuation
check_direct_connectivity
sudo -n rm -rf -- "${HOOK_DIR}" >/dev/null 2>&1 || true
sudo -n systemctl start podlazd.service >/dev/null
wait_for_inactive explicit_stop_then_start_stays_disconnected

assert_artifacts_do_not_contain_sensitive_values \
  issue259-package \
  "${PODLAZ_E2E_PROFILE_URI}" \
  "${PODLAZ_E2E_PROFILE_URI_LIST}" \
  "${PROFILE_ID}"

write_evidence issue259_acceptance_complete
log "issue 259 installed-package acceptance completed"
