#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd -P)"
SCRIPT="$ROOT/scripts/acceptance/release-laptop.sh"
# shellcheck source=/dev/null
source "$SCRIPT"

fail() { printf 'standalone_status_semantics: %s\n' "$*" >&2; exit 1; }
assert_eq() { [[ "$1" == "$2" ]] || fail "expected [$2], got [$1]"; }

classify() { ra_status_classify "$1" "$2"; }

invalid='{"connection":7,"transactions":[]}'
assert_eq "$(classify active "$invalid")" CONTRACT_INCOMPATIBLE

inactive='{"connection":"inactive","transactions":[]}'
assert_eq "$(classify inactive "$inactive")" TARGET_REACHED

active='{"connection":"active","mode":"tun","active_transaction_id":"tx-example","transactions":[{"id":"tx-example","state":"committed","requires_cleanup":false}],"tun_health":{"state":"verified","network_generation":4}}'
assert_eq "$(classify active "$active")" TARGET_REACHED

reconnecting='{"connection":"active","mode":"tun","lifecycle_phase":"reconnecting","active_transaction_id":"tx-example","transactions":[{"id":"tx-example","state":"committed","requires_cleanup":false}],"tun_health":{"state":"revalidating","network_generation":5}}'
assert_eq "$(classify active "$reconnecting")" TRANSIENT_PROGRESS

cleanup_pending='{"connection":"error (network session cleanup pending)","transactions":[],"startup_scan":{"status":"incomplete","network_session":{"authority":"present","intent":"resume","startup_gate":"blocked","resume_stage":"connect-replay","last_resume_outcome":"failed","last_tun_failure_phase":"preflight","rollback_status":"not-started","transaction_present":false,"legacy_migration":true,"cleanup_authority":"none","next_action":"retry-resume"}}}'
assert_eq "$(classify active "$cleanup_pending")" OWNERSHIP_AMBIGUOUS

cleanup_tx='{"connection":"active","mode":"tun","transactions":[{"id":"tx-example","state":"rolling_back","requires_cleanup":true}],"tun_health":{"state":"cleanup-required"}}'
assert_eq "$(classify active "$cleanup_tx")" OWNERSHIP_AMBIGUOUS

terminal='{"connection":"inactive","transactions":[],"terminal_reason":"connect_failed"}'
assert_eq "$(classify active "$terminal")" TERMINAL_PRODUCT_FAILURE
assert_eq "$(classify inactive "$terminal")" TARGET_REACHED

ambiguous='{"connection":"inactive","transactions":[],"startup_scan":{"status":"incomplete","network_session":{"authority":"present","intent":"resume","startup_gate":"blocked","last_resume_outcome":"incomplete","transaction_present":false,"legacy_migration":false,"cleanup_authority":"session-protection","next_action":"manual-diagnosis"}}}'
assert_eq "$(classify active "$ambiguous")" OWNERSHIP_AMBIGUOUS

malformed_transactions='{"connection":"active","mode":"tun","transactions":{}}'
assert_eq "$(classify active "$malformed_transactions")" CONTRACT_INCOMPATIBLE

legacy_active='{"connection":"active","mode":"tun","active_transaction_id":"tx-example","transactions":[{"id":"tx-example","state":"committed","requires_cleanup":false}]}'
assert_eq "$(classify active-legacy "$legacy_active")" TARGET_REACHED

service_failure="$({
  ra_capture() { RA_CAPTURE=$'ActiveState=failed\nSubState=failed\nResult=timeout'; RA_CAPTURE_RC=0; return 0; }
  ra_service_wait_classify
})"
assert_eq "$service_failure" SERVICE_FAILURE

observation_unavailable="$({
  ra_capture() { RA_CAPTURE=''; RA_CAPTURE_RC=1; return 1; }
  ra_service_wait_classify
})"
assert_eq "$observation_unavailable" OBSERVATION_UNAVAILABLE

RA_LAST_FAILURE_REASON=''
ra_status_json() { printf '%s' "$cleanup_pending"; }
if ra_wait_status active 5 >/dev/null 2>&1; then
  fail "cleanup-pending status unexpectedly reached active"
fi
[[ "$RA_LAST_FAILURE_REASON" == ownership* ]] || fail "cleanup-pending wait lost ownership classification: $RA_LAST_FAILURE_REASON"
[[ "$RA_LAST_FAILURE_REASON" != *status_contract_incompatible* ]] || fail "valid lifecycle failure was called contract incompatible"

fp1="$(ra_status_progress_fingerprint "$active")"
active2="$(jq '.tun_health.network_generation=5' <<<"$active")"
fp2="$(ra_status_progress_fingerprint "$active2")"
[[ "$fp1" != "$fp2" ]] || fail "network generation progress was not observable"
if grep -Fq 'tx-example' <<<"$fp1$fp2"; then fail "progress fingerprint retained transaction identity"; fi

printf 'standalone_status_semantics: PASS\n'
