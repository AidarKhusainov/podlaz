#!/usr/bin/env bash

# Finish an EXIT trap without allowing a cleanup failure to disappear behind a
# previously successful script body. The trap is removed before exit so the
# explicit final status cannot recurse through the same cleanup handler.
finish_exit_trap() {
  local original_status="$1" cleanup_status="$2" final_status

  [[ "${original_status}" =~ ^[0-9]+$ ]] || original_status=1
  [[ "${cleanup_status}" =~ ^[0-9]+$ ]] || cleanup_status=1
  final_status="${original_status}"
  if (( final_status == 0 && cleanup_status != 0 )); then
    final_status=1
  fi

  trap - EXIT
  exit "${final_status}"
}
