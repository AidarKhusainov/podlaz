#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"

command_name="${1:-}"
stderr_path="${2:-}"
[[ -n "${command_name}" ]] || fail "private failure command name is required"
[[ -f "${stderr_path}" && ! -L "${stderr_path}" ]] || fail "private failure stderr must be a regular file"
[[ "${stderr_path}" == "${E2E_TMP_ROOT}"/* ]] || fail "private failure stderr must stay below E2E_TMP_ROOT"

failure_class="unclassified"
if grep -Eqi 'authorization (denied|unavailable)|polkit' "${stderr_path}"; then
  failure_class="authorization"
elif grep -Eqi 'connection refused|daemon (is )?(unavailable|not running)|unix socket|transport' "${stderr_path}"; then
  failure_class="daemon-transport"
elif grep -Eqi 'xray|runtime config|runtime process' "${stderr_path}"; then
  failure_class="runtime"
elif grep -Eqi 'deadline exceeded|timed out|timeout' "${stderr_path}"; then
  failure_class="timeout"
fi

mkdir -p "${E2E_ARTIFACT_DIR}"
report="${E2E_ARTIFACT_DIR}/real-provider-failure.txt"
{
  printf 'command: %s\n' "$(safe_name "${command_name}")"
  printf 'class: %s\n' "${failure_class}"
} >"${report}"
