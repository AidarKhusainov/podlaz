#!/usr/bin/env bash

# State-aware wrapper around process_lifecycle.sh for the package convergence
# background connect command. Callers must set CONNECT_PID and
# CONNECT_START_TIME immediately after spawning the child.

CONNECT_PID="${CONNECT_PID:-}"
CONNECT_START_TIME="${CONNECT_START_TIME:-}"
CONNECT_EXIT_CODE="${CONNECT_EXIT_CODE:-}"

capture_connect_pre_recovery_proof() {
  local manifest helper digest

  # This library is also used by process-only unit tests. The ownership proof
  # boundary is enabled only for the installed-package convergence caller,
  # which defines all three paths before installing its EXIT trap.
  if [[ -z "${E2E_TMP_ROOT:-}" || -z "${TRANSACTION_DIR:-}" || -z "${FALLBACK_NETWORK_HELPER:-}" ]]; then
    return 0
  fi

  case "${PODLAZ_E2E_PRE_RECOVERY_MANIFEST_STATE:-}" in
    ready) return 0 ;;
    failed) return 1 ;;
  esac

  manifest="${E2E_TMP_ROOT}/tun-package-pre-recovery-network.json"
  helper="${SCRIPT_DIR}/tun-package-verification-network.py"
  export PODLAZ_E2E_PRE_RECOVERY_MANIFEST_PATH="${manifest}"

  if sudo -n rm -f -- "${manifest}" >/dev/null 2>&1 &&
    sudo -n python3 "${helper}" snapshot "${TRANSACTION_DIR}" "${manifest}" >/dev/null 2>&1 &&
    digest="$(sudo -n sha256sum "${manifest}" 2>/dev/null | awk 'NF >= 1 {print $1; exit}')" &&
    [[ "${digest}" =~ ^[0-9a-f]{64}$ ]]; then
    export PODLAZ_E2E_PRE_RECOVERY_MANIFEST_SHA256="${digest}"
    export PODLAZ_E2E_PRE_RECOVERY_MANIFEST_STATE=ready
    return 0
  fi

  # Exporting an explicit failed state prevents the child teardown from taking
  # a misleading late snapshot after cancellation or hook release.
  export PODLAZ_E2E_PRE_RECOVERY_MANIFEST_SHA256=""
  export PODLAZ_E2E_PRE_RECOVERY_MANIFEST_STATE=failed
  return 1
}

wait_connect_bounded() {
  local pid="$1" attempts="${2:-600}" status
  [[ "${pid}" == "${CONNECT_PID}" && -n "${CONNECT_START_TIME}" ]] || return 2
  if wait_child_bounded "${pid}" "${CONNECT_START_TIME}" "${attempts}"; then
    CONNECT_EXIT_CODE="${WAIT_CHILD_EXIT_CODE}"
    CONNECT_PID=""
    CONNECT_START_TIME=""
    return 0
  else
    status=$?
  fi
  return "${status}"
}

terminate_connect_bounded() {
  local pid="${CONNECT_PID:-}" start="${CONNECT_START_TIME:-}"
  local capture_status=0 termination_status=0

  # This is the first operation executed by the real convergence EXIT trap.
  # Capture proof before cancellation, but never let capture failure skip the
  # independent bounded termination and reap of the tracked child.
  if capture_connect_pre_recovery_proof; then
    capture_status=0
  else
    capture_status=$?
  fi

  if [[ -n "${pid}" && -n "${start}" ]]; then
    if terminate_child_bounded "${pid}" "${start}" 50; then
      CONNECT_EXIT_CODE="${WAIT_CHILD_EXIT_CODE}"
      CONNECT_PID=""
      CONNECT_START_TIME=""
    else
      termination_status=$?
    fi
  fi

  if [[ "${termination_status}" != "0" ]]; then
    return "${termination_status}"
  fi
  return "${capture_status}"
}
