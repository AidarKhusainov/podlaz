#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"

BASE_SCENARIO="${SCRIPT_DIR}/hosted-synthetic-tun.sh"
MACHINE="podlaz-synthetic-tun"
GUEST_IF="host0"
UPLINK_CONNECTION="synthetic-uplink"
FOREIGN_NFT_TABLE="pzsynt_foreign"
FOREIGN_TUN="pzrecon0"
FOREIGN_TUN_CIDR="192.0.2.62/32"
FOREIGN_TABLE="51962"
FOREIGN_ROUTE_A="203.0.113.62/32"
FOREIGN_ROUTE_B="203.0.113.63/32"
SESSION_STATE="/run/podlaz/network-session-continuation.json"
DAEMON_SOCKET="/run/podlaz/podlazd.sock"
HOOK_DIR="/run/podlaz-hosted-network-reconciliation"
OVERRIDE_DIR="/run/systemd/system/podlazd.service.d"
OVERRIDE_PATH="${OVERRIDE_DIR}/99-hosted-network-reconciliation.conf"
GUEST_PRIVATE="/tmp/podlaz-hosted-network-reconciliation"
ACTIVE_AUTHORITY_HELPER="/workspace/scripts/e2e/hosted_synthetic_active_authority.py"
STATUS_HELPER="/workspace/scripts/e2e/lib/daemon_status_semantics.py"
EXPECTED_COMMIT="${PODLAZ_E2E_CANDIDATE_COMMIT:-}"

SCENARIO=""
REPORT=""
PRIVATE_ROOT=""
BASE_TMP_ROOT=""
BASE_ARTIFACT_DIR=""
BASE_REPORT=""
BASE_STDOUT=""
BASE_STDERR=""
CONTROL_DIR=""
BASE_PID=""
BASE_EXIT_CODE=""
FAILURE_CLASS=diagnostic_unknown
FAILURE_STEP=bootstrap
REPORT_FINALIZED=false
PE_FAMILY=""
PE_TABLE=""
SESSION_ID=""
PROBE_IP=""
FOREIGN_EXPECTED_ROUTE="${FOREIGN_ROUTE_A}"
FOREIGN_CREATED=false
UPLINK_DOWN=false
HOOK_INSTALLED=false

EVIDENCE_KEYS=(
  candidate.positive_control
  reconciliation.fault_injected
  privacy.envelope_retained
  privacy.direct_uplink_blocked
  foreign.state_preserved
  reconciliation.verified_active
  base.terminal_cleanup
  artifact.privacy
)

configure_scenario() {
  local scenario="$1"
  case "${scenario}" in
    provider-observation|resolved-unknown|route-replacement|networkmanager-uplink) ;;
    *) fail "unsupported hosted network reconciliation scenario: ${scenario}" ;;
  esac
  SCENARIO="${scenario}"
  REPORT="${E2E_ARTIFACT_DIR}/hosted-network-reconciliation-${SCENARIO}.txt"
  PRIVATE_ROOT="${E2E_TMP_ROOT}/hosted-network-reconciliation-${SCENARIO}-private"
  BASE_TMP_ROOT="${PRIVATE_ROOT}/base-private"
  BASE_ARTIFACT_DIR="${PRIVATE_ROOT}/base-public"
  BASE_REPORT="${BASE_ARTIFACT_DIR}/hosted-synthetic-tun.txt"
  BASE_STDOUT="${PRIVATE_ROOT}/base.stdout"
  BASE_STDERR="${PRIVATE_ROOT}/base.stderr"
  CONTROL_DIR="${BASE_TMP_ROOT}/control"
}

record_evidence() {
  local key="$1" value="$2"
  printf '%s=%s\n' "${key}" "${value}" >>"${REPORT}"
}

evidence_recorded() {
  local key="$1"
  grep -Eq "^${key}=" "${REPORT}" 2>/dev/null
}

record_if_missing() {
  local key="$1" value="$2"
  evidence_recorded "${key}" || record_evidence "${key}" "${value}"
}

