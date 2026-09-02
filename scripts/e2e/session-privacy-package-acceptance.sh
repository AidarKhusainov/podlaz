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

require_cmd awk curl getent grep ip mktemp nft python3 resolvectl runuser sed sudo systemctl

: "${PODLAZ_E2E_PROFILE_URI:=}"
: "${PODLAZ_E2E_PROFILE_URI_LIST:=}"
: "${PODLAZ_E2E_DNS_CHECK_HOST:=github.com}"
: "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL:=https://api.ipify.org}"

if [[ -z "${PODLAZ_E2E_PROFILE_URI}" && -z "${PODLAZ_E2E_PROFILE_URI_LIST}" ]]; then
  fail "PODLAZ_E2E_PROFILE_URI or PODLAZ_E2E_PROFILE_URI_LIST is required"
fi

DAEMON_SOCKET="/run/podlaz/podlazd.sock"
TRANSACTION_DIR="/run/podlaz/transactions"
FOREIGN_TUN="podlaz-e2e-foreign0"
FOREIGN_TUN_CIDR="198.18.0.1/32"
FOREIGN_TABLE="51820"
FOREIGN_ROUTE="198.51.100.254/32"
FOREIGN_RULE_TARGET_A="198.51.100.254/32"
FOREIGN_RULE_TARGET_B="198.51.100.253/32"
FOREIGN_RULE_PRIORITY_A="9999"
FOREIGN_RULE_PRIORITY_B="10000"
FOREIGN_NFT_FAMILY="inet"
FOREIGN_NFT_TABLE="podlaz_session_privacy_foreign"
FOREIGN_DNS_LINK="pz-e2e-privacy0"
FOREIGN_DNS_SERVER="192.0.2.53"
FOREIGN_DNS_DOMAIN="~session-privacy.invalid"
EVIDENCE="${E2E_ARTIFACT_DIR}/session-privacy-acceptance.txt"
PROFILE_ID=""
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
  append_evidence_pass "${EVIDENCE}" "$1"
}

cleanup_foreign_fixture() {
  sudo -n resolvectl revert "${FOREIGN_DNS_LINK}" >/dev/null 2>&1 || true
  sudo -n ip link del dev "${FOREIGN_DNS_LINK}" >/dev/null 2>&1 || true
  sudo -n nft delete table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}" >/dev/null 2>&1 || true
  sudo -n ip -4 rule del priority "${FOREIGN_RULE_PRIORITY_B}" to "${FOREIGN_RULE_TARGET_B}" lookup "${FOREIGN_TABLE}" >/dev/null 2>&1 || true
  sudo -n ip -4 rule del priority "${FOREIGN_RULE_PRIORITY_A}" to "${FOREIGN_RULE_TARGET_A}" lookup "${FOREIGN_TABLE}" >/dev/null 2>&1 || true
  sudo -n ip -4 route del blackhole "${FOREIGN_ROUTE}" table "${FOREIGN_TABLE}" >/dev/null 2>&1 || true
  sudo -n ip link del dev "${FOREIGN_TUN}" >/dev/null 2>&1 || true
  FOREIGN_CREATED=false
}

cleanup() {
  local code=$?
  if [[ "${CONNECTED}" == "true" ]]; then
    run_installed_podlaz disconnect >/dev/null 2>&1 || true
  fi
  if [[ "${FOREIGN_CREATED}" == "true" ]]; then
    cleanup_foreign_fixture
  fi
  exit "${code}"
}
trap cleanup EXIT

assert_fixture_absent() {
  ! sudo -n ip link show dev "${FOREIGN_TUN}" >/dev/null 2>&1 || fail "session-privacy foreign TUN fixture already exists"
  ! sudo -n ip link show dev "${FOREIGN_DNS_LINK}" >/dev/null 2>&1 || fail "session-privacy foreign DNS fixture already exists"
  ! sudo -n nft list table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}" >/dev/null 2>&1 || fail "session-privacy foreign nft fixture already exists"
  ! sudo -n ip -4 rule show priority "${FOREIGN_RULE_PRIORITY_A}" | grep -q . || fail "session-privacy priority ${FOREIGN_RULE_PRIORITY_A} is already occupied"
  ! sudo -n ip -4 rule show priority "${FOREIGN_RULE_PRIORITY_B}" | grep -q . || fail "session-privacy priority ${FOREIGN_RULE_PRIORITY_B} is already occupied"
  ! sudo -n ip -4 route show table "${FOREIGN_TABLE}" | grep -q . || fail "session-privacy table ${FOREIGN_TABLE} is already occupied"
}

