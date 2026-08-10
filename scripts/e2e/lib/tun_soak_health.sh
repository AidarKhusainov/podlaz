#!/usr/bin/env bash

# Wait for the exact installed CLI status contract to converge to verified TUN
# health. Status stdout/stderr remain private; callers receive only allowlisted
# structural verdicts through SOAK_STATUS_VERDICT.
wait_for_verified_tun_status() {
  local label="${1:-}" timeout_seconds poll_seconds deadline
  local stdout_file stderr_file status_exit verdict

  [[ "${label}" =~ ^[a-z0-9-]+$ ]] || {
    SOAK_STATUS_VERDICT="invalid-status"
    fail "TUN status check label is invalid"
    return 1
  }

  timeout_seconds="${PODLAZ_E2E_TUN_HEALTH_TIMEOUT_SECONDS:-75}"
  poll_seconds="${PODLAZ_E2E_TUN_HEALTH_POLL_SECONDS:-1}"
  [[ "${timeout_seconds}" =~ ^[1-9][0-9]*$ ]] || {
    SOAK_STATUS_VERDICT="invalid-status"
    fail "TUN health timeout is invalid"
    return 1
  }
  [[ "${poll_seconds}" =~ ^[1-9][0-9]*$ ]] || {
    SOAK_STATUS_VERDICT="invalid-status"
    fail "TUN health poll interval is invalid"
    return 1
  }
  [[ -n "${SOAK_PRIVATE_DIR:-}" && -n "${TUN_SOAK_STATUS_TOOL:-}" && -n "${METRICS_TOOL:-}" ]] || {
    SOAK_STATUS_VERDICT="invalid-status"
    fail "TUN health checker is not configured"
    return 1
  }

  stdout_file="${SOAK_PRIVATE_DIR}/${label}-status.stdout"
  stderr_file="${SOAK_PRIVATE_DIR}/${label}-status.stderr"
  deadline=$((SECONDS + timeout_seconds))

  while :; do
    if run_installed_podlaz status >"${stdout_file}" 2>"${stderr_file}"; then
      status_exit=0
    else
      status_exit=$?
    fi

    if verdict="$(
      python3 "${TUN_SOAK_STATUS_TOOL}" classify \
        --stdout-file "${stdout_file}" \
        --exit-code "${status_exit}"
    )"; then
      :
    else
      verdict="invalid-status"
    fi

    case "${verdict}" in
      verified)
        SOAK_STATUS_VERDICT=""
        SOAK_COMMAND_EXIT=""
        SOAK_COMMAND_CLASSIFICATION=""
        return 0
        ;;
      retry-revalidating | retry-degraded)
        SOAK_STATUS_VERDICT="${verdict}"
        if ((SECONDS >= deadline)); then
          SOAK_STATUS_VERDICT="${verdict}-timeout"
          fail "${label} TUN health did not converge within the bounded wait"
          return 1
        fi
        sleep "${poll_seconds}"
        ;;
      command-error)
        SOAK_STATUS_VERDICT="command-error"
        SOAK_COMMAND_EXIT="${status_exit}"
        SOAK_COMMAND_CLASSIFICATION="$(
          python3 "${METRICS_TOOL}" classify-cli-error \
            --stderr-file "${stderr_file}"
        )" || SOAK_COMMAND_CLASSIFICATION="unclassified"
        fail "${label} TUN status command failed"
        return 1
        ;;
      terminal-cleanup-required | terminal-inactive | invalid-status)
        SOAK_STATUS_VERDICT="${verdict}"
        fail "${label} TUN health is not verified"
        return 1
        ;;
      *)
        SOAK_STATUS_VERDICT="invalid-status"
        fail "${label} TUN status classifier returned an invalid verdict"
        return 1
        ;;
    esac
  done
}
