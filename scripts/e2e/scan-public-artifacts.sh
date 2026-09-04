#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"

require_cmd find

result_file="${E2E_ARTIFACT_DIR}/real-provider-result.txt"
mapfile -d '' -t public_files < <(find "${E2E_ARTIFACT_DIR}" -mindepth 1 -maxdepth 1 -type f -print0)

[[ "${#public_files[@]}" -eq 1 ]] || fail "real-provider public artifacts must contain exactly one file"
[[ "${public_files[0]}" == "${result_file}" ]] || fail "unexpected real-provider public artifact"
[[ ! -L "${result_file}" ]] || fail "real-provider result must not be a symlink"

IFS= read -r result <"${result_file}" || fail "real-provider result is empty"
case "${result}" in
  "real-provider data-plane: success"|"real-provider data-plane: failure") ;;
  *) fail "real-provider result has unexpected content" ;;
esac
[[ "$(wc -l <"${result_file}")" -eq 1 ]] || fail "real-provider result must contain exactly one line"
