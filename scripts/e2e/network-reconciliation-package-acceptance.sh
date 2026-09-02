#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"
# shellcheck source=lib/evidence.sh
source "${SCRIPT_DIR}/lib/evidence.sh"
# shellcheck source=lib/installed_client.sh
source "${SCRIPT_DIR}/lib/installed_client.sh"
# shellcheck source=lib/profile_input.sh
source "${SCRIPT_DIR}/lib/profile_input.sh"
# shellcheck source=lib/readiness.sh
source "${SCRIPT_DIR}/lib/readiness.sh"
# shellcheck source=lib/status_polling.sh
source "${SCRIPT_DIR}/lib/status_polling.sh"

require_cmd awk curl getent ip mktemp nft nmcli python3 rtcwake runuser sleep sudo systemctl timeout

: "${PODLAZ_E2E_PROFILE_URI:=}"
: "${PODLAZ_E2E_PROFILE_URI_LIST:=}"
: "${PODLAZ_E2E_DNS_CHECK_HOST:=github.com}"
: "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL:=https://api.ipify.org}"
: "${PODLAZ_E2E_ALLOW_HOST_CHURN:=false}"

if [[ -z "${PODLAZ_E2E_PROFILE_URI}" && -z "${PODLAZ_E2E_PROFILE_URI_LIST}" ]]; then
  fail "PODLAZ_E2E_PROFILE_URI or PODLAZ_E2E_PROFILE_URI_LIST is required"
fi
if [[ "${PODLAZ_E2E_ALLOW_HOST_CHURN}" != "true" ]]; then
  fail "network-reconciliation acceptance requires PODLAZ_E2E_ALLOW_HOST_CHURN=true on the dedicated vpn-e2e runner"
fi

CONTINUATION_PATH="/run/podlaz/network-session-continuation.json"
TRANSACTION_DIR="/run/podlaz/transactions"
OVERRIDE_DIR="/etc/systemd/system/podlazd.service.d"
OVERRIDE_PATH="${OVERRIDE_DIR}/99-network-reconciliation-e2e.conf"
HOOK_DIR="/run/podlaz/network-reconciliation-e2e"
EVIDENCE="${E2E_ARTIFACT_DIR}/network-reconciliation-acceptance.txt"
FOREIGN_TUN="pz-e2e-recon0"
FOREIGN_TUN_CIDR="198.18.62.1/32"
FOREIGN_TABLE="51962"
FOREIGN_ROUTE_A="203.0.113.62/32"
FOREIGN_ROUTE_B="203.0.113.63/32"
PROFILE_ID=""
ENVELOPE_FAMILY=""
ENVELOPE_TABLE=""
CONNECTED=false
FOREIGN_CREATED=false
NM_CONNECTION=""
NM_DISCONNECTED=false

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
  append_evidence_pass "${EVIDENCE}" "$1"
}

status_is_verified_active() {
  local output
  output="$(mktemp "${E2E_TMP_ROOT}/network-reconciliation-status.XXXXXX")"
  run_installed_podlaz status --json >"${output}" 2>/dev/null || true
  if python3 - "${output}" <<'PY'
import json
import sys
try:
    with open(sys.argv[1], encoding="utf-8") as handle:
        payload = json.load(handle)
except Exception:
    raise SystemExit(1)
status = payload.get("status") or payload
health = status.get("tun_health") or {}
ok = (
    status.get("connection") == "active"
    and status.get("tun") == "active"
    and health.get("state") == "verified"
)
raise SystemExit(0 if ok else 1)
PY
  then
    rm -f -- "${output}"
    return 0
  fi
  rm -f -- "${output}"
  return 1
}

wait_for_verified_active() {
  local phase="$1"
  wait_for_service_active podlazd.service 40
  wait_for_status_match "${phase}" 120 status_is_verified_active
  write_evidence "${phase}"
}

