#!/usr/bin/env bash
set -Euo pipefail
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
failed=0
for test_script in \
  standalone_contract.sh \
  standalone_recovery.sh \
  standalone_safety.sh \
  standalone_abort_recovery.sh \
  standalone_restart_friendly.sh \
  standalone_failure_recovery.sh \
  standalone_replay_guards.sh \
  standalone_acceptance_design.sh
do
  printf '=== %s ===\n' "$test_script"
  if ! bash "$SCRIPT_DIR/$test_script"; then
    printf 'FAILED: %s\n' "$test_script" >&2
    failed=1
  fi
done
exit "$failed"
