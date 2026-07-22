#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"
# shellcheck source=lib/process_lifecycle.sh
source "${SCRIPT_DIR}/lib/process_lifecycle.sh"
# shellcheck source=lib/host_state.sh
source "${SCRIPT_DIR}/lib/host_state.sh"

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

capture_status() {
  local -n result_ref="$1"
  local captured_rc
  shift
  if "$@"; then
    captured_rc=0
  else
    captured_rc=$?
  fi
  result_ref="${captured_rc}"
}

assert_absent_state() {
  local label="$1" status
  shift
  capture_status status "$@"
  case "${status}" in
    0) return 0 ;;
    1) cleanup_error "teardown: ${label} remains" ;;
    *) cleanup_error "teardown: ${label} inspection failed" ;;
  esac
  return 1
}

snapshot_rollback_metadata() {
  if ! sudo -n rm -f -- "${ROLLBACK_MANIFEST}" >/dev/null 2>&1; then
    ROLLBACK_METADATA_VALID=false
    record_cleanup_evidence rollback_metadata_valid false
    cleanup_error "teardown: failed to clear previous rollback manifest"
    return 1
  fi
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
  assert_absent_state "E2E hook drop-in" inspect_path_state "${HOOK_DROPIN}" || status=1
  assert_absent_state "E2E hook directory" inspect_path_state "${HOOK_DIR}" || status=1
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

inspect_owned_xray_state() {
  local output
  if ! output="$(owned_xray_identities)"; then
    return "${HOST_STATE_ERROR}"
  fi
  [[ -n "${output}" ]] && return "${HOST_STATE_PRESENT}"
  return "${HOST_STATE_ABSENT}"
}

stop_owned_xray() {
  local identity pid start status=0 inspect_status
  local -a identities=()
  mapfile -t identities < <(owned_xray_identities)
  for identity in "${identities[@]}"; do
    pid="${identity%%:*}"
    start="${identity#*:}"
    if ! terminate_owned_process "${pid}" "${start}" "/usr/lib/podlaz/xray" "/run/podlaz/generated/" 50; then
      status=1
    fi
  done
  capture_status inspect_status inspect_owned_xray_state
  if [[ "${inspect_status}" != "0" ]]; then
    status=1
    cleanup_error "teardown: transaction-owned Xray process remains or cannot be inspected"
  fi
  return "${status}"
}

cleanup_podlaz_resolved() {
  local state
  capture_status state inspect_resolved_link_state podlaz0
  case "${state}" in
    0) return 0 ;;
    1) sudo -n resolvectl revert podlaz0 >/dev/null 2>&1 || true ;;
    *) cleanup_error "teardown: systemd-resolved inspection failed"; return 1 ;;
  esac
  assert_absent_state "systemd-resolved podlaz0 state" inspect_resolved_link_state podlaz0
}

cleanup_podlaz_nftables() {
  local state
  capture_status state inspect_nft_table_state inet podlaz
  case "${state}" in
    0) return 0 ;;
    1) sudo -n nft delete table inet podlaz >/dev/null 2>&1 || true ;;
    *) cleanup_error "teardown: nftables inspection failed"; return 1 ;;
  esac
  assert_absent_state "inet podlaz" inspect_nft_table_state inet podlaz
}

inspect_recorded_network_state() {
  local status
  [[ "${ROLLBACK_METADATA_VALID}" == "true" ]] || return "${HOST_STATE_ERROR}"
  if sudo -n python3 "${FALLBACK_NETWORK_HELPER}" verify "${ROLLBACK_MANIFEST}"; then
    return "${HOST_STATE_ABSENT}"
  else
    status=$?
  fi
  case "${status}" in
    1) return "${HOST_STATE_PRESENT}" ;;
    *) return "${HOST_STATE_ERROR}" ;;
  esac
}

