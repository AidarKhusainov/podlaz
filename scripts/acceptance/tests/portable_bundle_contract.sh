#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd -P)"
SCRIPT="$ROOT/scripts/acceptance/release-laptop.sh"
BUILDER="$ROOT/scripts/acceptance/build-release-laptop-standalone.sh"
fail() { printf 'portable_bundle_contract: %s\n' "$*" >&2; exit 1; }

[[ -x "$BUILDER" && -f "$BUILDER" && ! -L "$BUILDER" ]] || fail 'standalone builder missing or unsafe'
bash -n "$BUILDER" || fail 'standalone builder fails bash -n'
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
BUNDLE="$TMP/release-laptop.sh"
"$BUILDER" "$BUNDLE" >/dev/null || fail 'standalone build failed'
[[ -x "$BUNDLE" && -f "$BUNDLE" && ! -L "$BUNDLE" ]] || fail 'standalone output missing or unsafe'
bash -n "$BUNDLE" || fail 'standalone output fails bash -n'

help_output="$(RELEASE_ACCEPTANCE_TEST_MODE=1 SUDO_USER="${USER:-tester}" bash "$BUNDLE" --help 2>&1 || true)"
grep -Fq -- '--resume' <<<"$help_output" || fail 'generated help lost --resume'
grep -Fq 'generated standalone Bash file' <<<"$help_output" || fail 'generated help does not describe standalone layout'
! grep -Fq 'adjacent Bash modules' <<<"$help_output" || fail 'generated help still describes modular layout'

RELEASE_ACCEPTANCE_TEST_MODE=1 SUDO_USER="${USER:-tester}" bash -c '
  source "$1"
  for fn in ra_cli_parse ra_status_json ra_safe_cleanup ra_failure_bundle_capture ra_scenario_lower_upgrade ra_legacy_checkpoint_classify ra_main; do
    declare -F "$fn" >/dev/null || exit 10
  done
' _ "$BUNDLE" || fail 'generated standalone did not expose the complete controller'

[[ ! -d "$TMP/lib" ]] || fail 'portable bundle unexpectedly depends on copied modules'
printf 'portable_bundle_contract: PASS\n'
