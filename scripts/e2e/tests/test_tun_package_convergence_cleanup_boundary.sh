#!/usr/bin/env bash
set -Eeuo pipefail

TEST_ROOT="$(mktemp -d)"
trap 'rm -rf "${TEST_ROOT}"' EXIT
E2E_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export E2E_TMP_ROOT="${TEST_ROOT}/tmp"
export E2E_ARTIFACT_DIR="${TEST_ROOT}/artifacts"
export TRANSACTION_DIR="${TEST_ROOT}/transactions"
export FALLBACK_NETWORK_HELPER="${E2E_DIR}/tun-package-fallback-network.py"
mkdir -p "${E2E_TMP_ROOT}" "${E2E_ARTIFACT_DIR}" "${TRANSACTION_DIR}"

fail_test() {
  printf 'test failure: %s\n' "$*" >&2
  exit 1
}

cat >"${TRANSACTION_DIR}/tx.json" <<'JSON'
{
  "schema_version": "podlaz.transaction.v1",
  "owner": "podlaz",
  "state": "verifying",
  "desired_plan": {
    "routes": [
      {
        "kind": "route",
        "operation": "add",
        "owner": "podlaz:route",
        "table": "main",
        "cidr": "203.0.113.10/32",
        "via": "192.0.2.1",
        "dev": "eth0"
      }
    ],
    "steps": []
  },
  "applied_steps": [],
  "rollback": {
    "routes": [],
    "policy_rules": []
  }
}
JSON

SCRIPT_DIR="${E2E_DIR}"
FOREIGN_ADDRESS_LINK="podlaz-e2e-address0"
# shellcheck source=../lib/process_lifecycle.sh
source "${E2E_DIR}/lib/process_lifecycle.sh"
# shellcheck source=../lib/connect_lifecycle.sh
source "${E2E_DIR}/lib/connect_lifecycle.sh"

cleanup_definition="$(awk '
  /^cleanup\(\) \{/ { capture = 1 }
  capture { print }
  capture && /^}$/ { exit }
' "${E2E_DIR}/tun-package-convergence.sh")"
[[ -n "${cleanup_definition}" ]] || fail_test "real convergence cleanup function was not found"
eval "${cleanup_definition}"

run_capture_failure_live_child_case() (
  local child_pid child_start
  PROCESS_POLL_INTERVAL=0.01
  sleep 30 &
  child_pid=$!
  child_start="$(process_start_time "${child_pid}")"
  [[ -n "${child_start}" ]] || fail_test "failed to capture live child identity"
  printf '%s\n' "${child_pid}" >"${TEST_ROOT}/capture-failure-child.pid"
  CONNECT_PID="${child_pid}"
  CONNECT_START_TIME="${child_start}"
  CONNECT_EXIT_CODE=""

  capture_connect_pre_recovery_proof() { return 1; }
  clear_hook() {
    if child_process_exists "${child_pid}"; then
      printf 'live\n' >"${TEST_ROOT}/hook-process-state"
    else
      printf 'gone\n' >"${TEST_ROOT}/hook-process-state"
    fi
    if [[ -n "${CONNECT_PID}" || -n "${CONNECT_START_TIME}" ]]; then
      printf 'tracked\n' >"${TEST_ROOT}/hook-tracking-state"
    else
      printf 'cleared\n' >"${TEST_ROOT}/hook-tracking-state"
    fi
    return 0
  }
  bash() { return 0; }

  cleanup
)

set +e
run_capture_failure_live_child_case
capture_failure_status=$?
set -e
capture_failure_pid="$(cat "${TEST_ROOT}/capture-failure-child.pid")"
if child_process_exists "${capture_failure_pid}"; then
  kill -KILL "${capture_failure_pid}" >/dev/null 2>&1 || true
fi
[[ "${capture_failure_status}" != "0" ]] || \
  fail_test "capture failure was not propagated by the real convergence cleanup"
[[ "$(cat "${TEST_ROOT}/hook-process-state")" == "gone" ]] || \
  fail_test "hook release ran while the connect child still had a process identity"
[[ "$(cat "${TEST_ROOT}/hook-tracking-state")" == "cleared" ]] || \
  fail_test "hook release ran before connect PID tracking was cleared"

run_termination_failure_blocks_release_case() (
  CONNECT_PID="12345"
  CONNECT_START_TIME="67890"
  CONNECT_EXIT_CODE=""
  CONNECT_PROCESS_QUIESCED=false

  terminate_connect_bounded() {
    CONNECT_PROCESS_QUIESCED=false
    return 125
  }
  clear_hook() {
    : >"${TEST_ROOT}/hook-released-after-termination-failure"
    return 0
  }
  bash() {
    : >"${TEST_ROOT}/teardown-ran-after-termination-failure"
    return 0
  }

  cleanup
)

set +e
run_termination_failure_blocks_release_case
termination_failure_status=$?
set -e
[[ "${termination_failure_status}" != "0" ]] || \
  fail_test "termination failure was not propagated by the real convergence cleanup"
