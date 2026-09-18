#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"

BASE_SCENARIO="${SCRIPT_DIR}/hosted-synthetic-tun.sh"
MACHINE="podlaz-synthetic-tun"
GUEST_IF="host0"
UPLINK_CONNECTION="synthetic-uplink"
FOREIGN_TUN="pzforeign0"
FOREIGN_TUN_CIDR="198.18.0.1/32"
FOREIGN_TABLE="51820"
FOREIGN_ROUTE="198.51.100.254/32"
FOREIGN_RULE_TARGET_A="198.51.100.254/32"
FOREIGN_RULE_TARGET_B="198.51.100.253/32"
FOREIGN_RULE_PRIORITY_A="9999"
FOREIGN_RULE_PRIORITY_B="10000"
FOREIGN_NFT_FAMILY="inet"
FOREIGN_NFT_TABLE="pzforeign_state"
FOREIGN_DNS_LINK="pzforeign-dns0"
FOREIGN_DNS_SERVER="192.0.2.53"
FOREIGN_DNS_DOMAIN="~foreign.invalid"
FOREIGN_NM_CONNECTION="pzforeign-nm"
FOREIGN_NM_LINK="pzforeign-nm0"
FOREIGN_SERVICE="pzforeign-sentinel.service"
TRANSACTION_DIR="/run/podlaz/transactions"
DAEMON_SOCKET="/run/podlaz/podlazd.sock"
STATUS_HELPER="/workspace/scripts/e2e/lib/daemon_status_semantics.py"
GUEST_PRIVATE="/tmp/podlaz-hosted-foreign-state-safety"
EXPECTED_COMMIT="${PODLAZ_E2E_CANDIDATE_COMMIT:-}"

REPORT="${E2E_ARTIFACT_DIR}/hosted-foreign-state-safety.txt"
PRIVATE_ROOT="${E2E_TMP_ROOT}/hosted-foreign-state-safety-private"
BASE_TMP_ROOT="${PRIVATE_ROOT}/base-private"
BASE_ARTIFACT_DIR="${PRIVATE_ROOT}/base-public"
BASE_REPORT="${BASE_ARTIFACT_DIR}/hosted-synthetic-tun.txt"
BASE_STDOUT="${PRIVATE_ROOT}/base.stdout"
BASE_STDERR="${PRIVATE_ROOT}/base.stderr"
CONTROL_DIR="${BASE_TMP_ROOT}/control"
BASE_PID=""
FAILURE_CLASS=diagnostic_unknown
FAILURE_STEP=bootstrap
REPORT_FINALIZED=false
FOREIGN_CREATED=false
UPLINK_DOWN=false
FOREIGN_TUN_INDEX=""
FOREIGN_NM_UUID=""

EVIDENCE_KEYS=(
  candidate.provenance
  foreign.fixture_complete
  allocation.disjoint
  foreign.connect_preserved
  foreign.churn_preserved
  foreign.cleanup_preserved
  foreign.recovery_preserved
  base.baseline_restored
  base.outer_cleanup
  artifact.privacy
)

record_evidence() {
  printf '%s=%s\n' "$1" "$2" >>"${REPORT}"
}

recorded() {
  grep -Eq "^${1}=" "${REPORT}" 2>/dev/null
}

record_if_missing() {
  recorded "$1" || record_evidence "$1" "$2"
}

mark_failure() {
  FAILURE_CLASS="$1"
  FAILURE_STEP="${2//[^A-Za-z0-9_.-]/_}"
}

finalize_report() {
  local key
  [[ "${REPORT_FINALIZED}" == false ]] || return 0
  for key in "${EVIDENCE_KEYS[@]}"; do
    record_if_missing "${key}" fail
  done
  printf 'failure.class=%s\nfailure.step=%s\n' "${FAILURE_CLASS}" "${FAILURE_STEP}" >>"${REPORT}"
  REPORT_FINALIZED=true
}

