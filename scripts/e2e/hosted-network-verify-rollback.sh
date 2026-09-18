#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FAULT_PHASE="network-verify"
FAULT_FAILURE_PHASE="network-verify"
FAULT_CLASSIFICATION="network_verify_failure"
FAULT_EVENT="network-verify-injected"
REPORT_BASENAME="hosted-network-verify-rollback.txt"
# shellcheck source=lib/hosted_fault_rollback.sh
source "${SCRIPT_DIR}/lib/hosted_fault_rollback.sh"

run_hosted_fault_rollback "$@"
