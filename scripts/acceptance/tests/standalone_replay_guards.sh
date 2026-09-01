#!/usr/bin/env bash
set -Euo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd -P)"
# shellcheck source=/dev/null
source "$ROOT/scripts/acceptance/release-laptop.sh"

fail() { printf 'standalone_replay_guards: %s\n' "$*" >&2; exit 1; }
assert_eq() { [[ "$1" == "$2" ]] || fail "expected [$2], got [$1]"; }

for fn in \
  ra_package_setup_resume_prepare \
  ra_autostart_ensure_owned \
  ra_terminal_profile_ensure \
  ra_candidate_fault_seams_verify \
  ra_preflight_new_inputs_before_retire \
  ra_phase_blocks_auto_finalizer; do
  declare -F "$fn" >/dev/null || fail "missing replay guard seam: $fn"
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
ra_artifacts_init_new replay-run

candidate_path="$TMP/candidate.deb"
previous_path="$TMP/previous.deb"
candidate="$(jq -cn --arg p "$candidate_path" '{path:$p,package:"podlaz",version:"2.0",architecture:"amd64",sha256:"candidate",device:1,inode:2}')"
previous="$(jq -cn --arg p "$previous_path" '{path:$p,package:"podlaz",version:"1.0",architecture:"amd64",sha256:"previous",device:1,inode:3}')"
ra_state_init replay-run "$candidate" "2.0" '{"enabled":false}' profile-a
ra_state_jq '.private.run_config.previous=$p' --argjson p "$previous"

# Candidate mandatory fault seams are proven from the package payload before any
# checkpointed mutation. The helper inspects package bytes but never executes them.
seam_mode=complete
ra_pkg_assert_identity() { return 0; }
ra_capture() {
  if [[ "$1" == dpkg-deb && "$2" == -x ]]; then
    local root="$4"
    mkdir -p "$root/usr/lib/podlaz"
    {
      printf 'PODLAZ_E2E_TUN_ROLLBACK_PAUSE\n'
      printf 'PODLAZ_E2E_TUN_TERMINAL_FAILURE\n'
      [[ "$seam_mode" == complete ]] && printf 'PODLAZ_E2E_PRIVACY_TEARDOWN_PAUSE\n'
    } >"$root/usr/lib/podlaz/podlazd"
    chmod +x "$root/usr/lib/podlaz/podlazd"
    RA_CAPTURE=''
    RA_CAPTURE_RC=0
    return 0
  fi
  RA_CAPTURE='unsupported stub command'
  RA_CAPTURE_RC=1
  return 1
}
ra_candidate_fault_seams_verify "$candidate"
seam_mode=missing
set +e
ra_candidate_fault_seams_verify "$candidate" >/dev/null 2>&1
missing_seam_rc=$?
set -e
((missing_seam_rc != 0)) || fail "candidate missing a mandatory fault seam passed preflight"

# An interrupted lower-package acquisition with the exact lower version already
# installed is adopted without invoking dpkg a second time.
ra_state_jq '.mutations.package_setup={state:"acquiring",kind:"previous_package",scenario:"",identity:{previous:$p,candidate:$c}}' --argjson p "$previous" --argjson c "$candidate"
install_calls=0
ra_pkg_installed_version() { printf '1.0'; }
ra_pkg_install_exact() { install_calls=$((install_calls+1)); return 0; }
ra_wait_inactive() { return 0; }
ra_package_setup_resume_prepare "$candidate" "$previous"
assert_eq "$install_calls" 0
assert_eq "$(jq -r '.mutations.package_setup.state' "$RA_CHECKPOINT")" acquired

# Conversely, acquiring + exact pre-mutation package state is ambiguous: the
# previous dpkg invocation might not have started or might have failed before
# changing package state. Resume must not issue another dpkg blindly.
ra_state_jq '.mutations.package_setup={state:"acquiring",kind:"previous_package",scenario:"",identity:{previous:$p,candidate:$c}}' --argjson p "$previous" --argjson c "$candidate"
install_calls=0
ra_pkg_installed_version() { printf '2.0'; }
set +e
ra_package_setup_resume_prepare "$candidate" "$previous" >/dev/null 2>&1
package_retry_rc=$?
set -e
((package_retry_rc != 0)) || fail "ambiguous lower-package acquisition unexpectedly replayed"
assert_eq "$install_calls" 0

