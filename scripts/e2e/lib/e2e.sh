#!/usr/bin/env bash

# Shared helpers for podlaz E2E and package acceptance scripts.
# Scripts sourcing this file are expected to run with `set -Eeuo pipefail`.

E2E_ARTIFACT_DIR="${E2E_ARTIFACT_DIR:-${RUNNER_TEMP:-/tmp}/podlaz-e2e-artifacts}"
E2E_TMP_ROOT="${E2E_TMP_ROOT:-${RUNNER_TEMP:-/tmp}/podlaz-e2e-tmp}"
mkdir -p "${E2E_ARTIFACT_DIR}" "${E2E_TMP_ROOT}"

E2E_LIB_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
E2E_REDACTION_SCAN="${E2E_LIB_DIR}/redaction_scan.py"

E2E_STEP=0
LAST_STDOUT=""
LAST_STDERR=""

log() {
  printf '\n>>> %s\n' "$*"
}

fail() {
  printf 'ERROR: %s\n' "$*" >&2
  if [[ "${GITHUB_ACTIONS:-}" == "true" ]]; then
    printf '::error::%s\n' "$*" >&2
  fi
  exit 1
}

require_cmd() {
  local cmd
  for cmd in "$@"; do
    command -v "${cmd}" >/dev/null 2>&1 || fail "required command not found: ${cmd}"
  done
}

safe_name() {
  printf '%s' "$1" | tr -c 'A-Za-z0-9._-' '_'
}

run_capture() {
  local name="$1"
  shift
  local safe
  safe="$(safe_name "${name}")"
  E2E_STEP=$((E2E_STEP + 1))
  LAST_STDOUT="${E2E_ARTIFACT_DIR}/$(printf '%03d' "${E2E_STEP}")-${safe}.stdout"
  LAST_STDERR="${E2E_ARTIFACT_DIR}/$(printf '%03d' "${E2E_STEP}")-${safe}.stderr"

  local restore_errexit=0
  case $- in
    *e*) restore_errexit=1 ;;
  esac

  log "${name}: $*"
  set +e
  "$@" >"${LAST_STDOUT}" 2>"${LAST_STDERR}"
  local code=$?

  if [[ -s "${LAST_STDOUT}" ]]; then
    sed -e 's/^/stdout: /' "${LAST_STDOUT}"
  fi
  if [[ -s "${LAST_STDERR}" ]]; then
    sed -e 's/^/stderr: /' "${LAST_STDERR}" >&2
  fi

  if [[ "${code}" == "0" && "${restore_errexit}" == "1" ]]; then
    set -e
  fi
  return "${code}"
}

expect_success() {
  local name="$1"
  shift
  set +e
  run_capture "${name}" "$@"
  local code=$?
  set -e
  if [[ "${code}" != "0" ]]; then
    fail "${name} failed with exit code ${code}"
  fi
}

expect_exit() {
  local want="$1"
  local name="$2"
  shift 2
  set +e
  run_capture "${name}" "$@"
  local got=$?
  set -e
  if [[ "${got}" != "${want}" ]]; then
    fail "${name}: expected exit ${want}, got ${got}"
  fi
}

assert_contains() {
  local file="$1"
  local needle="$2"
  grep -F -- "${needle}" "${file}" >/dev/null || fail "expected ${file} to contain: ${needle}"
}

assert_not_contains() {
  local file="$1"
  local needle="$2"
  if grep -F -- "${needle}" "${file}" >/dev/null; then
    fail "expected ${file} not to contain: ${needle}"
  fi
}

assert_file_mode() {
  local path="$1"
  local want="$2"
  local got
  got="$(stat -c '%a' "${path}")"
  [[ "${got}" == "${want}" ]] || fail "${path}: mode ${got}, want ${want}"
}

assert_json_file() {
  local path="$1"
  python3 -m json.tool "${path}" >/dev/null || fail "invalid JSON: ${path}"
}

mask_value() {
  local value="${1:-}"
  [[ -n "${value}" ]] || return 0
  if [[ "${GITHUB_ACTIONS:-}" == "true" ]]; then
    printf '::add-mask::%s\n' "${value}"
  fi
}

assert_nonempty() {
  local value="$1"
  local description="$2"
  [[ -n "${value}" ]] || fail "${description} is empty"
}