mark_failure() {
  FAILURE_CLASS="$1"
  FAILURE_STEP="$2"
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
  [[ -f "${REPORT}" ]] || fail "hosted network reconciliation report is missing"
  python3 - "${REPORT}" <<'PY'
import sys

path = sys.argv[1]
required = {
    "candidate.positive_control",
    "reconciliation.fault_injected",
    "privacy.envelope_retained",
    "privacy.direct_uplink_blocked",
    "foreign.state_preserved",
    "reconciliation.verified_active",
    "base.terminal_cleanup",
    "artifact.privacy",
}
values = {}
with open(path, encoding="utf-8") as handle:
    for raw in handle:
        line = raw.rstrip("\n")
        if not line or "=" not in line:
            raise SystemExit(f"invalid report line: {line!r}")
        key, value = line.split("=", 1)
        if key in values:
            raise SystemExit(f"duplicate report key: {key}")
        values[key] = value
if set(values) != required | {"failure.class", "failure.step"}:
    raise SystemExit("unexpected hosted network reconciliation report schema")
for key in required:
    if values[key] != "pass":
        raise SystemExit(f"required evidence is not pass: {key}={values[key]}")
if values["failure.class"] != "none" or values["failure.step"] != "none":
    raise SystemExit("hosted network reconciliation reports a failure")
PY
}

assert_public_artifact_privacy() {
  local extra
  extra="$(find "${E2E_ARTIFACT_DIR}" -mindepth 1 -maxdepth 1 ! -name "hosted-network-reconciliation-${SCENARIO}.txt" -print -quit)"
  [[ -z "${extra}" ]] || return 1
  [[ -f "${REPORT}" && ! -L "${REPORT}" ]] || return 1
  ! grep -Eiq 'vless://|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|172\.31\.(253|254)\.' "${REPORT}"
}

guest_exec() {
  sudo -n systemd-run --machine="${MACHINE}" --expand-environment=no --wait --pipe --collect --quiet -- "$@"
}

inherit_base_failure() {
  local class step
  [[ "${FAILURE_CLASS}" == diagnostic_unknown ]] || return 0
  [[ -f "${BASE_REPORT}" ]] || return 0
  class="$(awk -F= '$1 == "failure.class" {print $2; exit}' "${BASE_REPORT}" 2>/dev/null || true)"
  step="$(awk -F= '$1 == "failure.step" {print $2; exit}' "${BASE_REPORT}" 2>/dev/null || true)"
  case "${class}" in
    product|fixture|infrastructure|capability|diagnostic_unknown) FAILURE_CLASS="${class}" ;;
    *) ;;
  esac
  [[ -n "${step}" && "${step}" != none ]] && FAILURE_STEP="base.${step}"
}

release_all_controls() {
  local phase continue
  [[ -d "${CONTROL_DIR}" ]] || return 0
  for phase in candidate-ready verified-active terminal-clean; do
    continue="${CONTROL_DIR}/${phase}.continue"
    [[ -e "${continue}" ]] || printf 'continue\n' >"${continue}"
    chmod 0600 "${continue}" >/dev/null 2>&1 || true
  done
}

remove_hook_override() {
  [[ "${HOOK_INSTALLED}" == true ]] || return 0
  guest_exec /bin/bash -lc "rm -f '${OVERRIDE_PATH}'; systemctl daemon-reload" >/dev/null 2>&1 || return 1
  HOOK_INSTALLED=false
}

cleanup_hook_dir() {
  guest_exec test -d "${HOOK_DIR}" >/dev/null 2>&1 || return 0
  guest_exec /bin/bash -lc "find '${HOOK_DIR}' -mindepth 1 -maxdepth 1 -type f \( -name 'reconciliation-soft-provider.trigger' -o -name 'reconciliation-soft-provider.injected' -o -name 'reconciliation-resolved-unknown.trigger' -o -name 'reconciliation-resolved-unknown.injected' \) -delete; test -z \"\$(find '${HOOK_DIR}' -mindepth 1 -maxdepth 1 -print -quit)\"; rmdir '${HOOK_DIR}'"
}

cleanup_foreign_fixture() {
  [[ "${FOREIGN_CREATED}" == true ]] || return 0
  guest_exec ip -4 route flush table "${FOREIGN_TABLE}" >/dev/null 2>&1 || true
  guest_exec ip link del dev "${FOREIGN_TUN}" >/dev/null 2>&1 || true
  FOREIGN_CREATED=false
}

restore_uplink_if_needed() {
  [[ "${UPLINK_DOWN}" == true ]] || return 0
  guest_exec nmcli connection up "${UPLINK_CONNECTION}" >/dev/null 2>&1 || return 1
  UPLINK_DOWN=false
}

