#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"

require_cmd awk curl getent ip nft python3 runuser sed sleep sudo systemctl timeout

: "${PODLAZ_E2E_PROFILE_URI:=}"
: "${PODLAZ_E2E_PROFILE_URI_LIST:=}"
: "${PODLAZ_E2E_DNS_CHECK_HOST:=github.com}"
: "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL:=https://api.ipify.org}"

if [[ -z "${PODLAZ_E2E_PROFILE_URI}" && -z "${PODLAZ_E2E_PROFILE_URI_LIST}" ]]; then
  fail "PODLAZ_E2E_PROFILE_URI or PODLAZ_E2E_PROFILE_URI_LIST is required"
fi

CONTINUATION_PATH="/run/podlaz/network-session-continuation.json"
OVERRIDE_DIR="/etc/systemd/system/podlazd.service.d"
OVERRIDE_PATH="${OVERRIDE_DIR}/99-issue261-e2e.conf"
HOOK_DIR="/run/podlaz/issue261-e2e"
FOREIGN_NFT_FAMILY="inet"
FOREIGN_NFT_TABLE="podlaz_pe_ffffffffffff"
EVIDENCE="${E2E_ARTIFACT_DIR}/issue261-acceptance.txt"
PROFILE_ID=""
ENVELOPE_FAMILY=""
ENVELOPE_TABLE=""
UPLINK=""
PROBE_HOST=""
PROBE_PORT=""
PROBE_IP=""
CONNECTED=false
FOREIGN_CREATED=false

mask_multiline_sensitive() {
  local value="${1:-}"
  [[ -n "${value}" ]] || return 0
  mask_value "${value}"
  while IFS= read -r line; do
    [[ -n "${line}" ]] && mask_value "${line}"
  done <<<"${value}"
}
mask_multiline_sensitive "${PODLAZ_E2E_PROFILE_URI}"
mask_multiline_sensitive "${PODLAZ_E2E_PROFILE_URI_LIST}"

write_evidence() {
  local key="$1"
  case "${key}" in
    *[!A-Za-z0-9_.-]*) fail "invalid issue 261 evidence key" ;;
  esac
  printf '%s=pass\n' "${key}" >>"${EVIDENCE}"
}

first_profile_uri() {
  if [[ -n "${PODLAZ_E2E_PROFILE_URI}" ]]; then
    printf '%s\n' "${PODLAZ_E2E_PROFILE_URI}"
    return
  fi
  while IFS= read -r uri; do
    [[ -n "${uri}" ]] || continue
    printf '%s\n' "${uri}"
    return
  done <<<"${PODLAZ_E2E_PROFILE_URI_LIST}"
}

run_installed_podlaz() {
  sudo -n runuser -u "$(id -un)" -g podlaz -- env \
    XDG_CONFIG_HOME="${XDG_CONFIG_HOME}" \
    XDG_STATE_HOME="${XDG_STATE_HOME}" \
    XDG_CACHE_HOME="${XDG_CACHE_HOME}" \
    /usr/bin/podlaz "$@"
}

wait_for_service() {
  local attempt
  for attempt in $(seq 1 300); do
    if sudo -n systemctl is-active --quiet podlazd.service; then
      return 0
    fi
    sleep 0.1
  done
  fail "podlazd.service did not become active"
}

status_matches() {
  local expected_connection="$1" expected_tun="$2" output
  output="$(mktemp "${E2E_TMP_ROOT}/issue261-status.XXXXXX")"
  if ! run_installed_podlaz status --json >"${output}" 2>/dev/null; then
    rm -f -- "${output}"
    return 1
  fi
  if python3 - "${output}" "${expected_connection}" "${expected_tun}" <<'PY'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as handle:
    payload = json.load(handle)
status = payload.get("status") or payload
raise SystemExit(0 if status.get("connection") == sys.argv[2] and status.get("tun") == sys.argv[3] else 1)
PY
  then
    rm -f -- "${output}"
    return 0
  fi
  rm -f -- "${output}"
  return 1
}

wait_for_active_tun() {
  local phase="$1" attempt
  wait_for_service
  for attempt in $(seq 1 300); do
    if status_matches active active; then
      write_evidence "${phase}"
      return 0
    fi
    sleep 0.2
  done
  fail "${phase}: protected TUN did not become active"
}

wait_for_inactive() {
  local phase="$1" attempt
  wait_for_service
  for attempt in $(seq 1 200); do
    if status_matches inactive disabled; then
      write_evidence "${phase}"
      return 0
    fi
    sleep 0.2
  done
  fail "${phase}: daemon did not become clean inactive"
}

