#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"

BASE_SCENARIO="${SCRIPT_DIR}/hosted-synthetic-tun.sh"
REPORT="${E2E_ARTIFACT_DIR}/hosted-daemon-recovery.txt"
MACHINE="podlaz-synthetic-tun"
GUEST_IF="host0"
FOREIGN_NFT_TABLE="pzsynt_foreign"
SESSION_STATE="/run/podlaz/network-session-continuation.json"
RESUME_DIAGNOSTIC="/run/podlaz/diagnostics/network-session-resume.json"
DAEMON_SOCKET="/run/podlaz/podlazd.sock"
RECOVERY_PRIVATE="/tmp/podlaz-hosted-daemon-recovery"
RESTART_OVERRIDE_DIR="/run/systemd/system/podlazd.service.d"
RESTART_OVERRIDE="${RESTART_OVERRIDE_DIR}/99-hosted-daemon-recovery.conf"
PRIVATE_ROOT="${E2E_TMP_ROOT}/hosted-daemon-recovery-private"
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
  daemon.crash_injected
  privacy.envelope_retained
  privacy.direct_uplink_blocked
  foreign.nft_preserved
  daemon.same_boot_resumed
  daemon.identity_replaced
  daemon.inactive_restart_clean
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
PROBE_IP=""
BOOT_ID=""

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
  [[ -f "${REPORT}" ]] || fail "hosted daemon recovery report is missing"
  python3 - "${REPORT}" <<'PY'
import sys

path = sys.argv[1]
required = {
    "candidate.positive_control",
    "daemon.crash_injected",
    "privacy.envelope_retained",
    "privacy.direct_uplink_blocked",
    "foreign.nft_preserved",
    "daemon.same_boot_resumed",
    "daemon.identity_replaced",
    "daemon.inactive_restart_clean",
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
    raise SystemExit("unexpected hosted daemon recovery report schema")
for key in required:
    if values[key] != "pass":
        raise SystemExit(f"required evidence is not pass: {key}={values[key]}")
if values["failure.class"] != "none" or values["failure.step"] != "none":
    raise SystemExit("hosted daemon recovery reports a failure")
PY
}

