#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"

require_cmd chmod find grep mv python3 sed wc

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
    if [[ "$(wc -l <"${result_file}")" -eq 3 ]]; then
      sed -n '2p' "${result_file}" | grep -Eq '^step: [A-Za-z0-9._-]+$' || fail "real-provider failure step is invalid"
      sed -n '3p' "${result_file}" | grep -Eq '^class: (authorization-denied|authorization-unavailable|daemon-internal|daemon-unavailable|unclassified)$' || fail "real-provider failure class is invalid"
      exit 0
    fi
    [[ "$(wc -l <"${result_file}")" -eq 1 ]] || fail "failed real-provider result must contain one raw or three sanitized lines"

    private_dir="${E2E_TMP_ROOT}/private-command"
    failure_marker="${private_dir}/failed-command"
    failure_stderr=""
    failure_step="data-plane"
    failure_class="unclassified"

    if [[ -f "${failure_marker}" && ! -L "${failure_marker}" ]]; then
      IFS= read -r failure_stderr_name <"${failure_marker}" || fail "private failure marker is empty"
      [[ "${failure_stderr_name}" =~ ^[0-9]{3}-[A-Za-z0-9._-]+[.]stderr$ ]] || fail "private failure marker is invalid"
      failure_stderr="${private_dir}/${failure_stderr_name}"
      [[ -f "${failure_stderr}" && ! -L "${failure_stderr}" ]] || fail "private failure stderr is unavailable"
      failure_step="${failure_stderr_name%.stderr}"
      failure_step="${failure_step#*-}"
      failure_class="$(python3 "${SCRIPT_DIR}/lib/tun_soak_metrics.py" classify-cli-error --stderr-file "${failure_stderr}")"
    fi

    sanitized_result="${result_file}.tmp"
    printf 'real-provider data-plane: failure\nstep: %s\nclass: %s\n' \
      "$(safe_name "${failure_step}")" "${failure_class}" >"${sanitized_result}"
    chmod 0600 "${sanitized_result}"
    mv -f -- "${sanitized_result}" "${result_file}"

    [[ "$(wc -l <"${result_file}")" -eq 3 ]] || fail "failed real-provider result must contain exactly three lines"
    sed -n '2p' "${result_file}" | grep -Eq '^step: [A-Za-z0-9._-]+$' || fail "real-provider failure step is invalid"
    sed -n '3p' "${result_file}" | grep -Eq '^class: (authorization-denied|authorization-unavailable|daemon-internal|daemon-unavailable|unclassified)$' || fail "real-provider failure class is invalid"
    ;;
  *) fail "real-provider result has unexpected content" ;;
esac
