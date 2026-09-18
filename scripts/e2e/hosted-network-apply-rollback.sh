#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
FAULT_PHASE="tun-address-apply"
FAULT_FAILURE_PHASE="network-apply"
FAULT_CLASSIFICATION="tun_address_apply_failure"
FAULT_EVENT="tun-address-apply-injected"
REPORT_BASENAME="hosted-network-apply-rollback.txt"
# shellcheck source=lib/hosted_fault_rollback.sh
source "${SCRIPT_DIR}/lib/hosted_fault_rollback.sh"

run_hosted_fault_rollback "$@"