cleanup_recorded_network() {
  local inspect_status
  if [[ "${ROLLBACK_METADATA_VALID}" != "true" ]]; then
    cleanup_error "teardown: refusing route/rule cleanup without validated rollback metadata"
    record_cleanup_evidence recorded_network_cleanup false
    return 1
  fi
  sudo -n python3 "${FALLBACK_NETWORK_HELPER}" cleanup "${ROLLBACK_MANIFEST}" >/dev/null 2>&1 || true
  capture_status inspect_status inspect_recorded_network_state
  if [[ "${inspect_status}" == "0" ]]; then
    record_cleanup_evidence recorded_network_cleanup true
    return 0
  fi
  record_cleanup_evidence recorded_network_cleanup false
  if [[ "${inspect_status}" == "1" ]]; then
    cleanup_error "teardown: recorded route or policy-rule tuple remains"
  else
    cleanup_error "teardown: recorded route or policy-rule inspection failed"
  fi
  return 1
}

cleanup_podlaz_link() {
  local state
  capture_status state inspect_link_state podlaz0
  case "${state}" in
    0) return 0 ;;
    1) sudo -n ip link del dev podlaz0 >/dev/null 2>&1 || true ;;
    *) cleanup_error "teardown: podlaz0 inspection failed"; return 1 ;;
  esac
  assert_absent_state "podlaz0" inspect_link_state podlaz0
}

remove_generated_state() {
  sudo -n rm -rf -- "${GENERATED_DIR}" >/dev/null 2>&1 || true
  assert_absent_state "generated runtime state" inspect_path_state "${GENERATED_DIR}"
}

remove_transaction_state() {
  sudo -n rm -rf -- "${TRANSACTION_DIR}" >/dev/null 2>&1 || true
  assert_absent_state "validated transaction state" inspect_path_state "${TRANSACTION_DIR}"
}

