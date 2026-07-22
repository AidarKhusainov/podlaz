#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"
# shellcheck source=lib/process_lifecycle.sh
source "${SCRIPT_DIR}/lib/process_lifecycle.sh"

: "${PODLAZ_E2E_PURGE_PACKAGE:=true}"
: "${PODLAZ_E2E_DNS_CHECK_HOST:=github.com}"
: "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL:=https://api.ipify.org}"

HOOK_DIR="/run/podlaz/e2e-tun-hooks"
HOOK_DROPIN="/run/systemd/system/podlazd.service.d/e2e-tun-hooks.conf"
DAEMON_SOCKET="/run/podlaz/podlazd.sock"
TRANSACTION_DIR="/run/podlaz/transactions"
GENERATED_DIR="/run/podlaz/generated"
CLEANUP_XDG="${E2E_TMP_ROOT}/tun-package-cleanup-xdg"
EVIDENCE="${E2E_ARTIFACT_DIR}/teardown-evidence.txt"
ROLLBACK_MANIFEST="${E2E_TMP_ROOT}/tun-package-rollback-network.json"
FALLBACK_NETWORK_HELPER="${SCRIPT_DIR}/tun-package-fallback-network.py"
ROLLBACK_METADATA_VALID=false

FOREIGN_NFT_FAMILY="inet"
FOREIGN_NFT_TABLE="podlaz_e2e_foreign_guard"
FOREIGN_ROUTE_TABLE="42424"
FOREIGN_ROUTE_CIDR="198.51.100.254/32"
FOREIGN_RULE_PRIORITY="42424"
FOREIGN_DNS_LINK="podlaz-e2e-dns0"
FOREIGN_SERVICE="podlaz-e2e-foreign.service"

PROCESS_USE_SUDO=true
mkdir -p "${CLEANUP_XDG}/config" "${CLEANUP_XDG}/state" "${CLEANUP_XDG}/cache"
: >"${EVIDENCE}"

cleanup_error() {
  printf 'ERROR: %s\n' "$*" >&2
  if [[ "${GITHUB_ACTIONS:-}" == "true" ]]; then
    printf '::error::%s\n' "$*" >&2
  fi
}

record_cleanup_evidence() {
  local key="$1" value="$2"
  case "${key}${value}" in
    *$'\n'*|*$'\r'*) cleanup_error "invalid cleanup evidence"; return 1 ;;
  esac
  printf '%s=%s\n' "${key}" "${value}" >>"${EVIDENCE}"
}

snapshot_rollback_metadata() {
  sudo -n rm -f -- "${ROLLBACK_MANIFEST}" >/dev/null 2>&1 || true
  if sudo -n python3 "${FALLBACK_NETWORK_HELPER}" snapshot "${TRANSACTION_DIR}" "${ROLLBACK_MANIFEST}"; then
    ROLLBACK_METADATA_VALID=true
    record_cleanup_evidence rollback_metadata_valid true
    return 0
  fi
  ROLLBACK_METADATA_VALID=false
  record_cleanup_evidence rollback_metadata_valid false
  cleanup_error "teardown: transaction rollback metadata is invalid or ambiguous"
  return 1
}

