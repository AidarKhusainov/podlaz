#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd -P)"
SCRIPT="$ROOT/scripts/acceptance/release-laptop.sh"
MODULE_DIR="$ROOT/scripts/acceptance/lib/release-laptop"
CONTROLLER_FILES=(
  "$MODULE_DIR/core.sh"
  "$MODULE_DIR/product.sh"
  "$MODULE_DIR/host_exercise.sh"
  "$MODULE_DIR/lifecycle.sh"
  "$MODULE_DIR/evidence.sh"
  "$MODULE_DIR/scenarios.sh"
  "$MODULE_DIR/legacy.sh"
  "$SCRIPT"
)
# shellcheck source=/dev/null
source "$SCRIPT"

fail() { printf 'standalone_safety: %s\n' "$*" >&2; exit 1; }
CONTROLLER_SOURCE="$(cat -- "${CONTROLLER_FILES[@]}")"
assert_controller_contains() { grep -Fq -- "$1" <<<"$CONTROLLER_SOURCE" || fail "expected controller source to contain [$1]"; }

# Persistent privileged authority must not live in a user-writable state tree.
[[ "$RA_STATE_DIR" == "${RELEASE_ACCEPTANCE_STATE_DIR:-/var/lib/podlaz-release-acceptance}" ]] || fail "unexpected privileged state directory: $RA_STATE_DIR"
assert_controller_contains 'flock'

# The approved standalone design explicitly forbids eval across the complete controller.
if grep -Eq '(^|[;[:space:]])eval([[:space:]]|$)' <<<"$CONTROLLER_SOURCE"; then
  fail "standalone harness must not use eval"
fi

# User-selected evidence paths are validated before chmod/chown/creation.
assert_controller_contains 'ra_validate_artifact_root'

# Candidate privacy proof must validate actual nft rule expressions/verdicts,
# not just table/chain/comment counts.
assert_controller_contains 'ra_privacy_verify_rule'
assert_controller_contains 'block-direct'
assert_controller_contains 'reject'
assert_controller_contains 'tun-egress'
assert_controller_contains 'bootstrap_ipv4'

# Canonical qualification is exactly 60 minutes; longer debug runs are partial too.
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
RA_CHECKPOINT="$TMP/current.json"
cat >"$RA_CHECKPOINT" <<'JSON'
{"scenarios":{"lower_release_upgrade":{"outcome":"PASS"},"privacy_active":{"outcome":"PASS"},"graceful_restart":{"outcome":"PASS"},"daemon_kill":{"outcome":"PASS"},"reinstall":{"outcome":"PASS"},"rollback_interruption":{"outcome":"PASS"},"stop_start_no_reconnect":{"outcome":"PASS"},"preconnect_coexistence":{"outcome":"PASS"},"active_coexistence":{"outcome":"PASS"},"resource_soak":{"outcome":"PASS"},"disconnect_cleanup":{"outcome":"PASS"},"coexistence_reconnect":{"outcome":"PASS"},"reconnect_resource_nonaccumulation":{"outcome":"PASS"},"runtime_terminal_convergence":{"outcome":"PASS"},"runtime_terminal_no_retry":{"outcome":"PASS"},"final_restoration":{"outcome":"PASS"},"reboot_autostart_off":{"outcome":"PASS"},"reboot_autostart_on":{"outcome":"PASS"},"explicit_disconnect_no_same_boot_retry":{"outcome":"PASS"},"reboot_terminal_autostart":{"outcome":"PASS"},"terminal_no_same_boot_retry":{"outcome":"PASS"},"wifi_reconnect":{"outcome":"PASS"},"suspend_resume":{"outcome":"PASS"}}}
JSON
RA_REBOOT_PHASES=1 RA_ALLOW_WIFI=1 RA_ALLOW_SUSPEND=1 RA_SOAK_MINUTES=61
[[ "$(ra_qualification)" == "PARTIAL_PASS" ]] || fail "61-minute debug soak must not QUALIFIED_PASS"
RA_SOAK_MINUTES=60
[[ "$(ra_qualification)" == "QUALIFIED_PASS" ]] || fail "canonical fully-passing 60-minute run should qualify"

# Reboot no-retry evidence is only meaningful if the verifier deliberately
# restarts the daemon in the same boot after successful autostart/disconnect/terminal.
reboot_verifiers="$(sed -n '/^ra_resume_reboot_on_verify()/,/^ra_preflight_capabilities()/p' <<<"$CONTROLLER_SOURCE")"
grep -Fq 'ra_successful_boot_restart_continuity' <<<"$reboot_verifiers" || fail "successful autostart does not restart daemon while active"
[[ "$(grep -c 'ra_same_boot_restart_stays_inactive' <<<"$reboot_verifiers")" -ge 2 ]] || fail "disconnect/terminal no-retry checks do not restart daemon"

# Abort/finalization must preserve the exact cleanup boundary. The outer
# compatibility dispatcher delegates current-v5 runs to the integrated controller;
# supported legacy runs use their separately tested narrow reconciliation proof.
abort_dispatch="$(sed -n '/^ra_run_abort()/,/^ra_run_restart()/p' <<<"$CONTROLLER_SOURCE")"
grep -Fq 'ra_run_abort_current' <<<"$abort_dispatch" || fail "current abort does not delegate to exact cleanup controller"
current_abort_body="$(sed -n '/^ra_run_abort_current()/,/^ra_run_restart_current()/p' <<<"$CONTROLLER_SOURCE")"
grep -Fq 'ra_safe_cleanup' <<<"$current_abort_body" || fail "current abort bypasses exact safe cleanup"
grep -Fq 'ra_legacy_checkpoint_reconcile' <<<"$abort_dispatch" || fail "legacy abort bypasses narrow reconciliation"
cleanup_body="$(sed -n '/^ra_safe_cleanup()/,/^ra_public_resource_summary()/p' <<<"$CONTROLLER_SOURCE")"
grep -Eq 'ra_verify_ordinary_network|ra_privacy_require_ordinary' <<<"$cleanup_body" || fail "safe cleanup can declare clean without ordinary-network verification"

printf 'standalone_safety: PASS\n'