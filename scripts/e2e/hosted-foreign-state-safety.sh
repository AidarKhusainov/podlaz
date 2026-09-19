#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"

BASE_SCENARIO="${SCRIPT_DIR}/hosted-synthetic-tun.sh"
MACHINE="podlaz-synthetic-tun"
GUEST_IF="host0"
STATUS_HELPER="/workspace/scripts/e2e/lib/daemon_status_semantics.py"
GUEST_XDG="/home/e2e/.local/share/podlaz-hosted-synthetic-tun"
FOREIGN_GUEST_PRIVATE="/tmp/podlaz-hosted-foreign-state"
EXPECTED_COMMIT="${PODLAZ_E2E_CANDIDATE_COMMIT:-}"

FOREIGN_TUN="pzforeign0"
FOREIGN_TUN_CIDR="198.18.0.1/32"
FOREIGN_TABLE="51820"
FOREIGN_ROUTE="198.51.100.254/32"
FOREIGN_RULE_TARGET_A="198.51.100.254/32"
FOREIGN_RULE_TARGET_B="198.51.100.253/32"
FOREIGN_RULE_PRIORITY_A="9999"
FOREIGN_RULE_PRIORITY_B="10000"
FOREIGN_DNS_LINK="pzforeign-dns0"
FOREIGN_DNS_SERVER="192.0.2.53"
FOREIGN_DNS_DOMAIN="~foreign.invalid"
FOREIGN_NFT_FAMILY="inet"
FOREIGN_NFT_TABLE="pzforeign_guard"
FOREIGN_SERVICE="pzforeign-state.service"
FOREIGN_NM_IF="pzforeignnm0"
FOREIGN_NM_CONN="pzforeign-nm"
FOREIGN_NM_CIDR="192.0.2.88/32"

PRIVATE_ROOT="${E2E_TMP_ROOT}/hosted-foreign-state-private"
BASE_TMP_ROOT="${PRIVATE_ROOT}/base-private"
BASE_ARTIFACT_DIR="${PRIVATE_ROOT}/base-public"
BASE_REPORT="${BASE_ARTIFACT_DIR}/hosted-synthetic-tun.txt"
BASE_STDOUT="${PRIVATE_ROOT}/base.stdout"
BASE_STDERR="${PRIVATE_ROOT}/base.stderr"
CONTROL_DIR="${BASE_TMP_ROOT}/control"
REPORT="${E2E_ARTIFACT_DIR}/hosted-foreign-state-safety.txt"
BASE_PID=""
FAILURE_CLASS=diagnostic_unknown
FAILURE_STEP=bootstrap
REPORT_FINALIZED=false
FOREIGN_CREATED=false
FOREIGN_TUN_INDEX=""
FOREIGN_NM_UUID=""
FOREIGN_NM_DOWN=false

EVIDENCE_KEYS=(
  candidate.positive_control
  candidate.allocation_disjoint
  foreign.connect_preserved
  foreign.churn_preserved
  foreign.recovery_preserved
  foreign.cleanup_preserved
  base.terminal_cleanup
  artifact.privacy
)

record_evidence() {
  printf '%s=%s\n' "$1" "$2" >>"${REPORT}"
}

evidence_recorded() {
  grep -Eq "^${1}=" "${REPORT}" 2>/dev/null
}

record_if_missing() {
  evidence_recorded "$1" || record_evidence "$1" "$2"
}

mark_failure() {
  case "$1" in
    product|fixture|infrastructure|capability|diagnostic_unknown) FAILURE_CLASS="$1" ;;
    *) FAILURE_CLASS=infrastructure ;;
  esac
  FAILURE_STEP="${2//[^A-Za-z0-9_.-]/_}"
}

finalize_report() {
  local key
  [[ "${REPORT_FINALIZED}" == false ]] || return 0
  for key in "${EVIDENCE_KEYS[@]}"; do
    record_if_missing "${key}" fail
  done
  printf 'failure.class=%s\n' "${FAILURE_CLASS}" >>"${REPORT}"
  printf 'failure.step=%s\n' "${FAILURE_STEP}" >>"${REPORT}"
  REPORT_FINALIZED=true
}

