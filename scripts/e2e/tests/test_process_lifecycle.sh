#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../lib/process_lifecycle.sh
source "${SCRIPT_DIR}/../lib/process_lifecycle.sh"

fail_test() {
  printf 'test failure: %s\n' "$*" >&2
  exit 1
}

wait_for_exit_without_reaping() {
  local pid="$1" attempt state
  for attempt in $(seq 1 100); do
    state="$(process_state "${pid}" 2>/dev/null || true)"
    [[ "${state}" == "Z" || -z "${state}" ]] && return 0
    sleep 0.01
  done
  fail_test "child did not exit"
}

signal_log=()
process_send_signal() {
  signal_log+=("$1:$2")
  return 0
}

sh -c 'sleep 0.05; exit 7' &
child_pid=$!
child_start="$(process_start_time "${child_pid}")"
[[ -n "${child_start}" ]] || fail_test "missing child start time"
wait_for_exit_without_reaping "${child_pid}"
terminate_child_bounded "${child_pid}" "${child_start}" 2 || fail_test "reaped non-zero child reported termination failure"
[[ "${WAIT_CHILD_REAPED}" == "true" ]] || fail_test "non-zero child was not marked reaped"
[[ "${WAIT_CHILD_EXIT_CODE}" == "7" ]] || fail_test "child exit code was not preserved"
[[ "${#signal_log[@]}" == "0" ]] || fail_test "reaped child was signalled"

fake_root="$(mktemp -d)"
trap 'rm -rf "${fake_root}"' EXIT
mkdir -p "${fake_root}/123"
printf '123 (xray) S 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 111 0\n' >"${fake_root}/123/stat"
printf '/run/podlaz/generated/xray.json\0' >"${fake_root}/123/cmdline"
touch "${fake_root}/xray"
ln -s "${fake_root}/xray" "${fake_root}/123/exe"
PROCESS_PROC_ROOT="${fake_root}"
signal_log=()
process_send_signal() {
  local signal="$1" pid="$2"
  signal_log+=("${signal}:${pid}")
  if [[ "${signal}" == "TERM" ]]; then
    printf '123 (foreign) S 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 222 0\n' >"${fake_root}/123/stat"
  fi
  return 0
}
terminate_owned_process "123" "111" "${fake_root}/xray" "/run/podlaz/generated/" 1 || fail_test "identity change after TERM should count as original process gone"
[[ "${signal_log[*]}" == "TERM:123" ]] || fail_test "KILL was sent after PID identity changed: ${signal_log[*]}"

printf 'process lifecycle tests passed\n'
