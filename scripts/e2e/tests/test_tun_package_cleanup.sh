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

# A failed purge must fail and must never claim package_purged=true.
package_present() { return 0; }
timeout() { return 124; }
sudo() { return 0; }
if purge_package; then
  fail_test "failed purge reported success"
fi
[[ "${last_evidence}" == "package_purged=false" ]] || fail_test "failed purge evidence was ${last_evidence}"

# Invalid metadata must prevent transaction removal.
ROLLBACK_METADATA_VALID=false
transaction_remove_calls=0
systemctl() { return 1; }
stop_owned_xray() { return 0; }
cleanup_podlaz_resolved() { return 0; }
cleanup_podlaz_nftables() { return 0; }
cleanup_podlaz_link() { return 0; }
remove_generated_state() { return 0; }
remove_transaction_state() { transaction_remove_calls=$((transaction_remove_calls + 1)); return 0; }
if fallback_cleanup; then
  fail_test "fallback accepted invalid metadata"
fi
[[ "${transaction_remove_calls}" == "0" ]] || fail_test "invalid transaction metadata was removed"

# Sentinel cleanup failures must propagate and record failure.
systemctl() { return 1; }
sentinel_rule_present() { return 0; }
sentinel_route_present() { return 1; }
sudo() {
  if [[ "$*" == *"rule del"* ]]; then
    return 1
  fi
  return 1
}
if cleanup_e2e_sentinels; then
  fail_test "sentinel cleanup failure was suppressed"
fi
[[ "${last_evidence}" == "e2e_sentinels_removed=false" ]] || fail_test "sentinel failure evidence was ${last_evidence}"

# Final success requires no reserved collision, direct connectivity, and package absence.
ROLLBACK_METADATA_VALID=true
direct_called=0
reserved_network_state_present() { return 1; }
sentinel_service_present() { return 1; }
resolved_has_podlaz_link() { return 1; }
owned_xray_identities() { return 0; }
sentinel_rule_present() { return 1; }
sentinel_route_present() { return 1; }
package_present() { return 1; }
systemctl() { return 1; }
assert_direct_connectivity() { direct_called=$((direct_called + 1)); return 0; }
sudo() {
  if [[ "$*" == *"python3"*" verify "* ]]; then
    return 0
  fi
  return 1
}

reserved_network_state_present() { return 0; }
if assert_cleanup_complete; then
  fail_test "reserved network conflict was accepted"
fi
reserved_network_state_present() { return 1; }
direct_called=0

assert_cleanup_complete || fail_test "clean state assertions unexpectedly failed"
[[ "${direct_called}" == "1" ]] || fail_test "direct connectivity was not asserted"
[[ "${last_evidence}" == "cleanup_assertions=pass" ]] || fail_test "cleanup success evidence was ${last_evidence}"

printf 'tun package cleanup tests passed\n'