validate_report() {
  [[ -f "${REPORT}" && ! -L "${REPORT}" ]] || fail "hosted foreign state report is missing or invalid"
  python3 - "${REPORT}" <<'PY'
import sys
required = {
    "candidate.positive_control",
    "candidate.allocation_disjoint",
    "foreign.connect_preserved",
    "foreign.churn_preserved",
    "foreign.recovery_preserved",
    "foreign.cleanup_preserved",
    "base.terminal_cleanup",
    "artifact.privacy",
}
values = {}
with open(sys.argv[1], encoding="utf-8") as handle:
    for raw in handle:
        line = raw.rstrip("\n")
        if not line or "=" not in line:
            raise SystemExit(f"invalid report line: {line!r}")
        key, value = line.split("=", 1)
        if key in values:
            raise SystemExit(f"duplicate report key: {key}")
        values[key] = value
if set(values) != required | {"failure.class", "failure.step"}:
    raise SystemExit("unexpected hosted foreign state report schema")
for key in required:
    if values[key] != "pass":
        raise SystemExit(f"required evidence is not pass: {key}={values[key]}")
if values["failure.class"] != "none" or values["failure.step"] != "none":
    raise SystemExit("hosted foreign state safety reports a failure")
PY
}

assert_public_artifact_privacy() {
  local extra
  extra="$(find "${E2E_ARTIFACT_DIR}" -mindepth 1 -maxdepth 1 ! -name 'hosted-foreign-state-safety.txt' -print -quit)"
  [[ -z "${extra}" ]] || return 1
  [[ -f "${REPORT}" && ! -L "${REPORT}" ]] || return 1
  ! grep -Eiq 'vless://|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|172[.]31[.](253|254)[.]|session[_-]?id=|transaction[_-]?id=' "${REPORT}"
}

guest_exec() {
  sudo -n systemd-run --machine="${MACHINE}" --expand-environment=no --wait --pipe --collect --quiet -- "$@"
}

inherit_base_failure() {
  local class step
  [[ -f "${BASE_REPORT}" ]] || return 0
  class="$(awk -F= '$1 == "failure.class" {print $2; exit}' "${BASE_REPORT}" 2>/dev/null || true)"
  step="$(awk -F= '$1 == "failure.step" {print $2; exit}' "${BASE_REPORT}" 2>/dev/null || true)"
  case "${class}" in
    product|fixture|infrastructure|capability|diagnostic_unknown)
      FAILURE_CLASS="${class}"
      ;;
    *)
      ;;
  esac
  if [[ -n "${step}" && "${step}" != none ]]; then
    FAILURE_STEP="base.${step//[^A-Za-z0-9_.-]/_}"
  fi
  printf 'base normalized failure: class=%s step=%s\n' "${class:-unknown}" "${step:-unknown}" >&2
  if [[ "${step}" == tun.doctor && -f "${BASE_TMP_ROOT}/private/doctor.json" ]]; then
    python3 - "${BASE_TMP_ROOT}/private/doctor.json" <<'PY' >&2 || true
import json,sys
try:
    with open(sys.argv[1], encoding="utf-8") as handle:
        report=json.load(handle)
except (OSError,json.JSONDecodeError):
    raise SystemExit(0)
status=str(report.get("status") or "unknown")
classification=str(report.get("primary_classification") or "unknown")
safe=lambda value: value if value.replace("_","").replace("-","").isalnum() else "invalid"
print(f"base doctor summary: status={safe(status)} primary_classification={safe(classification)}")
PY
  fi
}

release_all_controls() {
  local phase path
  [[ -d "${CONTROL_DIR}" ]] || return 0
  for phase in candidate-ready verified-active terminal-clean; do
    path="${CONTROL_DIR}/${phase}.continue"
    [[ -e "${path}" ]] || printf 'continue\n' >"${path}"
    chmod 0600 "${path}" >/dev/null 2>&1 || true
  done
}

