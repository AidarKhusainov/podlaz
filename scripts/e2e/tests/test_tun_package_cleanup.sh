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

last_evidence=""
record_cleanup_evidence() {
  last_evidence="$1=$2"
}
cleanup_error() { :; }

run_unproven_connect_termination_guard_case() (
  local guard="${E2E_TMP_ROOT}/tun-package-connect-termination-unproven"
  : >"${guard}"
  : >"${TEST_ROOT}/guard-fixture-created"

  require_cmd() { :; }
  snapshot_pre_recovery_metadata() {
    : >"${TEST_ROOT}/snapshot-ran-with-unproven-connect"
    return 0
  }
  clear_tun_hook() {
    : >"${TEST_ROOT}/hook-cleared-with-unproven-connect"
    return 0
  }
  attempt_daemon_recovery() { return 0; }
  fallback_cleanup() { return 0; }
  cleanup_e2e_sentinels() { return 0; }
  purge_package_if_safe() { return 0; }
  assert_cleanup_complete() { return 0; }
  record_cleanup_evidence() { :; }
  cleanup_error() { :; }

  if teardown_main; then
    fail_test "workflow cleanup accepted unproven connect termination"
  fi
  [[ ! -e "${TEST_ROOT}/snapshot-ran-with-unproven-connect" ]] || \
    fail_test "workflow cleanup captured or mutated state with unproven connect termination"
  [[ ! -e "${TEST_ROOT}/hook-cleared-with-unproven-connect" ]] || \
    fail_test "workflow cleanup released the hook with unproven connect termination"
)
run_unproven_connect_termination_guard_case
rm -f -- "${E2E_TMP_ROOT}/tun-package-connect-termination-unproven"

# A timed-out purge must remain a failure and may never publish success.
package_inspections=0
inspect_package_state() {
  package_inspections=$((package_inspections + 1))
  return 1
}
timeout() { return 124; }
sudo() { return 0; }
if purge_package; then
  fail_test "failed purge reported success"
fi
[[ "${last_evidence}" == "package_purged=false" ]] || fail_test "failed purge evidence was ${last_evidence}"

# Invalid metadata must prevent transaction deletion even if every unrelated
# fallback cleanup action succeeds.
ROLLBACK_METADATA_VALID=false
transaction_remove_calls=0
inspect_service_active_state() { return 0; }
stop_owned_xray() { return 0; }
cleanup_podlaz_resolved() { return 0; }
cleanup_podlaz_nftables() { return 0; }
cleanup_recorded_network() { return 1; }
cleanup_podlaz_link() { return 0; }
remove_generated_state() { return 0; }
remove_transaction_state() { transaction_remove_calls=$((transaction_remove_calls + 1)); return 0; }
if fallback_cleanup; then
  fail_test "fallback accepted invalid metadata"
fi
[[ "${transaction_remove_calls}" == "0" ]] || fail_test "invalid transaction metadata was removed"

# A sentinel inspection error must be propagated instead of treated as absence.
inspect_service_load_state() { return 0; }
inspect_link_state() { return 0; }
inspect_sentinel_rule_state() { return 2; }
inspect_sentinel_route_state() { return 0; }
inspect_nft_table_state() { return 0; }
inspect_e2e_sentinel_state() { return 2; }
if cleanup_e2e_sentinels; then
  fail_test "sentinel inspection failure was suppressed"
fi
[[ "${last_evidence}" == "e2e_sentinels_removed=false" ]] || fail_test "sentinel failure evidence was ${last_evidence}"

# The concrete Xray scanner must distinguish pgrep failure from no matches.
sudo() {
  if [[ "$*" == *"pgrep -f"* ]]; then
    return 2
  fi
  return 0
}
if inspect_owned_xray_state; then
  fail_test "Xray pgrep failure was treated as absence"
else
  xray_status=$?
fi
[[ "${xray_status}" == "2" ]] || fail_test "Xray pgrep failure returned ${xray_status}"

install_clean_assertion_baseline() {
  ROLLBACK_METADATA_VALID=true
  inspect_link_state() { return 0; }
  inspect_recorded_network_state() { return 0; }
  inspect_reserved_network_state() { return 0; }
  inspect_resolved_link_state() { return 0; }
  inspect_nft_table_state() { return 0; }
  inspect_owned_xray_state() { return 0; }
  inspect_directory_content_state() { return 0; }
  inspect_service_active_state() { return 0; }
  inspect_path_state() { return 0; }
  inspect_e2e_sentinel_state() { return 0; }
  inspect_package_state() { return 0; }
  assert_direct_connectivity() { return 0; }
}

run_inspection_failure_case() (
  local inspector="$1"
  install_clean_assertion_baseline
  last_evidence=""
  case "${inspector}" in
    link) inspect_link_state() { return 2; } ;;
    recorded-network) inspect_recorded_network_state() { return 2; } ;;
    reserved-network) inspect_reserved_network_state() { return 2; } ;;
    resolved) inspect_resolved_link_state() { return 2; } ;;
    nftables) inspect_nft_table_state() { return 2; } ;;
    xray) inspect_owned_xray_state() { return 2; } ;;
    directory) inspect_directory_content_state() { return 2; } ;;
    service) inspect_service_active_state() { return 2; } ;;
    path) inspect_path_state() { return 2; } ;;
    sentinels) inspect_e2e_sentinel_state() { return 2; } ;;
    package) inspect_package_state() { return 2; } ;;
    connectivity) assert_direct_connectivity() { return 1; } ;;
    *) fail_test "unknown inspector test ${inspector}" ;;
  esac
  if assert_cleanup_complete; then
    fail_test "${inspector} operational error produced cleanup success"
  fi
  [[ "${last_evidence}" == "cleanup_assertions=fail" ]] || \
    fail_test "${inspector} failure evidence was ${last_evidence}"
)

for inspector in \
  link recorded-network reserved-network resolved nftables xray directory service path sentinels package connectivity; do
  run_inspection_failure_case "${inspector}"
done

install_clean_assertion_baseline
last_evidence=""
assert_cleanup_complete || fail_test "clean state assertions unexpectedly failed"
[[ "${last_evidence}" == "cleanup_assertions=pass" ]] || fail_test "cleanup success evidence was ${last_evidence}"

printf 'tun package cleanup tests passed\n'
