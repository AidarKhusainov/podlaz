#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"

require_cmd apt find getent grep ip nft python3 resolvectl seq sleep sudo systemctl timeout tr

: "${PODLAZ_E2E_PURGE_PACKAGE:=true}"

HOOK_DIR="/run/podlaz/e2e-tun-hooks"
HOOK_DROPIN="/run/systemd/system/podlazd.service.d/e2e-tun-hooks.conf"
DAEMON_SOCKET="/run/podlaz/podlazd.sock"
CLEANUP_XDG="${E2E_TMP_ROOT}/tun-package-cleanup-xdg"
EVIDENCE="${E2E_ARTIFACT_DIR}/teardown-evidence.txt"

FOREIGN_NFT_FAMILY="inet"
FOREIGN_NFT_TABLE="podlaz_e2e_foreign_guard"
FOREIGN_ROUTE_TABLE="42424"
FOREIGN_RULE_PRIORITY="42424"
FOREIGN_DNS_LINK="podlaz-e2e-dns0"
FOREIGN_SERVICE="podlaz-e2e-foreign.service"

mkdir -p "${CLEANUP_XDG}/config" "${CLEANUP_XDG}/state" "${CLEANUP_XDG}/cache"
: >"${EVIDENCE}"

record_cleanup_evidence() {
  local key="$1" value="$2"
  case "${key}${value}" in
    *$'\n'*|*$'\r'*) fail "invalid cleanup evidence" ;;
  esac
  printf '%s=%s\n' "${key}" "${value}" >>"${EVIDENCE}"
}

clear_tun_hook() {
  sudo -n rm -f -- "${HOOK_DROPIN}" >/dev/null 2>&1 || true
  sudo -n rm -rf -- "${HOOK_DIR}" >/dev/null 2>&1 || true
  sudo -n systemctl daemon-reload >/dev/null 2>&1 || true
}

wait_for_daemon_socket_bounded() {
  local attempt
  for attempt in $(seq 1 100); do
    [[ -S "${DAEMON_SOCKET}" ]] && return 0
    sleep 0.1
  done
  return 1
}

attempt_daemon_recovery() {
  record_cleanup_evidence recovery_attempted true
  if [[ ! -x /usr/bin/podlaz ]] || ! getent group podlaz >/dev/null 2>&1; then
    record_cleanup_evidence recovery_available false
    return 0
  fi
  record_cleanup_evidence recovery_available true

  if ! timeout --signal=TERM --kill-after=5s 30s sudo -n systemctl restart podlazd.service >/dev/null 2>&1; then
    record_cleanup_evidence recovery_result daemon_restart_failed
    return 0
  fi
  if ! wait_for_daemon_socket_bounded; then
    record_cleanup_evidence recovery_result socket_timeout
    return 0
  fi
  if timeout --signal=TERM --kill-after=5s 30s \
    sudo -n -u "$(id -un)" -g podlaz env \
    XDG_CONFIG_HOME="${CLEANUP_XDG}/config" \
    XDG_STATE_HOME="${CLEANUP_XDG}/state" \
    XDG_CACHE_HOME="${CLEANUP_XDG}/cache" \
    /usr/bin/podlaz recover --execute --yes >/dev/null 2>&1; then
    record_cleanup_evidence recovery_result completed
  else
    record_cleanup_evidence recovery_result failed_fallback_required
  fi
}

owned_xray_pids() {
  local cmdline pid proc
  for proc in /proc/[0-9]*/cmdline; do
    [[ -r "${proc}" ]] || continue
    cmdline="$(tr '\0' ' ' <"${proc}" 2>/dev/null || true)"
    [[ "${cmdline}" == *"/usr/lib/podlaz/xray"* && "${cmdline}" == *"/run/podlaz/generated/"* ]] || continue
    pid="${proc#/proc/}"
    pid="${pid%/cmdline}"
    printf '%s\n' "${pid}"
  done
}

stop_owned_xray() {
  local pid
  local -a pids=()
  mapfile -t pids < <(owned_xray_pids)
  if [[ "${#pids[@]}" -eq 0 ]]; then
    return 0
  fi
  for pid in "${pids[@]}"; do
    sudo -n kill -TERM "${pid}" >/dev/null 2>&1 || true
  done
  sleep 1
  for pid in "${pids[@]}"; do
    sudo -n kill -KILL "${pid}" >/dev/null 2>&1 || true
  done
}

delete_owned_policy_rules() {
  local family priority
  for family in -4 -6; do
    for priority in 9999 10000; do
      while sudo -n ip "${family}" rule del priority "${priority}" >/dev/null 2>&1; do
        :
      done
    done
  done
}

