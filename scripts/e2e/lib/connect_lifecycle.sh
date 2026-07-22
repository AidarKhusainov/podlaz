#!/usr/bin/env bash

# State-aware wrapper around process_lifecycle.sh for the package convergence
# background connect command. Callers must set CONNECT_PID and
# CONNECT_START_TIME immediately after spawning the child.

CONNECT_PID="${CONNECT_PID:-}"
CONNECT_START_TIME="${CONNECT_START_TIME:-}"
CONNECT_EXIT_CODE="${CONNECT_EXIT_CODE:-}"

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
  [[ -n "${pid}" && -n "${start}" ]] || return 0
  if ! terminate_child_bounded "${pid}" "${start}" 50; then
    return 1
  fi
  CONNECT_EXIT_CODE="${WAIT_CHILD_EXIT_CODE}"
  CONNECT_PID=""
  CONNECT_START_TIME=""
}