validate_report() {
  [[ -f "${REPORT}" && ! -L "${REPORT}" ]] || fail "hosted foreign-state report is missing"
  python3 - "${REPORT}" <<'PY'
import sys
required={
"candidate.provenance","foreign.fixture_complete","allocation.disjoint",
"foreign.connect_preserved","foreign.churn_preserved","foreign.cleanup_preserved",
"foreign.recovery_preserved","base.baseline_restored","base.outer_cleanup","artifact.privacy",
}
values={}
with open(sys.argv[1],encoding="utf-8") as handle:
    for raw in handle:
        line=raw.rstrip("\n")
        if not line or "=" not in line:
            raise SystemExit(f"invalid report line: {line!r}")
        key,value=line.split("=",1)
        if key in values:
            raise SystemExit(f"duplicate report key: {key}")
        values[key]=value
if set(values) != required | {"failure.class","failure.step"}:
    raise SystemExit("unexpected hosted foreign-state report schema")
for key in required:
    if values[key] != "pass":
        raise SystemExit(f"required evidence is not pass: {key}={values[key]}")
if values["failure.class"] != "none" or values["failure.step"] != "none":
    raise SystemExit("hosted foreign-state scenario reports a failure")
PY
  grep -Fx 'foreign.connect_preserved=pass' "${REPORT}" >/dev/null
  grep -Fx 'foreign.churn_preserved=pass' "${REPORT}" >/dev/null
  grep -Fx 'foreign.cleanup_preserved=pass' "${REPORT}" >/dev/null
  grep -Fx 'foreign.recovery_preserved=pass' "${REPORT}" >/dev/null
  grep -Fx 'allocation.disjoint=pass' "${REPORT}" >/dev/null
}

assert_public_artifact_privacy() {
  local extra
  extra="$(find "${E2E_ARTIFACT_DIR}" -mindepth 1 -maxdepth 1 ! -name 'hosted-foreign-state-safety.txt' -print -quit)"
  [[ -z "${extra}" && -f "${REPORT}" && ! -L "${REPORT}" ]] || return 1
  ! grep -Eiq 'vless://|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|172[.]31[.](253|254)[.]|session[_-]?id=|transaction[_-]?id=' "${REPORT}"
}

guest_exec() {
  sudo -n systemd-run --machine="${MACHINE}" --expand-environment=no --wait --pipe --collect --quiet -- "$@"
}

release_all_controls() {
  local phase continue
  [[ -d "${CONTROL_DIR}" ]] || return 0
  for phase in guest-ready verified-active terminal-clean recovery-clean; do
    continue="${CONTROL_DIR}/${phase}.continue"
    [[ -e "${continue}" ]] || printf 'continue\n' >"${continue}"
    chmod 0600 "${continue}" >/dev/null 2>&1 || true
  done
}

restore_uplink_if_needed() {
  [[ "${UPLINK_DOWN}" == true ]] || return 0
  guest_exec nmcli connection up "${UPLINK_CONNECTION}" >/dev/null 2>&1 || return 1
  UPLINK_DOWN=false
}