wait_for_control_ready() {
  local phase="$1" ready="${CONTROL_DIR}/$1.ready" attempt code
  for attempt in $(seq 1 6000); do
    if [[ -f "${ready}" && ! -L "${ready}" ]]; then
      return 0
    fi
    if [[ -z "${BASE_PID}" ]] || ! kill -0 "${BASE_PID}" >/dev/null 2>&1; then
      if [[ -n "${BASE_PID}" ]]; then
        set +e
        wait "${BASE_PID}"
        code=$?
        set -e
        BASE_PID=""
        inherit_base_failure
        printf 'base scenario exited before %s boundary: %s\n' "${phase}" "${code}" >&2
      fi
      return 1
    fi
    sleep 0.1
  done
  return 1
}

release_control() {
  local phase="$1" ready="${CONTROL_DIR}/$1.ready" continue="${CONTROL_DIR}/$1.continue" attempt
  [[ -f "${ready}" && ! -L "${ready}" ]] || return 1
  [[ ! -e "${continue}" && ! -L "${continue}" ]] || return 1
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
    PODLAZ_E2E_HOSTED_CONTROL_PHASES="candidate-ready verified-active terminal-clean" \
    PODLAZ_E2E_HOSTED_CONTROL_TIMEOUT_SECONDS=180 \
    bash "${BASE_SCENARIO}" "${candidate}" >"${BASE_STDOUT}" 2>"${BASE_STDERR}" &
  BASE_PID=$!
}

wait_base_success() {
  local code
  [[ -n "${BASE_PID}" ]] || return 1
  set +e
  wait "${BASE_PID}"
  code=$?
  set -e
  BASE_PID=""
  (( code == 0 ))
}

capture_status() {
  guest_exec install -d -m 0700 "${FOREIGN_GUEST_PRIVATE}" >/dev/null
  guest_exec /bin/bash -lc "curl --fail --silent --show-error --max-time 3 --unix-socket /run/podlaz/podlazd.sock http://localhost/v1/status >'${FOREIGN_GUEST_PRIVATE}/status.json'"
}

wait_for_verified_active() {
  local attempt
  for attempt in $(seq 1 240); do
    if capture_status >/dev/null 2>&1 &&
       guest_exec python3 "${STATUS_HELPER}" verified-active "${FOREIGN_GUEST_PRIVATE}/status.json" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.5
  done
  return 1
}

assert_fixture_absent() {
  guest_exec /bin/bash -lc "! ip link show dev '${FOREIGN_TUN}' >/dev/null 2>&1"
  guest_exec /bin/bash -lc "! ip link show dev '${FOREIGN_DNS_LINK}' >/dev/null 2>&1"
  guest_exec /bin/bash -lc "! ip link show dev '${FOREIGN_NM_IF}' >/dev/null 2>&1"
  guest_exec /bin/bash -lc "! nft list table '${FOREIGN_NFT_FAMILY}' '${FOREIGN_NFT_TABLE}' >/dev/null 2>&1"
  guest_exec /bin/bash -lc "! ip -4 route show table '${FOREIGN_TABLE}' | grep -q ."
  guest_exec /bin/bash -lc "! ip -4 rule show priority '${FOREIGN_RULE_PRIORITY_A}' | grep -q ."
  guest_exec /bin/bash -lc "! ip -4 rule show priority '${FOREIGN_RULE_PRIORITY_B}' | grep -q ."
  guest_exec /bin/bash -lc "! systemctl is-active --quiet '${FOREIGN_SERVICE}'"
  guest_exec /bin/bash -lc "! nmcli -t -f NAME connection show | grep -Fx '${FOREIGN_NM_CONN}' >/dev/null"
}