assert_public_artifact_privacy() {
  local extra
  extra="$(find "${E2E_ARTIFACT_DIR}" -mindepth 1 -maxdepth 1 ! -name 'hosted-daemon-recovery.txt' -print -quit)"
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

remove_restart_delay_override() {
  guest_exec /bin/bash -lc "rm -f '${RESTART_OVERRIDE}'; systemctl daemon-reload >/dev/null 2>&1 || true" >/dev/null 2>&1 || true
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

cleanup() {
  local code=$? attempt
  trap - EXIT INT TERM
  set +e
  remove_restart_delay_override
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

main_pid() {
  guest_exec systemctl show -p MainPID --value podlazd.service | tr -d '[:space:]'
}

process_start_ticks() {
  local pid="$1"
  # The awk program is intentionally literal and is evaluated inside the guest.
  # shellcheck disable=SC2016
  guest_exec awk '{print $22}' "/proc/${pid}/stat" | tr -d '[:space:]'
}

assert_daemon_replaced() {
  local old_pid="$1" old_start="$2" new_pid new_start current
  new_pid="$(main_pid)"
  [[ "${new_pid}" =~ ^[1-9][0-9]*$ ]] || return 1
  new_start="$(process_start_ticks "${new_pid}")" || return 1
  [[ "${new_pid}:${new_start}" != "${old_pid}:${old_start}" ]] || return 1
  if guest_exec test -r "/proc/${old_pid}/stat" >/dev/null 2>&1; then
    current="$(process_start_ticks "${old_pid}")" || return 1
    [[ "${current}" != "${old_start}" ]] || return 1
  fi
}

wait_original_daemon_absent() {
  local old_pid="$1" old_start="$2" attempt current
  for attempt in $(seq 1 100); do
    if ! guest_exec test -r "/proc/${old_pid}/stat" >/dev/null 2>&1; then
      return 0
    fi
    current="$(process_start_ticks "${old_pid}" 2>/dev/null || true)"
    [[ -n "${current}" && "${current}" != "${old_start}" ]] && return 0
    sleep 0.05
  done
  return 1
}

load_envelope_identity() {
  guest_exec python3 -c 'import json,re,sys
with open(sys.argv[1], encoding="utf-8") as handle:
    state=json.load(handle)
protection=state.get("protection") or {}
family=protection.get("family")
table=protection.get("table")
if protection.get("state") != "armed" or family != "inet" or not re.fullmatch(r"podlaz_pe_[0-9a-f]{12}(?:_[1-9][0-9]{0,2})?", str(table or "")):
    raise SystemExit(1)
print(f"{family} {table}")' "${SESSION_STATE}"
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

install_restart_delay_override() {
  guest_exec /bin/bash -lc "install -d -m 0755 '${RESTART_OVERRIDE_DIR}'; printf '[Service]\\nRestartSec=8s\\n' >'${RESTART_OVERRIDE}'; systemctl daemon-reload" >/dev/null
}

wait_for_status() {
  local target="$1" attempt
  guest_exec install -d -m 0700 "${RECOVERY_PRIVATE}" >/dev/null
  for attempt in $(seq 1 240); do
    if guest_exec /bin/bash -lc "curl --fail --silent --show-error --max-time 3 --unix-socket '${DAEMON_SOCKET}' http://localhost/v1/status >'${RECOVERY_PRIVATE}/status.json' 2>/dev/null && python3 '${STATUS_HELPER}' '${target}' '${RECOVERY_PRIVATE}/status.json' >/dev/null" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.5
  done
  return 1
}

wait_for_verified_active() {
  wait_for_status verified-active
}

wait_for_clean_inactive() {
  wait_for_status clean-inactive
}

classify_resume_failure() {
  local diagnosis
  if ! guest_exec systemctl is-active --quiet podlazd.service >/dev/null 2>&1; then
    FAILURE_STEP=daemon.same_boot_resume.service_inactive
  elif ! guest_exec test -S "${DAEMON_SOCKET}" >/dev/null 2>&1; then
    FAILURE_STEP=daemon.same_boot_resume.socket_unavailable
  elif ! guest_exec test -e "${SESSION_STATE}" >/dev/null 2>&1; then
    FAILURE_STEP=daemon.same_boot_resume.authority_missing
  else
    diagnosis="$(guest_exec python3 "${STATUS_HELPER}" diagnose-active "${RECOVERY_PRIVATE}/status.json" "${RESUME_DIAGNOSTIC}" 2>/dev/null | tr -d '[:space:]' || true)"
    if [[ "${diagnosis}" =~ ^[a-z0-9_.-]+$ ]]; then
      FAILURE_STEP="daemon.same_boot_resume.${diagnosis}"
    else
      FAILURE_STEP=daemon.same_boot_resume.status_not_verified
    fi
  fi
}

assert_revalidated_active_authority() {
  guest_exec /bin/bash -lc "curl --fail --silent --show-error --max-time 3 --unix-socket '${DAEMON_SOCKET}' http://localhost/v1/status >'${RECOVERY_PRIVATE}/status.json'"
  guest_exec /bin/bash -lc "resolvectl dns >'${RECOVERY_PRIVATE}/resolved-dns.txt'; resolvectl domain >'${RECOVERY_PRIVATE}/resolved-domain.txt'; resolvectl default-route >'${RECOVERY_PRIVATE}/resolved-default-route.txt'; nft -j list ruleset >'${RECOVERY_PRIVATE}/nft-ruleset.json'"
  guest_exec python3 "${ACTIVE_AUTHORITY_HELPER}" \
    --status "${RECOVERY_PRIVATE}/status.json" \
    --transactions /run/podlaz/transactions \
    --session "${SESSION_STATE}" \
    --boot-id /proc/sys/kernel/random/boot_id \
    --runtime-config /run/podlaz/generated/xray.json \
    --resolved-dns "${RECOVERY_PRIVATE}/resolved-dns.txt" \
    --resolved-domain "${RECOVERY_PRIVATE}/resolved-domain.txt" \
    --resolved-default-route "${RECOVERY_PRIVATE}/resolved-default-route.txt" \
    --nft-ruleset "${RECOVERY_PRIVATE}/nft-ruleset.json" >/dev/null
  # Expansion is intentionally evaluated by guest bash.
  # shellcheck disable=SC2016
  guest_exec /bin/bash -lc 'daemon="$(systemctl show -p MainPID --value podlazd.service)"; found=false; for pid in $(pgrep -P "$daemon" 2>/dev/null || true); do if [[ "$(readlink -f "/proc/${pid}/exe" 2>/dev/null || true)" == /usr/lib/podlaz/xray ]]; then found=true; fi; done; "$found"'
}

assert_inactive_authority_clean() {
  wait_for_clean_inactive || return 1
  guest_exec /bin/bash -lc "test ! -e '${SESSION_STATE}' && test ! -e /run/podlaz/generated/xray.json && ! ip link show dev podlaz0 >/dev/null 2>&1 && nft list tables >'${RECOVERY_PRIVATE}/nft-tables.txt' && ! grep -E 'table inet podlaz_pe_[0-9a-f]+' '${RECOVERY_PRIVATE}/nft-tables.txt' >/dev/null && python3 -c 'import glob,sys; raise SystemExit(1 if glob.glob(\"/run/podlaz/transactions/*.json\") else 0)'" || return 1
  # Expansion is intentionally evaluated by guest bash.
  # shellcheck disable=SC2016
  guest_exec /bin/bash -lc 'daemon="$(systemctl show -p MainPID --value podlazd.service)"; for pid in $(pgrep -P "$daemon" 2>/dev/null || true); do [[ "$(readlink -f "/proc/${pid}/exe" 2>/dev/null || true)" != /usr/lib/podlaz/xray ]] || exit 1; done' || return 1
  assert_foreign_sentinel
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

validate_base_positive_control() {
  env E2E_TMP_ROOT="${BASE_TMP_ROOT}" E2E_ARTIFACT_DIR="${BASE_ARTIFACT_DIR}" \
    bash "${BASE_SCENARIO}" validate-report >/dev/null
  grep -Fx 'candidate.provenance=pass' "${BASE_REPORT}" >/dev/null
  grep -Fx 'tun.terminal_cleanup=pass' "${BASE_REPORT}" >/dev/null
  grep -Fx 'tun.recovery_clean=pass' "${BASE_REPORT}" >/dev/null
  grep -Fx 'outer.cleanup=pass' "${BASE_REPORT}" >/dev/null
}

run_scenario() {
  local candidate="$1" old_pid old_start inactive_pid inactive_start identity current_boot

  mark_failure diagnostic_unknown base.candidate_ready
  run_base_scenario "${candidate}"
  wait_for_control_ready candidate-ready || fail "base synthetic TUN did not reach candidate-ready control boundary"
  grep -Fx 'candidate.provenance=pass' "${BASE_REPORT}" >/dev/null || fail "base candidate provenance did not pass before inactive restart"

  mark_failure product daemon.inactive_restart
  BOOT_ID="$(guest_exec cat /proc/sys/kernel/random/boot_id | tr -d '[:space:]')"
  [[ -n "${BOOT_ID}" ]] || fail "current guest boot identity is unavailable"
  assert_inactive_authority_clean || fail "candidate-ready daemon already has unexpected Podlaz authority"
  inactive_pid="$(main_pid)"
  [[ "${inactive_pid}" =~ ^[1-9][0-9]*$ ]] || fail "inactive daemon MainPID is unavailable"
  inactive_start="$(process_start_ticks "${inactive_pid}")"
  guest_exec systemctl restart podlazd.service
  assert_inactive_authority_clean || fail "inactive daemon restart fabricated Podlaz authority"
  current_boot="$(guest_exec cat /proc/sys/kernel/random/boot_id | tr -d '[:space:]')"
  [[ "${current_boot}" == "${BOOT_ID}" ]] || fail "inactive daemon restart unexpectedly crossed a boot boundary"
  assert_daemon_replaced "${inactive_pid}" "${inactive_start}" || fail "inactive daemon restart did not replace process identity"
  record_evidence daemon.inactive_restart_clean pass
  release_control candidate-ready || fail "could not release candidate-ready control boundary"

  mark_failure diagnostic_unknown base.verified_active
  wait_for_control_ready verified-active || fail "base synthetic TUN did not reach verified-active control boundary"
  grep -Fx 'candidate.provenance=pass' "${BASE_REPORT}" >/dev/null || fail "base candidate provenance did not pass before fault injection"
  grep -Fx 'tun.verified_active=pass' "${BASE_REPORT}" >/dev/null || fail "base active authority did not pass before fault injection"

  mark_failure product daemon.active_authority
  read -r PE_FAMILY PE_TABLE <<<"$(load_envelope_identity)"
  [[ -n "${PE_FAMILY}" && -n "${PE_TABLE}" ]] || fail "active Network Session lacks exact Privacy Envelope authority"
  assert_privacy_envelope_present || fail "exact Privacy Envelope is absent before daemon crash"
  assert_foreign_sentinel || fail "foreign nft sentinel is absent before daemon crash"
  prepare_direct_probe || fail "could not prepare direct-uplink leak probe"
  old_pid="$(main_pid)"
  [[ "${old_pid}" =~ ^[1-9][0-9]*$ ]] || fail "active daemon MainPID is unavailable"
  old_start="$(process_start_ticks "${old_pid}")"
  install_restart_delay_override

  mark_failure product daemon.crash
  guest_exec systemctl kill --kill-who=main -s KILL podlazd.service
  wait_original_daemon_absent "${old_pid}" "${old_start}" || fail "killed daemon identity remained alive"
  record_evidence daemon.crash_injected pass

  mark_failure product daemon.fail_closed_window
  identity="$(load_envelope_identity)" || fail "Network Session authority disappeared during daemon restart window"
  [[ "${identity}" == "${PE_FAMILY} ${PE_TABLE}" ]] || fail "Privacy Envelope authority identity changed during daemon restart window"
  assert_privacy_envelope_present || fail "Privacy Envelope disappeared during daemon restart window"
  record_evidence privacy.envelope_retained pass
  assert_direct_uplink_blocked || fail "ordinary uplink escaped while daemon was unavailable"
  record_evidence privacy.direct_uplink_blocked pass
  assert_foreign_sentinel || fail "foreign nft state changed during daemon restart window"
  record_evidence foreign.nft_preserved pass

  mark_failure product daemon.same_boot_resume
  if ! wait_for_verified_active; then
    classify_resume_failure
    fail "same-boot daemon restart did not converge to verified-active"
  fi
  assert_revalidated_active_authority || fail "resumed generation lacks exact active authority"
  current_boot="$(guest_exec cat /proc/sys/kernel/random/boot_id | tr -d '[:space:]')"
  [[ "${current_boot}" == "${BOOT_ID}" ]] || fail "daemon recovery unexpectedly crossed a boot boundary"
  identity="$(load_envelope_identity)" || fail "resumed Network Session lost Privacy Envelope authority"
  [[ "${identity}" == "${PE_FAMILY} ${PE_TABLE}" ]] || fail "resumed Privacy Envelope identity differs from pre-crash authority"
  assert_foreign_sentinel || fail "foreign nft state changed after daemon resume"
  record_evidence daemon.same_boot_resumed pass
  assert_daemon_replaced "${old_pid}" "${old_start}" || fail "daemon process identity was not replaced"
  record_evidence daemon.identity_replaced pass
  remove_restart_delay_override

  mark_failure diagnostic_unknown base.active_positive_control
  release_control verified-active || fail "could not release verified-active control boundary"
  wait_for_control_ready terminal-clean || fail "base synthetic TUN did not reach exact terminal cleanup boundary"
  grep -Fx 'tun.terminal_cleanup=pass' "${BASE_REPORT}" >/dev/null || fail "base exact terminal cleanup did not pass"

  mark_failure diagnostic_unknown base.complete
  release_control terminal-clean || fail "could not release terminal-clean control boundary"
  wait_base_completion || fail "base synthetic TUN scenario failed after daemon recovery injection"
  validate_base_positive_control || fail "base synthetic TUN positive-control report is not clean"
  record_evidence candidate.positive_control pass
  record_evidence base.terminal_cleanup pass

  FAILURE_CLASS=none
  FAILURE_STEP=none
  assert_public_artifact_privacy || fail "hosted daemon recovery public evidence is not privacy-safe"
  record_evidence artifact.privacy pass
}

main() {
  (($# == 1)) || fail "usage: $0 CANDIDATE.deb"
  [[ -f "$1" && ! -L "$1" ]] || fail "candidate package must be a regular file"
  [[ "${EXPECTED_COMMIT}" =~ ^[0-9a-fA-F]{40}$ ]] || fail "PODLAZ_E2E_CANDIDATE_COMMIT must be an exact 40-hex commit"
  require_cmd awk bash chmod find grep install kill python3 rm seq sleep sudo systemd-run tr
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