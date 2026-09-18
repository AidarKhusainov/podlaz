#!/usr/bin/env bash

# Resolve the first configured private profile URI without logging it. Callers
# remain responsible for masking/handling the returned secret value.
first_configured_profile_uri() {
  if [[ -n "${PODLAZ_E2E_PROFILE_URI:-}" ]]; then
    printf '%s\n' "${PODLAZ_E2E_PROFILE_URI}"
    return 0
  fi
  if [[ -n "${PODLAZ_E2E_PROFILE_URI_FILE:-}" ]]; then
    [[ -f "${PODLAZ_E2E_PROFILE_URI_FILE}" && ! -L "${PODLAZ_E2E_PROFILE_URI_FILE}" ]] || return 1
    local file_uri
    IFS= read -r file_uri <"${PODLAZ_E2E_PROFILE_URI_FILE}" || true
    [[ -n "${file_uri}" ]] || return 1
    printf '%s\n' "${file_uri}"
    return 0
  fi
  local uri
  while IFS= read -r uri; do
    [[ -n "${uri}" ]] || continue
    printf '%s\n' "${uri}"
    return 0
  done <<<"${PODLAZ_E2E_PROFILE_URI_LIST:-}"
}
