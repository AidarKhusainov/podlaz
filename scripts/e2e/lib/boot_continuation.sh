#!/usr/bin/env bash

# Shared helpers for Issue #263 installed-package acceptance. The caller must
# source lib/e2e.sh first and run with `set -Eeuo pipefail`.

ISSUE263_DAEMON_SOCKET="/run/podlaz/podlazd.sock"
ISSUE263_MANIFEST_PATH="/var/lib/podlaz/boot-autostart-manifest.json"
ISSUE263_ATTEMPT_PATH="/run/podlaz/boot-autostart-attempt.json"
ISSUE263_PRODUCT_REASON_PATH="/run/podlaz/product-terminal-reason.json"
ISSUE263_STATE_HELPER="${SCRIPT_DIR}/lib/issue263_state.py"

issue263_mask_multiline_sensitive() {
  local value="${1:-}"
  [[ -n "${value}" ]] || return 0
  mask_value "${value}"
  while IFS= read -r line; do
    [[ -n "${line}" ]] && mask_value "${line}"
  done <<<"${value}"
}

issue263_first_profile_uri() {
  if [[ -n "${PODLAZ_E2E_PROFILE_URI:-}" ]]; then
    printf '%s\n' "${PODLAZ_E2E_PROFILE_URI}"
    return
  fi
  while IFS= read -r uri; do
    [[ -n "${uri}" ]] || continue
    printf '%s\n' "${uri}"
    return
  done <<<"${PODLAZ_E2E_PROFILE_URI_LIST:-}"
}

issue263_run_podlaz() {
  sudo -n runuser -u "$(id -un)" -g podlaz -- env \
    XDG_CONFIG_HOME="${XDG_CONFIG_HOME}" \
    XDG_STATE_HOME="${XDG_STATE_HOME}" \
    XDG_CACHE_HOME="${XDG_CACHE_HOME}" \
    /usr/bin/podlaz "$@"
}

issue263_wait_for_daemon() {
  local attempt
  for attempt in $(seq 1 300); do
    if [[ -S "${ISSUE263_DAEMON_SOCKET}" ]] && sudo -n systemctl is-active --quiet podlazd.service; then
      return 0
    fi
    sleep 0.1
  done
  fail "podlazd.service did not become ready for issue 263 acceptance"
}