status_is_inactive() {
  local output
  output="$(mktemp "${E2E_TMP_ROOT}/network-reconciliation-inactive.XXXXXX")"
  run_installed_podlaz status --json >"${output}" 2>/dev/null || true
  if python3 - "${output}" <<'PY'
import json
import sys
try:
    with open(sys.argv[1], encoding="utf-8") as handle:
        payload = json.load(handle)
except Exception:
    raise SystemExit(1)
status = payload.get("status") or payload
raise SystemExit(0 if status.get("connection") == "inactive" and status.get("tun") == "disabled" else 1)
PY
  then
    rm -f -- "${output}"
    return 0
  fi
  rm -f -- "${output}"
  return 1
}

wait_for_inactive() {
  local phase="$1"
  wait_for_status_match "${phase}" 80 status_is_inactive
  write_evidence "${phase}"
}

wait_for_marker() {
  local marker="$1" attempt
  for attempt in $(seq 1 800); do
    if sudo -n test -f "${HOOK_DIR}/${marker}"; then
      return 0
    fi
    sleep 0.1
  done
  fail "expected network-reconciliation E2E marker did not appear"
}

import_profile() {
  local uri out err
  uri="$(first_configured_profile_uri)"
  out="$(mktemp "${E2E_TMP_ROOT}/network-reconciliation-import.stdout.XXXXXX")"
  err="$(mktemp "${E2E_TMP_ROOT}/network-reconciliation-import.stderr.XXXXXX")"
  run_installed_podlaz profile import "${uri}" >"${out}" 2>"${err}" || fail "network-reconciliation profile import failed"
  PROFILE_ID="$(awk '/^Imported profile:/ {print $3}' "${out}")"
  [[ -n "${PROFILE_ID}" ]] || fail "network-reconciliation profile import returned no profile ID"
  mask_multiline_sensitive "${PROFILE_ID}"
  rm -f -- "${out}" "${err}"
}

install_reconciliation_override() {
  sudo -n mkdir -p "${OVERRIDE_DIR}"
  sudo -n rm -rf -- "${HOOK_DIR}"
  sudo -n mkdir -m 0700 "${HOOK_DIR}"
  sudo -n tee "${OVERRIDE_PATH}" >/dev/null <<EOF2
[Service]
Environment=PODLAZ_E2E_TUN_TERMINAL_FAILURE=true
Environment=PODLAZ_E2E_TUN_TERMINAL_FAILURE_DIR=${HOOK_DIR}
Environment=PODLAZ_E2E_TUN_RECONCILIATION_SOFT_FAILURE=true
Environment=PODLAZ_E2E_TUN_RECONCILIATION_RESOLVED_UNKNOWN=true
Environment=PODLAZ_E2E_TUN_RECONCILIATION_REBUILD_PAUSE=true
Environment=PODLAZ_E2E_TUN_HOOK_TIMEOUT_SECONDS=180
Environment=PODLAZ_E2E_PRIVACY_TEARDOWN_PAUSE=true
Environment=PODLAZ_E2E_PRIVACY_TEARDOWN_PAUSE_DIR=${HOOK_DIR}
Environment=PODLAZ_E2E_PRIVACY_TEARDOWN_PAUSE_TIMEOUT_SECONDS=180
EOF2
  sudo -n systemctl daemon-reload
  sudo -n systemctl restart podlazd.service
  wait_for_service_active podlazd.service 40
}

remove_override() {
  sudo -n rm -f -- "${OVERRIDE_PATH}"
  sudo -n systemctl daemon-reload >/dev/null 2>&1 || true
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
if protection.get("state") not in {"armed", "arming"}:
    raise SystemExit("privacy protection is not armed")
family = protection.get("family")
table = protection.get("table")
if family != "inet" or not re.fullmatch(r"podlaz_pe_[0-9a-f]{12}(?:_[1-9][0-9]{0,2})?", str(table or "")):
    raise SystemExit("invalid persisted privacy envelope identity")
print(f"{family} {table}")
PY
)" || fail "could not load exact Privacy Envelope authority"
  read -r ENVELOPE_FAMILY ENVELOPE_TABLE <<<"${identity}"
}

assert_privacy_envelope_present() {
  sudo -n nft list table "${ENVELOPE_FAMILY}" "${ENVELOPE_TABLE}" >/dev/null 2>&1 || fail "exact Privacy Envelope is not present"
}