create_foreign_fixture() {
  assert_fixture_absent
  guest_exec ip tuntap add dev "${FOREIGN_TUN}" mode tun
  guest_exec ip link set dev "${FOREIGN_TUN}" up
  guest_exec ip -4 address add "${FOREIGN_TUN_CIDR}" dev "${FOREIGN_TUN}"
  FOREIGN_TUN_INDEX="$(guest_exec /bin/bash -lc "ip -o link show dev '${FOREIGN_TUN}' | awk -F: 'NR == 1 {gsub(/[[:space:]]/, \"\", \$1); print \$1}'")"
  [[ "${FOREIGN_TUN_INDEX}" =~ ^[1-9][0-9]*$ ]]

  guest_exec ip -4 route add blackhole "${FOREIGN_ROUTE}" table "${FOREIGN_TABLE}"
  guest_exec ip -4 rule add priority "${FOREIGN_RULE_PRIORITY_A}" to "${FOREIGN_RULE_TARGET_A}" lookup "${FOREIGN_TABLE}"
  guest_exec ip -4 rule add priority "${FOREIGN_RULE_PRIORITY_B}" to "${FOREIGN_RULE_TARGET_B}" lookup "${FOREIGN_TABLE}"

  guest_exec nft add table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}"

  guest_exec ip link add "${FOREIGN_DNS_LINK}" type dummy
  guest_exec ip link set dev "${FOREIGN_DNS_LINK}" up
  guest_exec resolvectl dns "${FOREIGN_DNS_LINK}" "${FOREIGN_DNS_SERVER}"
  guest_exec resolvectl domain "${FOREIGN_DNS_LINK}" "${FOREIGN_DNS_DOMAIN}"
  guest_exec resolvectl default-route "${FOREIGN_DNS_LINK}" no

  guest_exec systemd-run --unit="${FOREIGN_SERVICE%.service}" --property=Type=simple /bin/sh -c 'exec sleep 600' >/dev/null

  guest_exec nmcli connection add type dummy ifname "${FOREIGN_NM_IF}" con-name "${FOREIGN_NM_CONN}" \
    connection.autoconnect no ipv4.method manual ipv4.addresses "${FOREIGN_NM_CIDR}" \
    ipv4.never-default yes ipv4.ignore-auto-dns yes ipv6.method disabled >/dev/null
  FOREIGN_NM_UUID="$(guest_exec nmcli -g connection.uuid connection show "${FOREIGN_NM_CONN}" | tr -d '[:space:]')"
  [[ "${FOREIGN_NM_UUID}" =~ ^[0-9a-fA-F-]{36}$ ]]
  guest_exec nmcli connection up uuid "${FOREIGN_NM_UUID}" >/dev/null

  FOREIGN_CREATED=true
}

assert_foreign_fixture_without_nm_active() {
  guest_exec /bin/bash -lc "ip tuntap show dev '${FOREIGN_TUN}' | grep -Eq '^${FOREIGN_TUN}:[[:space:]]+tun([[:space:]]|$)'"
  guest_exec ip -4 address show dev "${FOREIGN_TUN}" | grep -F "${FOREIGN_TUN_CIDR}" >/dev/null
  guest_exec ip -4 route show table "${FOREIGN_TABLE}" exact "${FOREIGN_ROUTE}" | grep -F "blackhole ${FOREIGN_ROUTE%/32}" >/dev/null
  guest_exec ip -4 rule show priority "${FOREIGN_RULE_PRIORITY_A}" | grep -F "to ${FOREIGN_RULE_TARGET_A%/32}" | grep -F "lookup ${FOREIGN_TABLE}" >/dev/null
  guest_exec ip -4 rule show priority "${FOREIGN_RULE_PRIORITY_B}" | grep -F "to ${FOREIGN_RULE_TARGET_B%/32}" | grep -F "lookup ${FOREIGN_TABLE}" >/dev/null
  guest_exec nft list table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}" >/dev/null
  guest_exec /bin/bash -lc "resolvectl status '${FOREIGN_DNS_LINK}' --no-pager >'${FOREIGN_GUEST_PRIVATE}/foreign-resolved.txt'; grep -F '${FOREIGN_DNS_SERVER}' '${FOREIGN_GUEST_PRIVATE}/foreign-resolved.txt' >/dev/null; grep -F '${FOREIGN_DNS_DOMAIN}' '${FOREIGN_GUEST_PRIVATE}/foreign-resolved.txt' >/dev/null"
  guest_exec systemctl is-active --quiet "${FOREIGN_SERVICE}"
  guest_exec /bin/bash -lc "test \"\$(nmcli -g connection.uuid connection show '${FOREIGN_NM_CONN}' | tr -d '[:space:]')\" = '${FOREIGN_NM_UUID}'"
}