fallback_cleanup() {
  local status=0 service_state
  record_cleanup_evidence fallback_cleanup_attempted true
  timeout --signal=TERM --kill-after=5s 20s sudo -n systemctl stop podlazd.service >/dev/null 2>&1 || true
  capture_status service_state inspect_service_active_state podlazd.service
  if [[ "${service_state}" != "0" ]]; then
    cleanup_error "teardown: podlazd.service did not stop or could not be inspected"
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

inspect_sentinel_rule_state() {
  local output
  if ! output="$(sudo -n ip -4 rule show 2>/dev/null)"; then
    return "${HOST_STATE_ERROR}"
  fi
  if grep -F "${FOREIGN_RULE_PRIORITY}:" <<<"${output}" | \
    grep -F "to ${FOREIGN_ROUTE_CIDR%/32}" | \
    grep -E "lookup (${FOREIGN_ROUTE_TABLE})([[:space:]]|$)" >/dev/null; then
    return "${HOST_STATE_PRESENT}"
  fi
  return "${HOST_STATE_ABSENT}"
}

inspect_sentinel_route_state() {
  local output
  if ! output="$(sudo -n ip -4 route show table all 2>/dev/null)"; then
    return "${HOST_STATE_ERROR}"
  fi
  if grep -E "^blackhole ${FOREIGN_ROUTE_CIDR%/32}([[:space:]]|/32[[:space:]]).*table ${FOREIGN_ROUTE_TABLE}([[:space:]]|$)" <<<"${output}" >/dev/null; then
    return "${HOST_STATE_PRESENT}"
  fi
  return "${HOST_STATE_ABSENT}"
}

inspect_reserved_network_state() {
  local family routes rules
  for family in -4 -6; do
    if ! routes="$(sudo -n ip "${family}" route show table all 2>/dev/null)"; then
      return "${HOST_STATE_ERROR}"
    fi
    if grep -E '(^|[[:space:]])table (podlaz|51820)([[:space:]]|$)' <<<"${routes}" >/dev/null; then
      return "${HOST_STATE_PRESENT}"
    fi
    if ! rules="$(sudo -n ip "${family}" rule show 2>/dev/null)"; then
      return "${HOST_STATE_ERROR}"
    fi
    if grep -E '(^|[[:space:]])(9999|10000):|lookup (podlaz|51820)([[:space:]]|$)' <<<"${rules}" >/dev/null; then
      return "${HOST_STATE_PRESENT}"
    fi
  done
  return "${HOST_STATE_ABSENT}"
}

inspect_e2e_sentinel_state() {
  local state any_present=0
  capture_status state inspect_service_load_state "${FOREIGN_SERVICE}"
  [[ "${state}" == "2" ]] && return "${HOST_STATE_ERROR}"
  [[ "${state}" == "1" ]] && any_present=1
  capture_status state inspect_link_state "${FOREIGN_DNS_LINK}"
  [[ "${state}" == "2" ]] && return "${HOST_STATE_ERROR}"
  [[ "${state}" == "1" ]] && any_present=1
  capture_status state inspect_sentinel_rule_state
  [[ "${state}" == "2" ]] && return "${HOST_STATE_ERROR}"
  [[ "${state}" == "1" ]] && any_present=1
  capture_status state inspect_sentinel_route_state
  [[ "${state}" == "2" ]] && return "${HOST_STATE_ERROR}"
  [[ "${state}" == "1" ]] && any_present=1
  capture_status state inspect_nft_table_state "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}"
  [[ "${state}" == "2" ]] && return "${HOST_STATE_ERROR}"
  [[ "${state}" == "1" ]] && any_present=1
  [[ "${any_present}" == "1" ]] && return "${HOST_STATE_PRESENT}"
  return "${HOST_STATE_ABSENT}"
}

cleanup_e2e_sentinels() {
  local status=0 state

  capture_status state inspect_service_load_state "${FOREIGN_SERVICE}"
  case "${state}" in
    0) ;;
    1)
      sudo -n systemctl stop "${FOREIGN_SERVICE}" >/dev/null 2>&1 || status=1
      sudo -n systemctl reset-failed "${FOREIGN_SERVICE}" >/dev/null 2>&1 || true
      sudo -n systemctl daemon-reload >/dev/null 2>&1 || status=1
      ;;
    *) status=1; cleanup_error "teardown: E2E sentinel service inspection failed" ;;
  esac

  capture_status state inspect_link_state "${FOREIGN_DNS_LINK}"
  case "${state}" in
    0) ;;
    1)
      sudo -n resolvectl revert "${FOREIGN_DNS_LINK}" >/dev/null 2>&1 || status=1
      sudo -n ip link del dev "${FOREIGN_DNS_LINK}" >/dev/null 2>&1 || status=1
      ;;
    *) status=1; cleanup_error "teardown: E2E DNS sentinel inspection failed" ;;
  esac

  capture_status state inspect_sentinel_rule_state
  case "${state}" in
    0) ;;
    1) sudo -n ip -4 rule del priority "${FOREIGN_RULE_PRIORITY}" to "${FOREIGN_ROUTE_CIDR}" lookup "${FOREIGN_ROUTE_TABLE}" >/dev/null 2>&1 || status=1 ;;
    *) status=1; cleanup_error "teardown: E2E rule sentinel inspection failed" ;;
  esac

  capture_status state inspect_sentinel_route_state
  case "${state}" in
    0) ;;
    1) sudo -n ip -4 route del blackhole "${FOREIGN_ROUTE_CIDR}" table "${FOREIGN_ROUTE_TABLE}" >/dev/null 2>&1 || status=1 ;;
    *) status=1; cleanup_error "teardown: E2E route sentinel inspection failed" ;;
  esac

  capture_status state inspect_nft_table_state "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}"
  case "${state}" in
    0) ;;
    1) sudo -n nft delete table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}" >/dev/null 2>&1 || status=1 ;;
    *) status=1; cleanup_error "teardown: E2E nftables sentinel inspection failed" ;;
  esac

  capture_status state inspect_e2e_sentinel_state
  [[ "${state}" == "0" ]] || status=1
  if [[ "${status}" == "0" ]]; then
    record_cleanup_evidence e2e_sentinels_removed true
  else
    record_cleanup_evidence e2e_sentinels_removed false
    cleanup_error "teardown: E2E sentinel state remains or could not be inspected"
  fi
  return "${status}"
}