cleanup_foreign_fixture() {
  [[ "${FOREIGN_CREATED}" == true ]] || return 0
  guest_exec /bin/bash -lc "
    if systemctl is-active --quiet '${FOREIGN_SERVICE}'; then systemctl stop '${FOREIGN_SERVICE}'; fi
    systemctl reset-failed '${FOREIGN_SERVICE}' >/dev/null 2>&1 || true
    uuid=\$(nmcli -g UUID connection show '${FOREIGN_NM_CONNECTION}' 2>/dev/null || true)
    if [[ -n \"\$uuid\" && \"\$uuid\" == '${FOREIGN_NM_UUID}' ]]; then nmcli connection delete '${FOREIGN_NM_CONNECTION}' >/dev/null; fi
    resolvectl revert '${FOREIGN_DNS_LINK}' >/dev/null 2>&1 || true
    ip link del dev '${FOREIGN_DNS_LINK}' >/dev/null 2>&1 || true
    nft delete table '${FOREIGN_NFT_FAMILY}' '${FOREIGN_NFT_TABLE}' >/dev/null 2>&1 || true
    ip -4 rule del priority '${FOREIGN_RULE_PRIORITY_B}' to '${FOREIGN_RULE_TARGET_B}' lookup '${FOREIGN_TABLE}' >/dev/null 2>&1 || true
    ip -4 rule del priority '${FOREIGN_RULE_PRIORITY_A}' to '${FOREIGN_RULE_TARGET_A}' lookup '${FOREIGN_TABLE}' >/dev/null 2>&1 || true
    ip -4 route del blackhole '${FOREIGN_ROUTE}' table '${FOREIGN_TABLE}' >/dev/null 2>&1 || true
    idx=\$(ip -o link show dev '${FOREIGN_TUN}' 2>/dev/null | awk -F: 'NR==1 {gsub(/[[:space:]]/,\"\",\$1); print \$1}')
    if [[ -n \"\$idx\" && \"\$idx\" == '${FOREIGN_TUN_INDEX}' ]]; then ip link del dev '${FOREIGN_TUN}'; fi
  " >/dev/null 2>&1 || true
  FOREIGN_CREATED=false
}

cleanup() {
  local code=$? attempt
  trap - EXIT INT TERM
  set +e
  restore_uplink_if_needed || true
  cleanup_foreign_fixture
  release_all_controls
  if [[ -n "${BASE_PID}" ]] && kill -0 "${BASE_PID}" >/dev/null 2>&1; then
    for attempt in $(seq 1 480); do
      kill -0 "${BASE_PID}" >/dev/null 2>&1 || break
      sleep 0.5
    done
    if kill -0 "${BASE_PID}" >/dev/null 2>&1; then
      kill -TERM "${BASE_PID}" >/dev/null 2>&1 || true
    fi
    wait "${BASE_PID}" >/dev/null 2>&1 || true
  fi
  if [[ -f "${REPORT}" ]]; then
    assert_public_artifact_privacy && record_if_missing artifact.privacy pass
    finalize_report
  fi
  exit "${code}"
}

wait_for_control_ready() {
  local phase="$1" ready="${CONTROL_DIR}/$1.ready" attempt code
  for attempt in $(seq 1 6000); do
    [[ -f "${ready}" && ! -L "${ready}" ]] && return 0
    if [[ -z "${BASE_PID}" ]] || ! kill -0 "${BASE_PID}" >/dev/null 2>&1; then
      if [[ -n "${BASE_PID}" ]]; then
        set +e; wait "${BASE_PID}"; code=$?; set -e
        BASE_PID=""
      fi
      return 1
    fi
    sleep 0.1
  done
  return 1
}

release_control() {
  local phase="$1" ready="${CONTROL_DIR}/$1.ready" continue="${CONTROL_DIR}/$1.continue" attempt
  [[ -f "${ready}" && ! -L "${ready}" && ! -e "${continue}" ]] || return 1
  printf 'continue\n' >"${continue}"
  chmod 0600 "${continue}"
  for attempt in $(seq 1 100); do
    [[ ! -e "${ready}" ]] && return 0
    sleep 0.05
  done
  return 1
}

run_base_scenario() {
  local candidate="$1"
  rm -rf "${PRIVATE_ROOT}"
  install -d -m 0700 "${PRIVATE_ROOT}" "${BASE_TMP_ROOT}" "${BASE_ARTIFACT_DIR}" "${CONTROL_DIR}"
  env \
    E2E_TMP_ROOT="${BASE_TMP_ROOT}" \
    E2E_ARTIFACT_DIR="${BASE_ARTIFACT_DIR}" \
    PODLAZ_E2E_CANDIDATE_COMMIT="${EXPECTED_COMMIT}" \
    PODLAZ_E2E_HOSTED_CONTROL_DIR="${CONTROL_DIR}" \
    PODLAZ_E2E_HOSTED_CONTROL_PHASES="guest-ready verified-active terminal-clean recovery-clean" \
    PODLAZ_E2E_HOSTED_CONTROL_TIMEOUT_SECONDS=180 \
    bash "${BASE_SCENARIO}" "${candidate}" >"${BASE_STDOUT}" 2>"${BASE_STDERR}" &
  BASE_PID=$!
}

wait_base_completion() {
  local code
  [[ -n "${BASE_PID}" ]] || return 1
  set +e; wait "${BASE_PID}"; code=$?; set -e
  BASE_PID=""
  (( code == 0 ))
}

capture_status() {
  guest_exec install -d -m 0700 "${GUEST_PRIVATE}" >/dev/null
  guest_exec /bin/bash -lc "curl --fail --silent --show-error --max-time 3 --unix-socket '${DAEMON_SOCKET}' http://localhost/v1/status >'${GUEST_PRIVATE}/status.json'"
}

wait_for_verified_active() {
  local attempt
  for attempt in $(seq 1 240); do
    if capture_status >/dev/null 2>&1 &&
       guest_exec python3 "${STATUS_HELPER}" verified-active "${GUEST_PRIVATE}/status.json" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.5
  done
  return 1
}

wait_for_uplink_active() {
  local attempt
  for attempt in $(seq 1 120); do
    if guest_exec nmcli -t -f NAME,DEVICE connection show --active | grep -Fx "${UPLINK_CONNECTION}:${GUEST_IF}" >/dev/null; then
      return 0
    fi
    sleep 0.25
  done
  return 1
}

create_foreign_fixture() {
  guest_exec test ! -e "${GUEST_PRIVATE}"
  guest_exec install -d -m 0700 "${GUEST_PRIVATE}"
  guest_exec /bin/bash -lc "
    ! ip link show dev '${FOREIGN_TUN}' >/dev/null 2>&1
    ! ip link show dev '${FOREIGN_DNS_LINK}' >/dev/null 2>&1
    ! ip link show dev '${FOREIGN_NM_LINK}' >/dev/null 2>&1
    ! nft list table '${FOREIGN_NFT_FAMILY}' '${FOREIGN_NFT_TABLE}' >/dev/null 2>&1
    ! ip -4 route show table '${FOREIGN_TABLE}' | grep -q .
    ! ip -4 rule show priority '${FOREIGN_RULE_PRIORITY_A}' | grep -q .
    ! ip -4 rule show priority '${FOREIGN_RULE_PRIORITY_B}' | grep -q .
    ! nmcli connection show '${FOREIGN_NM_CONNECTION}' >/dev/null 2>&1
    ! systemctl is-active --quiet '${FOREIGN_SERVICE}'
    ip tuntap add dev '${FOREIGN_TUN}' mode tun
    ip link set dev '${FOREIGN_TUN}' up
    ip -4 address add '${FOREIGN_TUN_CIDR}' dev '${FOREIGN_TUN}'
    ip -4 route add blackhole '${FOREIGN_ROUTE}' table '${FOREIGN_TABLE}'
    ip -4 rule add priority '${FOREIGN_RULE_PRIORITY_A}' to '${FOREIGN_RULE_TARGET_A}' lookup '${FOREIGN_TABLE}'
    ip -4 rule add priority '${FOREIGN_RULE_PRIORITY_B}' to '${FOREIGN_RULE_TARGET_B}' lookup '${FOREIGN_TABLE}'
    nft add table '${FOREIGN_NFT_FAMILY}' '${FOREIGN_NFT_TABLE}'
    ip link add '${FOREIGN_DNS_LINK}' type dummy
    ip link set dev '${FOREIGN_DNS_LINK}' up
    resolvectl dns '${FOREIGN_DNS_LINK}' '${FOREIGN_DNS_SERVER}'
    resolvectl domain '${FOREIGN_DNS_LINK}' '${FOREIGN_DNS_DOMAIN}'
    resolvectl default-route '${FOREIGN_DNS_LINK}' no
    nmcli connection add type dummy ifname '${FOREIGN_NM_LINK}' con-name '${FOREIGN_NM_CONNECTION}' ipv4.method disabled ipv6.method disabled connection.autoconnect no >/dev/null
    nmcli connection up '${FOREIGN_NM_CONNECTION}' >/dev/null
    systemd-run --unit='${FOREIGN_SERVICE}' --property=Type=simple --property=Restart=no /usr/bin/sleep infinity >/dev/null
  "
  FOREIGN_TUN_INDEX="$(guest_exec ip -o link show dev "${FOREIGN_TUN}" | awk -F: 'NR==1 {gsub(/[[:space:]]/,"",$1); print $1}')"
  FOREIGN_NM_UUID="$(guest_exec nmcli -g UUID connection show "${FOREIGN_NM_CONNECTION}" | tr -d '[:space:]')"
  [[ "${FOREIGN_TUN_INDEX}" =~ ^[1-9][0-9]*$ && -n "${FOREIGN_NM_UUID}" ]]
  FOREIGN_CREATED=true
}

assert_foreign_fixture() {
  guest_exec ip link show dev "${FOREIGN_TUN}" >/dev/null
  guest_exec ip -4 address show dev "${FOREIGN_TUN}" | grep -F "${FOREIGN_TUN_CIDR}" >/dev/null
  guest_exec ip -4 route show table "${FOREIGN_TABLE}" exact "${FOREIGN_ROUTE}" | grep -F "blackhole ${FOREIGN_ROUTE%/32}" >/dev/null
  guest_exec ip -4 rule show priority "${FOREIGN_RULE_PRIORITY_A}" | grep -F "to ${FOREIGN_RULE_TARGET_A%/32}" | grep -F "lookup ${FOREIGN_TABLE}" >/dev/null
  guest_exec ip -4 rule show priority "${FOREIGN_RULE_PRIORITY_B}" | grep -F "to ${FOREIGN_RULE_TARGET_B%/32}" | grep -F "lookup ${FOREIGN_TABLE}" >/dev/null
  guest_exec nft list table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}" >/dev/null
  guest_exec resolvectl status "${FOREIGN_DNS_LINK}" --no-pager | grep -F "${FOREIGN_DNS_SERVER}" >/dev/null
  guest_exec resolvectl status "${FOREIGN_DNS_LINK}" --no-pager | grep -F "${FOREIGN_DNS_DOMAIN}" >/dev/null
  guest_exec systemctl is-active --quiet "${FOREIGN_SERVICE}"
  guest_exec nmcli -t -f NAME,DEVICE connection show --active | grep -Fx "${FOREIGN_NM_CONNECTION}:${FOREIGN_NM_LINK}" >/dev/null
  [[ "$(guest_exec nmcli -g UUID connection show "${FOREIGN_NM_CONNECTION}" | tr -d '[:space:]')" == "${FOREIGN_NM_UUID}" ]]
  [[ "$(guest_exec ip -o link show dev "${FOREIGN_TUN}" | awk -F: 'NR==1 {gsub(/[[:space:]]/,"",$1); print $1}')" == "${FOREIGN_TUN_INDEX}" ]]
}

assert_collision_free_allocation() {
  guest_exec python3 - "${TRANSACTION_DIR}" "${FOREIGN_TUN_CIDR}" "${FOREIGN_TABLE}" "${FOREIGN_RULE_PRIORITY_A}" "${FOREIGN_RULE_PRIORITY_B}" <<'PY'
import glob
import json
import os
import sys
root,address,table,prio_a,prio_b=sys.argv[1:]
paths=glob.glob(os.path.join(root,"*.json"))
committed=[]
for path in paths:
    with open(path,encoding="utf-8") as handle:
        tx=json.load(handle)
    if tx.get("owner")=="podlaz" and tx.get("mode")=="tun" and tx.get("state")=="committed":
        committed.append(tx)
if len(committed)!=1:
    raise SystemExit(f"expected one committed TUN transaction, found {len(committed)}")
tx=committed[0]
desired=tx.get("desired_plan") or {}
allocated=((desired.get("tun_address") or {}).get("cidr") or "")
if not allocated or allocated==address:
    raise SystemExit(f"TUN address allocation collided: {allocated!r}")
tables={
    str(route.get("table"))
    for route in desired.get("routes") or []
    if route.get("cidr") in ("default","0.0.0.0/0") and route.get("dev")=="podlaz0"
}
if len(tables)!=1 or table in tables:
    raise SystemExit(f"routing table allocation collided: {sorted(tables)!r}")
priorities=[]
for step in desired.get("steps") or []:
    if step.get("kind")!="policy-rule":
        continue
    fields=str(step.get("target") or "").split()
    if len(fields)>=2 and fields[0]=="priority":
        priorities.append(int(fields[1]))
occupied={int(prio_a),int(prio_b)}
if len(priorities)!=2 or occupied.intersection(priorities):
    raise SystemExit(f"policy-rule allocation collided: {priorities!r}")
if not priorities[0] < priorities[1]:
    raise SystemExit(f"policy-rule ordering is invalid: {priorities!r}")
PY
}

inject_uplink_churn() {
  guest_exec nmcli connection down "${UPLINK_CONNECTION}" >/dev/null
  UPLINK_DOWN=true
  guest_exec nmcli -t -f NAME,DEVICE connection show --active | grep -Fx "${UPLINK_CONNECTION}:${GUEST_IF}" >/dev/null && return 1
  assert_foreign_fixture
  guest_exec nmcli connection up "${UPLINK_CONNECTION}" >/dev/null
  UPLINK_DOWN=false
  wait_for_uplink_active
  wait_for_verified_active
}

run_scenario() {
  local candidate="$1"
  mark_failure diagnostic_unknown base.guest_ready
  run_base_scenario "${candidate}"
  wait_for_control_ready guest-ready || fail "base synthetic TUN did not reach guest-ready"

  mark_failure fixture foreign.fixture
  create_foreign_fixture || fail "could not create complete foreign-state fixture"
  assert_foreign_fixture || fail "foreign-state fixture is invalid before candidate install"
  record_evidence foreign.fixture_complete pass
  release_control guest-ready || fail "could not release guest-ready"

  mark_failure diagnostic_unknown base.verified_active
  wait_for_control_ready verified-active || fail "base synthetic TUN did not reach verified-active"
  grep -Fx 'candidate.provenance=pass' "${BASE_REPORT}" >/dev/null || fail "candidate provenance did not pass"
  grep -Fx 'tun.verified_active=pass' "${BASE_REPORT}" >/dev/null || fail "base verified-active control did not pass"
  record_evidence candidate.provenance pass

  mark_failure product foreign.active
  assert_foreign_fixture || fail "foreign state changed during connect"
  record_evidence foreign.connect_preserved pass
  assert_collision_free_allocation || fail "candidate allocation intersected occupied foreign identities"
  record_evidence allocation.disjoint pass

  mark_failure product foreign.churn
  inject_uplink_churn || fail "synthetic uplink churn did not converge"
  assert_foreign_fixture || fail "foreign state changed during network churn"
  record_evidence foreign.churn_preserved pass

  mark_failure diagnostic_unknown base.active_completion
  release_control verified-active || fail "could not release verified-active"
  wait_for_control_ready terminal-clean || fail "base synthetic TUN did not reach terminal-clean"

  mark_failure product foreign.cleanup
  assert_foreign_fixture || fail "foreign state changed during Podlaz cleanup"
  record_evidence foreign.cleanup_preserved pass
  release_control terminal-clean || fail "could not release terminal-clean"

  mark_failure diagnostic_unknown base.recovery
  wait_for_control_ready recovery-clean || fail "base synthetic TUN did not complete clean recovery"
  grep -Fx 'guest.baseline_restored=pass' "${BASE_REPORT}" >/dev/null || fail "base guest baseline was not restored"
  record_evidence base.baseline_restored pass

  mark_failure product foreign.recovery
  assert_foreign_fixture || fail "foreign state changed during recovery"
  record_evidence foreign.recovery_preserved pass
  release_control recovery-clean || fail "could not release recovery-clean"

  mark_failure diagnostic_unknown base.complete
  wait_base_completion || fail "base synthetic TUN scenario failed"
  grep -Fx 'outer.cleanup=pass' "${BASE_REPORT}" >/dev/null || fail "base outer cleanup did not pass"
  record_evidence base.outer_cleanup pass
  FOREIGN_CREATED=false

  FAILURE_CLASS=none
  FAILURE_STEP=none
  assert_public_artifact_privacy || fail "hosted foreign-state public evidence is not privacy-safe"
  record_evidence artifact.privacy pass
}

main() {
  (($# == 1)) || fail "usage: $0 CANDIDATE.deb"
  [[ -f "$1" && ! -L "$1" ]] || fail "candidate package must be a regular file"
  [[ "${EXPECTED_COMMIT}" =~ ^[0-9a-fA-F]{40}$ ]] || fail "PODLAZ_E2E_CANDIDATE_COMMIT must be exact"
  require_cmd awk bash chmod find grep install ip nft python3 rm seq sleep sudo systemd-run
  install -d -m 0700 "${E2E_TMP_ROOT}" "${E2E_ARTIFACT_DIR}"
  : >"${REPORT}"
  chmod 0600 "${REPORT}"
  trap cleanup EXIT INT TERM
  run_scenario "$1"
  trap - EXIT INT TERM
  finalize_report
  validate_report
}

if [[ "${1:-}" == validate-report ]]; then
  validate_report
  exit 0
fi

main "$@"
