#!/usr/bin/env bash

validate_installed_daemon_status() {
  if [ "$#" -ne 2 ]; then
    echo "usage: validate_installed_daemon_status <binary> <output-prefix>" >&2
    return 2
  fi

  local binary="$1"
  local output_prefix="$2"
  local stdout_file="${output_prefix}.stdout"
  local stderr_file="${output_prefix}.stderr"
  local status_code=0

  "${binary}" status >"${stdout_file}" 2>"${stderr_file}" || status_code=$?

  cat "${stdout_file}"
  if [ -s "${stderr_file}" ]; then
    cat "${stderr_file}" >&2
  fi

  case "${status_code}" in
    0 | 3)
      ;;
    *)
      echo "${binary} status returned unexpected exit code ${status_code}" >&2
      return "${status_code}"
      ;;
  esac

  if ! grep -Fxq 'Daemon: running' "${stdout_file}"; then
    echo "${binary} status did not confirm that the packaged daemon is running" >&2
    return 1
  fi
}
