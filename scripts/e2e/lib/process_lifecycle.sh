#!/usr/bin/env bash

# Shared bounded process lifecycle helpers for self-hosted E2E scripts.
# Callers are expected to run with `set -Eeuo pipefail`.

PROCESS_PROC_ROOT="${PROCESS_PROC_ROOT:-/proc}"
PROCESS_POLL_INTERVAL="${PROCESS_POLL_INTERVAL:-0.1}"
PROCESS_USE_SUDO="${PROCESS_USE_SUDO:-false}"
WAIT_CHILD_REAPED=false
WAIT_CHILD_EXIT_CODE=""
WAIT_CHILD_IDENTITY_CHANGED=false

process_read_file() {
  local path="$1"
  if [[ "${PROCESS_USE_SUDO}" == "true" ]]; then
    sudo -n cat -- "${path}"
  else
    cat -- "${path}"
  fi
}

process_readlink() {
  local path="$1"
  if [[ "${PROCESS_USE_SUDO}" == "true" ]]; then
    sudo -n readlink -f -- "${path}"
  else
    readlink -f -- "${path}"
  fi
}

process_stat_fields() {
  local pid="$1" stat tail
  stat="$(process_read_file "${PROCESS_PROC_ROOT}/${pid}/stat" 2>/dev/null)" || return 1
  [[ "${stat}" == *") "* ]] || return 1
  tail="${stat##*) }"
  printf '%s\n' "${tail}"
}

process_snapshot() {
  local pid="$1" tail
  local -a fields=()
  tail="$(process_stat_fields "${pid}")" || return 1
  read -r -a fields <<<"${tail}"
  [[ "${#fields[@]}" -ge 20 && "${fields[19]}" =~ ^[0-9]+$ ]] || return 1
  printf '%s %s\n' "${fields[0]}" "${fields[19]}"
}

process_state() {
  local pid="$1" snapshot
  snapshot="$(process_snapshot "${pid}")" || return 1
  printf '%s\n' "${snapshot%% *}"
}

process_start_time() {
  local pid="$1" snapshot
  snapshot="$(process_snapshot "${pid}")" || return 1
  printf '%s\n' "${snapshot#* }"
}

child_job_running() {
  local pid="$1" job_pid
  while IFS= read -r job_pid; do
    [[ "${job_pid}" == "${pid}" ]] && return 0
  done < <(jobs -pr)
  return 1
}

process_identity_matches() {
  local pid="$1" expected_start="$2" current_start
  [[ -n "${expected_start}" ]] || return 1
  current_start="$(process_start_time "${pid}" 2>/dev/null)" || return 1
  [[ "${current_start}" == "${expected_start}" ]]
}

owned_process_identity_matches() {
  local pid="$1" expected_start="$2" expected_exe="$3" required_cmdline_fragment="$4"
  local exe cmdline
  process_identity_matches "${pid}" "${expected_start}" || return 1
  exe="$(process_readlink "${PROCESS_PROC_ROOT}/${pid}/exe" 2>/dev/null)" || return 1
  [[ "${exe}" == "${expected_exe}" ]] || return 1
  if [[ "${PROCESS_USE_SUDO}" == "true" ]]; then
    cmdline="$(sudo -n cat -- "${PROCESS_PROC_ROOT}/${pid}/cmdline" 2>/dev/null | tr '\0' ' ')" || return 1
  else
    cmdline="$(tr '\0' ' ' <"${PROCESS_PROC_ROOT}/${pid}/cmdline" 2>/dev/null)" || return 1
  fi
  [[ "${cmdline}" == *"${required_cmdline_fragment}"* ]]
}

process_send_signal() {
  local signal="$1" pid="$2"
  if [[ "${PROCESS_USE_SUDO}" == "true" ]]; then
    sudo -n kill -"${signal}" "${pid}"
  else
    kill -"${signal}" "${pid}"
  fi
}