# An already-acquired autostart mutation must be observed, not replayed.
owned_manifest='{"enabled":true,"sha256":"owned"}'
pre_manifest='{"enabled":false}'
ra_state_jq '.mutations.autostart_enable={state:"acquired",kind:"autostart_policy",scenario:"reboot_autostart_on",identity:{action:"enable",profile:"profile-a",pre_manifest:$pre,owned_manifest:$owned}}' --argjson pre "$pre_manifest" --argjson owned "$owned_manifest"
autostart_calls=0
ra_manifest_matches_snapshot() { [[ "$1" == "$owned_manifest" ]]; }
ra_product() { autostart_calls=$((autostart_calls+1)); return 0; }
ra_autostart_ensure_owned autostart_enable enable profile-a
assert_eq "$autostart_calls" 0

# An acquiring autostart mutation that is still exactly at its pre-snapshot is
# not replay-safe, even when the pre-snapshot happens to look semantically like
# the target. It must fail closed rather than issue the mutation again.
pre_pending='{"enabled":true,"sha256":"pre"}'
ra_state_jq '.mutations.autostart_pending={state:"acquiring",kind:"autostart_policy",scenario:"reboot_autostart_on",identity:{action:"enable",profile:"profile-a",pre_manifest:$pre}}' --argjson pre "$pre_pending"
autostart_calls=0
ra_manifest_matches_snapshot() { [[ "$1" == "$pre_pending" ]]; }
ra_boot_manifest_semantic_matches() { return 0; }
ra_product() { autostart_calls=$((autostart_calls+1)); return 0; }
set +e
ra_autostart_ensure_owned autostart_pending enable profile-a >/dev/null 2>&1
autostart_retry_rc=$?
set -e
((autostart_retry_rc != 0)) || fail "ambiguous autostart acquisition unexpectedly replayed"
assert_eq "$autostart_calls" 0

# A synthetic terminal profile already discovered during an interrupted import
# must be adopted and returned without importing a duplicate.
ra_state_jq '.private.terminal_profile_acquisition={state:"acquiring",baseline_ids:["profile-a"]}'
profile_list_calls=0
import_calls=0
ra_profile_ids_json() { profile_list_calls=$((profile_list_calls+1)); printf '%s\n' '["profile-a","terminal-test"]'; }
ra_terminal_profile_is_exact() { [[ "$1" == terminal-test ]]; }
ra_product() { import_calls=$((import_calls+1)); return 0; }
id="$(ra_terminal_profile_ensure)"
assert_eq "$id" terminal-test
assert_eq "$import_calls" 0
assert_eq "$(jq -r '.private.terminal_profile_acquisition.state' "$RA_CHECKPOINT")" acquired

# Acquiring with no discovered profile is ambiguous and must not import again.
ra_state_jq '.private.terminal_profile_acquisition={state:"acquiring",baseline_ids:["profile-a"]}'
import_calls=0
ra_profile_ids_json() { printf '%s\n' '["profile-a"]'; }
ra_product() { import_calls=$((import_calls+1)); return 0; }
set +e
ra_terminal_profile_ensure >/dev/null 2>&1
terminal_retry_rc=$?
set -e
((terminal_retry_rc != 0)) || fail "ambiguous terminal-profile import unexpectedly replayed"
assert_eq "$import_calls" 0

# Pre-retire validation must classify the package version that cleanup is proven
# to leave behind, not a temporary lower version still installed by the old run.
predicted_installed=''
RA_CANDIDATE="$candidate_path"
RA_PREVIOUS_DEB=''
RA_PROFILE=profile-a
ra_pkg_inspect() { printf '%s' "$candidate"; }
ra_pkg_installed_version() { printf '1.0'; }
ra_preflight_release_boundary() { predicted_installed="$3"; return 0; }
ra_validate_artifact_root() { return 0; }
ra_profile_validate() { return 0; }
ra_candidate_fault_seams_verify() { return 0; }
ra_preflight_new_inputs_before_retire
assert_eq "$predicted_installed" 2.0

# An explicit profile owned by the old run would be deleted during retirement.
# It must be rejected before old-run cleanup rather than failing only afterward.
ra_state_jq '.private.terminal_profile="terminal-test"'
RA_PROFILE=terminal-test
set +e
ra_preflight_new_inputs_before_retire >/dev/null 2>&1
synthetic_profile_rc=$?
set -e
((synthetic_profile_rc != 0)) || fail "owned synthetic profile was accepted as a fresh-run input"
RA_PROFILE=profile-a

# Unknown/invalid daemon payloads are protocol failures, not full-timeout progress.
RA_PRIVATE_DIR="$TMP/private-status"
mkdir -p "$RA_PRIVATE_DIR"
ra_capture() { RA_CAPTURE='not-json'; RA_CAPTURE_RC=0; return 0; }
set +e
ra_status_json >/dev/null 2>&1
status_rc=$?
set -e
assert_eq "$status_rc" 2