fallback_cleanup() {
  record_cleanup_evidence fallback_cleanup_attempted true
  timeout --signal=TERM --kill-after=5s 20s sudo -n systemctl stop podlazd.service >/dev/null 2>&1 || true
  stop_owned_xray
  sudo -n resolvectl revert podlaz0 >/dev/null 2>&1 || true
  sudo -n nft delete table inet podlaz >/dev/null 2>&1 || true
  if sudo -n python3 "${SCRIPT_DIR}/tun-package-fallback-routes.py" /run/podlaz/transactions; then
    record_cleanup_evidence fallback_routes_removed true
  else
    record_cleanup_evidence fallback_routes_removed false
    return 1
  fi
  delete_owned_policy_rules
  sudo -n ip -4 route flush table 51820 >/dev/null 2>&1 || true
  sudo -n ip -6 route flush table 51820 >/dev/null 2>&1 || true
  sudo -n ip link del dev podlaz0 >/dev/null 2>&1 || true
  sudo -n rm -rf -- /run/podlaz/generated /run/podlaz/transactions >/dev/null 2>&1 || true
}

cleanup_e2e_sentinels() {
  sudo -n systemctl stop "${FOREIGN_SERVICE}" >/dev/null 2>&1 || true
  sudo -n systemctl reset-failed "${FOREIGN_SERVICE}" >/dev/null 2>&1 || true
  sudo -n resolvectl revert "${FOREIGN_DNS_LINK}" >/dev/null 2>&1 || true
  sudo -n ip link del dev "${FOREIGN_DNS_LINK}" >/dev/null 2>&1 || true
  sudo -n ip -4 rule del priority "${FOREIGN_RULE_PRIORITY}" >/dev/null 2>&1 || true
  sudo -n ip -4 route flush table "${FOREIGN_ROUTE_TABLE}" >/dev/null 2>&1 || true
  sudo -n nft delete table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}" >/dev/null 2>&1 || true
  record_cleanup_evidence e2e_sentinels_removed true
}

purge_package() {
  if [[ "${PODLAZ_E2E_PURGE_PACKAGE}" != "true" ]]; then
    record_cleanup_evidence package_purged false
    return 0
  fi
  timeout --signal=TERM --kill-after=10s 90s sudo -n apt purge -y podlaz >/dev/null 2>&1 || true
  if command -v deb-systemd-helper >/dev/null 2>&1; then
    sudo -n deb-systemd-helper purge podlazd.service >/dev/null 2>&1 || true
  fi
  sudo -n systemctl daemon-reload >/dev/null 2>&1 || true
  sudo -n systemctl reset-failed podlazd.service >/dev/null 2>&1 || true
  record_cleanup_evidence package_purged true
}

assert_cleanup_complete() {
  local output
  if sudo -n ip link show dev podlaz0 >/dev/null 2>&1; then
    fail "teardown: podlaz0 still exists"
  fi
  output="$(sudo -n ip -4 route show table 51820 2>/dev/null || true)"
  [[ -z "${output//[[:space:]]/}" ]] || fail "teardown: IPv4 table 51820 is not empty"
  output="$(sudo -n ip -6 route show table 51820 2>/dev/null || true)"
  [[ -z "${output//[[:space:]]/}" ]] || fail "teardown: IPv6 table 51820 is not empty"
  if sudo -n ip -4 rule show 2>/dev/null | grep -E '(^|[[:space:]])(9999|10000):|lookup (podlaz|51820)([[:space:]]|$)' >/dev/null; then
    fail "teardown: podlaz IPv4 policy rule remains"
  fi
  if sudo -n ip -6 rule show 2>/dev/null | grep -E '(^|[[:space:]])(9999|10000):|lookup (podlaz|51820)([[:space:]]|$)' >/dev/null; then
    fail "teardown: podlaz IPv6 policy rule remains"
  fi
  if sudo -n resolvectl status --no-pager 2>/dev/null | grep -E '^Link [0-9]+ \(podlaz0\)$' >/dev/null; then
    fail "teardown: systemd-resolved still has podlaz0"
  fi
  if sudo -n nft list table inet podlaz >/dev/null 2>&1; then
    fail "teardown: inet podlaz still exists"
  fi
  if [[ -n "$(owned_xray_pids)" ]]; then
    fail "teardown: transaction-owned Xray process remains"
  fi
  if sudo -n test -d /run/podlaz/generated && sudo -n find /run/podlaz/generated -mindepth 1 -print -quit | grep -q .; then
    fail "teardown: generated runtime config remains"
  fi
  if sudo -n test -d /run/podlaz/transactions && sudo -n find /run/podlaz/transactions -mindepth 1 -print -quit | grep -q .; then
    fail "teardown: transaction metadata remains"
  fi
  if systemctl is-active --quiet podlazd.service; then
    fail "teardown: podlazd.service is still active"
  fi
  if sudo -n ip link show dev "${FOREIGN_DNS_LINK}" >/dev/null 2>&1; then
    fail "teardown: E2E DNS sentinel link remains"
  fi
  if sudo -n nft list table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}" >/dev/null 2>&1; then
    fail "teardown: E2E nftables sentinel remains"
  fi
  if systemctl is-active --quiet "${FOREIGN_SERVICE}"; then
    fail "teardown: E2E service sentinel remains"
  fi
  record_cleanup_evidence cleanup_assertions pass
}

clear_tun_hook
attempt_daemon_recovery
fallback_cleanup
cleanup_e2e_sentinels
purge_package
assert_cleanup_complete