create_foreign_routing_fixture() {
  ! sudo -n ip link show dev "${FOREIGN_TUN}" >/dev/null 2>&1 || fail "foreign TUN fixture already exists"
  sudo -n ip tuntap add dev "${FOREIGN_TUN}" mode tun
  sudo -n ip link set dev "${FOREIGN_TUN}" up
  sudo -n ip -4 address add "${FOREIGN_TUN_CIDR}" dev "${FOREIGN_TUN}"
  sudo -n ip -4 route add blackhole "${FOREIGN_ROUTE_A}" table "${FOREIGN_TABLE}"
  FOREIGN_CREATED=true
}

replace_foreign_route() {
  sudo -n ip -4 route replace blackhole "${FOREIGN_ROUTE_B}" table "${FOREIGN_TABLE}"
  sudo -n ip -4 route del blackhole "${FOREIGN_ROUTE_A}" table "${FOREIGN_TABLE}"
}

assert_foreign_routing_fixture() {
  sudo -n ip link show dev "${FOREIGN_TUN}" >/dev/null 2>&1 || fail "foreign TUN was removed"
  sudo -n ip -4 address show dev "${FOREIGN_TUN}" | grep -F "${FOREIGN_TUN_CIDR}" >/dev/null || fail "foreign TUN address changed"
  sudo -n ip -4 route show table "${FOREIGN_TABLE}" | grep -F "${FOREIGN_ROUTE_B%/32}" >/dev/null || fail "surrounding foreign route was removed"
}

cleanup_foreign_routing_fixture() {
  sudo -n ip -4 route flush table "${FOREIGN_TABLE}" >/dev/null 2>&1 || true
  sudo -n ip link del dev "${FOREIGN_TUN}" >/dev/null 2>&1 || true
  FOREIGN_CREATED=false
}

active_core_pid() {
  sudo -n python3 - "${TRANSACTION_DIR}" <<'PY'
import glob
import json
import os
import sys
candidates = []
for path in glob.glob(os.path.join(sys.argv[1], "*.json")):
    try:
        with open(path, encoding="utf-8") as handle:
            tx = json.load(handle)
    except Exception:
        continue
    if tx.get("owner") != "podlaz" or tx.get("state") != "committed":
        continue
    for child in (tx.get("rollback") or {}).get("child_processes") or []:
        if child.get("owner") == "podlaz" and child.get("label") == "xray" and int(child.get("pid") or 0) > 1:
            candidates.append((str(tx.get("updated_at") or ""), int(child["pid"])))
if not candidates:
    raise SystemExit("no committed Podlaz Xray child process found")
print(sorted(candidates)[-1][1])
PY
}

assert_ordinary_network() {
  getent hosts "${PODLAZ_E2E_DNS_CHECK_HOST}" >/dev/null 2>&1 || fail "ordinary DNS unavailable after terminal teardown"
  curl -4 -fsS --max-time 30 "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL}" >/dev/null || fail "ordinary network unavailable after terminal teardown"
}

exercise_suspend_resume() {
  assert_privacy_envelope_present
  sudo -n rtcwake -m mem -s 8 >/dev/null 2>&1 || fail "dedicated vpn-e2e runner could not complete RTC suspend/resume"
  wait_for_verified_active suspend_resume_recovered
  assert_privacy_envelope_present
}

exercise_networkmanager_dhcp_churn() {
  local uplink
  uplink="$(ip -4 route show table main default | awk 'NR==1 {for (i=1; i<=NF; i++) if ($i=="dev") {print $(i+1); exit}}')"
  [[ -n "${uplink}" && "${uplink}" != "podlaz0" ]] || fail "could not identify NetworkManager uplink for DHCP churn"
  NM_CONNECTION="$(nmcli -g GENERAL.CONNECTION device show "${uplink}" 2>/dev/null | head -n1)"
  [[ -n "${NM_CONNECTION}" && "${NM_CONNECTION}" != "--" ]] || fail "uplink has no active NetworkManager connection"
  sudo -n nmcli connection down "${NM_CONNECTION}" >/dev/null 2>&1 || fail "could not bring down controlled NetworkManager connection"
  NM_DISCONNECTED=true
  sleep 2
  sudo -n nmcli connection up "${NM_CONNECTION}" >/dev/null 2>&1 || fail "could not restore controlled NetworkManager connection"
  NM_DISCONNECTED=false
  wait_for_verified_active dhcp_churn_recovered
  assert_privacy_envelope_present
}

