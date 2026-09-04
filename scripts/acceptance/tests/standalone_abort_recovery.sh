#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd -P)"
# shellcheck source=/dev/null
source "$ROOT/scripts/acceptance/release-laptop.sh"

fail() { printf 'standalone_abort_recovery: %s\n' "$*" >&2; exit 1; }

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
ra_artifacts_init_new test-run

candidate_path="$TMP/missing-candidate.deb"
candidate="$(jq -cn --arg p "$candidate_path" '{path:$p,package:"podlaz",version:"2.0",architecture:"amd64",sha256:"deadbeef",device:1,inode:2}')"
manifest='{"enabled":false}'
ra_state_init test-run "$candidate" "1.0" "$manifest" profile-a
ra_set_phase running-pre-reboot

# The candidate .deb has disappeared, but the exact candidate version is already installed.
# Abort must not require package bytes unless it actually needs to reinstall/restore them.
ra_pkg_installed_version() { printf '2.0'; }
ra_failure_bundle_capture() { return 0; }
ra_safe_disconnect_if_owned() { return 0; }
ra_cleanup_owned_mutations() { return 0; }
ra_restore_original_policy() { return 0; }
ra_package_cleanup_reconcile() { return 0; }
ra_cleanup_expected_package_verify() { return 0; }
ra_require_mutations_released() { return 0; }
ra_verify_inactive_boundary() { return 0; }
ra_privacy_require_ordinary() { return 0; }
ra_restore_service_state() { return 0; }
ra_verify_run_tree() { return 0; }
ra_report_write() { return 0; }
ra_state_remove() { ABORT_REMOVED=1; return 0; }
ABORT_REMOVED=0

ra_run_abort >/dev/null || fail "abort rejected a missing candidate artifact even though candidate is already installed"
[[ "$ABORT_REMOVED" == 1 ]] || fail "abort did not complete cleanup"

# An active acceptance session must be disconnected before package reconciliation.
rm -rf "$RELEASE_ACCEPTANCE_STATE_DIR"
mkdir -p "$RELEASE_ACCEPTANCE_STATE_DIR"
ra_artifacts_init_new order-run
candidate_path="$TMP/candidate.deb"
printf 'candidate\n' >"$candidate_path"
candidate="$(jq -cn --arg p "$candidate_path" '{path:$p,package:"podlaz",version:"2.0",architecture:"amd64",sha256:"unused",device:1,inode:2}')"
ra_state_init order-run "$candidate" "1.0" "$manifest" profile-a
ra_state_jq '.mutations.package_setup={state:"acquired",kind:"previous_package",identity:{previous:{version:"1.0"},candidate:.candidate}}'
order_file="$TMP/abort-order"
: >"$order_file"
session_state=active
ra_failure_bundle_capture() { return 0; }
ra_safe_disconnect_if_owned() { printf 'disconnect\n' >>"$order_file"; session_state=inactive; return 0; }
ra_cleanup_owned_mutations() { return 0; }
ra_restore_original_policy() { return 0; }
ra_package_cleanup_reconcile() { printf 'package\n' >>"$order_file"; return 0; }
ra_cleanup_expected_package_verify() { return 0; }
ra_require_mutations_released() { return 0; }
ra_verify_inactive_boundary() { [[ "$session_state" == inactive ]]; }
ra_privacy_require_ordinary() { return 0; }
ra_restore_service_state() { return 0; }
ra_verify_run_tree() { return 0; }
ra_report_write() { return 0; }
ra_state_remove() { return 0; }
ra_run_abort >/dev/null || fail "abort ordering fixture failed"
first="$(head -n1 "$order_file")"
[[ "$first" == disconnect ]] || fail "abort performed cleanup before controlled disconnect: $first"
disconnect_line="$(grep -n '^disconnect$' "$order_file" | cut -d: -f1)"
package_line="$(grep -n '^package$' "$order_file" | cut -d: -f1)"
[[ -n "$disconnect_line" && -n "$package_line" && "$disconnect_line" -lt "$package_line" ]] || fail "package reconciliation happened before disconnect"

printf 'standalone_abort_recovery: PASS\n'