issue263_daemon_status_matches() {
  local expected_connection="$1" expected_tun="$2" require_verified="$3" output
  output="$(mktemp "${E2E_TMP_ROOT}/issue263-status.XXXXXX")"
  if ! sudo -n curl --fail --silent --show-error --max-time 5 \
      --unix-socket "${ISSUE263_DAEMON_SOCKET}" http://localhost/v1/status >"${output}" 2>/dev/null; then
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

issue263_wait_for_verified_active() {
  local attempt
  issue263_wait_for_daemon
  for attempt in $(seq 1 600); do
    if issue263_daemon_status_matches active active true; then
      return 0
    fi
    sleep 0.2
  done
  fail "issue 263 TUN session did not converge to verified active"
}

issue263_wait_for_inactive() {
  local attempt
  issue263_wait_for_daemon
  for attempt in $(seq 1 400); do
    if issue263_daemon_status_matches inactive disabled false; then
      return 0
    fi
    sleep 0.2
  done
  fail "issue 263 daemon did not converge to clean inactive"
}

issue263_import_real_profile() {
  local uri out err
  uri="$(issue263_first_profile_uri)"
  [[ -n "${uri}" ]] || fail "issue 263 acceptance requires a profile URI"
  out="$(mktemp "${E2E_TMP_ROOT}/issue263-import.stdout.XXXXXX")"
  err="$(mktemp "${E2E_TMP_ROOT}/issue263-import.stderr.XXXXXX")"
  if ! issue263_run_podlaz profile import "${uri}" >"${out}" 2>"${err}"; then
    rm -f -- "${out}" "${err}"
    fail "issue 263 profile import failed"
  fi
  ISSUE263_PROFILE_ID="$(awk '/^Imported profile:/ {print $3}' "${out}")"
  [[ -n "${ISSUE263_PROFILE_ID}" ]] || fail "issue 263 profile import returned no profile ID"
  issue263_mask_multiline_sensitive "${ISSUE263_PROFILE_ID}"
  export ISSUE263_PROFILE_ID
  rm -f -- "${out}" "${err}"
}

issue263_import_terminal_failure_profile() {
  local uri out err
  uri='vless://00000000-0000-4000-8000-000000000001@vpn.invalid:443?security=tls&type=tcp&sni=vpn.invalid#Issue263Failure'
  out="$(mktemp "${E2E_TMP_ROOT}/issue263-failure-import.stdout.XXXXXX")"
  err="$(mktemp "${E2E_TMP_ROOT}/issue263-failure-import.stderr.XXXXXX")"
  if ! issue263_run_podlaz profile import "${uri}" >"${out}" 2>"${err}"; then
    rm -f -- "${out}" "${err}"
    fail "issue 263 failure-profile import failed"
  fi
  ISSUE263_FAILURE_PROFILE_ID="$(awk '/^Imported profile:/ {print $3}' "${out}")"
  [[ -n "${ISSUE263_FAILURE_PROFILE_ID}" ]] || fail "issue 263 failure-profile import returned no profile ID"
  issue263_mask_multiline_sensitive "${ISSUE263_FAILURE_PROFILE_ID}"
  export ISSUE263_FAILURE_PROFILE_ID
  rm -f -- "${out}" "${err}"
}

issue263_assert_autostart_line() {
  local expected="$1" output
  output="$(mktemp "${E2E_TMP_ROOT}/issue263-autostart-status.XXXXXX")"
  issue263_run_podlaz autostart status >"${output}" 2>/dev/null || fail "autostart status failed"
  grep -Fx -- "${expected}" "${output}" >/dev/null || fail "unexpected autostart status"
  rm -f -- "${output}"
}

issue263_prepare_simulated_later_boot() {
  local marker="$1"
  sudo -n test -f "${ISSUE263_MANIFEST_PATH}" || fail "boot autostart manifest is missing"
  sudo -n rm -f -- "${ISSUE263_ATTEMPT_PATH}" "${ISSUE263_PRODUCT_REASON_PATH}"
  sudo -n python3 "${ISSUE263_STATE_HELPER}" \
    make-manifest-eligible "${ISSUE263_MANIFEST_PATH}" "${marker}"
}

issue263_assert_attempt() {
  local state="$1" reason="${2:-}"
  if [[ -n "${reason}" ]]; then
    sudo -n python3 "${ISSUE263_STATE_HELPER}" assert-attempt "${ISSUE263_ATTEMPT_PATH}" "${state}" "${reason}"
  else
    sudo -n python3 "${ISSUE263_STATE_HELPER}" assert-attempt "${ISSUE263_ATTEMPT_PATH}" "${state}"
  fi
}

issue263_attempt_fingerprint() {
  sudo -n python3 "${ISSUE263_STATE_HELPER}" attempt-control-fingerprint "${ISSUE263_ATTEMPT_PATH}"
}

issue263_assert_product_reason() {
  local expected="$1" output attempt
  output="$(mktemp "${E2E_TMP_ROOT}/issue263-product-status.XXXXXX")"
  for attempt in $(seq 1 200); do
    if issue263_run_podlaz status >"${output}" 2>/dev/null && grep -Fx -- "Reason: ${expected}" "${output}" >/dev/null; then
      rm -f -- "${output}"
      return 0
    fi
    sleep 0.2
  done
  rm -f -- "${output}"
  fail "issue 263 product terminal reason was not published"
}

issue263_restart_daemon() {
  sudo -n systemctl restart podlazd.service
  issue263_wait_for_daemon
}

issue263_main_pid() {
  sudo -n systemctl show -p MainPID --value podlazd.service | tr -d '[:space:]'
}