clear_tun_hook() {
  local status=0
  sudo -n rm -f -- "${HOOK_DROPIN}" >/dev/null 2>&1 || status=1
  sudo -n rm -rf -- "${HOOK_DIR}" >/dev/null 2>&1 || status=1
  sudo -n systemctl daemon-reload >/dev/null 2>&1 || status=1
  if [[ "${status}" == "0" ]]; then
    record_cleanup_evidence hook_cleanup true
  else
    record_cleanup_evidence hook_cleanup false
    cleanup_error "teardown: failed to remove E2E hook state"
  fi
  return "${status}"
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

owned_xray_identities() {
  local pid start proc
  for proc in /proc/[0-9]*; do
    [[ -d "${proc}" ]] || continue
    pid="${proc#/proc/}"
    start="$(process_start_time "${pid}" 2>/dev/null || true)"
    [[ -n "${start}" ]] || continue
    owned_process_identity_matches "${pid}" "${start}" "/usr/lib/podlaz/xray" "/run/podlaz/generated/" || continue
    printf '%s:%s\n' "${pid}" "${start}"
  done
}

stop_owned_xray() {
  local identity pid start status=0
  local -a identities=()
  mapfile -t identities < <(owned_xray_identities)
  for identity in "${identities[@]}"; do
    pid="${identity%%:*}"
    start="${identity#*:}"
    if ! terminate_owned_process "${pid}" "${start}" "/usr/lib/podlaz/xray" "/run/podlaz/generated/" 50; then
      status=1
    fi
  done
  if [[ -n "$(owned_xray_identities)" ]]; then
    status=1
  fi
  if [[ "${status}" != "0" ]]; then
    cleanup_error "teardown: transaction-owned Xray process remains"
  fi
  return "${status}"
}

resolved_has_podlaz_link() {
  sudo -n resolvectl status --no-pager 2>/dev/null | grep -E '^Link [0-9]+ \(podlaz0\)$' >/dev/null
}

cleanup_podlaz_resolved() {
  if ! resolved_has_podlaz_link; then
    return 0
  fi
  sudo -n resolvectl revert podlaz0 >/dev/null 2>&1 || true
  if resolved_has_podlaz_link; then
    cleanup_error "teardown: systemd-resolved still has podlaz0"
    return 1
  fi
}

cleanup_podlaz_nftables() {
  if sudo -n nft list table inet podlaz >/dev/null 2>&1; then
    sudo -n nft delete table inet podlaz >/dev/null 2>&1 || true
  fi
  if sudo -n nft list table inet podlaz >/dev/null 2>&1; then
    cleanup_error "teardown: inet podlaz still exists"
    return 1
  fi
}

cleanup_recorded_network() {
  if [[ "${ROLLBACK_METADATA_VALID}" != "true" ]]; then
    cleanup_error "teardown: refusing route/rule cleanup without validated rollback metadata"
    record_cleanup_evidence recorded_network_cleanup false
    return 1
  fi
  if sudo -n python3 "${FALLBACK_NETWORK_HELPER}" cleanup "${ROLLBACK_MANIFEST}" && \
    sudo -n python3 "${FALLBACK_NETWORK_HELPER}" verify "${ROLLBACK_MANIFEST}"; then
    record_cleanup_evidence recorded_network_cleanup true
    return 0
  fi
  record_cleanup_evidence recorded_network_cleanup false
  cleanup_error "teardown: recorded route or policy-rule tuple remains"
  return 1
}

cleanup_podlaz_link() {
  if sudo -n ip link show dev podlaz0 >/dev/null 2>&1; then
    sudo -n ip link del dev podlaz0 >/dev/null 2>&1 || true
  fi
  if sudo -n ip link show dev podlaz0 >/dev/null 2>&1; then
    cleanup_error "teardown: podlaz0 still exists"
    return 1
  fi
}

remove_generated_state() {
  if ! sudo -n rm -rf -- "${GENERATED_DIR}" >/dev/null 2>&1; then
    cleanup_error "teardown: failed to remove generated runtime state"
    return 1
  fi
}

remove_transaction_state() {
  if ! sudo -n rm -rf -- "${TRANSACTION_DIR}" >/dev/null 2>&1; then
    cleanup_error "teardown: failed to remove validated transaction state"
    return 1
  fi
}

fallback_cleanup() {
  local status=0
  record_cleanup_evidence fallback_cleanup_attempted true
  timeout --signal=TERM --kill-after=5s 20s sudo -n systemctl stop podlazd.service >/dev/null 2>&1 || true
  if systemctl is-active --quiet podlazd.service; then
    cleanup_error "teardown: podlazd.service did not stop"
    status=1
  fi
  stop_owned_xray || status=1
  cleanup_podlaz_resolved || status=1
  cleanup_podlaz_nftables || status=1
  cleanup_recorded_network || status=1
  cleanup_podlaz_link || status=1
  remove_generated_state || status=1

  if [[ "${status}" == "0" ]]; then
    remove_transaction_state || status=1
  else
    record_cleanup_evidence transaction_metadata_preserved true
  fi
  return "${status}"
}

sentinel_rule_present() {
  sudo -n ip -4 rule show 2>/dev/null | \
    grep -F "${FOREIGN_RULE_PRIORITY}:" | \
    grep -F "to ${FOREIGN_ROUTE_CIDR%/32}" | \
    grep -E "lookup (${FOREIGN_ROUTE_TABLE})([[:space:]]|$)" >/dev/null
}

sentinel_route_present() {
  sudo -n ip -4 route show table "${FOREIGN_ROUTE_TABLE}" exact "${FOREIGN_ROUTE_CIDR}" 2>/dev/null | \
    grep -F "blackhole ${FOREIGN_ROUTE_CIDR%/32}" >/dev/null
}

cleanup_e2e_sentinels() {
  local status=0
  if systemctl is-active --quiet "${FOREIGN_SERVICE}"; then
    sudo -n systemctl stop "${FOREIGN_SERVICE}" >/dev/null 2>&1 || status=1
  fi
  if systemctl is-failed --quiet "${FOREIGN_SERVICE}"; then
    sudo -n systemctl reset-failed "${FOREIGN_SERVICE}" >/dev/null 2>&1 || status=1
  fi
  if sudo -n ip link show dev "${FOREIGN_DNS_LINK}" >/dev/null 2>&1; then
    sudo -n resolvectl revert "${FOREIGN_DNS_LINK}" >/dev/null 2>&1 || status=1
    sudo -n ip link del dev "${FOREIGN_DNS_LINK}" >/dev/null 2>&1 || status=1
  fi
  if sentinel_rule_present; then
    sudo -n ip -4 rule del priority "${FOREIGN_RULE_PRIORITY}" to "${FOREIGN_ROUTE_CIDR}" lookup "${FOREIGN_ROUTE_TABLE}" >/dev/null 2>&1 || status=1
  fi
  if sentinel_route_present; then
    sudo -n ip -4 route del blackhole "${FOREIGN_ROUTE_CIDR}" table "${FOREIGN_ROUTE_TABLE}" >/dev/null 2>&1 || status=1
  fi
  if sudo -n nft list table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}" >/dev/null 2>&1; then
    sudo -n nft delete table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}" >/dev/null 2>&1 || status=1
  fi

  if systemctl is-active --quiet "${FOREIGN_SERVICE}" || \
    sudo -n ip link show dev "${FOREIGN_DNS_LINK}" >/dev/null 2>&1 || \
    sentinel_rule_present || sentinel_route_present || \
    sudo -n nft list table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}" >/dev/null 2>&1; then
    status=1
  fi
  if [[ "${status}" == "0" ]]; then
    record_cleanup_evidence e2e_sentinels_removed true
  else
    record_cleanup_evidence e2e_sentinels_removed false
    cleanup_error "teardown: one or more E2E sentinels remain"
  fi
  return "${status}"
}

