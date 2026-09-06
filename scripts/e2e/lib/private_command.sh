#!/usr/bin/env bash

# Capture commands that may expose profile, endpoint, credential, or other
# private runtime data. Output stays below E2E_TMP_ROOT and is never echoed.
capture_private_command() {
  local name="$1"
  shift
  local safe private_dir code restore_errexit=0

  case $- in
    *e*) restore_errexit=1 ;;
  esac
  safe="$(safe_name "${name}")"
  private_dir="${E2E_TMP_ROOT}/private-command"
  mkdir -p "${private_dir}"
  chmod 0700 "${private_dir}"

  E2E_STEP=$((E2E_STEP + 1))
  LAST_STDOUT="${private_dir}/$(printf '%03d' "${E2E_STEP}")-${safe}.stdout"
  LAST_STDERR="${private_dir}/$(printf '%03d' "${E2E_STEP}")-${safe}.stderr"
  : >"${LAST_STDOUT}"
  : >"${LAST_STDERR}"
  chmod 0600 "${LAST_STDOUT}" "${LAST_STDERR}"

  log "${name}: command arguments and output are intentionally not printed"
  set +e
  "$@" >"${LAST_STDOUT}" 2>"${LAST_STDERR}"
  code=$?
  if [[ "${restore_errexit}" == "1" ]]; then
    set -e
  fi
  return "${code}"
}

expect_private_success() {
  local name="$1"
  shift
  local code restore_errexit=0

  case $- in
    *e*) restore_errexit=1 ;;
  esac
  set +e
  capture_private_command "${name}" "$@"
  code=$?
  if [[ "${restore_errexit}" == "1" ]]; then
    set -e
  fi
  [[ "${code}" == "0" ]] || fail "${name} failed with exit code ${code}"
}
