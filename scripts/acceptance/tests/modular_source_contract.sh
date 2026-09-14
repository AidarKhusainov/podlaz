#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd -P)"
SCRIPT="$ROOT/scripts/acceptance/release-laptop.sh"
MODULE_DIR="$ROOT/scripts/acceptance/lib/release-laptop"
MODULES=(core.sh product.sh host_exercise.sh lifecycle.sh evidence.sh scenarios.sh legacy.sh)

fail() {
  printf 'modular_source_contract: %s\n' "$*" >&2
  exit 1
}

[[ -x "$SCRIPT" && -f "$SCRIPT" && ! -L "$SCRIPT" ]] || fail 'entrypoint missing or unsafe'
[[ -d "$MODULE_DIR" && ! -L "$MODULE_DIR" ]] || fail 'module directory missing or unsafe'
for module in "${MODULES[@]}"; do
  path="$MODULE_DIR/$module"
  [[ -f "$path" && ! -L "$path" ]] || fail "module missing or unsafe: $module"
  bash -n "$path" || fail "module syntax invalid: $module"
done

bash -n "$SCRIPT" || fail 'entrypoint syntax invalid'

for forbidden in 'ra_failure_bundle_capture()' 'ra_scenario_lower_upgrade()' 'ra_legacy_checkpoint_classify()'; do
  ! grep -Fq "$forbidden" "$SCRIPT" || fail "entrypoint still contains controller implementation: $forbidden"
done

RELEASE_ACCEPTANCE_TEST_MODE=1 SUDO_USER="${USER:-tester}" bash -c '
  source "$1"
  expected=(core.sh product.sh host_exercise.sh lifecycle.sh evidence.sh scenarios.sh legacy.sh)
  [[ "${RA_RELEASE_MODULES[*]}" == "${expected[*]}" ]] || exit 9
  for fn in ra_cli_parse ra_status_json ra_safe_cleanup ra_failure_bundle_capture ra_scenario_lower_upgrade ra_legacy_checkpoint_classify ra_main; do
    declare -F "$fn" >/dev/null || exit 10
  done
' _ "$SCRIPT" || fail 'sourced entrypoint module order/controller surface drifted'

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/lib"
cp -a "$SCRIPT" "$TMP/release-laptop.sh"
cp -a "$MODULE_DIR" "$TMP/lib/release-laptop"
rm -f "$TMP/lib/release-laptop/evidence.sh"
set +e
out="$(RELEASE_ACCEPTANCE_TEST_MODE=1 SUDO_USER="${USER:-tester}" bash "$TMP/release-laptop.sh" --help 2>&1)"
rc=$?
set -e
((rc != 0)) || fail 'entrypoint ran despite missing evidence module'
grep -Fq 'required controller module is missing or unsafe' <<<"$out" || fail 'missing-module failure was not explicit'

rm -rf "$TMP/lib/release-laptop"
cp -a "$MODULE_DIR" "$TMP/lib/release-laptop"
mv "$TMP/lib/release-laptop/evidence.sh" "$TMP/lib/release-laptop/evidence.real"
ln -s evidence.real "$TMP/lib/release-laptop/evidence.sh"
set +e
out="$(RELEASE_ACCEPTANCE_TEST_MODE=1 SUDO_USER="${USER:-tester}" bash "$TMP/release-laptop.sh" --help 2>&1)"
rc=$?
set -e
((rc != 0)) || fail 'entrypoint ran despite symlinked evidence module'
grep -Fq 'required controller module is missing or unsafe' <<<"$out" || fail 'symlink-module failure was not explicit'

printf 'modular_source_contract: PASS\n'