cleanup() {
  local code=$? attempt
  trap - EXIT INT TERM
  set +e
  restore_uplink_if_needed || true
  remove_hook_override || true
  cleanup_hook_dir || true
  cleanup_foreign_fixture
  release_all_controls
  if [[ -n "${BASE_PID}" ]] && kill -0 "${BASE_PID}" >/dev/null 2>&1; then
    for attempt in $(seq 1 480); do
      kill -0 "${BASE_PID}" >/dev/null 2>&1 || break
      sleep 0.5
    done
    if kill -0 "${BASE_PID}" >/dev/null 2>&1; then
      kill -TERM "${BASE_PID}" >/dev/null 2>&1 || true
      sleep 1
    fi
    wait "${BASE_PID}" >/dev/null 2>&1 || true
  fi
  inherit_base_failure
  if [[ -f "${REPORT}" ]]; then
    if assert_public_artifact_privacy; then
      record_if_missing artifact.privacy pass
    fi
    finalize_report
  fi
  exit "${code}"
}

wait_for_control_ready() {
  local phase="$1" ready attempt code
  ready="${CONTROL_DIR}/${phase}.ready"
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
        BASE_EXIT_CODE="${code}"
        BASE_PID=""
      fi
      inherit_base_failure
      return 1
    fi
    sleep 0.1
  done
  return 1
}

release_control() {
  local phase="$1" ready continue attempt
  ready="${CONTROL_DIR}/${phase}.ready"
  continue="${CONTROL_DIR}/${phase}.continue"
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

wait_base_completion() {
  local code
  [[ -n "${BASE_PID}" ]] || return 1
  set +e
  wait "${BASE_PID}"
  code=$?
  set -e
  BASE_EXIT_CODE="${code}"
  BASE_PID=""
  if (( code != 0 )); then
    inherit_base_failure
    return 1
  fi
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

validate_base_control() {
  env \
    E2E_TMP_ROOT="${BASE_TMP_ROOT}" \
    E2E_ARTIFACT_DIR="${BASE_ARTIFACT_DIR}" \
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

capture_status() {
  guest_exec install -d -m 0700 "${GUEST_PRIVATE}" >/dev/null
  guest_exec /bin/bash -lc "curl --fail --silent --show-error --max-time 3 --unix-socket '${DAEMON_SOCKET}' http://localhost/v1/status >'${GUEST_PRIVATE}/status.json'"
}

wait_for_guest_status() {
  local target="$1" attempts="${2:-240}" attempt
  for attempt in $(seq 1 "${attempts}"); do
    if capture_status >/dev/null 2>&1 &&
        guest_exec python3 "${STATUS_HELPER}" "${target}" "${GUEST_PRIVATE}/status.json" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.5
  done
  return 1
}

wait_for_verified_active() {
  wait_for_guest_status verified-active 240
}

install_reconciliation_override() {
  local feature_line
  case "${SCENARIO}" in
    provider-observation) feature_line='Environment=PODLAZ_E2E_TUN_RECONCILIATION_SOFT_FAILURE=true' ;;
    resolved-unknown) feature_line='Environment=PODLAZ_E2E_TUN_RECONCILIATION_RESOLVED_UNKNOWN=true' ;;
    *) return 0 ;;
  esac
  guest_exec test ! -e "${HOOK_DIR}"
  guest_exec test ! -e "${OVERRIDE_PATH}"
  guest_exec install -d -m 0700 "${HOOK_DIR}"
  guest_exec install -d -m 0755 "${OVERRIDE_DIR}"
  guest_exec /bin/bash -lc "printf '%s\\n' '[Service]' 'Environment=PODLAZ_E2E_TUN_TERMINAL_FAILURE=true' 'Environment=PODLAZ_E2E_TUN_TERMINAL_FAILURE_DIR=${HOOK_DIR}' '${feature_line}' 'Environment=PODLAZ_E2E_TUN_HOOK_TIMEOUT_SECONDS=120' >'${OVERRIDE_PATH}'; chmod 0644 '${OVERRIDE_PATH}'; systemctl daemon-reload; systemctl restart podlazd.service"
  HOOK_INSTALLED=true
  wait_for_guest_status clean-inactive 80
}