[[ ! -e "${TEST_ROOT}/hook-released-after-termination-failure" ]] || \
  fail_test "hook was released without proven connect process quiescence"
[[ ! -e "${TEST_ROOT}/teardown-ran-after-termination-failure" ]] || \
  fail_test "shared teardown ran without proven connect process quiescence"
[[ -f "${E2E_TMP_ROOT}/tun-package-connect-termination-unproven" ]] || \
  fail_test "termination failure did not persist a workflow-cleanup guard"

CONNECT_PID=""
CONNECT_START_TIME=""
CONNECT_EXIT_CODE=""
CONNECT_PROCESS_QUIESCED=true
rm -f -- "${E2E_TMP_ROOT}/tun-package-connect-termination-unproven"

clear_hook() {
  [[ "${PODLAZ_E2E_PRE_RECOVERY_MANIFEST_STATE:-}" == "ready" ]] || \
    fail_test "hook release ran without a ready pre-release ownership proof"
  [[ -f "${PODLAZ_E2E_PRE_RECOVERY_MANIFEST_PATH:-}" ]] || \
    fail_test "hook release ran before the verification manifest existed"
  [[ "${PODLAZ_E2E_PRE_RECOVERY_MANIFEST_SHA256:-}" =~ ^[0-9a-f]{64}$ ]] || \
    fail_test "hook release ran without an immutable manifest checksum"

  # Model the real caller boundary: releasing the hook lets the daemon durably
  # add the exact desired main-table tuple, then rollback removes transaction
  # metadata while the route itself remains.
  : >"${TEST_ROOT}/residual-main-route"
  rm -f -- "${TRANSACTION_DIR}/tx.json"
}

bash() (
  [[ "${1:-}" == "${E2E_DIR}/tun-package-cleanup.sh" ]] || \
    fail_test "unexpected teardown command: $*"

  export PODLAZ_E2E_CLEANUP_SOURCE_ONLY=true
  # shellcheck source=../tun-package-cleanup.sh
  source "${E2E_DIR}/tun-package-cleanup.sh"

  require_cmd() { :; }
  cleanup_error() { :; }
  record_cleanup_evidence() { :; }
  clear_tun_hook() { return 0; }
  attempt_daemon_recovery() { return 0; }
  timeout() { return 0; }
  inspect_service_active_state() { return 0; }
  stop_owned_xray() { return 0; }
  cleanup_podlaz_resolved() { return 0; }
  cleanup_podlaz_nftables() { return 0; }
  cleanup_podlaz_link() { return 0; }
  cleanup_e2e_sentinels() { return 0; }
  assert_cleanup_complete() { return 1; }
  remove_generated_state() { : >"${TEST_ROOT}/generated-removed"; return 0; }
  remove_transaction_state() { : >"${TEST_ROOT}/transaction-removed"; return 0; }
  purge_package() { : >"${TEST_ROOT}/package-purged"; return 0; }

  snapshot_rollback_metadata() {
    cat >"${AUTHORITATIVE_ROLLBACK_MANIFEST}" <<'JSON'
{"routes": [], "rules": [], "schema_version": "podlaz.e2e.rollback-network.v1"}
JSON
    ROLLBACK_METADATA_VALID=true
    return 0
  }

  sudo() {
    if [[ "${1:-}" == "-n" ]]; then
      shift
    fi
    if [[ "${1:-}" == "python3" && "${2:-}" == "${FALLBACK_NETWORK_HELPER}" ]]; then
      case "${3:-}:${4:-}" in
        "cleanup:${AUTHORITATIVE_ROLLBACK_MANIFEST}") return 0 ;;
        "verify:${AUTHORITATIVE_ROLLBACK_MANIFEST}") return 0 ;;
        "verify:${PRE_RECOVERY_ROLLBACK_MANIFEST}")
          [[ -f "${TEST_ROOT}/residual-main-route" ]] && return 1
          return 0
          ;;
      esac
    fi
    command "$@"
  }

  teardown_main
)

set +e
(
  set +e
  cleanup
)
cleanup_status=$?
set -e

[[ "${cleanup_status}" != "0" ]] || \
  fail_test "real convergence trap accepted a residual main-table tuple"
sudo -n grep -F '203.0.113.10/32' "${E2E_TMP_ROOT}/tun-package-pre-recovery-network.json" >/dev/null || \
  fail_test "pre-release verification envelope missed the future exact tuple"
[[ ! -e "${TEST_ROOT}/generated-removed" ]] || \
  fail_test "generated state was removed after ownership verification failed"
[[ ! -e "${TEST_ROOT}/transaction-removed" ]] || \
  fail_test "transaction identity was removed after ownership verification failed"
[[ ! -e "${TEST_ROOT}/package-purged" ]] || \
  fail_test "package was purged after ownership verification failed"

printf 'tun package convergence cleanup boundary tests passed\n'