assert_foreign_fixture() {
  assert_foreign_fixture_without_nm_active
  guest_exec nmcli -t -f NAME,DEVICE connection show --active | grep -Fx "${FOREIGN_NM_CONN}:${FOREIGN_NM_IF}" >/dev/null
  guest_exec ip -4 address show dev "${FOREIGN_NM_IF}" | grep -F "${FOREIGN_NM_CIDR}" >/dev/null
}

assert_collision_free_allocation() {
  guest_exec python3 - /run/podlaz/transactions "${FOREIGN_TUN_CIDR}" "${FOREIGN_TABLE}" "${FOREIGN_RULE_PRIORITY_A}" "${FOREIGN_RULE_PRIORITY_B}" <<'PY'
import glob,json,sys
txdir, occupied_cidr, occupied_table, pa, pb = sys.argv[1:]
paths=glob.glob(txdir.rstrip("/")+"/*.json")
committed=[]
for path in paths:
    with open(path,encoding="utf-8") as handle:
        tx=json.load(handle)
    if tx.get("owner")=="podlaz" and tx.get("mode")=="tun" and tx.get("state")=="committed":
        committed.append(tx)
if len(committed)!=1:
    raise SystemExit(f"expected one committed candidate transaction, found {len(committed)}")
tx=committed[0]
desired=tx.get("desired_plan") or {}
address=(desired.get("tun_address") or {}).get("cidr")
if not address or address==occupied_cidr:
    raise SystemExit(f"TUN address collided with occupied identity: {address!r}")
routes=desired.get("routes") or []
tables={
    str(route.get("table"))
    for route in routes
    if route.get("cidr") in ("default","0.0.0.0/0") and route.get("dev")=="podlaz0"
}
if len(tables)!=1 or occupied_table in tables:
    raise SystemExit(f"routing table collided with occupied identity: {sorted(tables)!r}")
priorities=[]
for step in desired.get("steps") or []:
    if step.get("kind")!="policy-rule":
        continue
    fields=str(step.get("target") or "").split()
    if len(fields)>=2 and fields[0]=="priority":
        priorities.append(int(fields[1]))
if len(priorities)!=2 or any(value in {int(pa),int(pb)} for value in priorities):
    raise SystemExit(f"policy priorities collided with occupied identities: {priorities!r}")
if priorities[0] >= priorities[1]:
    raise SystemExit(f"policy priority ordering is invalid: {priorities!r}")
PY
}

churn_foreign_networkmanager() {
  guest_exec nmcli connection down uuid "${FOREIGN_NM_UUID}" >/dev/null
  FOREIGN_NM_DOWN=true
  guest_exec /bin/bash -lc "! nmcli -t -f NAME,DEVICE connection show --active | grep -Fx '${FOREIGN_NM_CONN}:${FOREIGN_NM_IF}' >/dev/null"
  assert_foreign_fixture_without_nm_active
  guest_exec nmcli connection up uuid "${FOREIGN_NM_UUID}" >/dev/null
  FOREIGN_NM_DOWN=false
  assert_foreign_fixture
  wait_for_verified_active
}

assert_recovery_does_not_claim_foreign_state() {
  guest_exec /bin/bash -lc "/usr/bin/podlaz recover --execute --yes --json >'${FOREIGN_GUEST_PRIVATE}/recover-execute.json'"
  guest_exec python3 - "${FOREIGN_GUEST_PRIVATE}/recover-execute.json" <<'PY'
import json,sys
with open(sys.argv[1],encoding="utf-8") as handle:
    payload=json.load(handle)
if payload.get("status")!="ok" or payload.get("mode")!="execute":
    raise SystemExit("foreign-only recovery execute did not report clean status")
if payload.get("recovery") or payload.get("warnings") or payload.get("errors"):
    raise SystemExit(f"foreign-only state became recovery authority: {payload!r}")
PY
  assert_foreign_fixture
}

