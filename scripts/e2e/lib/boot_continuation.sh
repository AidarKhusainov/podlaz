#!/usr/bin/env bash

# Shared helpers for boot-continuation installed-package acceptance. The caller
# must source lib/e2e.sh first and run with `set -Eeuo pipefail`.
# shellcheck source=readiness.sh
source "${SCRIPT_DIR}/lib/readiness.sh"
# shellcheck source=installed_client.sh
source "${SCRIPT_DIR}/lib/installed_client.sh"
# shellcheck source=profile_input.sh
source "${SCRIPT_DIR}/lib/profile_input.sh"
# shellcheck source=status_polling.sh
source "${SCRIPT_DIR}/lib/status_polling.sh"

BOOT_CONTINUATION_DAEMON_SOCKET="/run/podlaz/podlazd.sock"
BOOT_CONTINUATION_MANIFEST_PATH="/var/lib/podlaz/boot-autostart-manifest.json"
BOOT_CONTINUATION_ATTEMPT_PATH="/run/podlaz/boot-autostart-attempt.json"
BOOT_CONTINUATION_PRODUCT_REASON_PATH="/run/podlaz/product-terminal-reason.json"
BOOT_CONTINUATION_STATE_HELPER="${SCRIPT_DIR}/lib/boot_continuation_state.py"

boot_continuation_mask_multiline_sensitive() {
  local value="${1:-}"
  [[ -n "${value}" ]] || return 0
  mask_value "${value}"
  while IFS= read -r line; do
    [[ -n "${line}" ]] && mask_value "${line}"
  done <<<"${value}"
}

boot_continuation_first_profile_uri() {
  first_configured_profile_uri
}

boot_continuation_run_podlaz() {
  run_installed_podlaz "$@"
}

boot_continuation_wait_for_daemon() {
  wait_for_daemon_ready "${BOOT_CONTINUATION_DAEMON_SOCKET}" podlazd.service 30
}

boot_continuation_daemon_status_matches() {
  local expected_connection="$1" expected_tun="$2" require_verified="$3" output
  output="$(mktemp "${E2E_TMP_ROOT}/boot-continuation-status.XXXXXX")"
  if ! sudo -n curl --fail --silent --show-error --max-time 5 \
      --unix-socket "${BOOT_CONTINUATION_DAEMON_SOCKET}" http://localhost/v1/status >"${output}" 2>/dev/null; then
    rm -f -- "${output}"
    return 1
  fi
  if python3 - "${output}" "${expected_connection}" "${expected_tun}" "${require_verified}" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    status = json.load(handle)
if status.get("connection") != sys.argv[2] or status.get("tun") != sys.argv[3]:
    raise SystemExit(1)
if sys.argv[4] == "true":
    health = status.get("tun_health") or {}
    if health.get("state") != "verified":
        raise SystemExit(1)
raise SystemExit(0)
PY
  then
    rm -f -- "${output}"
    return 0
  fi
  rm -f -- "${output}"
  return 1
}

boot_continuation_wait_for_verified_active() {
  boot_continuation_wait_for_daemon
  wait_for_status_match "boot-continuation TUN session" 120 \
    boot_continuation_daemon_status_matches active active true
}

boot_continuation_wait_for_inactive() {
  boot_continuation_wait_for_daemon
  wait_for_status_match "boot-continuation daemon inactive state" 80 \
    boot_continuation_daemon_status_matches inactive disabled false
}

boot_continuation_import_real_profile() {
  local uri out err
  uri="$(boot_continuation_first_profile_uri)"
  [[ -n "${uri}" ]] || fail "boot-continuation acceptance requires a profile URI"
  out="$(mktemp "${E2E_TMP_ROOT}/boot-continuation-import.stdout.XXXXXX")"
  err="$(mktemp "${E2E_TMP_ROOT}/boot-continuation-import.stderr.XXXXXX")"
  if ! boot_continuation_run_podlaz profile import "${uri}" >"${out}" 2>"${err}"; then
    rm -f -- "${out}" "${err}"
    fail "boot-continuation profile import failed"
  fi
  BOOT_CONTINUATION_PROFILE_ID="$(awk '/^Imported profile:/ {print $3}' "${out}")"
  [[ -n "${BOOT_CONTINUATION_PROFILE_ID}" ]] || fail "boot-continuation profile import returned no profile ID"
  boot_continuation_mask_multiline_sensitive "${BOOT_CONTINUATION_PROFILE_ID}"
  export BOOT_CONTINUATION_PROFILE_ID
  rm -f -- "${out}" "${err}"
}

