#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd -P)"
SCRIPT="$ROOT/scripts/acceptance/release-laptop.sh"
# shellcheck source=/dev/null
source "$SCRIPT"

fail() { printf 'standalone_safety: %s\n' "$*" >&2; exit 1; }
assert_contains_file() { grep -Fq -- "$2" "$1" || fail "expected $1 to contain [$2]"; }

# Persistent privileged authority must not live in a user-writable state tree.
assert_contains_file "$SCRIPT" 'RA_STATE_DIR="${RELEASE_ACCEPTANCE_STATE_DIR:-/var/lib/podlaz-release-acceptance}"'
assert_contains_file "$SCRIPT" 'flock'

# User-selected evidence paths are validated before chmod/chown/creation.
assert_contains_file "$SCRIPT" 'ra_validate_artifact_root'

# Candidate privacy proof must validate actual nft rule expressions/verdicts,
# not just table/chain/comment counts.
assert_contains_file "$SCRIPT" 'ra_privacy_verify_rule'
assert_contains_file "$SCRIPT" 'block-direct'
assert_contains_file "$SCRIPT" 'reject'
assert_contains_file "$SCRIPT" 'tun-egress'
assert_contains_file "$SCRIPT" 'bootstrap_ipv4'

# Canonical qualification is exactly 60 minutes; longer debug runs are partial too.
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
RA_CHECKPOINT="$TMP/current.json"
cat >"$RA_CHECKPOINT" <<'JSON'
{"scenarios":{"lower_release_upgrade":{"outcome":"PASS"},"privacy_active":{"outcome":"PASS"},"graceful_restart":{"outcome":"PASS"},"daemon_kill":{"outcome":"PASS"},"reinstall":{"outcome":"PASS"},"rollback_interruption":{"outcome":"PASS"},"preconnect_coexistence":{"outcome":"PASS"},"active_coexistence":{"outcome":"PASS"},"disconnect_cleanup":{"outcome":"PASS"},"runtime_terminal_convergence":{"outcome":"PASS"},"final_restoration":{"outcome":"PASS"},"reboot_autostart_off":{"outcome":"PASS"},"reboot_autostart_on":{"outcome":"PASS"},"explicit_disconnect_no_same_boot_retry":{"outcome":"PASS"},"reboot_terminal_autostart":{"outcome":"PASS"},"terminal_no_same_boot_retry":{"outcome":"PASS"},"wifi_reconnect":{"outcome":"PASS"},"suspend_resume":{"outcome":"PASS"},"resource_soak":{"outcome":"PASS"}}}
JSON
RA_REBOOT_PHASES=1 RA_ALLOW_WIFI=1 RA_ALLOW_SUSPEND=1 RA_SOAK_MINUTES=61
[[ "$(ra_qualification)" == "PARTIAL_PASS" ]] || fail "61-minute debug soak must not QUALIFIED_PASS"
RA_SOAK_MINUTES=60
[[ "$(ra_qualification)" == "QUALIFIED_PASS" ]] || fail "canonical fully-passing 60-minute run should qualify"

# Reboot no-retry evidence is only meaningful if the daemon is deliberately
# restarted in the same boot after successful autostart/disconnect/terminal.
resume_body="$(sed -n '/^ra_run_resume()/,/^ra_run_abort()/p' "$SCRIPT")"
[[ "$(grep -c 'systemctl restart.*RA_SERVICE' <<<"$resume_body")" -ge 3 ]] || fail "reboot resume flow lacks mandatory same-boot daemon restart checks"

# Abort/finalization must prove ordinary networking before declaring clean.
abort_body="$(sed -n '/^ra_run_abort()/,/^ra_main()/p' "$SCRIPT")"
grep -Fq 'ra_verify_ordinary_network' <<<"$abort_body" || fail "abort can declare clean without ordinary-network verification"

printf 'standalone_safety: PASS\n'
