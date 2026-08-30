#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd -P)"
SCRIPT="$ROOT/scripts/acceptance/release-laptop.sh"

fail() {
  printf 'standalone_contract: %s\n' "$*" >&2
  exit 1
}

[[ -f "$SCRIPT" ]] || fail "release-laptop.sh missing"
[[ -x "$SCRIPT" ]] || fail "release-laptop.sh is not executable"
bash -n "$SCRIPT" || fail "release-laptop.sh fails bash -n"

if grep -Eqi '(^|[;&|()]|exec[[:space:]]+)[[:space:]]*(python|python3)([[:space:]]|$)|-m[[:space:]]+release_acceptance([[:space:]]|$)' "$SCRIPT"; then
  fail "release-laptop.sh still executes Python runtime/modules"
fi

if find "$ROOT/scripts/acceptance/release_acceptance" -type f -name '*.py' -print -quit 2>/dev/null | grep -q .; then
  fail "Python runtime modules still exist"
fi

help_output="$(RELEASE_ACCEPTANCE_TEST_MODE=1 SUDO_USER="${USER:-tester}" bash "$SCRIPT" --help 2>&1 || true)"
grep -q -- '--resume' <<<"$help_output" || fail "help does not document --resume"
grep -q -- '--abort' <<<"$help_output" || fail "help does not document --abort"
grep -q -- '--previous-deb' <<<"$help_output" || fail "help does not document --previous-deb"

printf 'standalone_contract: PASS\n'
