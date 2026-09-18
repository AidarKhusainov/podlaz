#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"

BASE_SCENARIO="${SCRIPT_DIR}/hosted-synthetic-tun.sh"
REPORT="${E2E_ARTIFACT_DIR}/hosted-xray-replacement.txt"
MACHINE="podlaz-synthetic-tun"
GUEST_IF="host0"
FOREIGN_NFT_TABLE="pzsynt_foreign"
SESSION_STATE="/run/podlaz/network-session-continuation.json"
DAEMON_SOCKET="/run/podlaz/podlazd.sock"
TUN_DIAGNOSTIC="/run/podlaz/diagnostics/tun-last.json"
XRAY_HOOK_DIR="/run/podlaz-hosted-xray-replacement-hooks"
OVERRIDE_DIR="/run/systemd/system/podlazd.service.d"
OVERRIDE_PATH="${OVERRIDE_DIR}/99-hosted-xray-replacement.conf"
XRAY_PRIVATE="/tmp/podlaz-hosted-xray-replacement"
PRIVATE_ROOT="${E2E_TMP_ROOT}/hosted-xray-replacement-private"
BASE_TMP_ROOT="${PRIVATE_ROOT}/base-private"
BASE_ARTIFACT_DIR="${PRIVATE_ROOT}/base-public"
BASE_REPORT="${BASE_ARTIFACT_DIR}/hosted-synthetic-tun.txt"
BASE_STDOUT="${PRIVATE_ROOT}/base.stdout"
BASE_STDERR="${PRIVATE_ROOT}/base.stderr"
CONTROL_DIR="${BASE_TMP_ROOT}/control"
ACTIVE_AUTHORITY_HELPER="/workspace/scripts/e2e/hosted_synthetic_active_authority.py"
STATUS_HELPER="/workspace/scripts/e2e/lib/daemon_status_semantics.py"
EXPECTED_COMMIT="${PODLAZ_E2E_CANDIDATE_COMMIT:-}"

EVIDENCE_KEYS=(
  candidate.positive_control
  xray.crash_injected
  privacy.envelope_retained
  privacy.direct_uplink_blocked
  foreign.nft_preserved
  xray.generation_replaced
  xray.same_session_verified
  base.terminal_cleanup
  artifact.privacy
)
FAILURE_CLASS=diagnostic_unknown
FAILURE_STEP=bootstrap
REPORT_FINALIZED=false
BASE_PID=""
BASE_EXIT_CODE=""
PE_FAMILY=""
PE_TABLE=""
SESSION_ID=""
BOOT_ID=""
PROBE_IP=""

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
  [[ -f "${REPORT}" ]] || fail "hosted Xray replacement report is missing"
  python3 - "${REPORT}" <<'PY'
import sys

path = sys.argv[1]
required = {
    "candidate.positive_control",
    "xray.crash_injected",
    "privacy.envelope_retained",
    "privacy.direct_uplink_blocked",
    "foreign.nft_preserved",
    "xray.generation_replaced",
    "xray.same_session_verified",
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
    raise SystemExit("unexpected hosted Xray replacement report schema")
for key in required:
    if values[key] != "pass":
        raise SystemExit(f"required evidence is not pass: {key}={values[key]}")
if values["failure.class"] != "none" or values["failure.step"] != "none":
    raise SystemExit("hosted Xray replacement reports a failure")
PY
}

assert_public_artifact_privacy() {
  local extra
  extra="$(find "${E2E_ARTIFACT_DIR}" -mindepth 1 -maxdepth 1 ! -name 'hosted-xray-replacement.txt' -print -quit)"
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
  for phase in guest-ready verified-active terminal-clean; do
    continue="${CONTROL_DIR}/${phase}.continue"
    [[ -e "${continue}" ]] || printf 'continue\n' >"${continue}"
    chmod 0600 "${continue}" >/dev/null 2>&1 || true
  done
}

release_rebuild_pause() {
  guest_exec /bin/bash -lc "test ! -e '${XRAY_HOOK_DIR}/reconciliation-rebuild.ready' || touch '${XRAY_HOOK_DIR}/reconciliation-rebuild.continue'" >/dev/null 2>&1 || true
}

release_rollback_pause() {
  guest_exec /bin/bash -lc "rm -f '${XRAY_HOOK_DIR}/rollback-pause.arm'; test ! -e '${XRAY_HOOK_DIR}/rollback-pause.ready' || touch '${XRAY_HOOK_DIR}/rollback-pause.continue'" >/dev/null 2>&1
}

