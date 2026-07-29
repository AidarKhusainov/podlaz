#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cleanup_source_only_requested="${PODLAZ_E2E_CLEANUP_SOURCE_ONLY:-false}"
PODLAZ_E2E_CLEANUP_SOURCE_ONLY=true
# shellcheck source=tun-package-cleanup-core.sh
source "${SCRIPT_DIR}/tun-package-cleanup-core.sh"
PODLAZ_E2E_CLEANUP_SOURCE_ONLY="${cleanup_source_only_requested}"

PRE_RECOVERY_ROLLBACK_MANIFEST="${PODLAZ_E2E_PRE_RECOVERY_MANIFEST_PATH:-${E2E_TMP_ROOT}/tun-package-pre-recovery-network.json}"
AUTHORITATIVE_ROLLBACK_MANIFEST="${E2E_TMP_ROOT}/tun-package-authoritative-network.json"
ROLLBACK_MANIFEST="${AUTHORITATIVE_ROLLBACK_MANIFEST}"
VERIFICATION_NETWORK_HELPER="${SCRIPT_DIR}/tun-package-verification-network.py"
CONNECT_TERMINATION_UNPROVEN_MARKER="${E2E_TMP_ROOT}/tun-package-connect-termination-unproven"
EXTERNAL_PRE_RECOVERY_MANIFEST_STATE="${PODLAZ_E2E_PRE_RECOVERY_MANIFEST_STATE:-unset}"
EXTERNAL_PRE_RECOVERY_MANIFEST_SHA256="${PODLAZ_E2E_PRE_RECOVERY_MANIFEST_SHA256:-}"
PRE_RECOVERY_METADATA_VALID=false
IDENTITY_MATERIAL_RELEASED=false

adopt_pre_recovery_metadata() {
  local actual_digest verify_status

  PRE_RECOVERY_METADATA_VALID=false
  if [[ ! "${EXTERNAL_PRE_RECOVERY_MANIFEST_SHA256}" =~ ^[0-9a-f]{64}$ ]]; then
    record_cleanup_evidence pre_recovery_metadata_valid false
    cleanup_error "teardown: imported pre-recovery manifest checksum is invalid"
    return 1
  fi
  if ! actual_digest="$(sudo -n sha256sum "${PRE_RECOVERY_ROLLBACK_MANIFEST}" 2>/dev/null | awk 'NF >= 1 {print $1; exit}')"; then
    record_cleanup_evidence pre_recovery_metadata_valid false
    cleanup_error "teardown: imported pre-recovery manifest cannot be hashed"
    return 1
  fi
  if [[ "${actual_digest}" != "${EXTERNAL_PRE_RECOVERY_MANIFEST_SHA256}" ]]; then
    record_cleanup_evidence pre_recovery_metadata_valid false
    cleanup_error "teardown: imported pre-recovery manifest changed after capture"
    return 1
  fi

  if sudo -n python3 "${FALLBACK_NETWORK_HELPER}" verify "${PRE_RECOVERY_ROLLBACK_MANIFEST}" >/dev/null 2>&1; then
    verify_status=0
  else
    verify_status=$?
  fi
  case "${verify_status}" in
    0|1) ;;
    *)
      record_cleanup_evidence pre_recovery_metadata_valid false
      cleanup_error "teardown: imported pre-recovery manifest is invalid or cannot be inspected"
      return 1
      ;;
  esac

  PRE_RECOVERY_METADATA_VALID=true
  record_cleanup_evidence pre_recovery_metadata_valid true
  record_cleanup_evidence pre_recovery_metadata_verification_only true
  record_cleanup_evidence pre_recovery_metadata_imported true
  record_cleanup_evidence pre_recovery_metadata_immutable true
}

snapshot_pre_recovery_metadata() {
  PRE_RECOVERY_METADATA_VALID=false
  case "${EXTERNAL_PRE_RECOVERY_MANIFEST_STATE}" in
    ready)
      adopt_pre_recovery_metadata
      return
      ;;
    failed)
      record_cleanup_evidence pre_recovery_metadata_valid false
      record_cleanup_evidence pre_recovery_metadata_imported false
      cleanup_error "teardown: caller failed to capture ownership proof before cancellation or hook release"
      return 1
      ;;
    unset|"") ;;
    *)
      record_cleanup_evidence pre_recovery_metadata_valid false
      cleanup_error "teardown: unsupported imported pre-recovery manifest state"
      return 1
      ;;
  esac

  if ! sudo -n rm -f -- "${PRE_RECOVERY_ROLLBACK_MANIFEST}" >/dev/null 2>&1; then
    record_cleanup_evidence pre_recovery_metadata_valid false
    cleanup_error "teardown: failed to clear previous pre-recovery verification manifest"
    return 1
  fi
  if sudo -n python3 "${VERIFICATION_NETWORK_HELPER}" snapshot \
    "${TRANSACTION_DIR}" "${PRE_RECOVERY_ROLLBACK_MANIFEST}"; then
    PRE_RECOVERY_METADATA_VALID=true
    record_cleanup_evidence pre_recovery_metadata_valid true
    record_cleanup_evidence pre_recovery_metadata_verification_only true
    record_cleanup_evidence pre_recovery_metadata_imported false
    return 0
  fi
  record_cleanup_evidence pre_recovery_metadata_valid false
  cleanup_error "teardown: pre-recovery transaction metadata is invalid or ambiguous"
  return 1
}

