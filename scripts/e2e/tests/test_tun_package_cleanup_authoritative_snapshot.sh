#!/usr/bin/env bash
set -Eeuo pipefail

TEST_ROOT="$(mktemp -d)"
trap 'rm -rf "${TEST_ROOT}"' EXIT
export E2E_TMP_ROOT="${TEST_ROOT}/tmp"
export E2E_ARTIFACT_DIR="${TEST_ROOT}/artifacts"
mkdir -p "${E2E_TMP_ROOT}" "${E2E_ARTIFACT_DIR}"
export PODLAZ_E2E_CLEANUP_SOURCE_ONLY=true

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../tun-package-cleanup.sh
source "${SCRIPT_DIR}/../tun-package-cleanup.sh"

fail_test() {
  printf 'test failure: %s\n' "$*" >&2
  exit 1
}

run_manifest_race_case() (
  transaction_version=1
  snapshot_calls=0
  authoritative_version=""
  cleanup_version=""
  snapshot_quiescence=""

  cleanup_error() { :; }
  record_cleanup_evidence() { :; }
  timeout() {
    # The daemon durably records another exact main-table tuple while the
    # preliminary snapshot is already stale, then becomes inactive.
    transaction_version=2
    return 0
  }
  inspect_service_active_state() { return 0; }
  stop_owned_xray() { return 0; }
  snapshot_rollback_metadata() {
    snapshot_calls=$((snapshot_calls + 1))
    authoritative_version="${transaction_version}"
    snapshot_quiescence+="${RUNTIME_PROCESSES_QUIESCED:-false},"
    ROLLBACK_METADATA_VALID=true
    return 0
  }
  cleanup_podlaz_resolved() { return 0; }
  cleanup_podlaz_nftables() { return 0; }
  cleanup_recorded_network() {
    cleanup_version="${authoritative_version}"
    return 0
  }
  cleanup_podlaz_link() { return 0; }
  remove_generated_state() { return 0; }
  remove_transaction_state() { return 0; }

  # Model a non-authoritative pre-stop snapshot. Fallback must replace it after
  # every podlaz network mutator has been proven quiescent.
  snapshot_rollback_metadata
  fallback_cleanup || fail_test "fallback rejected the deterministic race fixture"

  [[ "${snapshot_calls}" == "2" ]] || \
    fail_test "fallback did not take an authoritative post-quiescence snapshot"
  [[ "${snapshot_quiescence}" == "false,true," ]] || \
    fail_test "snapshot quiescence sequence was ${snapshot_quiescence}"
  [[ "${cleanup_version}" == "2" ]] || \
    fail_test "cleanup used stale transaction version ${cleanup_version}"
)

run_pre_recovery_order_case() (
  order=""

  require_cmd() { :; }
  snapshot_pre_recovery_metadata() {
    order+="pre-recovery-snapshot,"
    PRE_RECOVERY_METADATA_VALID=true
    return 0
  }
  clear_tun_hook() { order+="hook-cleanup,"; return 0; }
  attempt_daemon_recovery() { order+="recovery,"; return 0; }
  fallback_cleanup() { order+="fallback,"; return 0; }
  cleanup_e2e_sentinels() { order+="sentinels,"; return 0; }
  purge_package_if_safe() { order+="purge,"; return 0; }
  assert_cleanup_complete() { order+="assertions,"; return 0; }
  record_cleanup_evidence() { :; }

  teardown_main || fail_test "ordered teardown fixture failed"
  [[ "${order}" == "pre-recovery-snapshot,hook-cleanup,recovery,fallback,sentinels,purge,assertions," ]] || \
    fail_test "pre-recovery ownership proof order was ${order}"
)

