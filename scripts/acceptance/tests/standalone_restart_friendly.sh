#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd -P)"
SCRIPT="$ROOT/scripts/acceptance/release-laptop.sh"
# shellcheck source=/dev/null
source "$SCRIPT"

fail() { printf 'standalone_restart_friendly: %s\n' "$*" >&2; exit 1; }
assert_eq() { [[ "$1" == "$2" ]] || fail "expected [$2], got [$1]"; }

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

status_active='{"schema_version":"v1","connection":"active","mode":"tun","tun":"enabled (podlaz0)","tun_health":{"state":"verified"},"transactions":[{"state":"committed","requires_cleanup":false}]}'
status_revalidating='{"schema_version":"v1","connection":"active","mode":"tun","tun":"enabled (podlaz0)","tun_health":{"state":"revalidating"},"transactions":[{"state":"committed","requires_cleanup":false}]}'
status_terminal='{"schema_version":"v1","connection":"inactive","mode":"tun","tun":"disabled","tun_health":{"state":"terminal"},"transactions":[{"state":"terminal","requires_cleanup":false}]}'

# Regression: the human-readable `tun` field is presentation only.
ra_status_json() { printf '%s\n' "$status_active"; }
ra_wait_active 1 >/dev/null 2>&1 || fail "semantic active status with formatted tun presentation did not satisfy wait active"

# Regression: revalidating is progress and must converge to verified without being classified as failure.
sequence_file="$TMP/status-sequence"
printf '%s\n%s\n' "$status_revalidating" "$status_active" >"$sequence_file"
ra_status_json() {
  local first
  first="$(head -n1 "$sequence_file")"
  if [[ "$(wc -l <"$sequence_file")" -gt 1 ]]; then tail -n +2 "$sequence_file" >"$sequence_file.next"; mv "$sequence_file.next" "$sequence_file"; fi
  printf '%s\n' "$first"
}
ra_wait_active 2 >/dev/null 2>&1 || fail "revalidating did not progress to verified active state"

# Regression: authoritative terminal/impossible state must stop polling immediately.
calls_file="$TMP/status-calls"
: >"$calls_file"
ra_status_json() { printf 'x\n' >>"$calls_file"; printf '%s\n' "$status_terminal"; }
if ra_wait_active 2 >/dev/null 2>&1; then fail "terminal status unexpectedly satisfied active wait"; fi
calls="$(wc -l <"$calls_file" | tr -d ' ')"
[[ "$calls" -le 2 ]] || fail "terminal state consumed repeated polling instead of failing immediately (calls=$calls)"

# Regression: if the candidate is already installed, a strictly-lower release is mandatory
# before any checkpoint is created. Permanent preflight failure must leave no run checkpoint.
rm -rf "$RELEASE_ACCEPTANCE_STATE_DIR" "$TMP/artifacts"
mkdir -p "$RELEASE_ACCEPTANCE_STATE_DIR"
RA_CHECKPOINT="$RELEASE_ACCEPTANCE_STATE_DIR/current.json"
RA_ARTIFACT_DIR="$TMP/artifacts"
RA_CANDIDATE="$TMP/candidate.deb"
RA_PREVIOUS_DEB=""
RA_PROFILE="profile-a"
state_init_called=0
artifacts_init_called=0
ra_checkpoint_exists() { return 1; }
ra_pkg_inspect() { jq -cn --arg p "$1" '{path:$p,package:"podlaz",version:"2.0",architecture:"amd64",sha256:"candidate",device:1,inode:1}'; }
ra_pkg_installed_version() { printf '2.0'; }
ra_preflight_clean_boundary() { return 0; }
ra_boot_manifest_capture() { printf '%s\n' '{"enabled":false}'; }
ra_artifacts_init_new() { artifacts_init_called=1; return 0; }
ra_privacy_baseline() { printf '%s\n' '{"uplink":"eth-test","host":"example.invalid","port":443,"ip":"192.0.2.10"}'; }
ra_profile_select() { printf '%s\n' profile-a; }
ra_state_init() { state_init_called=1; printf '%s\n' '{}' >"$RA_CHECKPOINT"; return 0; }
ra_state_jq() { return 0; }
ra_package_setup_prepare() { return 1; }
ra_set_phase() { return 0; }
ra_run_pre_reboot() { return 0; }
if ra_run_new >/dev/null 2>&1; then fail "candidate-already-installed run without lower release unexpectedly passed preflight"; fi
assert_eq "$artifacts_init_called" 0
assert_eq "$state_init_called" 0
[[ ! -e "$RA_CHECKPOINT" ]] || fail "permanent preflight failure created a checkpoint"

printf 'standalone_restart_friendly: PASS\n'