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

run_daemon_stop_failure_case() (
  destructive_calls=0
  cleanup_error() { :; }
  record_cleanup_evidence() { :; }
  timeout() { return 0; }
  inspect_service_active_state() { return 1; }
  stop_owned_xray() { destructive_calls=$((destructive_calls + 1)); return 0; }
  cleanup_podlaz_resolved() { destructive_calls=$((destructive_calls + 1)); return 0; }
  cleanup_podlaz_nftables() { destructive_calls=$((destructive_calls + 1)); return 0; }
  cleanup_recorded_network() { destructive_calls=$((destructive_calls + 1)); return 0; }
  cleanup_podlaz_link() { destructive_calls=$((destructive_calls + 1)); return 0; }
  remove_generated_state() { destructive_calls=$((destructive_calls + 1)); return 0; }
  remove_transaction_state() { destructive_calls=$((destructive_calls + 1)); return 0; }

  if fallback_cleanup; then
    fail_test "fallback accepted a live daemon"
  fi
  [[ "${destructive_calls}" == "0" ]] || \
    fail_test "fallback mutated owned state after daemon stop failure"
  [[ "${RUNTIME_PROCESSES_QUIESCED:-false}" == "false" ]] || \
    fail_test "live daemon was marked quiesced"
)

run_xray_stop_failure_case() (
  destructive_calls=0
  cleanup_error() { :; }
  record_cleanup_evidence() { :; }
  timeout() { return 0; }
  inspect_service_active_state() { return 0; }
  stop_owned_xray() { return 1; }
  cleanup_podlaz_resolved() { destructive_calls=$((destructive_calls + 1)); return 0; }
  cleanup_podlaz_nftables() { destructive_calls=$((destructive_calls + 1)); return 0; }
  cleanup_recorded_network() { destructive_calls=$((destructive_calls + 1)); return 0; }
  cleanup_podlaz_link() { destructive_calls=$((destructive_calls + 1)); return 0; }
  remove_generated_state() { destructive_calls=$((destructive_calls + 1)); return 0; }
  remove_transaction_state() { destructive_calls=$((destructive_calls + 1)); return 0; }

  if fallback_cleanup; then
    fail_test "fallback accepted a live or uninspectable Xray"
  fi
  [[ "${destructive_calls}" == "0" ]] || \
    fail_test "fallback removed identity material after Xray stop failure"
  [[ "${RUNTIME_PROCESSES_QUIESCED:-false}" == "false" ]] || \
    fail_test "live Xray was marked quiesced"
)

run_network_failure_preserves_identity_case() (
  generated_calls=0
  transaction_calls=0
  cleanup_error() { :; }
  record_cleanup_evidence() { :; }
  timeout() { return 0; }
  inspect_service_active_state() { return 0; }
  stop_owned_xray() { return 0; }
  cleanup_podlaz_resolved() { return 0; }
  cleanup_podlaz_nftables() { return 0; }
  cleanup_recorded_network() { return 1; }
  cleanup_podlaz_link() { return 0; }
  remove_generated_state() { generated_calls=$((generated_calls + 1)); return 0; }
  remove_transaction_state() { transaction_calls=$((transaction_calls + 1)); return 0; }

  if fallback_cleanup; then
    fail_test "fallback accepted incomplete network cleanup"
  fi
  [[ "${generated_calls}" == "0" ]] || \
    fail_test "generated config was removed before complete cleanup"
  [[ "${transaction_calls}" == "0" ]] || \
    fail_test "transaction metadata was removed before complete cleanup"
  [[ "${RUNTIME_PROCESSES_QUIESCED:-false}" == "true" ]] || \
    fail_test "confirmed process quiescence was not recorded"
)

run_package_purge_gate_case() (
  purge_calls=0
  cleanup_error() { :; }
  record_cleanup_evidence() { :; }
  purge_package() { purge_calls=$((purge_calls + 1)); return 0; }

  RUNTIME_PROCESSES_QUIESCED=false
  if purge_package_if_safe; then
    fail_test "package purge gate accepted live runtime processes"
  fi
  [[ "${purge_calls}" == "0" ]] || fail_test "package purge ran before process quiescence"

  RUNTIME_PROCESSES_QUIESCED=true
  purge_package_if_safe || fail_test "package purge gate rejected confirmed quiescence"
  [[ "${purge_calls}" == "1" ]] || fail_test "package purge did not run exactly once"
)

run_daemon_stop_failure_case
run_xray_stop_failure_case
run_network_failure_preserves_identity_case
run_package_purge_gate_case

printf 'tun package cleanup identity-preservation tests passed\n'