run_recovery_metadata_loss_case() (
  transaction_present=true
  residual_main_route=true
  post_snapshot_empty=false
  authoritative_cleanup_calls=0
  pre_recovery_cleanup_calls=0
  pre_recovery_verify_calls=0
  generated_remove_calls=0
  transaction_remove_calls=0
  purge_calls=0

  cleanup_error() { :; }
  record_cleanup_evidence() { :; }
  timeout() { return 0; }
  inspect_service_active_state() { return 0; }
  stop_owned_xray() { return 0; }
  cleanup_podlaz_resolved() { return 0; }
  cleanup_podlaz_nftables() { return 0; }
  cleanup_podlaz_link() { return 0; }
  remove_generated_state() { generated_remove_calls=$((generated_remove_calls + 1)); return 0; }
  remove_transaction_state() { transaction_remove_calls=$((transaction_remove_calls + 1)); return 0; }
  purge_package() { purge_calls=$((purge_calls + 1)); return 0; }

  snapshot_pre_recovery_metadata() {
    [[ "${transaction_present}" == "true" ]] || fail_test "pre-recovery snapshot missed the transaction"
    PRE_RECOVERY_METADATA_VALID=true
    return 0
  }
  attempt_daemon_recovery() {
    transaction_present=false
    # Recovery reports completion but the exact main-table tuple survives.
    residual_main_route=true
    return 0
  }
  snapshot_rollback_metadata() {
    [[ "${transaction_present}" == "false" ]] || fail_test "authoritative snapshot ran before recovery"
    post_snapshot_empty=true
    ROLLBACK_METADATA_VALID=true
    return 0
  }

  sudo() {
    if [[ "${1:-}" == "-n" ]]; then
      shift
    fi
    if [[ "${1:-}" == "python3" && "${2:-}" == "${FALLBACK_NETWORK_HELPER}" ]]; then
      case "${3:-}:${4:-}" in
        "cleanup:${AUTHORITATIVE_ROLLBACK_MANIFEST}")
          authoritative_cleanup_calls=$((authoritative_cleanup_calls + 1))
          return 0
          ;;
        "cleanup:${PRE_RECOVERY_ROLLBACK_MANIFEST}")
          pre_recovery_cleanup_calls=$((pre_recovery_cleanup_calls + 1))
          return 0
          ;;
        "verify:${PRE_RECOVERY_ROLLBACK_MANIFEST}")
          pre_recovery_verify_calls=$((pre_recovery_verify_calls + 1))
          [[ "${residual_main_route}" == "true" ]] && return 1
          return 0
          ;;
        "verify:${AUTHORITATIVE_ROLLBACK_MANIFEST}")
          return 0
          ;;
      esac
    fi
    return 0
  }

  snapshot_pre_recovery_metadata || fail_test "pre-recovery verification manifest was rejected"
  attempt_daemon_recovery
  if fallback_cleanup; then
    fail_test "fallback accepted a route left after recovery deleted its transaction"
  fi

  [[ "${post_snapshot_empty}" == "true" ]] || fail_test "post-quiescence manifest was not empty"
  [[ "${authoritative_cleanup_calls}" == "1" ]] || \
    fail_test "authoritative manifest cleanup count was ${authoritative_cleanup_calls}"
  [[ "${pre_recovery_cleanup_calls}" == "0" ]] || \
    fail_test "verification-only pre-recovery manifest authorized mutation"
  [[ "${pre_recovery_verify_calls}" == "1" ]] || \
    fail_test "pre-recovery tuple was not verified after fallback"
  [[ "${generated_remove_calls}" == "0" ]] || \
    fail_test "generated identity material was removed before ownership-union absence"
  [[ "${transaction_remove_calls}" == "0" ]] || \
    fail_test "transaction identity material was removed before ownership-union absence"
  [[ "${IDENTITY_MATERIAL_RELEASED}" == "false" ]] || \
    fail_test "identity material release was authorized with a residual main-table route"

  if purge_package_if_safe; then
    fail_test "package purge was authorized with a residual ownership tuple"
  fi
  [[ "${purge_calls}" == "0" ]] || fail_test "package executable identity was removed"
)

run_manifest_race_case
run_pre_recovery_order_case
run_recovery_metadata_loss_case
printf 'tun package authoritative snapshot tests passed\n'
