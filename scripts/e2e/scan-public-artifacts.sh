#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"

require_cmd find python3

result_file="${E2E_ARTIFACT_DIR}/real-provider-result.txt"
mapfile -d '' -t public_entries < <(find "${E2E_ARTIFACT_DIR}" -mindepth 1 -print0)

[[ "${#public_entries[@]}" -eq 1 ]] || fail "real-provider public artifacts must contain exactly one filesystem entry"
[[ "${public_entries[0]}" == "${result_file}" ]] || fail "unexpected real-provider public artifact"
[[ -f "${result_file}" && ! -L "${result_file}" ]] || fail "real-provider result must be a regular file"

IFS= read -r result <"${result_file}" || fail "real-provider result is empty"
case "${result}" in
  "real-provider data-plane: success")
    [[ "$(wc -l <"${result_file}")" -eq 1 ]] || fail "successful real-provider result must contain exactly one line"
    ;;
  "real-provider data-plane: failure")
    private_dir="${E2E_TMP_ROOT}/private-command"
    failure_marker="${private_dir}/failed-command"
    failure_stderr=""
    if [[ -f "${failure_marker}" && ! -L "${failure_marker}" ]]; then
      IFS= read -r failure_stderr_name <"${failure_marker}" || fail "private failure marker is empty"
      [[ "${failure_stderr_name}" =~ ^[0-9]{3}-[A-Za-z0-9._-]+[.]stderr$ ]] || fail "private failure marker is invalid"
      failure_stderr="${private_dir}/${failure_stderr_name}"
      [[ -f "${failure_stderr}" && ! -L "${failure_stderr}" ]] || fail "private failure stderr is unavailable"
    fi

    if [[ -n "${failure_stderr}" ]]; then
      failure_name="${failure_stderr_name%.stderr}"
      failure_name="${failure_name#*-}"
      failure_class="$(python3 "${SCRIPT_DIR}/lib/tun_soak_metrics.py" classify-cli-error --stderr-file "${failure_stderr}")"
      printf 'command: %s\nclass: %s\n' "$(safe_name "${failure_name}")" "${failure_class}" >>"${result_file}"
    else
      printf 'command: unavailable\nclass: unclassified\n' >>"${result_file}"
    fi

    [[ "$(wc -l <"${result_file}")" -eq 3 ]] || fail "failed real-provider result must contain exactly three lines"
    sed -n '2p' "${result_file}" | grep -Eq '^command: [A-Za-z0-9._-]+$' || fail "real-provider failure command is invalid"
    sed -n '3p' "${result_file}" | grep -Eq '^class: (authorization-denied|authorization-unavailable|daemon-internal|daemon-unavailable|unclassified)$' || fail "real-provider failure class is invalid"
    ;;
  *) fail "real-provider result has unexpected content" ;;
esac
