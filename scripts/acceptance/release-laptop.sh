#!/usr/bin/env bash
set -Euo pipefail

RA_RELEASE_MODULES=(
  core.sh
  product.sh
  host_exercise.sh
  lifecycle.sh
  evidence.sh
  scenarios.sh
  legacy.sh
)

ra_release_load_modules() {
  local entry_dir module_dir module path
  entry_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)" || return 1
  module_dir="$entry_dir/lib/release-laptop"
  [[ -d "$module_dir" && ! -L "$module_dir" ]] || {
    printf 'release-laptop: required controller module directory is missing or unsafe: %s\n' "$module_dir" >&2
    return 1
  }
  for module in "${RA_RELEASE_MODULES[@]}"; do
    path="$module_dir/$module"
    [[ -f "$path" && ! -L "$path" ]] || {
      printf 'release-laptop: required controller module is missing or unsafe: %s\n' "$path" >&2
      return 1
    }
    # shellcheck source=/dev/null
    source "$path" || return 1
  done
}

if [[ "${RA_RELEASE_STANDALONE:-0}" != 1 ]]; then
  if ! ra_release_load_modules; then
    if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
      exit 1
    fi
    return 1
  fi
fi

ra_main() {
  local rc
  ra_cli_parse "$@"; rc=$?
  if [[ "$rc" == 64 ]]; then ra_usage; return 0; fi
  ((rc==0)) || { ra_usage >&2; return "$rc"; }
  ra_require_root_and_user || return $?
  ra_init_paths
  ra_require_tools || return $?
  ra_lock_acquire || return 1
  trap 'ra_signal_handler INT; exit 130' INT
  trap 'ra_signal_handler TERM; exit 143' TERM
  case "$RA_MODE" in new) ra_run_new; rc=$? ;; resume) ra_run_resume; rc=$? ;; abort) ra_run_abort; rc=$? ;; restart) ra_run_restart; rc=$? ;; *) rc=1 ;; esac
  trap - INT TERM
  if ((rc!=0)) && ((rc!=RA_RC_PAUSED)) && [[ "$RA_MODE" != abort ]] && ((RA_SUPPRESS_FINALIZER==0)) && ra_checkpoint_exists >/dev/null 2>&1 && ! ra_phase_blocks_auto_finalizer; then ra_failure_finalize operation_failed "$rc" || true; return 1; fi
  return "$rc"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  ra_main "$@"
  exit $?
fi
