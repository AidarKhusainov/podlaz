#!/usr/bin/env bash

# Wait for the exact installed CLI status contract to converge to verified TUN
# health. Status stdout/stderr remain private; callers receive only allowlisted
# structural verdicts through SOAK_STATUS_VERDICT.
run_tun_status_command() {
  local timeout_seconds="$1"
  run_installed_podlaz_bounded "${timeout_seconds}" status
}

wait_for_verified_tun_status() {
  local label="${1:-}" timeout_seconds status_timeout_seconds poll_seconds deadline
  local stdout_file stderr_file status_exit verdict remaining_seconds invocation_timeout sleep_seconds

  [[ "${label}" =~ ^[a-z0-9-]+$ ]] || {
    SOAK_STATUS_VERDICT="invalid-status"
    fail "TUN status check label is invalid"
    return 1
  }

  timeout_seconds="${PODLAZ_E2E_TUN_HEALTH_TIMEOUT_SECONDS:-75}"
  status_timeout_seconds="${PODLAZ_E2E_TUN_STATUS_TIMEOUT_SECONDS:-10}"
  poll_seconds="${PODLAZ_E2E_TUN_HEALTH_POLL_SECONDS:-1}"
  [[ "${timeout_seconds}" =~ ^[1-9][0-9]*$ ]] || {
    SOAK_STATUS_VERDICT="invalid-status"
    fail "TUN health timeout is invalid"
    return 1
  }
  [[ "${status_timeout_seconds}" =~ ^[1-9][0-9]*$ ]] || {
    SOAK_STATUS_VERDICT="invalid-status"
    fail "TUN status invocation timeout is invalid"
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
    remaining_seconds=$((deadline - SECONDS))
    if ((remaining_seconds <= 0)); then
      SOAK_STATUS_VERDICT="command-timeout"
      fail "${label} TUN health exceeded the bounded wait"
      return 1
    fi
    invocation_timeout="${status_timeout_seconds}"
    if ((invocation_timeout > remaining_seconds)); then
      invocation_timeout="${remaining_seconds}"
    fi

    if run_tun_status_command "${invocation_timeout}" >"${stdout_file}" 2>"${stderr_file}"; then
      status_exit=0
    else
      status_exit=$?
    fi
    case "${status_exit}" in
      124 | 137)
        SOAK_STATUS_VERDICT="command-timeout"
        SOAK_COMMAND_EXIT="${status_exit}"
        SOAK_COMMAND_CLASSIFICATION="unclassified"
        fail "${label} TUN status command exceeded its bounded invocation"
        return 1
        ;;
    esac

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
        remaining_seconds=$((deadline - SECONDS))
        if ((remaining_seconds <= 0)); then
          SOAK_STATUS_VERDICT="${verdict}-timeout"
          fail "${label} TUN health did not converge within the bounded wait"
          return 1
        fi
        sleep_seconds="${poll_seconds}"
        if ((sleep_seconds > remaining_seconds)); then
          sleep_seconds="${remaining_seconds}"
        fi
        sleep "${sleep_seconds}"
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

# Run the normal installed TUN diagnostics through an explicit bounded command
# boundary. Exit 3 is a valid diagnostic result: the soak has its own required
# DNS/HTTPS probes and separately proves that the active lifecycle remains
# verified after the read-only diagnostic completes.
run_tun_diagnostic_command() {
  run_installed_podlaz_bounded "${PODLAZ_E2E_TUN_DIAGNOSTIC_TIMEOUT_SECONDS:-90}" doctor --tun
}

run_bounded_tun_diagnostic() {
  local label="${1:-}" stdout_file stderr_file diagnostic_exit

  [[ "${label}" =~ ^[a-z0-9-]+$ ]] || {
    SOAK_COMMAND_CLASSIFICATION="unclassified"
    fail "TUN diagnostic label is invalid"
    return 1
  }
  [[ -n "${SOAK_PRIVATE_DIR:-}" && -n "${METRICS_TOOL:-}" ]] || {
    SOAK_COMMAND_CLASSIFICATION="unclassified"
    fail "TUN diagnostic checker is not configured"
    return 1
  }

  stdout_file="${SOAK_PRIVATE_DIR}/${label}-doctor.stdout"
  stderr_file="${SOAK_PRIVATE_DIR}/${label}-doctor.stderr"
  if run_tun_diagnostic_command >"${stdout_file}" 2>"${stderr_file}"; then
    diagnostic_exit=0
  else
    diagnostic_exit=$?
  fi

  DOCTOR_RUNS="${DOCTOR_RUNS:-0}"
  DOCTOR_RUNS=$((DOCTOR_RUNS + 1))
  case "${diagnostic_exit}" in
    0) ;;
    3)
      DOCTOR_UNHEALTHY_RUNS="${DOCTOR_UNHEALTHY_RUNS:-0}"
      DOCTOR_UNHEALTHY_RUNS=$((DOCTOR_UNHEALTHY_RUNS + 1))
      ;;
    *)
      SOAK_COMMAND_EXIT="${diagnostic_exit}"
      SOAK_COMMAND_CLASSIFICATION="$(
        python3 "${METRICS_TOOL}" classify-cli-error \
          --stderr-file "${stderr_file}"
      )" || SOAK_COMMAND_CLASSIFICATION="unclassified"
      fail "${label} TUN diagnostic command failed"
      return 1
      ;;
  esac

  wait_for_verified_tun_status "${label}-post-doctor"
}
