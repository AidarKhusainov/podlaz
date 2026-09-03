#!/usr/bin/env bash
set -Eeuo pipefail
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
bash "$SCRIPT_DIR/standalone_contract.sh"
bash "$SCRIPT_DIR/standalone_recovery.sh"
bash "$SCRIPT_DIR/standalone_safety.sh"
bash "$SCRIPT_DIR/standalone_abort_recovery.sh"
bash "$SCRIPT_DIR/standalone_restart_friendly.sh"
bash "$SCRIPT_DIR/standalone_failure_recovery.sh"
bash "$SCRIPT_DIR/standalone_replay_guards.sh"
bash "$SCRIPT_DIR/standalone_acceptance_design.sh"
bash "$SCRIPT_DIR/standalone_release_evidence.sh"
bash "$SCRIPT_DIR/standalone_legacy_checkpoint_reconcile.sh"
bash "$SCRIPT_DIR/standalone_status_semantics.sh"
