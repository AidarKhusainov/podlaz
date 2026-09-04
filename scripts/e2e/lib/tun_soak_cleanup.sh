#!/usr/bin/env bash

run_tun_package_cleanup_once() {
  PODLAZ_E2E_PURGE_PACKAGE=true bash "${SCRIPT_DIR}/tun-package-cleanup.sh"
}

# Package cleanup is idempotent, but apt/systemd state can converge immediately
# after the first bounded pass. Keep raw output private and retry the complete
# ownership-safe cleanup once before publishing only a structural failure.
run_tun_soak_cleanup() {
  local label="${1:-}" attempts retry_seconds attempt log_file

  [[ "${label}" =~ ^[a-z0-9-]+$ ]] || {
    printf 'ERROR: invalid soak cleanup label\n' >&2
    return 1
  }
  attempts="${PODLAZ_E2E_SOAK_CLEANUP_ATTEMPTS:-2}"
  retry_seconds="${PODLAZ_E2E_SOAK_CLEANUP_RETRY_SECONDS:-2}"
  [[ "${attempts}" =~ ^[1-9][0-9]*$ ]] || {
    printf 'ERROR: invalid soak cleanup attempt count\n' >&2
    return 1
  }
  [[ "${retry_seconds}" =~ ^[0-9]+$ ]] || {
    printf 'ERROR: invalid soak cleanup retry interval\n' >&2
    return 1
  }
  [[ -n "${SOAK_PRIVATE_DIR:-}" ]] || {
    printf 'ERROR: soak cleanup private directory is unavailable\n' >&2
    return 1
  }
  mkdir -p "${SOAK_PRIVATE_DIR}"

  for attempt in $(seq 1 "${attempts}"); do
    log_file="${SOAK_PRIVATE_DIR}/${label}-cleanup-attempt-${attempt}.log"
    if run_tun_package_cleanup_once >"${log_file}" 2>&1; then
      return 0
    fi
    if ((attempt < attempts && retry_seconds > 0)); then
      sleep "${retry_seconds}"
    fi
  done

  printf 'ERROR: %s cleanup failed after %s bounded attempts\n' "${label}" "${attempts}" >&2
  if [[ "${GITHUB_ACTIONS:-}" == "true" ]]; then
    printf '::error::%s cleanup failed after %s bounded attempts\n' "${label}" "${attempts}" >&2
  fi
  return 1
}
