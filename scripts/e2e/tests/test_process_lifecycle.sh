#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=../lib/process_lifecycle.sh
source "${SCRIPT_DIR}/../lib/process_lifecycle.sh"
# shellcheck source=../lib/connect_lifecycle.sh
source "${SCRIPT_DIR}/../lib/connect_lifecycle.sh"

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

PROCESS_POLL_INTERVAL=0.01

# A real live child must produce the bounded timeout instead of allowing the
# surrounding `if` compound command to overwrite 124 with success.
sleep 30 &
timeout_pid=$!
timeout_start="$(process_start_time "${timeout_pid}")"
if wait_child_bounded "${timeout_pid}" "${timeout_start}" 1; then
  fail_test "live child wait unexpectedly succeeded"
else
  timeout_status=$?
fi
[[ "${timeout_status}" == "124" ]] || fail_test "bounded wait returned ${timeout_status}, expected 124"
child_process_exists "${timeout_pid}" || fail_test "timeout child stopped before termination"
terminate_child_bounded "${timeout_pid}" "${timeout_start}" 50 || fail_test "live timeout child was not terminated"
[[ "${WAIT_CHILD_REAPED}" == "true" ]] || fail_test "terminated child was not reaped"
[[ "${WAIT_CHILD_EXIT_CODE}" == "143" || "${WAIT_CHILD_EXIT_CODE}" == "137" ]] || \
  fail_test "terminated child exit code was ${WAIT_CHILD_EXIT_CODE}"
child_process_exists "${timeout_pid}" && fail_test "terminated child still has a process identity"

# A transient /proc inspection failure must not trigger wait on a live child.
sleep 30 &
transient_pid=$!
transient_start="$(process_start_time "${transient_pid}")"
original_process_snapshot="$(declare -f process_snapshot)"
inspection_calls=0
process_snapshot() {
  inspection_calls=$((inspection_calls + 1))
  if [[ "${inspection_calls}" == "1" ]]; then
    return 1
  fi
  eval "${original_process_snapshot}"
  process_snapshot "$@"
}
if wait_child_bounded "${transient_pid}" "${transient_start}" 1; then
  fail_test "transient inspection failure was treated as child completion"
else
  transient_status=$?
fi
[[ "${transient_status}" == "124" ]] || fail_test "transient inspection returned ${transient_status}"
[[ "${WAIT_CHILD_REAPED}" == "false" ]] || fail_test "live child was reaped after transient inspection failure"
child_process_exists "${transient_pid}" || fail_test "transient child is no longer running"
eval "${original_process_snapshot}"
terminate_child_bounded "${transient_pid}" "${transient_start}" 50 || fail_test "transient child cleanup failed"

# The connect wrapper must preserve the exact timeout and retain tracking state.
sleep 30 &
CONNECT_PID=$!
CONNECT_START_TIME="$(process_start_time "${CONNECT_PID}")"
CONNECT_EXIT_CODE=""
if wait_connect_bounded "${CONNECT_PID}" 1; then
  fail_test "connect bounded wait converted timeout to success"
else
  connect_status=$?
fi
[[ "${connect_status}" == "124" ]] || fail_test "connect bounded wait returned ${connect_status}"
[[ -n "${CONNECT_PID}" && -n "${CONNECT_START_TIME}" ]] || fail_test "timed-out connect tracking was cleared"
terminate_connect_bounded || fail_test "timed-out connect termination failed"
[[ -z "${CONNECT_PID}" && -z "${CONNECT_START_TIME}" ]] || fail_test "terminated connect tracking remains"
[[ "${CONNECT_EXIT_CODE}" == "143" || "${CONNECT_EXIT_CODE}" == "137" ]] || \
  fail_test "connect termination exit code was ${CONNECT_EXIT_CODE}"

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
