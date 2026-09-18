#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd -P)"
SCRIPT="$ROOT/scripts/acceptance/release-laptop.sh"
# shellcheck source=/dev/null
source "$SCRIPT"

fail() { printf 'failure_evidence_applicability_contract: %s\n' "$*" >&2; exit 1; }

assert_boot_attempt_policy() {
  local scenario="$1" expected_applicability="$2" expected_absence_satisfies="$3"
  local applicability absence_satisfies
  IFS=$'\t' read -r applicability absence_satisfies <<<"$(ra_failure_boot_attempt_policy "$scenario")"
  [[ "$applicability" == "$expected_applicability" ]] || fail "$scenario applicability: got $applicability, want $expected_applicability"
  [[ "$absence_satisfies" == "$expected_absence_satisfies" ]] || fail "$scenario absence predicate: got $absence_satisfies, want $expected_absence_satisfies"
}

assert_boot_attempt_policy lower_release_upgrade not_applicable 1
assert_boot_attempt_policy reboot_autostart_off required 1
assert_boot_attempt_policy reboot_autostart_on required 0
assert_boot_attempt_policy reboot_terminal_autostart required 0
assert_boot_attempt_policy explicit_disconnect_no_same_boot_retry required 0
assert_boot_attempt_policy terminal_no_same_boot_retry required 0

printf 'failure_evidence_applicability_contract: PASS\n'