create_foreign_fixture() {
  guest_exec test ! -e "${GUEST_PRIVATE}"
  guest_exec install -d -m 0700 "${GUEST_PRIVATE}"
  if guest_exec ip link show dev "${FOREIGN_TUN}" >/dev/null 2>&1; then
    return 1
  fi
  guest_exec ip tuntap add dev "${FOREIGN_TUN}" mode tun
  guest_exec ip link set dev "${FOREIGN_TUN}" up
  guest_exec ip -4 address add "${FOREIGN_TUN_CIDR}" dev "${FOREIGN_TUN}"
  guest_exec ip -4 route add blackhole "${FOREIGN_ROUTE_A}" table "${FOREIGN_TABLE}"
  FOREIGN_CREATED=true
  guest_exec /bin/bash -lc "nft -j list table inet '${FOREIGN_NFT_TABLE}' >'${GUEST_PRIVATE}/foreign-nft-before.json'"
}

assert_foreign_fixture() {
  guest_exec ip link show dev "${FOREIGN_TUN}" >/dev/null 2>&1
  guest_exec ip -4 address show dev "${FOREIGN_TUN}" | grep -F "${FOREIGN_TUN_CIDR}" >/dev/null
  guest_exec ip -4 route show table "${FOREIGN_TABLE}" | grep -F "${FOREIGN_EXPECTED_ROUTE%/32}" >/dev/null
  guest_exec /bin/bash -lc "nft -j list table inet '${FOREIGN_NFT_TABLE}' >'${GUEST_PRIVATE}/foreign-nft-current.json'; cmp -s '${GUEST_PRIVATE}/foreign-nft-before.json' '${GUEST_PRIVATE}/foreign-nft-current.json'"
}

load_active_identity() {
  read -r SESSION_ID PE_FAMILY PE_TABLE <<<"$(guest_exec python3 - "${SESSION_STATE}" <<'PY'
import json,re,sys
with open(sys.argv[1], encoding="utf-8") as handle:
    state=json.load(handle)
protection=state.get("protection") or {}
session=str(state.get("session_id") or "")
family=str(protection.get("family") or "")
table=str(protection.get("table") or "")
if state.get("intent") != "resume" or not re.fullmatch(r"[0-9a-f]{32}", session):
    raise SystemExit("active Network Session identity is invalid")
if protection.get("state") != "armed" or family != "inet":
    raise SystemExit("Privacy Envelope is not armed")
if not re.fullmatch(r"podlaz_pe_[0-9a-f]{12}(?:_[1-9][0-9]{0,2})?", table):
    raise SystemExit("Privacy Envelope identity is invalid")
print(session, family, table)
PY
)"
  [[ -n "${SESSION_ID}" && -n "${PE_FAMILY}" && -n "${PE_TABLE}" ]]
}

assert_same_session_protection() {
  guest_exec python3 - "${SESSION_STATE}" "${SESSION_ID}" "${PE_FAMILY}" "${PE_TABLE}" <<'PY'
import json,sys
with open(sys.argv[1], encoding="utf-8") as handle:
    state=json.load(handle)
protection=state.get("protection") or {}
if state.get("session_id") != sys.argv[2] or state.get("intent") != "resume":
    raise SystemExit("Network Session identity or intent changed")
if protection.get("state") != "armed":
    raise SystemExit("Privacy Envelope is not armed")
if protection.get("family") != sys.argv[3] or protection.get("table") != sys.argv[4]:
    raise SystemExit("Privacy Envelope identity changed")
PY
}

assert_privacy_envelope_present() {
  guest_exec nft list table "${PE_FAMILY}" "${PE_TABLE}" >/dev/null 2>&1
}

prepare_direct_probe() {
  PROBE_IP="$(guest_exec getent ahostsv4 example.com | awk 'NR == 1 {print $1}')"
  [[ -n "${PROBE_IP}" ]]
}

assert_direct_uplink_blocked() {
  guest_exec ip link show dev "${GUEST_IF}" >/dev/null 2>&1 || return 1
  if guest_exec timeout 6 curl -4 -fsSk --interface "${GUEST_IF}" \
      --connect-timeout 3 --max-time 5 \
      --resolve "example.com:443:${PROBE_IP}" \
      https://example.com/ >/dev/null 2>&1; then
    return 1
  fi
}

