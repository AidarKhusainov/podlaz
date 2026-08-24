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

  if [ "${status_code}" -ne 0 ]; then
    echo "${binary} status returned unexpected exit code ${status_code}" >&2
    return "${status_code}"
  fi

  if ! grep -Fxq 'Status: Disconnected' "${stdout_file}"; then
    echo "${binary} status did not report a clean disconnected product state" >&2
    return 1
  fi
  if ! grep -Fxq 'Autostart: Disabled' "${stdout_file}"; then
    echo "${binary} status did not report disabled boot autostart for a fresh package" >&2
    return 1
  fi

  if grep -Eq '^(Daemon|Service|Runtime directory|Runtime config|Proxy|TUN|Routes|DNS|Firewall|Transactions|Recovery candidates):' "${stdout_file}"; then
    echo "${binary} status exposed operator diagnostics in the primary product view" >&2
    return 1
  fi
}