# Empty stderr from a failed exact resource lookup is not absence evidence on a
# real host. Test mode is deliberately disabled only for this pure observation.
export RELEASE_ACCEPTANCE_TEST_MODE=0
ra_capture() { RA_CAPTURE=''; RA_CAPTURE_RC=1; return 1; }
set +e
ra_observe_link_exact podlaz-accept-a0 tun >/dev/null
observe_rc=$?
set -e
((observe_rc != 0)) || fail "empty failed ip lookup was treated as proven absence"
export RELEASE_ACCEPTANCE_TEST_MODE=1

# A completed disruptive daemon restart must be adopted after shell death instead
# of issuing the mutation a second time.
ra_set_phase running-pre-reboot
ra_scenario_set_state graceful_restart running
ra_state_jq '.scenarios.graceful_restart.private={before_pid:100,mutation_requested:true}'
systemctl_calls=0
ra_main_pid() { printf '200'; }
ra_wait_active() { return 0; }
ra_privacy_require_protected() { return 0; }
ra_privacy_watch_cancel() { return 0; }
ra_capture() { systemctl_calls=$((systemctl_calls+1)); return 0; }
ra_recover_running_scenario graceful_restart
assert_eq "$systemctl_calls" 0
assert_eq "$(jq -r '.scenarios.graceful_restart.state' "$RA_CHECKPOINT")" passed

# The same persisted request with the original PID still alive is ambiguous and
# must not issue a second restart.
ra_scenario_set_state graceful_restart running
ra_state_jq '.scenarios.graceful_restart.private={before_pid:100,mutation_requested:true}'
systemctl_calls=0
ra_main_pid() { printf '100'; }
set +e
ra_recover_running_scenario graceful_restart >/dev/null 2>&1
restart_retry_rc=$?
set -e
((restart_retry_rc != 0)) || fail "ambiguous daemon restart unexpectedly replayed"
assert_eq "$systemctl_calls" 0

# Running scenarios whose recovery would release state and then execute the same
# disruptive scenario again are restart-required. They are retired exactly and a
# fresh run may start; they are never replayed inside the old run.
for nonreplay in rollback_interruption warmed_inactive_candidate_baseline preconnect_coexistence resource_soak disconnect_cleanup runtime_terminal_convergence; do
  ra_set_phase running-pre-reboot
  ra_state_jq '.current_scenario=$n|.scenarios[$n]=((.scenarios[$n]//{name:$n})+{state:"running"})' --arg n "$nonreplay"
  assert_eq "$(ra_existing_checkpoint_classify)" cleanup-restart
done

# Fine-grained lifecycle/package scenarios with explicit live adoption remain
# eligible for automatic continuation, and verifying never reruns its mutation.
for replayable in graceful_restart daemon_kill stop_start_no_reconnect lower_release_upgrade reinstall; do
  ra_set_phase running-pre-reboot
  ra_state_jq '.current_scenario=$n|.scenarios[$n]=((.scenarios[$n]//{name:$n})+{state:"running"})' --arg n "$replayable"
  assert_eq "$(ra_existing_checkpoint_classify)" replay-safe
done
ra_state_jq '.current_scenario="resource_soak"|.scenarios.resource_soak.state="verifying"'
assert_eq "$(ra_existing_checkpoint_classify)" replay-safe
ra_state_jq '.current_scenario="future_unknown"|.scenarios.future_unknown={name:"future_unknown",state:"running"}'
assert_eq "$(ra_existing_checkpoint_classify)" ambiguous

# The reboot boundary must be durable before autostart is mutated.
ra_set_phase running-pre-reboot
observed_prepare_phase=''
ra_autostart_ensure_owned() { observed_prepare_phase="$(jq -r '.phase' "$RA_CHECKPOINT")"; return 0; }
ra_boot_id() { printf 'boot-a'; }
ra_prepare_reboot_off >/dev/null
assert_eq "$observed_prepare_phase" preparing-reboot-autostart-off
assert_eq "$(jq -r '.phase' "$RA_CHECKPOINT")" await-reboot-autostart-off

# A retained/ambiguous phase explicitly blocks generic automatic finalization.
ra_set_phase fail-cleanup-failed
ra_phase_blocks_auto_finalizer || fail "fail-cleanup-failed did not block generic finalizer"
ra_set_phase await-reboot-autostart-off
ra_phase_blocks_auto_finalizer || fail "reboot wait did not block generic finalizer"

printf 'standalone_replay_guards: PASS\n'