package_present() {
  dpkg-query -W podlaz >/dev/null 2>&1
}

purge_package() {
  local status=0
  if [[ "${PODLAZ_E2E_PURGE_PACKAGE}" != "true" ]]; then
    record_cleanup_evidence package_purged false
    return 0
  fi
  if package_present; then
    timeout --signal=TERM --kill-after=10s 90s sudo -n apt purge -y podlaz >/dev/null 2>&1 || status=1
  fi
  if command -v deb-systemd-helper >/dev/null 2>&1; then
    sudo -n deb-systemd-helper purge podlazd.service >/dev/null 2>&1 || status=1
  fi
  sudo -n systemctl daemon-reload >/dev/null 2>&1 || status=1
  sudo -n systemctl reset-failed podlazd.service >/dev/null 2>&1 || true
  if package_present; then
    status=1
  fi
  if [[ "${status}" == "0" ]]; then
    record_cleanup_evidence package_purged true
  else
    record_cleanup_evidence package_purged false
    cleanup_error "teardown: package purge failed or package remains installed"
  fi
  return "${status}"
}

assert_direct_connectivity() {
  local public_ip
  if ! getent hosts "${PODLAZ_E2E_DNS_CHECK_HOST}" >/dev/null 2>&1; then
    cleanup_error "teardown: direct DNS resolution is unavailable"
    return 1
  fi
  public_ip="$(curl -4 -fsS --max-time 30 "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL}" 2>/dev/null)" || {
    cleanup_error "teardown: direct IPv4 egress is unavailable"
    return 1
  }
  if [[ -z "${public_ip}" ]]; then
    cleanup_error "teardown: direct IPv4 egress returned an empty response"
    return 1
  fi
  record_cleanup_evidence direct_connectivity_restored true
}