create_foreign_fixture() {
  assert_fixture_absent
  sudo -n ip tuntap add dev "${FOREIGN_TUN}" mode tun
  sudo -n ip link set dev "${FOREIGN_TUN}" up
  sudo -n ip -4 address add "${FOREIGN_TUN_CIDR}" dev "${FOREIGN_TUN}"
  sudo -n ip -4 route add blackhole "${FOREIGN_ROUTE}" table "${FOREIGN_TABLE}"
  sudo -n ip -4 rule add priority "${FOREIGN_RULE_PRIORITY_A}" to "${FOREIGN_RULE_TARGET_A}" lookup "${FOREIGN_TABLE}"
  sudo -n ip -4 rule add priority "${FOREIGN_RULE_PRIORITY_B}" to "${FOREIGN_RULE_TARGET_B}" lookup "${FOREIGN_TABLE}"
  sudo -n nft add table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}"
  sudo -n ip link add "${FOREIGN_DNS_LINK}" type dummy
  sudo -n ip link set dev "${FOREIGN_DNS_LINK}" up
  sudo -n resolvectl dns "${FOREIGN_DNS_LINK}" "${FOREIGN_DNS_SERVER}"
  sudo -n resolvectl domain "${FOREIGN_DNS_LINK}" "${FOREIGN_DNS_DOMAIN}"
  sudo -n resolvectl default-route "${FOREIGN_DNS_LINK}" no
  FOREIGN_CREATED=true
}

assert_foreign_fixture() {
  local phase="$1" resolved
  sudo -n ip link show dev "${FOREIGN_TUN}" >/dev/null 2>&1 || fail "${phase}: foreign TUN was removed"
  sudo -n ip -4 address show dev "${FOREIGN_TUN}" | grep -F "${FOREIGN_TUN_CIDR}" >/dev/null || fail "${phase}: foreign TUN address changed"
  sudo -n ip -4 route show table "${FOREIGN_TABLE}" exact "${FOREIGN_ROUTE}" | grep -F "blackhole ${FOREIGN_ROUTE%/32}" >/dev/null || fail "${phase}: foreign routing table changed"
  sudo -n ip -4 rule show priority "${FOREIGN_RULE_PRIORITY_A}" | grep -F "to ${FOREIGN_RULE_TARGET_A%/32}" | grep -F "lookup ${FOREIGN_TABLE}" >/dev/null || fail "${phase}: foreign priority ${FOREIGN_RULE_PRIORITY_A} changed"
  sudo -n ip -4 rule show priority "${FOREIGN_RULE_PRIORITY_B}" | grep -F "to ${FOREIGN_RULE_TARGET_B%/32}" | grep -F "lookup ${FOREIGN_TABLE}" >/dev/null || fail "${phase}: foreign priority ${FOREIGN_RULE_PRIORITY_B} changed"
  sudo -n nft list table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}" >/dev/null 2>&1 || fail "${phase}: foreign nftables state changed"
  resolved="$(mktemp "${E2E_TMP_ROOT}/session-privacy-resolved.XXXXXX")"
  sudo -n resolvectl status "${FOREIGN_DNS_LINK}" --no-pager >"${resolved}"
  grep -F "${FOREIGN_DNS_SERVER}" "${resolved}" >/dev/null || fail "${phase}: foreign DNS server changed"
  grep -F "${FOREIGN_DNS_DOMAIN}" "${resolved}" >/dev/null || fail "${phase}: foreign DNS domain changed"
  rm -f -- "${resolved}"
  write_evidence "foreign_baseline_${phase}"
}

