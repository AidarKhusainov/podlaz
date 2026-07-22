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

run_manifest_race_case
printf 'tun package authoritative snapshot tests passed\n'