boot_continuation_import_terminal_failure_profile() {
  local uri out err
  uri='vless://00000000-0000-4000-8000-000000000001@vpn.invalid:443?security=tls&type=tcp&sni=vpn.invalid#BootContinuationFailure'
  out="$(mktemp "${E2E_TMP_ROOT}/boot-continuation-failure-import.stdout.XXXXXX")"
  err="$(mktemp "${E2E_TMP_ROOT}/boot-continuation-failure-import.stderr.XXXXXX")"
  if ! boot_continuation_run_podlaz profile import "${uri}" >"${out}" 2>"${err}"; then
    rm -f -- "${out}" "${err}"
    fail "boot-continuation failure-profile import failed"
  fi
  BOOT_CONTINUATION_FAILURE_PROFILE_ID="$(awk '/^Imported profile:/ {print $3}' "${out}")"
  [[ -n "${BOOT_CONTINUATION_FAILURE_PROFILE_ID}" ]] || fail "boot-continuation failure-profile import returned no profile ID"
  boot_continuation_mask_multiline_sensitive "${BOOT_CONTINUATION_FAILURE_PROFILE_ID}"
  export BOOT_CONTINUATION_FAILURE_PROFILE_ID
  rm -f -- "${out}" "${err}"
}

boot_continuation_assert_autostart_line() {
  local expected="$1" output
  output="$(mktemp "${E2E_TMP_ROOT}/boot-continuation-autostart-status.XXXXXX")"
  boot_continuation_run_podlaz autostart status >"${output}" 2>/dev/null || fail "autostart status failed"
  grep -Fx -- "${expected}" "${output}" >/dev/null || fail "unexpected autostart status"
  rm -f -- "${output}"
}

boot_continuation_prepare_simulated_later_boot() {
  local marker="$1"
  sudo -n test -f "${BOOT_CONTINUATION_MANIFEST_PATH}" || fail "boot autostart manifest is missing"
  sudo -n rm -f -- "${BOOT_CONTINUATION_ATTEMPT_PATH}" "${BOOT_CONTINUATION_PRODUCT_REASON_PATH}"
  sudo -n python3 "${BOOT_CONTINUATION_STATE_HELPER}" \
    make-manifest-eligible "${BOOT_CONTINUATION_MANIFEST_PATH}" "${marker}"
}

boot_continuation_assert_attempt() {
  local state="$1" reason="${2:-}"
  if [[ -n "${reason}" ]]; then
    sudo -n python3 "${BOOT_CONTINUATION_STATE_HELPER}" assert-attempt "${BOOT_CONTINUATION_ATTEMPT_PATH}" "${state}" "${reason}"
  else
    sudo -n python3 "${BOOT_CONTINUATION_STATE_HELPER}" assert-attempt "${BOOT_CONTINUATION_ATTEMPT_PATH}" "${state}"
  fi
}

boot_continuation_attempt_fingerprint() {
  sudo -n python3 "${BOOT_CONTINUATION_STATE_HELPER}" attempt-control-fingerprint "${BOOT_CONTINUATION_ATTEMPT_PATH}"
}

boot_continuation_assert_product_reason() {
  local expected="$1" output attempt
  output="$(mktemp "${E2E_TMP_ROOT}/boot-continuation-product-status.XXXXXX")"
  for attempt in $(seq 1 200); do
    if boot_continuation_run_podlaz status >"${output}" 2>/dev/null && grep -Fx -- "Reason: ${expected}" "${output}" >/dev/null; then
      rm -f -- "${output}"
      return 0
    fi
    sleep 0.2
  done
  rm -f -- "${output}"
  fail "boot-continuation product terminal reason was not published"
}

boot_continuation_restart_daemon() {
  sudo -n systemctl restart podlazd.service
  boot_continuation_wait_for_daemon
}

boot_continuation_main_pid() {
  sudo -n systemctl show -p MainPID --value podlazd.service | tr -d '[:space:]'
}