import_profile() {
  local out err uri
  uri="$(first_configured_profile_uri)"
  out="$(mktemp "${E2E_TMP_ROOT}/session-privacy-import.stdout.XXXXXX")"
  err="$(mktemp "${E2E_TMP_ROOT}/session-privacy-import.stderr.XXXXXX")"
  run_installed_podlaz profile import "${uri}" >"${out}" 2>"${err}" || fail "session-privacy profile import failed"
  PROFILE_ID="$(awk '/^Imported profile:/ {print $3}' "${out}")"
  [[ -n "${PROFILE_ID}" ]] || fail "session-privacy profile import returned no profile ID"
  mask_multiline_sensitive "${PROFILE_ID}"
  rm -f -- "${out}" "${err}"
}

assert_dynamic_transaction_allocation() {
  sudo -n python3 - "${TRANSACTION_DIR}" <<'PY'
import glob
import json
import os
import sys

paths = glob.glob(os.path.join(sys.argv[1], "*.json"))
if len(paths) != 1:
    raise SystemExit(f"expected one active transaction, found {len(paths)}")
with open(paths[0], encoding="utf-8") as handle:
    tx = json.load(handle)
if tx.get("state") != "committed":
    raise SystemExit(f"transaction is not committed: {tx.get('state')!r}")
desired = tx.get("desired_plan") or {}
address = (desired.get("tun_address") or {}).get("cidr")
if not address or address == "198.18.0.1/32":
    raise SystemExit(f"TUN address was not collision-reallocated: {address!r}")
routes = desired.get("routes") or []
tables = {
    str(route.get("table"))
    for route in routes
    if route.get("cidr") in ("default", "0.0.0.0/0") and route.get("dev") == "podlaz0"
}
if len(tables) != 1 or "51820" in tables:
    raise SystemExit(f"routing table was not collision-reallocated: {sorted(tables)!r}")
priorities = []
for step in desired.get("steps") or []:
    if step.get("kind") != "policy-rule":
        continue
    fields = str(step.get("target") or "").split()
    if len(fields) >= 2 and fields[0] == "priority":
        priorities.append(int(fields[1]))
if len(priorities) != 2 or any(value in (9999, 10000) for value in priorities):
    raise SystemExit(f"policy priorities were not collision-reallocated: {priorities!r}")
if not priorities[0] < priorities[1]:
    raise SystemExit(f"server bypass does not precede tunnel rule: {priorities!r}")
PY
  write_evidence collision_free_allocation
}

assert_active_data_plane() {
  local status public_ip
  status="$(mktemp "${E2E_TMP_ROOT}/session-privacy-status.XXXXXX")"
  run_installed_podlaz status --json >"${status}" 2>/dev/null || fail "session-privacy status failed while connected"
  python3 - "${status}" <<'PY'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as handle:
    payload = json.load(handle)
status = payload.get("status") or payload
if status.get("connection") != "active" or status.get("tun") != "active":
    raise SystemExit(f"unexpected active status: {status!r}")
PY
  rm -f -- "${status}"
  sudo -n resolvectl --cache=no --interface=podlaz0 -4 query "${PODLAZ_E2E_DNS_CHECK_HOST}" >/dev/null 2>&1 || fail "session-privacy protected DNS path failed"
  public_ip="$(curl -4 -fsS --max-time 30 "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL}")"
  [[ -n "${public_ip}" ]] || fail "session-privacy protected IPv4 data plane returned an empty response"
  mask_multiline_sensitive "${public_ip}"
  write_evidence protected_data_plane
}

setup_isolated_xdg "session-privacy-package-acceptance"
: >"${EVIDENCE}"
sudo -n systemctl start podlazd.service >/dev/null
wait_for_daemon_ready "${DAEMON_SOCKET}" podlazd.service 15
import_profile
create_foreign_fixture
assert_foreign_fixture before_connect

run_installed_podlaz connect --mode tun "${PROFILE_ID}" >/dev/null 2>&1 || fail "session-privacy coexistence connect failed"
CONNECTED=true
assert_dynamic_transaction_allocation
assert_active_data_plane
assert_foreign_fixture while_connected

run_installed_podlaz disconnect >/dev/null 2>&1 || fail "session-privacy coexistence disconnect failed"
CONNECTED=false
assert_foreign_fixture after_disconnect

cleanup_foreign_fixture
assert_fixture_absent
write_evidence foreign_fixture_cleanup
log "session-privacy installed-package coexistence acceptance passed"