cleanup() {
  local code=$? attempt
  trap - EXIT INT TERM
  set +e
  release_rebuild_pause
  release_rollback_pause || true
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
  local phase ready attempt code
  phase="$1"
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
  local phase ready continue attempt
  phase="$1"
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

install_rebuild_override() {
  guest_exec /bin/bash -lc "install -d -m 0755 '${OVERRIDE_DIR}'; install -d -m 0700 '${XRAY_HOOK_DIR}'; printf '%s\n' '[Service]' 'Environment=PODLAZ_E2E_TUN_TERMINAL_FAILURE=true' 'Environment=PODLAZ_E2E_TUN_TERMINAL_FAILURE_DIR=${XRAY_HOOK_DIR}' 'Environment=PODLAZ_E2E_TUN_RECONCILIATION_REBUILD_PAUSE=true' 'Environment=PODLAZ_E2E_TUN_ROLLBACK_PAUSE=true' 'Environment=PODLAZ_E2E_TUN_ROLLBACK_PAUSE_DIR=${XRAY_HOOK_DIR}' 'Environment=PODLAZ_E2E_TUN_HOOK_TIMEOUT_SECONDS=180' >'${OVERRIDE_PATH}'"
}

load_session_identity() {
  guest_exec python3 -c 'import json,re,sys
with open(sys.argv[1], encoding="utf-8") as handle:
    state=json.load(handle)
protection=state.get("protection") or {}
session_id=str(state.get("session_id") or "")
boot_id=str(state.get("boot_id") or "")
family=protection.get("family")
table=protection.get("table")
if state.get("intent") != "resume" or not re.fullmatch(r"[0-9a-f]{32}", session_id) or not boot_id:
    raise SystemExit(1)
if protection.get("state") != "armed" or family != "inet" or not re.fullmatch(r"podlaz_pe_[0-9a-f]{12}(?:_[1-9][0-9]{0,2})?", str(table or "")):
    raise SystemExit(1)
print(session_id, boot_id, family, table)' "${SESSION_STATE}"
}

assert_privacy_envelope_present() {
  guest_exec nft list table "${PE_FAMILY}" "${PE_TABLE}" >/dev/null 2>&1
}

assert_foreign_sentinel() {
  guest_exec nft list table inet "${FOREIGN_NFT_TABLE}" >/dev/null 2>&1
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

tracked_xray_identity() {
  guest_exec python3 -c 'import glob,json,os,sys
matches=[]
for path in glob.glob(os.path.join(sys.argv[1], "*.json")):
    try:
        with open(path, encoding="utf-8") as handle:
            tx=json.load(handle)
    except Exception:
        continue
    if tx.get("owner") != "podlaz" or tx.get("state") != "committed":
        continue
    tx_id=str(tx.get("id") or "")
    for child in (tx.get("rollback") or {}).get("child_processes") or []:
        pid=child.get("pid")
        start=str(child.get("start_time") or "")
        if child.get("owner") == "podlaz" and child.get("label") == "xray" and isinstance(pid,int) and pid > 1 and start.isdigit():
            matches.append((pid,start,tx_id))
if len(matches) != 1:
    raise SystemExit(1)
pid,start,tx_id=matches[0]
print(pid,start,tx_id)' /run/podlaz/transactions
}

process_start_ticks() {
  local pid="$1"
  # The awk program is intentionally literal and is evaluated inside the guest.
  # shellcheck disable=SC2016
  guest_exec awk '{print $22}' "/proc/${pid}/stat" | tr -d '[:space:]'
}

assert_exact_xray_identity() {
  local pid="$1" start="$2" current exe
  guest_exec test -r "/proc/${pid}/stat" >/dev/null 2>&1 || return 1
  current="$(process_start_ticks "${pid}")" || return 1
  [[ "${current}" == "${start}" ]] || return 1
  exe="$(guest_exec readlink -f "/proc/${pid}/exe" | tr -d '[:space:]')" || return 1
  [[ "${exe}" == /usr/lib/podlaz/xray ]]
}

wait_original_xray_absent() {
  local pid="$1" start="$2" attempt current
  for attempt in $(seq 1 100); do
    if ! guest_exec test -r "/proc/${pid}/stat" >/dev/null 2>&1; then
      return 0
    fi
    current="$(process_start_ticks "${pid}" 2>/dev/null || true)"
    [[ -n "${current}" && "${current}" != "${start}" ]] && return 0
    sleep 0.05
  done
  return 1
}

wait_for_marker() {
  local marker="$1" attempt
  for attempt in $(seq 1 2400); do
    guest_exec test -f "${XRAY_HOOK_DIR}/${marker}" >/dev/null 2>&1 && return 0
    [[ -n "${BASE_PID}" ]] && kill -0 "${BASE_PID}" >/dev/null 2>&1 || return 1
    sleep 0.1
  done
  return 1
}

arm_rollback_pause() {
  guest_exec /bin/bash -lc "rm -f '${XRAY_HOOK_DIR}/rollback-pause.ready' '${XRAY_HOOK_DIR}/rollback-pause.continue'; touch '${XRAY_HOOK_DIR}/rollback-pause.arm'; chmod 0600 '${XRAY_HOOK_DIR}/rollback-pause.arm'"
}

diagnose_rollback_pause() {
  guest_exec python3 -c 'import glob,json,os,re,subprocess,sys
matches=[]
for path in glob.glob("/run/podlaz/transactions/*.json"):
    try:
        with open(path, encoding="utf-8") as handle:
            tx=json.load(handle)
    except Exception:
        continue
    if tx.get("owner")=="podlaz" and tx.get("mode")=="tun" and tx.get("state")=="rolling_back":
        matches.append(tx)
if len(matches)!=1:
    print("pre-rollback.authority-unavailable")
    raise SystemExit(0)
tx=matches[0]
desired=tx.get("desired_plan") or {}
address=desired.get("tun_address") or {}
if not (address.get("interface_name")=="podlaz0" and isinstance(address.get("link_index"),int) and address.get("link_index")>0 and address.get("link_kind")=="tun" and address.get("appeared_after_core") is True and address.get("owner")=="podlaz:tun-address"):
    print("pre-rollback.bound-address-invalid")
    raise SystemExit(0)
steps=tx.get("applied_steps") or []
allowed={"tun-address","route","policy-rule","dns","nftables"}
if not isinstance(steps,list) or any(not isinstance(step,dict) for step in steps):
    print("pre-rollback.steps-invalid")
    raise SystemExit(0)
kinds=[]
for step in steps:
    kind=str(step.get("kind") or "")
    owner=str(step.get("owner") or "")
    if kind not in allowed or not owner.startswith("podlaz:"):
        print("pre-rollback.steps-invalid")
        raise SystemExit(0)
    if kind not in kinds:
        kinds.append(kind)
step_token="steps-none" if not kinds else "steps-"+"-".join(kinds)
tun=desired.get("tun") or {}
name=str(tun.get("interface_name") or "")
planned_mtu=tun.get("mtu")
if name!="podlaz0" or not isinstance(planned_mtu,int) or planned_mtu<=0:
    print("pre-rollback.bound-address."+step_token+".live-link.desired-invalid")
    raise SystemExit(0)
result=subprocess.run(["ip","-details","-o","link","show","dev",name], capture_output=True, text=True, check=False)
if result.returncode!=0:
    print("pre-rollback.bound-address."+step_token+".live-link.unavailable")
    raise SystemExit(0)
text=result.stdout.strip()
fields=text.split()
try:
    current_index=int(fields[0].rstrip(":"))
except (ValueError,IndexError):
    current_index=0
ifindex_token="ifindex-match" if current_index==address.get("link_index") else "ifindex-mismatch"
kind_token="tun" if any(fields[i]=="tun" and i+2<len(fields) and fields[i+1]=="type" and fields[i+2]=="tun" for i in range(len(fields))) else "not-tun"
current_mtu=0
for i,field in enumerate(fields[:-1]):
    if field=="mtu":
        try:
            current_mtu=int(fields[i+1])
        except ValueError:
            current_mtu=0
        break
mtu_token="mtu-match" if current_mtu==planned_mtu else "mtu-mismatch"
first=text.splitlines()[0] if text else ""
flags_match=re.search(r"<([^>]*)>", first)
flags={part.strip() for part in (flags_match.group(1).split(",") if flags_match else [])}
up_token="up" if "UP" in flags or "state UP" in first else "down"
trace_dir=sys.argv[1]
trace_events=[
    ("dnsvalid","dnsaware-validate-passed"),
    ("basevalid","tun-base-validate-passed"),
    ("prestart","tun-preapply-started"),
    ("prepass","tun-preapply-passed"),
    ("prefail","tun-preapply-failed"),
    ("addrstart","tun-address-apply-started"),
    ("addrpass","tun-address-apply-passed"),
    ("addrfail","tun-address-apply-failed"),
]
trace="-".join(label+("1" if os.path.isfile(os.path.join(trace_dir,"apply-trace."+event)) else "0") for label,event in trace_events)
print("pre-rollback.bound-address."+step_token+".live-link."+kind_token+"."+ifindex_token+"."+mtu_token+"."+up_token+".trace-"+trace)' "${XRAY_HOOK_DIR}" | tr -d '[:space:]'
}

wait_for_rolling_back_absent() {
  local attempt
  for attempt in $(seq 1 100); do
    if guest_exec python3 -c 'import glob,json
for path in glob.glob("/run/podlaz/transactions/*.json"):
    try:
        with open(path, encoding="utf-8") as handle:
            tx=json.load(handle)
    except Exception:
        continue
    if tx.get("owner")=="podlaz" and tx.get("mode")=="tun" and tx.get("state")=="rolling_back":
        raise SystemExit(1)
raise SystemExit(0)' >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.2
  done
  return 1
}

diagnose_rebuild_failure() {
  local diagnosis
  guest_exec install -d -m 0700 "${XRAY_PRIVATE}" >/dev/null 2>&1 || true
  if ! guest_exec /bin/bash -lc "curl --fail --silent --show-error --max-time 3 --unix-socket '${DAEMON_SOCKET}' http://localhost/v1/status >'${XRAY_PRIVATE}/status.json' 2>/dev/null"; then
    printf 'status-unavailable\n'
    return 0
  fi
  diagnosis="$(guest_exec python3 "${STATUS_HELPER}" diagnose-rebuild "${XRAY_PRIVATE}/status.json" "${TUN_DIAGNOSTIC}" 2>/dev/null | tr -d '[:space:]' || true)"
  if [[ -z "${diagnosis}" || ! "${diagnosis}" =~ ^[a-z0-9_.-]+$ ]]; then
    diagnosis="diagnosis-unavailable"
  fi
  printf '%s\n' "${diagnosis}"
}

wait_for_verified_active() {
  local attempt
  guest_exec install -d -m 0700 "${XRAY_PRIVATE}" >/dev/null
  for attempt in $(seq 1 240); do
    if guest_exec /bin/bash -lc "curl --fail --silent --show-error --max-time 3 --unix-socket '${DAEMON_SOCKET}' http://localhost/v1/status >'${XRAY_PRIVATE}/status.json' 2>/dev/null && python3 '${STATUS_HELPER}' verified-active '${XRAY_PRIVATE}/status.json' >/dev/null" >/dev/null 2>&1; then
      return 0
    fi
    if guest_exec test -f "${XRAY_HOOK_DIR}/rollback-pause.ready" >/dev/null 2>&1; then
      return 2
    fi
    sleep 0.5
  done
  return 1
}

assert_revalidated_active_authority() {
  guest_exec /bin/bash -lc "curl --fail --silent --show-error --max-time 3 --unix-socket '${DAEMON_SOCKET}' http://localhost/v1/status >'${XRAY_PRIVATE}/status.json'"
  guest_exec /bin/bash -lc "resolvectl dns >'${XRAY_PRIVATE}/resolved-dns.txt'; resolvectl domain >'${XRAY_PRIVATE}/resolved-domain.txt'; resolvectl default-route >'${XRAY_PRIVATE}/resolved-default-route.txt'; nft -j list ruleset >'${XRAY_PRIVATE}/nft-ruleset.json'"
  guest_exec python3 "${ACTIVE_AUTHORITY_HELPER}" \
    --status "${XRAY_PRIVATE}/status.json" \
    --transactions /run/podlaz/transactions \
    --session "${SESSION_STATE}" \
    --boot-id /proc/sys/kernel/random/boot_id \
    --runtime-config /run/podlaz/generated/xray.json \
    --resolved-dns "${XRAY_PRIVATE}/resolved-dns.txt" \
    --resolved-domain "${XRAY_PRIVATE}/resolved-domain.txt" \
    --resolved-default-route "${XRAY_PRIVATE}/resolved-default-route.txt" \
    --nft-ruleset "${XRAY_PRIVATE}/nft-ruleset.json" >/dev/null
  assert_foreign_sentinel
}

assert_xray_replaced() {
  local old_pid="$1" old_start="$2" new_pid new_start tx_id current
  read -r new_pid new_start tx_id <<<"$(tracked_xray_identity)" || return 1
  [[ "${new_pid}" =~ ^[1-9][0-9]*$ && "${new_start}" =~ ^[0-9]+$ && -n "${tx_id}" ]] || return 1
  [[ "${new_pid}:${new_start}" != "${old_pid}:${old_start}" ]] || return 1
  assert_exact_xray_identity "${new_pid}" "${new_start}" || return 1
  if guest_exec test -r "/proc/${old_pid}/stat" >/dev/null 2>&1; then
    current="$(process_start_ticks "${old_pid}")" || return 1
    [[ "${current}" != "${old_start}" ]] || return 1
  fi
}

assert_same_session() {
  local identity current_session current_boot current_family current_table
  identity="$(load_session_identity)" || return 1
  read -r current_session current_boot current_family current_table <<<"${identity}"
  [[ "${current_session}" == "${SESSION_ID}" ]] || return 1
  [[ "${current_boot}" == "${BOOT_ID}" ]] || return 1
  [[ "${current_family}" == "${PE_FAMILY}" ]] || return 1
  [[ "${current_table}" == "${PE_TABLE}" ]] || return 1
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
    PODLAZ_E2E_HOSTED_CONTROL_PHASES="guest-ready verified-active terminal-clean" \
    PODLAZ_E2E_HOSTED_CONTROL_TIMEOUT_SECONDS=180 \
    bash "${BASE_SCENARIO}" "${candidate}" >"${BASE_STDOUT}" 2>"${BASE_STDERR}" &
  BASE_PID=$!
}

validate_base_positive_control() {
  env E2E_TMP_ROOT="${BASE_TMP_ROOT}" E2E_ARTIFACT_DIR="${BASE_ARTIFACT_DIR}" \
    bash "${BASE_SCENARIO}" validate-report >/dev/null
  grep -Fx 'candidate.provenance=pass' "${BASE_REPORT}" >/dev/null
  grep -Fx 'tun.verified_active=pass' "${BASE_REPORT}" >/dev/null
  grep -Fx 'tun.terminal_cleanup=pass' "${BASE_REPORT}" >/dev/null
  grep -Fx 'tun.recovery_clean=pass' "${BASE_REPORT}" >/dev/null
  grep -Fx 'outer.cleanup=pass' "${BASE_REPORT}" >/dev/null
}

run_scenario() {
  local candidate="$1" identity old_pid old_start old_tx outcome pre_rollback diagnosis

  mark_failure diagnostic_unknown base.guest_ready
  run_base_scenario "${candidate}"
  wait_for_control_ready guest-ready || fail "base synthetic TUN did not reach guest-ready control boundary"

  mark_failure fixture xray.rebuild_hook
  install_rebuild_override || fail "could not install Xray rebuild E2E override before daemon start"
  release_control guest-ready || fail "could not release guest-ready control boundary"

  mark_failure diagnostic_unknown base.verified_active
  wait_for_control_ready verified-active || fail "base synthetic TUN did not reach verified-active control boundary"
  grep -Fx 'candidate.provenance=pass' "${BASE_REPORT}" >/dev/null || fail "base candidate provenance did not pass before Xray fault injection"
  grep -Fx 'tun.verified_active=pass' "${BASE_REPORT}" >/dev/null || fail "base active authority did not pass before Xray fault injection"

  mark_failure product xray.active_authority
  identity="$(load_session_identity)" || fail "active Network Session lacks exact privacy authority"
  read -r SESSION_ID BOOT_ID PE_FAMILY PE_TABLE <<<"${identity}"
  assert_privacy_envelope_present || fail "exact Privacy Envelope is absent before Xray crash"
  assert_foreign_sentinel || fail "foreign nft sentinel is absent before Xray crash"
  prepare_direct_probe || fail "could not prepare direct-uplink leak probe"
  read -r old_pid old_start old_tx <<<"$(tracked_xray_identity)" || fail "active transaction lacks one exact persisted Xray child"
  [[ "${old_pid}" =~ ^[1-9][0-9]*$ && "${old_start}" =~ ^[0-9]+$ && -n "${old_tx}" ]] || fail "persisted Xray identity is incomplete"
  assert_exact_xray_identity "${old_pid}" "${old_start}" || fail "persisted Xray identity does not match the live child"

  mark_failure product xray.crash
  guest_exec kill -KILL "${old_pid}"
  wait_original_xray_absent "${old_pid}" "${old_start}" || fail "killed Xray identity remained alive"
  record_evidence xray.crash_injected pass

  mark_failure product xray.rebuild_window
  wait_for_marker reconciliation-rebuild.ready || fail "Xray reconciliation did not reach the rebuild pause"
  assert_privacy_envelope_present || fail "Privacy Envelope disappeared during Xray rebuild"
  record_evidence privacy.envelope_retained pass
  assert_direct_uplink_blocked || fail "ordinary uplink escaped during Xray rebuild"
  record_evidence privacy.direct_uplink_blocked pass
  assert_foreign_sentinel || fail "foreign nft state changed during Xray rebuild"
  record_evidence foreign.nft_preserved pass
  assert_same_session || fail "Network Session identity changed during Xray rebuild"

  mark_failure product xray.rebuild_resume
  arm_rollback_pause || fail "could not arm bounded Xray rollback diagnosis"
  guest_exec touch "${XRAY_HOOK_DIR}/reconciliation-rebuild.continue"
  if wait_for_verified_active; then
    guest_exec rm -f "${XRAY_HOOK_DIR}/rollback-pause.arm"
  else
    outcome=$?
    if (( outcome == 2 )); then
      pre_rollback="$(diagnose_rollback_pause 2>/dev/null || true)"
      [[ "${pre_rollback}" =~ ^pre-rollback\.[a-z0-9.-]+$ ]] || pre_rollback="pre-rollback.diagnosis-unavailable"
      release_rollback_pause || fail "could not release bounded Xray rollback diagnosis"
      wait_for_rolling_back_absent || fail "Xray rebuild rollback did not leave rolling_back state"
      diagnosis="$(diagnose_rebuild_failure)"
      mark_failure product "xray.rebuild_resume.${pre_rollback}.${diagnosis}"
    else
      diagnosis="$(diagnose_rebuild_failure)"
      mark_failure product "xray.rebuild_resume.${diagnosis}"
    fi
    fail "Xray rebuild did not converge to verified-active"
  fi
  assert_revalidated_active_authority || fail "rebuilt Xray generation lacks exact active authority"
  assert_xray_replaced "${old_pid}" "${old_start}" || fail "Xray rebuild did not publish a fresh tracked generation"
  record_evidence xray.generation_replaced pass
  assert_same_session || fail "rebuilt generation did not retain the same current-boot Network Session"
  assert_privacy_envelope_present || fail "rebuilt generation lost the exact Privacy Envelope"
  assert_foreign_sentinel || fail "foreign nft state changed after Xray rebuild"
  record_evidence xray.same_session_verified pass

  mark_failure diagnostic_unknown base.active_positive_control
  release_control verified-active || fail "could not release verified-active control boundary"
  wait_for_control_ready terminal-clean || fail "base synthetic TUN did not reach exact terminal cleanup boundary"
  grep -Fx 'tun.terminal_cleanup=pass' "${BASE_REPORT}" >/dev/null || fail "base exact terminal cleanup did not pass"
  assert_foreign_sentinel || fail "foreign nft state changed before terminal-clean handoff"
  record_evidence base.terminal_cleanup pass

  mark_failure diagnostic_unknown base.complete
  release_control terminal-clean || fail "could not release terminal-clean control boundary"
  wait_base_completion || fail "base synthetic TUN scenario failed after Xray replacement injection"
  validate_base_positive_control || fail "base synthetic TUN positive-control report is not clean"
  record_evidence candidate.positive_control pass

  FAILURE_CLASS=none
  FAILURE_STEP=none
  assert_public_artifact_privacy || fail "hosted Xray replacement public evidence is not privacy-safe"
  record_evidence artifact.privacy pass
}

main() {
  (($# == 1)) || fail "usage: $0 CANDIDATE.deb"
  [[ -f "$1" && ! -L "$1" ]] || fail "candidate package must be a regular file"
  [[ "${EXPECTED_COMMIT}" =~ ^[0-9a-fA-F]{40}$ ]] || fail "PODLAZ_E2E_CANDIDATE_COMMIT must be an exact 40-hex commit"
  require_cmd awk bash chmod find grep install kill python3 readlink rm seq sleep sudo systemd-run timeout tr
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
