#!/usr/bin/env bash

TUN_PACKAGE_ASSERTIONS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=host_state.sh
source "${TUN_PACKAGE_ASSERTIONS_DIR}/host_state.sh"

: "${TUN_PACKAGE_GENERATED_DIR:=/run/podlaz/generated}"
: "${TUN_PACKAGE_TRANSACTION_DIR:=/run/podlaz/transactions}"
: "${TUN_PACKAGE_ADDRESS_CIDR:=198.18.0.1/32}"

_tun_package_capture_status() {
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

_tun_package_assert_absent_state() {
  local phase="$1" label="$2" status
  shift 2
  _tun_package_capture_status status "$@"
  case "${status}" in
    0) return 0 ;;
    1) printf 'ERROR: %s: %s remains\n' "${phase}" "${label}" >&2 ;;
    *) printf 'ERROR: %s: %s inspection failed\n' "${phase}" "${label}" >&2 ;;
  esac
  return 1
}

inspect_exact_network_manifest_state() {
  local helper="$1" manifest="$2" status
  if sudo -n python3 "${helper}" verify "${manifest}" >/dev/null 2>&1; then
    return "${HOST_STATE_ABSENT}"
  else
    status=$?
  fi
  case "${status}" in
    1) return "${HOST_STATE_PRESENT}" ;;
    *) return "${HOST_STATE_ERROR}" ;;
  esac
}

inspect_tun_package_address_state() {
  local interface="$1" cidr="$2" output status line family address
  local exact_count=0 ipv4_count=0
  if output="$(sudo -n ip -4 -o address show dev "${interface}" 2>/dev/null)"; then
    :
  else
    status=$?
    if inspect_link_state "${interface}"; then
      return "${HOST_STATE_ABSENT}"
    else
      status=$?
    fi
    [[ "${status}" == "${HOST_STATE_PRESENT}" ]] && return "${HOST_STATE_ERROR}"
    return "${HOST_STATE_ERROR}"
  fi
  while IFS= read -r line; do
    [[ -n "${line//[[:space:]]/}" ]] || continue
    read -r _ _ family address _ <<<"${line}"
    [[ "${family}" == "inet" && -n "${address}" ]] || return "${HOST_STATE_ERROR}"
    ipv4_count=$((ipv4_count + 1))
    [[ "${address}" == "${cidr}" ]] && exact_count=$((exact_count + 1))
  done <<<"${output}"
  if [[ "${ipv4_count}" == "0" ]]; then
    return "${HOST_STATE_ABSENT}"
  fi
  if [[ "${ipv4_count}" == "1" && "${exact_count}" == "1" ]]; then
    return "${HOST_STATE_PRESENT}"
  fi
  return "${HOST_STATE_ERROR}"
}

assert_tun_package_address_present() {
  local phase="$1" interface="${2:-podlaz0}" cidr="${3:-${TUN_PACKAGE_ADDRESS_CIDR}}" status
  _tun_package_capture_status status inspect_tun_package_address_state "${interface}" "${cidr}"
  case "${status}" in
    "${HOST_STATE_PRESENT}") return 0 ;;
    "${HOST_STATE_ABSENT}") printf 'ERROR: %s: exact TUN address %s is absent from %s\n' "${phase}" "${cidr}" "${interface}" >&2 ;;
    *) printf 'ERROR: %s: TUN address inventory for %s is conflicting or unavailable\n' "${phase}" "${interface}" >&2 ;;
  esac
  return 1
}

inspect_tun_package_xray_state() {
  local output status pid
  if output="$(sudo -n pgrep -f '^/usr/lib/podlaz/xray([[:space:]]|$).*\/run\/podlaz\/generated\/' 2>/dev/null)"; then
    status=0
  else
    status=$?
  fi
  case "${status}" in
    1) return "${HOST_STATE_ABSENT}" ;;
    0) ;;
    *) return "${HOST_STATE_ERROR}" ;;
  esac
  [[ -n "${output//[[:space:]]/}" ]] || return "${HOST_STATE_ERROR}"
  while IFS= read -r pid; do
    [[ "${pid}" =~ ^[1-9][0-9]*$ ]] || return "${HOST_STATE_ERROR}"
  done <<<"${output}"
  return "${HOST_STATE_PRESENT}"
}

verify_tun_package_network_absent() {
  local phase="$1" helper="$2" manifest="$3"
  _tun_package_assert_absent_state \
    "${phase}" \
    "recorded route/rule state" \
    inspect_exact_network_manifest_state "${helper}" "${manifest}"
}

verify_tun_package_resources_absent() {
  local phase="$1" helper="$2" manifest="$3" status=0
  verify_tun_package_network_absent "${phase}" "${helper}" "${manifest}" || status=1
  _tun_package_assert_absent_state "${phase}" "daemon-owned TUN address" inspect_tun_package_address_state podlaz0 "${TUN_PACKAGE_ADDRESS_CIDR}" || status=1
  _tun_package_assert_absent_state "${phase}" "podlaz0" inspect_link_state podlaz0 || status=1
  _tun_package_assert_absent_state \
    "${phase}" \
    "systemd-resolved podlaz0 state" \
    inspect_resolved_link_state podlaz0 || status=1
  _tun_package_assert_absent_state \
    "${phase}" \
    "inet podlaz" \
    inspect_nft_table_state inet podlaz || status=1
  _tun_package_assert_absent_state \
    "${phase}" \
    "transaction-owned Xray process" \
    inspect_tun_package_xray_state || status=1
  _tun_package_assert_absent_state \
    "${phase}" \
    "generated runtime config" \
    inspect_directory_content_state "${TUN_PACKAGE_GENERATED_DIR}" || status=1
  _tun_package_assert_absent_state \
    "${phase}" \
    "transaction metadata" \
    inspect_directory_content_state "${TUN_PACKAGE_TRANSACTION_DIR}" || status=1
  return "${status}"
}