reap_child() {
  local pid="$1" code restore_errexit=0
  case $- in
    *e*) restore_errexit=1 ;;
  esac
  set +e
  wait "${pid}"
  code=$?
  if [[ "${restore_errexit}" == "1" ]]; then
    set -e
  fi
  WAIT_CHILD_REAPED=true
  WAIT_CHILD_EXIT_CODE="${code}"
  return 0
}

wait_child_bounded() {
  local pid="$1" expected_start="$2" attempts="${3:-50}" attempt snapshot state start
  WAIT_CHILD_REAPED=false
  WAIT_CHILD_EXIT_CODE=""
  WAIT_CHILD_IDENTITY_CHANGED=false

  for attempt in $(seq 1 "${attempts}"); do
    if snapshot="$(process_snapshot "${pid}" 2>/dev/null)"; then
      state="${snapshot%% *}"
      start="${snapshot#* }"
      if [[ "${state}" == "Z" ]]; then
        reap_child "${pid}"
        return 0
      fi
      if [[ "${start}" != "${expected_start}" ]]; then
        WAIT_CHILD_IDENTITY_CHANGED=true
        return 0
      fi
    else
      # A transient /proc read failure is not evidence that the child exited.
      # Bash's own job table is the independent authority for this child.
      if ! child_job_running "${pid}"; then
        reap_child "${pid}"
        return 0
      fi
    fi
    sleep "${PROCESS_POLL_INTERVAL}"
  done
  return 124
}

terminate_child_bounded() {
  local pid="$1" expected_start="$2" attempts="${3:-50}" status
  if wait_child_bounded "${pid}" "${expected_start}" "${attempts}"; then
    return 0
  else
    status=$?
  fi
  [[ "${status}" == "124" ]] || return "${status}"

  if ! process_identity_matches "${pid}" "${expected_start}"; then
    # Do not signal an identity that can no longer be proven. Reap only when
    # Bash no longer reports the tracked child as running; otherwise fail
    # closed so the caller cannot proceed as if termination succeeded.
    if ! child_job_running "${pid}"; then
      reap_child "${pid}"
      return 0
    fi
    return 125
  fi

  process_send_signal TERM "${pid}" >/dev/null 2>&1 || true
  if wait_child_bounded "${pid}" "${expected_start}" "${attempts}"; then
    return 0
  else
    status=$?
  fi
  [[ "${status}" == "124" ]] || return "${status}"

  if process_identity_matches "${pid}" "${expected_start}"; then
    process_send_signal KILL "${pid}" >/dev/null 2>&1 || true
  fi
  wait_child_bounded "${pid}" "${expected_start}" "${attempts}"
}

wait_owned_process_gone() {
  local pid="$1" expected_start="$2" expected_exe="$3" required_cmdline_fragment="$4" attempts="${5:-50}" attempt
  for attempt in $(seq 1 "${attempts}"); do
    if ! owned_process_identity_matches "${pid}" "${expected_start}" "${expected_exe}" "${required_cmdline_fragment}"; then
      return 0
    fi
    sleep "${PROCESS_POLL_INTERVAL}"
  done
  return 124
}

terminate_owned_process() {
  local pid="$1" expected_start="$2" expected_exe="$3" required_cmdline_fragment="$4" attempts="${5:-50}"
  if ! owned_process_identity_matches "${pid}" "${expected_start}" "${expected_exe}" "${required_cmdline_fragment}"; then
    return 0
  fi
  process_send_signal TERM "${pid}" >/dev/null 2>&1 || true
  if wait_owned_process_gone "${pid}" "${expected_start}" "${expected_exe}" "${required_cmdline_fragment}" "${attempts}"; then
    return 0
  fi
  if owned_process_identity_matches "${pid}" "${expected_start}" "${expected_exe}" "${required_cmdline_fragment}"; then
    process_send_signal KILL "${pid}" >/dev/null 2>&1 || true
  fi
  wait_owned_process_gone "${pid}" "${expected_start}" "${expected_exe}" "${required_cmdline_fragment}" "${attempts}"
}