cleanup_foreign_fixture() {
  local current_index current_uuid
  [[ "${FOREIGN_CREATED}" == true ]] || return 0

  if [[ "${FOREIGN_NM_DOWN}" == true ]]; then
    guest_exec nmcli connection up uuid "${FOREIGN_NM_UUID}" >/dev/null 2>&1 || true
    FOREIGN_NM_DOWN=false
  fi
  current_uuid="$(guest_exec nmcli -g connection.uuid connection show "${FOREIGN_NM_CONN}" 2>/dev/null | tr -d '[:space:]' || true)"
  if [[ -n "${current_uuid}" ]]; then
    [[ "${current_uuid}" == "${FOREIGN_NM_UUID}" ]] || return 1
    guest_exec nmcli connection delete uuid "${FOREIGN_NM_UUID}" >/dev/null
  fi

  guest_exec systemctl stop "${FOREIGN_SERVICE}" >/dev/null 2>&1 || true

  guest_exec resolvectl revert "${FOREIGN_DNS_LINK}" >/dev/null 2>&1 || true
  if guest_exec ip link show dev "${FOREIGN_DNS_LINK}" >/dev/null 2>&1; then
    guest_exec ip link del dev "${FOREIGN_DNS_LINK}"
  fi

  if guest_exec nft list table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}" >/dev/null 2>&1; then
    guest_exec nft delete table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}"
  fi

  if guest_exec ip -4 rule show priority "${FOREIGN_RULE_PRIORITY_B}" | grep -F "to ${FOREIGN_RULE_TARGET_B%/32}" | grep -F "lookup ${FOREIGN_TABLE}" >/dev/null; then
    guest_exec ip -4 rule del priority "${FOREIGN_RULE_PRIORITY_B}" to "${FOREIGN_RULE_TARGET_B}" lookup "${FOREIGN_TABLE}"
  fi
  if guest_exec ip -4 rule show priority "${FOREIGN_RULE_PRIORITY_A}" | grep -F "to ${FOREIGN_RULE_TARGET_A%/32}" | grep -F "lookup ${FOREIGN_TABLE}" >/dev/null; then
    guest_exec ip -4 rule del priority "${FOREIGN_RULE_PRIORITY_A}" to "${FOREIGN_RULE_TARGET_A}" lookup "${FOREIGN_TABLE}"
  fi
  if guest_exec ip -4 route show table "${FOREIGN_TABLE}" exact "${FOREIGN_ROUTE}" | grep -F "blackhole ${FOREIGN_ROUTE%/32}" >/dev/null; then
    guest_exec ip -4 route del blackhole "${FOREIGN_ROUTE}" table "${FOREIGN_TABLE}"
  fi

  current_index="$(guest_exec /bin/bash -lc "ip -o link show dev '${FOREIGN_TUN}' 2>/dev/null | awk -F: 'NR == 1 {gsub(/[[:space:]]/, \"\", \$1); print \$1}'" 2>/dev/null || true)"
  if [[ -n "${current_index}" ]]; then
    [[ "${current_index}" == "${FOREIGN_TUN_INDEX}" ]] || return 1
    guest_exec /bin/bash -lc "ip tuntap show dev '${FOREIGN_TUN}' | grep -Eq '^${FOREIGN_TUN}:[[:space:]]+tun([[:space:]]|$)'"
    guest_exec ip link del dev "${FOREIGN_TUN}"
  fi

  FOREIGN_CREATED=false
  assert_fixture_absent
}

cleanup() {
  local saved=$? attempt cleanup_failed=0
  trap - EXIT INT TERM
  set +e
  cleanup_foreign_fixture || cleanup_failed=1
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
  inherit_base_failure
  if [[ -f "${REPORT}" ]]; then
    if assert_public_artifact_privacy; then
      record_if_missing artifact.privacy pass
    else
      record_if_missing artifact.privacy fail
      cleanup_failed=1
    fi
    finalize_report
  fi
  (( saved == 0 && cleanup_failed != 0 )) && saved=1
  set -e
  exit "${saved}"
}

