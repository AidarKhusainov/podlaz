#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd -P)"
SCRIPT="$ROOT/scripts/acceptance/release-laptop.sh"
# shellcheck source=/dev/null
source "$SCRIPT"

fail() { printf 'standalone_failure_recovery: %s\n' "$*" >&2; exit 1; }
assert_eq() { [[ "$1" == "$2" ]] || fail "expected [$2], got [$1]"; }
assert_contains() { grep -Fq -- "$2" <<<"$1" || fail "expected [$1] to contain [$2]"; }

for fn in ra_failure_finalize ra_safe_cleanup ra_failure_bundle_capture ra_existing_checkpoint_classify ra_scenario_set_state ra_signal_handler; do
  declare -F "$fn" >/dev/null || fail "missing restart-friendly controller seam: $fn"
done

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
export RELEASE_ACCEPTANCE_TEST_MODE=1
export RELEASE_ACCEPTANCE_TEST_HOME="$TMP/home"
export RELEASE_ACCEPTANCE_STATE_DIR="$TMP/state"
export RELEASE_ACCEPTANCE_USER_STATE_HOME="$TMP/home/.local/state"
export SUDO_USER=tester
mkdir -p "$RELEASE_ACCEPTANCE_TEST_HOME"
RA_USER=tester
RA_UID="$(id -u)"
RA_GID="$(id -g)"
RA_HOME="$RELEASE_ACCEPTANCE_TEST_HOME"
ra_init_paths
RA_ARTIFACT_DIR="$RA_HOME/artifacts"
ra_artifacts_init_new failure-run
candidate="$(jq -cn --arg p "$TMP/candidate.deb" '{path:$p,package:"podlaz",version:"2.0",architecture:"amd64",sha256:"unused",device:1,inode:2}')"
ra_state_init failure-run "$candidate" "1.0" '{"enabled":false}' profile-a
ra_set_phase running-pre-reboot

# Persisted scenario state is explicit and monotonic through the required controller states.
for state in pending prepared running verifying passed failed; do
  ra_scenario_set_state scenario-a "$state"
  assert_eq "$(jq -r '.scenarios["scenario-a"].state' "$RA_CHECKPOINT")" "$state"
done

# Evidence capture happens before any cleanup and a clean reconciliation removes the checkpoint.
order_file="$TMP/finalizer-order"
output_file="$TMP/finalizer-output"
: >"$order_file"
removed=0
ra_failure_bundle_capture() { printf 'diagnostics\n' >>"$order_file"; return 0; }
ra_safe_cleanup() { printf 'cleanup\n' >>"$order_file"; return 0; }
ra_report_write() { return 0; }
ra_state_remove() { printf 'remove\n' >>"$order_file"; removed=1; return 0; }
set +e
ra_failure_finalize unexpected_test_failure 19 >"$output_file"
rc=$?
set -e
output="$(cat "$output_file")"
((rc != 0)) || fail "failure finalizer must preserve a failing exit status"
assert_contains "$output" FAILED_CLEAN
assert_eq "$(head -n1 "$order_file")" diagnostics
[[ "$removed" == 1 ]] || fail "clean finalization did not remove checkpoint"

# Ambiguous cleanup retains the checkpoint and reports cleanup failure.
: >"$order_file"
removed=0
RA_FINALIZER_ACTIVE=0
ra_failure_bundle_capture() { printf 'diagnostics\n' >>"$order_file"; return 0; }
ra_safe_cleanup() { printf 'cleanup\n' >>"$order_file"; return 1; }
set +e
ra_failure_finalize ambiguous_cleanup 23 >"$output_file"
rc=$?
set -e
output="$(cat "$output_file")"
((rc != 0)) || fail "ambiguous failure finalizer unexpectedly succeeded"
assert_contains "$output" FAIL_CLEANUP_FAILED
[[ "$removed" == 0 ]] || fail "ambiguous cleanup removed the checkpoint"

# The finalizer is guarded against recursion. A nested invocation must not capture or clean twice.
RA_FINALIZER_ACTIVE=1
: >"$order_file"
set +e
ra_failure_finalize recursive 25 >/dev/null
rc=$?
set -e
((rc != 0)) || fail "recursive finalizer invocation unexpectedly succeeded"
[[ ! -s "$order_file" ]] || fail "recursive finalizer invocation performed cleanup work"
RA_FINALIZER_ACTIVE=0

# Reboot wait is an intentional pause: ordinary invocation must retain it and require --resume.
ra_set_phase await-reboot-autostart-off
assert_eq "$(ra_existing_checkpoint_classify)" reboot-wait
ra_set_phase await-reboot-autostart-on
assert_eq "$(ra_existing_checkpoint_classify)" reboot-wait
ra_set_phase await-reboot-terminal
assert_eq "$(ra_existing_checkpoint_classify)" reboot-wait

# Replay-safe and failed-cleanable states are classified separately from ambiguous ownership.
ra_set_phase preparing-lower-release
assert_eq "$(ra_existing_checkpoint_classify)" replay-safe
ra_set_phase failed-cleanable
assert_eq "$(ra_existing_checkpoint_classify)" cleanup-restart
ra_set_phase fail-cleanup-failed
assert_eq "$(ra_existing_checkpoint_classify)" ambiguous

# --restart is a real new-run mode, not an alias for broad abort.
RA_MODE=new
RA_CANDIDATE=""
RA_PREVIOUS_DEB=""
RA_PROFILE=""
RA_ARTIFACT_DIR=""
RA_SOAK_MINUTES=60
RA_ALLOW_WIFI=1
RA_ALLOW_SUSPEND=1
RA_REBOOT_PHASES=1
ra_cli_parse --restart "$TMP/candidate.deb"
assert_eq "$RA_MODE" restart
assert_eq "$RA_CANDIDATE" "$TMP/candidate.deb"

# A retained cleanup ambiguity must not be mutated again just because SIGTERM
# arrives while the operator is inspecting it.
signal_calls=0
ra_failure_finalize() { signal_calls=$((signal_calls+1)); return 1; }
ra_set_phase fail-cleanup-failed
set +e
ra_signal_handler TERM >/dev/null 2>&1
set -e
assert_eq "$signal_calls" 0

# SIGTERM during a normal post-checkpoint run still routes through the guarded finalizer.
ra_set_phase running-pre-reboot
set +e
ra_signal_handler TERM >/dev/null 2>&1
set -e
assert_eq "$signal_calls" 1

printf 'standalone_failure_recovery: PASS\n'