assert_revalidated_active_authority() {
  capture_status
  guest_exec /bin/bash -lc "resolvectl dns >'${GUEST_PRIVATE}/resolved-dns.txt'; resolvectl domain >'${GUEST_PRIVATE}/resolved-domain.txt'; resolvectl default-route >'${GUEST_PRIVATE}/resolved-default-route.txt'; nft -j list ruleset >'${GUEST_PRIVATE}/nft-ruleset.json'"
  guest_exec python3 "${ACTIVE_AUTHORITY_HELPER}" \
    --status "${GUEST_PRIVATE}/status.json" \
    --transactions /run/podlaz/transactions \
    --session "${SESSION_STATE}" \
    --boot-id /proc/sys/kernel/random/boot_id \
    --runtime-config /run/podlaz/generated/xray.json \
    --resolved-dns "${GUEST_PRIVATE}/resolved-dns.txt" \
    --resolved-domain "${GUEST_PRIVATE}/resolved-domain.txt" \
    --resolved-default-route "${GUEST_PRIVATE}/resolved-default-route.txt" \
    --nft-ruleset "${GUEST_PRIVATE}/nft-ruleset.json"
  assert_same_session_protection
}

wait_for_marker() {
  local marker="$1" attempt
  for attempt in $(seq 1 1200); do
    if guest_exec test -f "${HOOK_DIR}/${marker}" >/dev/null 2>&1; then
      return 0
    fi
    [[ -n "${BASE_PID}" ]] && kill -0 "${BASE_PID}" >/dev/null 2>&1 || return 1
    sleep 0.1
  done
  return 1
}


wait_for_fault_health() {
  local expected_state="$1" expected_classification="$2" attempt
  for attempt in $(seq 1 40); do
    if capture_status >/dev/null 2>&1 && guest_exec python3 - "${GUEST_PRIVATE}/status.json" "${expected_state}" "${expected_classification}" <<'PY' >/dev/null 2>&1
import json,sys
with open(sys.argv[1], encoding="utf-8") as handle:
    payload=json.load(handle)
status=payload.get("status") or payload
health=status.get("tun_health") or {}
ok=(
    status.get("connection")=="active"
    and status.get("mode")=="tun"
    and health.get("state")==sys.argv[2]
    and health.get("classification")==sys.argv[3]
)
raise SystemExit(0 if ok else 1)
PY
    then
      return 0
    fi
    sleep 0.05
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

assert_protected_window() {
  assert_privacy_envelope_present
  assert_direct_uplink_blocked
  assert_same_session_protection
  assert_foreign_fixture
}

inject_fault() {
  case "${SCENARIO}" in
    provider-observation)
      guest_exec test ! -e "${HOOK_DIR}/reconciliation-soft-provider.trigger"
      guest_exec touch "${HOOK_DIR}/reconciliation-soft-provider.trigger"
      wait_for_marker reconciliation-soft-provider.injected
      wait_for_fault_health degraded connectivity_failed
      ;;
    resolved-unknown)
      guest_exec test ! -e "${HOOK_DIR}/reconciliation-resolved-unknown.trigger"
      guest_exec touch "${HOOK_DIR}/reconciliation-resolved-unknown.trigger"
      wait_for_marker reconciliation-resolved-unknown.injected
      wait_for_fault_health revalidating network_converging
      ;;
    route-replacement)
      guest_exec ip -4 route replace blackhole "${FOREIGN_ROUTE_B}" table "${FOREIGN_TABLE}"
      guest_exec ip -4 route del blackhole "${FOREIGN_ROUTE_A}" table "${FOREIGN_TABLE}"
      FOREIGN_EXPECTED_ROUTE="${FOREIGN_ROUTE_B}"
      ;;
    networkmanager-uplink)
      guest_exec nmcli connection down "${UPLINK_CONNECTION}" >/dev/null
      UPLINK_DOWN=true
      guest_exec nmcli -t -f NAME,DEVICE connection show --active | grep -Fx "${UPLINK_CONNECTION}:${GUEST_IF}" >/dev/null && return 1
      assert_protected_window
      guest_exec nmcli connection up "${UPLINK_CONNECTION}" >/dev/null
      UPLINK_DOWN=false
      wait_for_uplink_active
      ;;
  esac
}