validate_base_control() {
  env E2E_TMP_ROOT="${BASE_TMP_ROOT}" E2E_ARTIFACT_DIR="${BASE_ARTIFACT_DIR}" \
    bash "${BASE_SCENARIO}" validate-report >/dev/null
  grep -Fx 'candidate.provenance=pass' "${BASE_REPORT}" >/dev/null
  grep -Fx 'tun.verified_active=pass' "${BASE_REPORT}" >/dev/null
  grep -Fx 'tun.system_dns=pass' "${BASE_REPORT}" >/dev/null
  grep -Fx 'tun.https_tls=pass' "${BASE_REPORT}" >/dev/null
  grep -Fx 'tun.terminal_cleanup=pass' "${BASE_REPORT}" >/dev/null
  grep -Fx 'guest.baseline_restored=pass' "${BASE_REPORT}" >/dev/null
  grep -Fx 'tun.recovery_clean=pass' "${BASE_REPORT}" >/dev/null
  grep -Fx 'outer.cleanup=pass' "${BASE_REPORT}" >/dev/null
}

run_scenario() {
  local candidate="$1"

  mark_failure diagnostic_unknown base.candidate_ready
  run_base_scenario "${candidate}"
  wait_for_control_ready candidate-ready || fail "base synthetic TUN did not reach candidate-ready boundary"
  grep -Fx 'candidate.provenance=pass' "${BASE_REPORT}" >/dev/null || fail "base candidate provenance did not pass"
  record_evidence candidate.positive_control pass

  mark_failure fixture foreign.fixture
  guest_exec install -d -m 0700 "${FOREIGN_GUEST_PRIVATE}"
  create_foreign_fixture || fail "could not create comprehensive foreign network fixture"
  assert_foreign_fixture || fail "foreign fixture is not exact before connect"

  mark_failure diagnostic_unknown base.connect
  release_control candidate-ready || fail "could not release candidate-ready boundary"
  wait_for_control_ready verified-active || fail "base synthetic TUN did not reach verified-active with occupied identities"

  mark_failure product foreign.allocation
  assert_collision_free_allocation || fail "candidate allocation overlaps occupied foreign identities"
  record_evidence candidate.allocation_disjoint pass
  assert_foreign_fixture || fail "foreign state changed during connect"
  record_evidence foreign.connect_preserved pass

  mark_failure product foreign.churn
  churn_foreign_networkmanager || fail "foreign NetworkManager churn disturbed candidate or foreign state"
  assert_foreign_fixture || fail "foreign state changed after surrounding churn"
  record_evidence foreign.churn_preserved pass

  mark_failure diagnostic_unknown base.active_completion
  release_control verified-active || fail "could not release verified-active boundary"
  wait_for_control_ready terminal-clean || fail "base synthetic TUN did not prove terminal cleanup"

  mark_failure product foreign.cleanup
  assert_foreign_fixture || fail "Podlaz terminal cleanup changed foreign state"
  record_evidence foreign.cleanup_preserved pass

  mark_failure product foreign.recovery
  assert_recovery_does_not_claim_foreign_state || fail "recovery observation became foreign cleanup authority"
  record_evidence foreign.recovery_preserved pass

  mark_failure fixture foreign.fixture_cleanup
  cleanup_foreign_fixture || fail "could not safely remove exact foreign fixture"

  mark_failure diagnostic_unknown base.complete
  release_control terminal-clean || fail "could not release terminal-clean boundary"
  wait_base_success || fail "base synthetic TUN scenario failed after foreign-state qualification"
  validate_base_control || fail "base hosted synthetic TUN report is not clean"
  record_evidence base.terminal_cleanup pass

  FAILURE_CLASS=none
  FAILURE_STEP=none
  assert_public_artifact_privacy || fail "hosted foreign state public evidence is not privacy-safe"
  record_evidence artifact.privacy pass
}

main() {
  (($# == 1)) || fail "usage: $0 CANDIDATE.deb"
  [[ -f "$1" && ! -L "$1" ]] || fail "candidate package must be a regular file"
  [[ "${EXPECTED_COMMIT}" =~ ^[0-9a-fA-F]{40}$ ]] || fail "PODLAZ_E2E_CANDIDATE_COMMIT must be an exact 40-hex commit"
  require_cmd awk bash chmod find grep install jq kill python3 rm seq sleep sudo systemd-run
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