inspect_manifest_network_state() {
  local manifest="$1" status
  if sudo -n python3 "${FALLBACK_NETWORK_HELPER}" verify "${manifest}"; then
    return "${HOST_STATE_ABSENT}"
  else
    status=$?
  fi
  case "${status}" in
    1) return "${HOST_STATE_PRESENT}" ;;
    *) return "${HOST_STATE_ERROR}" ;;
  esac
}

inspect_recorded_network_state() {
  local state
  [[ "${PRE_RECOVERY_METADATA_VALID}" == "true" ]] || return "${HOST_STATE_ERROR}"
  [[ "${ROLLBACK_METADATA_VALID}" == "true" ]] || return "${HOST_STATE_ERROR}"

  capture_status state inspect_manifest_network_state "${PRE_RECOVERY_ROLLBACK_MANIFEST}"
  [[ "${state}" == "0" ]] || return "${state}"
  capture_status state inspect_manifest_network_state "${AUTHORITATIVE_ROLLBACK_MANIFEST}"
  return "${state}"
}

fallback_cleanup() {
  local status=0 service_state
  RUNTIME_PROCESSES_QUIESCED=false
  ROLLBACK_METADATA_VALID=false
  IDENTITY_MATERIAL_RELEASED=false
  record_cleanup_evidence fallback_cleanup_attempted true
  timeout --signal=TERM --kill-after=5s 20s sudo -n systemctl stop podlazd.service >/dev/null 2>&1 || true
  capture_status service_state inspect_service_active_state podlazd.service
  if [[ "${service_state}" != "0" ]]; then
    cleanup_error "teardown: podlazd.service did not stop or could not be inspected"
    record_cleanup_evidence runtime_processes_quiesced false
    record_cleanup_evidence identity_material_preserved true
    return 1
  fi
  if ! stop_owned_xray; then
    record_cleanup_evidence runtime_processes_quiesced false
    record_cleanup_evidence identity_material_preserved true
    return 1
  fi
  RUNTIME_PROCESSES_QUIESCED=true
  record_cleanup_evidence runtime_processes_quiesced true

  if ! snapshot_rollback_metadata; then
    record_cleanup_evidence transaction_metadata_preserved true
    record_cleanup_evidence identity_material_preserved true
    return 1
  fi

  cleanup_podlaz_resolved || status=1
  cleanup_podlaz_nftables || status=1
  cleanup_recorded_network || status=1
  cleanup_podlaz_link || status=1

  if [[ "${status}" == "0" ]]; then
    remove_generated_state || status=1
  fi
  if [[ "${status}" == "0" ]]; then
    remove_transaction_state || status=1
  else
    record_cleanup_evidence transaction_metadata_preserved true
    record_cleanup_evidence identity_material_preservation_required true
  fi

  if [[ "${status}" == "0" ]]; then
    IDENTITY_MATERIAL_RELEASED=true
    record_cleanup_evidence ownership_union_absent true
    record_cleanup_evidence identity_material_release_authorized true
  else
    record_cleanup_evidence ownership_union_absent false
    record_cleanup_evidence identity_material_release_authorized false
    record_cleanup_evidence identity_material_preserved true
  fi
  return "${status}"
}

purge_package_if_safe() {
  if [[ "${PODLAZ_E2E_PURGE_PACKAGE}" != "true" ]]; then
    purge_package
    return
  fi
  if [[ "${RUNTIME_PROCESSES_QUIESCED}" != "true" ]]; then
    record_cleanup_evidence package_purge_deferred true
    record_cleanup_evidence package_purged false
    cleanup_error "teardown: refusing package purge while daemon or owned Xray absence is unproven"
    return 1
  fi
  if [[ "${IDENTITY_MATERIAL_RELEASED}" != "true" ]]; then
    record_cleanup_evidence package_purge_deferred true
    record_cleanup_evidence package_purged false
    cleanup_error "teardown: refusing package purge before the pre-recovery and authoritative ownership union is proven absent"
    return 1
  fi
  record_cleanup_evidence package_purge_deferred false
  purge_package
}

teardown_main() {
  local cleanup_status=0

  if [[ -e "${CONNECT_TERMINATION_UNPROVEN_MARKER}" || -L "${CONNECT_TERMINATION_UNPROVEN_MARKER}" ]]; then
    record_cleanup_evidence connect_process_quiesced false
    record_cleanup_evidence identity_material_preserved true
    record_cleanup_evidence identity_material_release_authorized false
    record_cleanup_evidence package_purge_deferred true
    cleanup_error "teardown: refusing hook release and recovery because connect process termination is unproven"
    return 1
  fi

  require_cmd apt awk curl dpkg-query getent grep ip nft pgrep python3 readlink resolvectl seq sha256sum sleep sudo systemctl timeout tr

  if ! snapshot_pre_recovery_metadata; then
    record_cleanup_evidence identity_material_preserved true
    record_cleanup_evidence identity_material_release_authorized false
    return 1
  fi
  clear_tun_hook || cleanup_status=1
  attempt_daemon_recovery
  fallback_cleanup || cleanup_status=1
  cleanup_e2e_sentinels || cleanup_status=1
  purge_package_if_safe || cleanup_status=1
  assert_cleanup_complete || cleanup_status=1
  return "${cleanup_status}"
}

if [[ "${PODLAZ_E2E_CLEANUP_SOURCE_ONLY:-false}" == "true" ]]; then
  # shellcheck disable=SC2317
  return 0 2>/dev/null || exit 0
fi

teardown_main