import_profile() {
  local uri out err
  uri="$(first_profile_uri)"
  out="$(mktemp "${E2E_TMP_ROOT}/issue261-import.stdout.XXXXXX")"
  err="$(mktemp "${E2E_TMP_ROOT}/issue261-import.stderr.XXXXXX")"
  run_installed_podlaz profile import "${uri}" >"${out}" 2>"${err}" || fail "issue 261 profile import failed"
  PROFILE_ID="$(awk '/^Imported profile:/ {print $3}' "${out}")"
  [[ -n "${PROFILE_ID}" ]] || fail "issue 261 profile import returned no profile ID"
  mask_multiline_sensitive "${PROFILE_ID}"
  rm -f -- "${out}" "${err}"
}

load_envelope_identity() {
  local identity
  identity="$(sudo -n python3 - "${CONTINUATION_PATH}" <<'PY'
import json
import re
import sys
with open(sys.argv[1], encoding="utf-8") as handle:
    state = json.load(handle)
protection = state.get("protection") or {}
if protection.get("state") != "armed":
    raise SystemExit(f"privacy protection is not armed: {protection.get('state')!r}")
family = protection.get("family")
table = protection.get("table")
if family != "inet" or not re.fullmatch(r"podlaz_pe_[0-9a-f]{12}(?:_[1-9][0-9]{0,2})?", str(table or "")):
    raise SystemExit("invalid persisted privacy envelope identity")
print(f"{family} {table}")
PY
)" || fail "could not load armed privacy envelope authority"
  read -r ENVELOPE_FAMILY ENVELOPE_TABLE <<<"${identity}"
  [[ -n "${ENVELOPE_FAMILY}" && -n "${ENVELOPE_TABLE}" ]] || fail "empty privacy envelope identity"
}

assert_privacy_envelope_present() {
  sudo -n nft list table "${ENVELOPE_FAMILY}" "${ENVELOPE_TABLE}" >/dev/null 2>&1 || fail "expected exact Privacy Envelope to remain present"
}

prepare_direct_probe() {
  UPLINK="$(ip -4 route show default | awk 'NR==1 {for (i=1; i<=NF; i++) if ($i=="dev") {print $(i+1); exit}}')"
  [[ -n "${UPLINK}" && "${UPLINK}" != "podlaz0" ]] || fail "could not identify ordinary uplink"
  read -r PROBE_HOST PROBE_PORT < <(python3 - "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL}" <<'PY'
import sys
from urllib.parse import urlsplit
url = urlsplit(sys.argv[1])
if not url.hostname:
    raise SystemExit("public IP check URL has no host")
port = url.port or (443 if url.scheme == "https" else 80)
print(url.hostname, port)
PY
)
  PROBE_IP="$(getent ahostsv4 "${PROBE_HOST}" | awk 'NR==1 {print $1}')"
  [[ -n "${PROBE_IP}" ]] || fail "could not pre-resolve direct-leak probe host"
  mask_multiline_sensitive "${PROBE_IP}"
}

assert_direct_uplink_blocked() {
  if timeout 6s curl -4 -fsSk --interface "${UPLINK}" \
      --connect-timeout 3 --max-time 5 \
      --resolve "${PROBE_HOST}:${PROBE_PORT}:${PROBE_IP}" \
      "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL}" >/dev/null 2>&1; then
    fail "ordinary direct uplink egress escaped the Privacy Envelope"
  fi
}

create_foreign_collision_guard() {
  if sudo -n nft list table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}" >/dev/null 2>&1; then
    fail "foreign Privacy Envelope-shaped fixture already exists"
  fi
  sudo -n nft add table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}"
  FOREIGN_CREATED=true
}

assert_foreign_collision_guard() {
  sudo -n nft list table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}" >/dev/null 2>&1 || fail "foreign collision-shaped nftables table was removed"
}

install_restart_delay_override() {
  sudo -n mkdir -p "${OVERRIDE_DIR}"
  sudo -n tee "${OVERRIDE_PATH}" >/dev/null <<'EOF'
[Service]
RestartSec=8s
EOF
  sudo -n systemctl daemon-reload
}

