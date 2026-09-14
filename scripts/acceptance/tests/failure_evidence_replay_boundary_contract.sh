#!/usr/bin/env bash
set -Eeuo pipefail
umask 0077

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd -P)"
SCRIPT="$ROOT/scripts/acceptance/release-laptop.sh"
# shellcheck source=/dev/null
source "$SCRIPT"

fail() { printf 'failure_evidence_replay_boundary_contract: %s\n' "$*" >&2; exit 1; }
assert_eq() { [[ "$1" == "$2" ]] || fail "$3: got [$1], want [$2]"; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
export RELEASE_ACCEPTANCE_TEST_MODE=1
RA_USER=tester
RA_UID="$(id -u)"
RA_GID="$(id -g)"
RA_HOME="$TMP/home"
mkdir -m 0700 "$RA_HOME"
RA_STATE_DIR="$TMP/state"
mkdir -m 0700 "$RA_STATE_DIR"
RA_CHECKPOINT="$RA_STATE_DIR/current.json"
RA_LOCK_FILE="$RA_STATE_DIR/lock"
RA_CURRENT_SCENARIO=lower_release_upgrade

TEST_INSTALLED_VERSION=""
TEST_WAIT_ACTIVE_COUNT=0
TEST_MARKER_CLEARED_COUNT=0

write_checkpoint() {
  cat >"$RA_CHECKPOINT" <<JSON
{
  "schema_version":"podlaz.release-acceptance-checkpoint.v5",
  "candidate":{"package":"podlaz","version":"2.0","architecture":"amd64","sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
  "mutations":{},
  "scenarios":{"lower_release_upgrade":{"name":"lower_release_upgrade","state":"running","private":{}}},
  "private":{"selected_profile":"profile-a","service_active_before":true}
}
JSON
  chmod 0600 "$RA_CHECKPOINT"
}

marker() {
  jq -r '.scenarios.lower_release_upgrade.private.replay_diagnostic_required // false' "$RA_CHECKPOINT"
}

ra_pkg_installed_version() { printf '%s' "$TEST_INSTALLED_VERSION"; }
ra_connect() { return 0; }
ra_wait_active_legacy() { return 0; }
ra_main_pid() { printf '101'; }
ra_privacy_local_proof() { return 1; }
ra_privacy_watch_start() { return 0; }
ra_privacy_watch_stop() { return 0; }
ra_privacy_watch_cancel() { return 0; }
ra_candidate_upgrade_begin() {
  ra_state_jq '.mutations.candidate_upgrade={state:"acquiring",identity:{applied:false}}'
}
ra_pkg_install_exact() { TEST_INSTALLED_VERSION=2.0; }
ra_mut_mark_acquired() { return 0; }
ra_wait_new_pid() { return 0; }
ra_wait_active() {
  assert_eq "$(marker)" true 'candidate replay observation marker during active wait'
  ((TEST_WAIT_ACTIVE_COUNT+=1))
  return 0
}
ra_package_setup_release_after_candidate() {
  if ((TEST_WAIT_ACTIVE_COUNT > TEST_MARKER_CLEARED_COUNT)); then
    assert_eq "$(marker)" false 'candidate replay observation marker after active convergence'
    TEST_MARKER_CLEARED_COUNT="$TEST_WAIT_ACTIVE_COUNT"
  fi
  return 0
}
ra_profile_validate() { return 0; }
ra_privacy_require_protected() {
  if ((TEST_WAIT_ACTIVE_COUNT > TEST_MARKER_CLEARED_COUNT)); then
    assert_eq "$(marker)" false 'candidate replay observation marker before post-convergence checks'
    TEST_MARKER_CLEARED_COUNT="$TEST_WAIT_ACTIVE_COUNT"
  fi
  return 0
}
ra_record() { return 0; }
ra_mut_begin_release() { return 0; }
ra_mut_mark_released() { return 0; }

# Resume after candidate installation still observes the candidate replay boundary.
write_checkpoint
TEST_INSTALLED_VERSION=2.0
TEST_WAIT_ACTIVE_COUNT=0
TEST_MARKER_CLEARED_COUNT=0
ra_scenario_lower_upgrade || fail 'candidate-installed resume path failed'
assert_eq "$TEST_WAIT_ACTIVE_COUNT" 1 'candidate-installed resume active wait count'
assert_eq "$(marker)" false 'candidate-installed resume final marker'

# Fresh lower-release -> candidate replacement observes the same boundary after dpkg.
write_checkpoint
TEST_INSTALLED_VERSION=1.0
TEST_WAIT_ACTIVE_COUNT=0
TEST_MARKER_CLEARED_COUNT=0
ra_scenario_lower_upgrade || fail 'fresh candidate upgrade path failed'
assert_eq "$TEST_WAIT_ACTIVE_COUNT" 1 'fresh candidate upgrade active wait count'
assert_eq "$(marker)" false 'fresh candidate upgrade final marker'

printf 'failure_evidence_replay_boundary_contract: PASS\n'