run_scenario() {
  local candidate="$1"

  mark_failure diagnostic_unknown base.candidate_ready
  run_base_scenario "${candidate}"
  wait_for_control_ready candidate-ready || fail "base synthetic TUN did not reach candidate-ready boundary"
  grep -Fx 'candidate.provenance=pass' "${BASE_REPORT}" >/dev/null || fail "base candidate provenance did not pass"
  record_evidence candidate.positive_control pass

  mark_failure fixture reconciliation.fixture
  create_foreign_fixture || fail "could not create foreign network reconciliation fixture"
  install_reconciliation_override || fail "could not install supported reconciliation fault hook"
  release_control candidate-ready || fail "could not release candidate-ready boundary"

  mark_failure diagnostic_unknown base.verified_active
  wait_for_control_ready verified-active || fail "base synthetic TUN did not reach verified-active boundary"
  grep -Fx 'tun.verified_active=pass' "${BASE_REPORT}" >/dev/null || fail "base verified-active control did not pass"

  mark_failure product reconciliation.active_authority
  load_active_identity || fail "active Network Session/Privacy Envelope authority is invalid"
  prepare_direct_probe || fail "could not prepare direct-uplink leak probe"
  assert_protected_window || fail "active protection or foreign-state control is invalid before fault"

  mark_failure product "reconciliation.inject.${SCENARIO}"
  inject_fault || fail "hosted network reconciliation fault injection failed"
  record_evidence reconciliation.fault_injected pass

  mark_failure product "reconciliation.protected_window.${SCENARIO}"
  assert_protected_window || fail "Privacy Envelope or foreign state was lost during reconciliation"
  record_evidence privacy.envelope_retained pass
  record_evidence privacy.direct_uplink_blocked pass
  record_evidence foreign.state_preserved pass

  mark_failure product "reconciliation.converge.${SCENARIO}"
  wait_for_verified_active || fail "network reconciliation did not recover to verified-active within the bound"
  assert_revalidated_active_authority || fail "reconciled active authority is not exact"
  assert_privacy_envelope_present || fail "Privacy Envelope was not retained after reconciliation"
  assert_direct_uplink_blocked || fail "direct uplink bypassed Privacy Envelope after reconciliation"
  assert_foreign_fixture || fail "foreign network state changed after reconciliation"
  record_evidence reconciliation.verified_active pass

  mark_failure diagnostic_unknown base.active_completion
  release_control verified-active || fail "could not release verified-active boundary"
  wait_for_control_ready terminal-clean || fail "base synthetic TUN did not prove terminal cleanup"

  mark_failure product reconciliation.foreign_terminal
  assert_foreign_fixture || fail "Podlaz terminal cleanup changed foreign reconciliation fixture"

  mark_failure fixture reconciliation.fixture_cleanup
  remove_hook_override || fail "could not remove reconciliation fault override"
  cleanup_hook_dir || fail "could not remove reconciliation hook markers"
  cleanup_foreign_fixture

  mark_failure diagnostic_unknown base.complete
  release_control terminal-clean || fail "could not release terminal-clean boundary"
  wait_base_completion || fail "base synthetic TUN scenario failed after reconciliation"
  validate_base_control || fail "base hosted synthetic TUN report is not clean"
  record_evidence base.terminal_cleanup pass

  FAILURE_CLASS=none
  FAILURE_STEP=none
  assert_public_artifact_privacy || fail "hosted network reconciliation public evidence is not privacy-safe"
  record_evidence artifact.privacy pass
}

main() {
  (($# == 2)) || fail "usage: $0 SCENARIO CANDIDATE.deb"
  configure_scenario "$1"
  [[ -f "$2" && ! -L "$2" ]] || fail "candidate package must be a regular file"
  [[ "${EXPECTED_COMMIT}" =~ ^[0-9a-fA-F]{40}$ ]] || fail "PODLAZ_E2E_CANDIDATE_COMMIT must be an exact 40-hex commit"
  require_cmd awk bash chmod cmp curl find grep install ip nft python3 rm seq sleep sudo systemd-run timeout
  install -d -m 0700 "${E2E_TMP_ROOT}" "${E2E_ARTIFACT_DIR}"
  : >"${REPORT}"
  chmod 0600 "${REPORT}"
  trap cleanup EXIT INT TERM
  run_scenario "$2"
  trap - EXIT INT TERM
  finalize_report
  validate_report
}

if [[ "${1:-}" == validate-report ]]; then
  (($# == 2)) || fail "usage: $0 validate-report SCENARIO"
  configure_scenario "$2"
  validate_report
  exit 0
fi

main "$@"