install_terminal_pause_override() {
  sudo -n mkdir -p "${OVERRIDE_DIR}"
  sudo -n rm -rf -- "${HOOK_DIR}"
  sudo -n mkdir -m 0700 "${HOOK_DIR}"
  sudo -n tee "${OVERRIDE_PATH}" >/dev/null <<EOF
[Service]
Environment=PODLAZ_E2E_PRIVACY_TEARDOWN_PAUSE=true
Environment=PODLAZ_E2E_PRIVACY_TEARDOWN_PAUSE_DIR=${HOOK_DIR}
Environment=PODLAZ_E2E_PRIVACY_TEARDOWN_PAUSE_TIMEOUT_SECONDS=120
EOF
  sudo -n systemctl daemon-reload
  sudo -n systemctl restart podlazd.service
  wait_for_active_tun terminal_hook_restart_recovered
}

remove_override() {
  sudo -n rm -f -- "${OVERRIDE_PATH}"
  sudo -n systemctl daemon-reload >/dev/null 2>&1 || true
}

wait_for_marker() {
  local marker="$1" attempt
  for attempt in $(seq 1 400); do
    if sudo -n test -f "${marker}"; then
      return 0
    fi
    sleep 0.1
  done
  fail "terminal teardown marker did not appear"
}

assert_ordinary_network() {
  getent hosts "${PODLAZ_E2E_DNS_CHECK_HOST}" >/dev/null 2>&1 || fail "ordinary DNS unavailable after terminal teardown"
  curl -4 -fsS --max-time 30 "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL}" >/dev/null || fail "ordinary network unavailable after terminal teardown"
}

cleanup() {
  local code=$?
  remove_override
  sudo -n rm -rf -- "${HOOK_DIR}" >/dev/null 2>&1 || true
  if [[ "${CONNECTED}" == "true" ]] && [[ -x /usr/bin/podlaz ]]; then
    run_installed_podlaz disconnect >/dev/null 2>&1 || true
  fi
  if [[ "${FOREIGN_CREATED}" == "true" ]]; then
    sudo -n nft delete table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}" >/dev/null 2>&1 || true
  fi
  exit "${code}"
}
trap cleanup EXIT

setup_isolated_xdg "issue261-package-acceptance"
: >"${EVIDENCE}"
sudo -n systemctl start podlazd.service
wait_for_service
import_profile
create_foreign_collision_guard

run_installed_podlaz connect --mode tun "${PROFILE_ID}" >/dev/null 2>&1 || fail "issue 261 protected connect failed"
CONNECTED=true
wait_for_active_tun protected_connected
load_envelope_identity
assert_privacy_envelope_present
write_evidence privacy_envelope_armed
assert_foreign_collision_guard
prepare_direct_probe

install_restart_delay_override
systemctl kill --kill-who=main -s KILL podlazd.service
assert_privacy_envelope_present
assert_direct_uplink_blocked
assert_foreign_collision_guard
wait_for_active_tun daemon_crash_recovered_without_manual_repair
remove_override
load_envelope_identity
assert_privacy_envelope_present

install_terminal_pause_override
load_envelope_identity
prepare_direct_probe
disconnect_log="$(mktemp "${E2E_TMP_ROOT}/issue261-disconnect.XXXXXX")"
run_installed_podlaz disconnect >"${disconnect_log}" 2>&1 &
disconnect_pid=$!
wait_for_marker "${HOOK_DIR}/terminal-data-plane-clean.ready"
assert_privacy_envelope_present
assert_direct_uplink_blocked
assert_foreign_collision_guard
sudo -n touch "${HOOK_DIR}/terminal-data-plane-clean.continue"
set +e
wait "${disconnect_pid}"
disconnect_code=$?
set -e
rm -f -- "${disconnect_log}"
[[ "${disconnect_code}" == "0" ]] || fail "terminal disconnect failed with exit ${disconnect_code}"
CONNECTED=false
wait_for_inactive terminal_disconnected
if sudo -n test -e "${CONTINUATION_PATH}"; then
  fail "terminal teardown left Network Session authority"
fi
if sudo -n nft list table "${ENVELOPE_FAMILY}" "${ENVELOPE_TABLE}" >/dev/null 2>&1; then
  fail "terminal teardown left Privacy Envelope"
fi
sleep 3
status_matches inactive disabled || fail "terminal teardown unexpectedly auto-reconnected"
write_evidence terminal_no_auto_reconnect
assert_ordinary_network
write_evidence ordinary_network_after_terminal
assert_foreign_collision_guard
write_evidence foreign_collision_survived

sudo -n nft delete table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}"
FOREIGN_CREATED=false
remove_override
sudo -n rm -rf -- "${HOOK_DIR}"
log "issue 261 installed-package privacy-envelope acceptance passed"