cleanup() {
  local code=$?
  if [[ "${NM_DISCONNECTED}" == "true" && -n "${NM_CONNECTION}" ]]; then
    sudo -n nmcli connection up "${NM_CONNECTION}" >/dev/null 2>&1 || true
  fi
  remove_override
  sudo -n rm -rf -- "${HOOK_DIR}" >/dev/null 2>&1 || true
  if [[ "${CONNECTED}" == "true" ]] && [[ -x /usr/bin/podlaz ]]; then
    run_installed_podlaz disconnect >/dev/null 2>&1 || true
  fi
  if [[ "${FOREIGN_CREATED}" == "true" ]]; then
    cleanup_foreign_routing_fixture
  fi
  exit "${code}"
}
trap cleanup EXIT

setup_isolated_xdg "network-reconciliation-package-acceptance"
: >"${EVIDENCE}"
install_reconciliation_override
import_profile
run_installed_podlaz connect --mode tun "${PROFILE_ID}" >/dev/null 2>&1 || fail "network-reconciliation protected connect failed"
CONNECTED=true
wait_for_verified_active protected_connected
load_envelope_identity
assert_privacy_envelope_present

sudo -n touch "${HOOK_DIR}/reconciliation-soft-provider.trigger"
wait_for_marker reconciliation-soft-provider.injected
wait_for_verified_active soft_provider_failure_stayed_connected

sudo -n touch "${HOOK_DIR}/reconciliation-resolved-unknown.trigger"
wait_for_marker reconciliation-resolved-unknown.injected
wait_for_verified_active resolved_convergence_recovered

create_foreign_routing_fixture
replace_foreign_route
wait_for_verified_active route_replacement_recovered
assert_foreign_routing_fixture
write_evidence surrounding_tun_preserved

exercise_suspend_resume
exercise_networkmanager_dhcp_churn
assert_foreign_routing_fixture

OLD_CORE_PID="$(active_core_pid)"
sudo -n kill -KILL "${OLD_CORE_PID}"
wait_for_marker reconciliation-rebuild.ready
assert_privacy_envelope_present
assert_foreign_routing_fixture
write_evidence privacy_envelope_present_during_rebuild
sudo -n touch "${HOOK_DIR}/reconciliation-rebuild.continue"
wait_for_verified_active core_exit_degraded_source_rebuilt
NEW_CORE_PID="$(active_core_pid)"
[[ "${NEW_CORE_PID}" != "${OLD_CORE_PID}" ]] || fail "degraded rebuild did not publish a fresh supervised Xray generation"
assert_privacy_envelope_present
assert_foreign_routing_fixture

sudo -n touch "${HOOK_DIR}/terminal-failure.trigger"
wait_for_marker terminal-data-plane-clean.ready
assert_privacy_envelope_present
assert_foreign_routing_fixture
sudo -n touch "${HOOK_DIR}/terminal-data-plane-clean.continue"
CONNECTED=false
wait_for_inactive terminal_failure_bounded
if sudo -n test -e "${CONTINUATION_PATH}"; then
  fail "terminal reconciliation left Network Session authority"
fi
if sudo -n nft list table "${ENVELOPE_FAMILY}" "${ENVELOPE_TABLE}" >/dev/null 2>&1; then
  fail "terminal reconciliation left Privacy Envelope"
fi
sleep 3
status_is_inactive || fail "terminal reconciliation unexpectedly auto-reconnected"
write_evidence terminal_no_auto_reconnect
assert_ordinary_network
write_evidence ordinary_network_after_terminal
assert_foreign_routing_fixture
write_evidence surrounding_tun_preserved_after_terminal

cleanup_foreign_routing_fixture
remove_override
sudo -n rm -rf -- "${HOOK_DIR}"
log "network-reconciliation evidence-driven installed-package acceptance passed"
