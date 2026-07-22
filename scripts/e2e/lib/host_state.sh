#!/usr/bin/env bash

# Tri-state host inspection helpers.
# Return 0 only when absence/clean state is proven, 1 when the resource is
# present, and 2 when the inspection itself failed or produced unknown output.

HOST_STATE_ABSENT=0
HOST_STATE_PRESENT=1
HOST_STATE_ERROR=2

inspect_link_state() {
  local wanted="$1" output line rest name
  if ! output="$(sudo -n ip -o link show 2>/dev/null)"; then
    return "${HOST_STATE_ERROR}"
  fi
  while IFS= read -r line; do
    rest="${line#*: }"
    name="${rest%%:*}"
    name="${name%%@*}"
    [[ "${name}" == "${wanted}" ]] && return "${HOST_STATE_PRESENT}"
  done <<<"${output}"
  return "${HOST_STATE_ABSENT}"
}

inspect_resolved_link_state() {
  local wanted="$1" output
  if ! output="$(sudo -n resolvectl status --no-pager 2>/dev/null)"; then
    return "${HOST_STATE_ERROR}"
  fi
  if grep -E "^Link [0-9]+ \(${wanted//./\\.}\)$" <<<"${output}" >/dev/null; then
    return "${HOST_STATE_PRESENT}"
  fi
  return "${HOST_STATE_ABSENT}"
}

inspect_nft_table_state() {
  local family="$1" table="$2" output
  if ! output="$(sudo -n nft list tables 2>/dev/null)"; then
    return "${HOST_STATE_ERROR}"
  fi
  if grep -Fx "table ${family} ${table}" <<<"${output}" >/dev/null; then
    return "${HOST_STATE_PRESENT}"
  fi
  return "${HOST_STATE_ABSENT}"
}

inspect_path_state() {
  local path="$1" status
  if sudo -n python3 - "${path}" <<'PY'
import os
import sys
try:
    os.lstat(sys.argv[1])
except FileNotFoundError:
    raise SystemExit(0)
except OSError:
    raise SystemExit(2)
raise SystemExit(1)
PY
  then
    return "${HOST_STATE_ABSENT}"
  else
    status=$?
  fi
  case "${status}" in
    1) return "${HOST_STATE_PRESENT}" ;;
    *) return "${HOST_STATE_ERROR}" ;;
  esac
}

inspect_directory_content_state() {
  local path="$1" status
  if sudo -n python3 - "${path}" <<'PY'
import os
import sys
path = sys.argv[1]
try:
    with os.scandir(path) as entries:
        next(entries)
except FileNotFoundError:
    raise SystemExit(0)
except StopIteration:
    raise SystemExit(0)
except (NotADirectoryError, PermissionError, OSError):
    raise SystemExit(2)
raise SystemExit(1)
PY
  then
    return "${HOST_STATE_ABSENT}"
  else
    status=$?
  fi
  case "${status}" in
    1) return "${HOST_STATE_PRESENT}" ;;
    *) return "${HOST_STATE_ERROR}" ;;
  esac
}

inspect_package_state() {
  local wanted="$1" output package status
  if ! output="$(dpkg-query -W -f='${binary:Package}\t${db:Status-Status}\n' 2>/dev/null)"; then
    return "${HOST_STATE_ERROR}"
  fi
  while IFS=$'\t' read -r package status; do
    package="${package%%:*}"
    [[ "${package}" == "${wanted}" ]] || continue
    case "${status}" in
      not-installed|config-files) ;;
      installed|unpacked|half-configured|half-installed|triggers-awaited|triggers-pending)
        return "${HOST_STATE_PRESENT}"
        ;;
      *) return "${HOST_STATE_ERROR}" ;;
    esac
  done <<<"${output}"
  return "${HOST_STATE_ABSENT}"
}

inspect_service_load_state() {
  local unit="$1" output load_state
  if ! output="$(systemctl show --property=LoadState "${unit}" 2>/dev/null)"; then
    return "${HOST_STATE_ERROR}"
  fi
  load_state="${output#LoadState=}"
  case "${load_state}" in
    not-found) return "${HOST_STATE_ABSENT}" ;;
    loaded|masked|error|bad-setting) return "${HOST_STATE_PRESENT}" ;;
    *) return "${HOST_STATE_ERROR}" ;;
  esac
}

inspect_service_active_state() {
  local unit="$1" output load_state active_state line
  if ! output="$(systemctl show --property=LoadState --property=ActiveState "${unit}" 2>/dev/null)"; then
    return "${HOST_STATE_ERROR}"
  fi
  load_state=""
  active_state=""
  while IFS= read -r line; do
    case "${line}" in
      LoadState=*) load_state="${line#LoadState=}" ;;
      ActiveState=*) active_state="${line#ActiveState=}" ;;
    esac
  done <<<"${output}"
  [[ -n "${load_state}" && -n "${active_state}" ]] || return "${HOST_STATE_ERROR}"
  if [[ "${load_state}" == "not-found" ]]; then
    return "${HOST_STATE_ABSENT}"
  fi
  case "${active_state}" in
    inactive|failed) return "${HOST_STATE_ABSENT}" ;;
    active|activating|deactivating|reloading|refreshing) return "${HOST_STATE_PRESENT}" ;;
    *) return "${HOST_STATE_ERROR}" ;;
  esac
}