assert_artifacts_do_not_contain_sensitive_values() {
  local label="$1"
  shift
  local report="${E2E_ARTIFACT_DIR}/$(safe_name "${label}")-redaction-scan.txt"
  require_cmd python3
  if ! python3 "${E2E_REDACTION_SCAN}" sensitive-values "${E2E_ARTIFACT_DIR}" "${report}" "$@"; then
    fail "${label}: sensitive value appeared in e2e artifacts"
  fi
}

assert_artifacts_do_not_contain_file_contents() {
  local label="$1"
  shift
  local report="${E2E_ARTIFACT_DIR}/$(safe_name "${label}")-content-redaction-scan.txt"
  require_cmd python3
  if ! python3 "${E2E_REDACTION_SCAN}" file-contents "${E2E_ARTIFACT_DIR}" "${report}" "$@"; then
    fail "${label}: generated content appeared in e2e artifacts"
  fi
}

runtime_config_paths_from_status_json() {
  local status_json="$1"
  require_cmd python3
  python3 - "${status_json}" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    payload = json.load(handle)
value = payload.get("runtime_config_path")
if isinstance(value, str) and value.strip():
    print(value.strip())
PY
}

copy_runtime_config_for_scan() {
  local path="$1"
  local copy="$2"
  if [[ -r "${path}" ]]; then
    cat -- "${path}" >"${copy}"
    return 0
  fi
  if command -v sudo >/dev/null 2>&1 && command -v getent >/dev/null 2>&1 && getent group podlaz-xray >/dev/null 2>&1; then
    if sudo -n -u "$(id -un)" -g podlaz-xray cat -- "${path}" >"${copy}" 2>"${copy}.stderr"; then
      rm -f -- "${copy}.stderr"
      return 0
    fi
    rm -f -- "${copy}.stderr"
  fi
  return 1
}

record_runtime_config_read_boundary() {
  local label="$1"
  local path="$2"
  local evidence="$3"
  local stat_output
  {
    printf 'Runtime config content scan boundary\n'
    printf 'label: %s\n' "${label}"
    printf 'path: %s\n' "${path}"
    printf 'reason: runtime config is not readable by the e2e runner without privileged file reads\n'
    if stat_output="$(stat -Lc 'mode=%A owner=%U group=%G path=%n' -- "${path}" 2>&1)"; then
      printf '%s\n' "${stat_output}"
    else
      printf 'stat: %s\n' "${stat_output}"
    fi
  } >>"${evidence}"
}

assert_active_runtime_config_artifacts_safe() {
  local label="$1"
  local status_json="$2"
  local paths=()
  local source_copies=()
  local path copy report evidence scan_code=0

  mapfile -t paths < <(runtime_config_paths_from_status_json "${status_json}")
  if [[ "${#paths[@]}" -eq 0 ]]; then
    fail "${label}: active status did not expose runtime_config_path for generated-content redaction scan"
  fi

  evidence="${E2E_ARTIFACT_DIR}/$(safe_name "${label}")-runtime-config-read-boundary.txt"
  : >"${evidence}"

  for path in "${paths[@]}"; do
    [[ "${path}" == /* ]] || fail "${label}: runtime_config_path is not absolute"
    copy="$(mktemp "${E2E_TMP_ROOT}/$(safe_name "${label}").runtime-config.XXXXXX")"
    chmod 600 "${copy}"
    if copy_runtime_config_for_scan "${path}" "${copy}"; then
      source_copies+=("${copy}")
    else
      rm -f -- "${copy}"
      record_runtime_config_read_boundary "${label}" "${path}" "${evidence}"
    fi
  done

  report="${E2E_ARTIFACT_DIR}/$(safe_name "${label}")-runtime-config-redaction-scan.txt"
  if [[ "${#source_copies[@]}" -gt 0 ]]; then
    set +e
    python3 "${E2E_REDACTION_SCAN}" file-contents "${E2E_ARTIFACT_DIR}" "${report}" "${source_copies[@]}"
    scan_code=$?
    set -e
    rm -f -- "${source_copies[@]}"
    if [[ "${scan_code}" != "0" ]]; then
      fail "${label}: runtime config content appeared in e2e artifacts"
    fi
  else
    printf 'runtime config content scan skipped: content not readable without privileged file reads\n' >"${report}"
  fi
}