assert_cleanup_complete() {
  local status=0
  if sudo -n ip link show dev podlaz0 >/dev/null 2>&1; then
    cleanup_error "teardown: podlaz0 still exists"
    status=1
  fi
  if [[ "${ROLLBACK_METADATA_VALID}" != "true" ]] || \
    ! sudo -n python3 "${FALLBACK_NETWORK_HELPER}" verify "${ROLLBACK_MANIFEST}"; then
    cleanup_error "teardown: recorded route/rule absence is not proven"
    status=1
  fi
  if resolved_has_podlaz_link; then
    cleanup_error "teardown: systemd-resolved still has podlaz0"
    status=1
  fi
  if sudo -n nft list table inet podlaz >/dev/null 2>&1; then
    cleanup_error "teardown: inet podlaz still exists"
    status=1
  fi
  if [[ -n "$(owned_xray_identities)" ]]; then
    cleanup_error "teardown: transaction-owned Xray process remains"
    status=1
  fi
  if sudo -n test -d "${GENERATED_DIR}" && sudo -n find "${GENERATED_DIR}" -mindepth 1 -print -quit | grep -q .; then
    cleanup_error "teardown: generated runtime config remains"
    status=1
  fi
  if sudo -n test -d "${TRANSACTION_DIR}" && sudo -n find "${TRANSACTION_DIR}" -mindepth 1 -print -quit | grep -q .; then
    cleanup_error "teardown: transaction metadata remains"
    status=1
  fi
  if systemctl is-active --quiet podlazd.service; then
    cleanup_error "teardown: podlazd.service is still active"
    status=1
  fi
  if sudo -n test -e "${HOOK_DROPIN}" || sudo -n test -d "${HOOK_DIR}"; then
    cleanup_error "teardown: E2E hook state remains"
    status=1
  fi
  if systemctl is-active --quiet "${FOREIGN_SERVICE}" || \
    sudo -n ip link show dev "${FOREIGN_DNS_LINK}" >/dev/null 2>&1 || \
    sentinel_rule_present || sentinel_route_present || \
    sudo -n nft list table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}" >/dev/null 2>&1; then
    cleanup_error "teardown: E2E sentinel state remains"
    status=1
  fi
  if [[ "${PODLAZ_E2E_PURGE_PACKAGE}" == "true" ]] && package_present; then
    cleanup_error "teardown: podlaz package remains installed"
    status=1
  fi
  assert_direct_connectivity || status=1
  if [[ "${status}" == "0" ]]; then
    record_cleanup_evidence cleanup_assertions pass
  else
    record_cleanup_evidence cleanup_assertions fail
  fi
  return "${status}"
}

if [[ "${PODLAZ_E2E_CLEANUP_SOURCE_ONLY:-false}" == "true" ]]; then
  return 0 2>/dev/null || exit 0
fi

require_cmd apt curl dpkg-query find getent grep ip nft python3 readlink resolvectl seq sleep sudo systemctl timeout tr

cleanup_status=0
snapshot_rollback_metadata || cleanup_status=1
clear_tun_hook || cleanup_status=1
attempt_daemon_recovery
fallback_cleanup || cleanup_status=1
cleanup_e2e_sentinels || cleanup_status=1
purge_package || cleanup_status=1
assert_cleanup_complete || cleanup_status=1
exit "${cleanup_status}"