purge_package() {
  local status=0 package_state
  if [[ "${PODLAZ_E2E_PURGE_PACKAGE}" != "true" ]]; then
    record_cleanup_evidence package_purged false
    return 0
  fi

  capture_status package_state inspect_package_state podlaz
  case "${package_state}" in
    0) ;;
    1) timeout --signal=TERM --kill-after=10s 90s sudo -n apt purge -y podlaz >/dev/null 2>&1 || status=1 ;;
    *) status=1; cleanup_error "teardown: package state inspection failed before purge" ;;
  esac

  if command -v deb-systemd-helper >/dev/null 2>&1; then
    sudo -n deb-systemd-helper purge podlazd.service >/dev/null 2>&1 || status=1
  fi
  sudo -n systemctl daemon-reload >/dev/null 2>&1 || status=1
  sudo -n systemctl reset-failed podlazd.service >/dev/null 2>&1 || true

  capture_status package_state inspect_package_state podlaz
  [[ "${package_state}" == "0" ]] || status=1
  if [[ "${status}" == "0" ]]; then
    record_cleanup_evidence package_purged true
  else
    record_cleanup_evidence package_purged false
    cleanup_error "teardown: package purge failed, package remains, or package state cannot be inspected"
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
  local status=0 service_state package_state
  assert_absent_state "podlaz0" inspect_link_state podlaz0 || status=1
  assert_absent_state "recorded route/rule state" inspect_recorded_network_state || status=1
  assert_absent_state "reserved route/rule state" inspect_reserved_network_state || status=1
  assert_absent_state "systemd-resolved podlaz0 state" inspect_resolved_link_state podlaz0 || status=1
  assert_absent_state "inet podlaz" inspect_nft_table_state inet podlaz || status=1
  assert_absent_state "transaction-owned Xray process" inspect_owned_xray_state || status=1
  assert_absent_state "generated runtime config" inspect_directory_content_state "${GENERATED_DIR}" || status=1
  assert_absent_state "transaction metadata" inspect_directory_content_state "${TRANSACTION_DIR}" || status=1

  capture_status service_state inspect_service_active_state podlazd.service
  if [[ "${service_state}" != "0" ]]; then
    if [[ "${service_state}" == "1" ]]; then
      cleanup_error "teardown: podlazd.service is still active"
    else
      cleanup_error "teardown: podlazd.service state inspection failed"
    fi
    status=1
  fi

  assert_absent_state "E2E hook drop-in" inspect_path_state "${HOOK_DROPIN}" || status=1
  assert_absent_state "E2E hook directory" inspect_path_state "${HOOK_DIR}" || status=1
  assert_absent_state "E2E sentinel state" inspect_e2e_sentinel_state || status=1

  if [[ "${PODLAZ_E2E_PURGE_PACKAGE}" == "true" ]]; then
    capture_status package_state inspect_package_state podlaz
    if [[ "${package_state}" != "0" ]]; then
      if [[ "${package_state}" == "1" ]]; then
        cleanup_error "teardown: podlaz package remains installed"
      else
        cleanup_error "teardown: podlaz package state inspection failed"
      fi
      status=1
    fi
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
  # shellcheck disable=SC2317
  return 0 2>/dev/null || exit 0
fi

require_cmd apt curl dpkg-query getent grep ip nft python3 readlink resolvectl seq sleep sudo systemctl timeout tr

cleanup_status=0
snapshot_rollback_metadata || cleanup_status=1
clear_tun_hook || cleanup_status=1
attempt_daemon_recovery
fallback_cleanup || cleanup_status=1
cleanup_e2e_sentinels || cleanup_status=1
purge_package || cleanup_status=1
assert_cleanup_complete || cleanup_status=1
exit "${cleanup_status}"
