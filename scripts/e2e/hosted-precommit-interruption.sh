#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"

BASE_SCENARIO="${SCRIPT_DIR}/hosted-synthetic-tun.sh"
REPORT="${E2E_ARTIFACT_DIR}/hosted-precommit-interruption.txt"
MACHINE="podlaz-synthetic-tun"
GUEST_XDG="/home/e2e/.local/share/podlaz-hosted-synthetic-tun"
FOCUSED_GUEST_PRIVATE="/tmp/podlaz-hosted-precommit-interruption"
DAEMON_SOCKET="/run/podlaz/podlazd.sock"
STATUS_HELPER="/workspace/scripts/e2e/lib/daemon_status_semantics.py"
HOOK_DIR="/run/podlaz/e2e-hosted-precommit-interruption"
HOOK_DROPIN_DIR="/run/systemd/system/podlazd.service.d"
HOOK_DROPIN="${HOOK_DROPIN_DIR}/99-hosted-precommit-interruption.conf"
HOOK_READY="${HOOK_DIR}/before-commit-pause.ready"
RECOVERY_RULE="/etc/polkit-1/rules.d/50-podlaz-hosted-precommit-recovery.rules"

PRIVATE_ROOT="${E2E_TMP_ROOT}/hosted-precommit-private"
BASE_TMP_ROOT="${PRIVATE_ROOT}/base-private"
BASE_ARTIFACT_DIR="${PRIVATE_ROOT}/base-public"
BASE_REPORT="${BASE_ARTIFACT_DIR}/hosted-synthetic-tun.txt"
BASE_STDOUT="${PRIVATE_ROOT}/base.stdout"
BASE_STDERR="${PRIVATE_ROOT}/base.stderr"
CONTROL_DIR="${BASE_TMP_ROOT}/control"
BASE_GUEST_ROOT="${BASE_TMP_ROOT}/system-guest"
BASE_GUEST_BASELINE="${BASE_TMP_ROOT}/private/guest-baseline"
EXPECTED_COMMIT="${PODLAZ_E2E_CANDIDATE_COMMIT:-}"

EVIDENCE_KEYS=(
  hook.precommit_reached
  connect.interrupted_not_success
  precommit.no_network_mutation
  restart.no_false_resume_authority
  recovery.exact_transaction_only
  foreign.state_preserved
  terminal.clean
  hidden_reconnect.absent
  base.outer_cleanup
  artifact.privacy
)

FAILURE_CLASS=diagnostic_unknown
FAILURE_STEP=bootstrap
REPORT_FINALIZED=false
BASE_PID=""
RECOVERY_RULE_INSTALLED=false
HOOK_INSTALLED=false

record_evidence() { printf '%s=%s\n' "$1" "$2" >>"${REPORT}"; }
evidence_recorded() { grep -Eq "^${1}=" "${REPORT}" 2>/dev/null; }
record_if_missing() { evidence_recorded "$1" || record_evidence "$1" "$2"; }
mark_failure() { FAILURE_CLASS="$1"; FAILURE_STEP="$2"; }

finalize_report() {
  local key
  [[ "${REPORT_FINALIZED}" == false ]] || return 0
  for key in "${EVIDENCE_KEYS[@]}"; do record_if_missing "${key}" fail; done
  printf 'failure.class=%s\n' "${FAILURE_CLASS}" >>"${REPORT}"
  printf 'failure.step=%s\n' "${FAILURE_STEP}" >>"${REPORT}"
  REPORT_FINALIZED=true
}

validate_report() {
  [[ -f "${REPORT}" && ! -L "${REPORT}" ]] || fail "hosted pre-commit interruption report is missing"
  python3 - "${REPORT}" <<'PY'
import sys
required={"hook.precommit_reached","connect.interrupted_not_success","precommit.no_network_mutation",
"restart.no_false_resume_authority","recovery.exact_transaction_only","foreign.state_preserved",
"terminal.clean","hidden_reconnect.absent","base.outer_cleanup","artifact.privacy"}
values={}
with open(sys.argv[1],encoding="utf-8") as handle:
    for raw in handle:
        line=raw.rstrip("\n")
        if not line or "=" not in line: raise SystemExit(f"invalid report line: {line!r}")
        key,value=line.split("=",1)
        if key in values: raise SystemExit(f"duplicate report key: {key}")
        values[key]=value
if set(values)!=required|{"failure.class","failure.step"}: raise SystemExit("unexpected hosted pre-commit report schema")
if any(values[key]!="pass" for key in required): raise SystemExit("required hosted pre-commit evidence failed")
if values["failure.class"]!="none" or values["failure.step"]!="none": raise SystemExit("hosted pre-commit report contains failure metadata")
PY
}

assert_public_artifact_privacy() {
  local extra
  extra="$(find "${E2E_ARTIFACT_DIR}" -mindepth 1 -maxdepth 1 ! -name 'hosted-precommit-interruption.txt' -print -quit)"
  [[ -z "${extra}" ]] && [[ -f "${REPORT}" && ! -L "${REPORT}" ]] &&
    ! grep -Eiq 'vless://|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|172[.]31[.](253|254)[.]|session[_-]?id=|transaction[_-]?id=' "${REPORT}"
}

guest_exec() {
  sudo -n systemd-run --machine="${MACHINE}" --expand-environment=no --wait --pipe --collect --quiet -- "$@"
}

run_e2e_podlaz() {
  guest_exec runuser -u e2e -- env XDG_CONFIG_HOME="${GUEST_XDG}/config" XDG_STATE_HOME="${GUEST_XDG}/state"     XDG_CACHE_HOME="${GUEST_XDG}/cache" /usr/bin/podlaz "$@"
}

inherit_base_failure() {
  local class step
  [[ -f "${BASE_REPORT}" ]] || return 0
  class="$(awk -F= '$1 == "failure.class" {print $2; exit}' "${BASE_REPORT}" 2>/dev/null || true)"
  step="$(awk -F= '$1 == "failure.step" {print $2; exit}' "${BASE_REPORT}" 2>/dev/null || true)"
  case "${class}" in product|fixture|infrastructure|capability|diagnostic_unknown) FAILURE_CLASS="${class}";; esac
  [[ -n "${step}" && "${step}" != none ]] && FAILURE_STEP="base.${step//[^A-Za-z0-9_.-]/_}"
}

release_all_controls() {
  local phase path
  [[ -d "${CONTROL_DIR}" ]] || return 0
  for phase in candidate-ready connect-failed; do
    path="${CONTROL_DIR}/${phase}.continue"
    [[ -e "${path}" ]] || printf 'continue\n' >"${path}" 2>/dev/null || true
    chmod 0600 "${path}" >/dev/null 2>&1 || true
  done
}

remove_recovery_authorization() {
  [[ "${RECOVERY_RULE_INSTALLED}" == true ]] || return 0
  sudo -n rm -f -- "${BASE_GUEST_ROOT}${RECOVERY_RULE}" >/dev/null 2>&1 || true
  guest_exec systemctl restart polkit.service >/dev/null 2>&1 || true
  RECOVERY_RULE_INSTALLED=false
}

clear_precommit_hook() {
  [[ "${HOOK_INSTALLED}" == true ]] || return 0
  guest_exec /bin/bash -lc "rm -f '${HOOK_DROPIN}'; systemctl daemon-reload"
  HOOK_INSTALLED=false
}

cleanup() {
  local code=$? attempt
  trap - EXIT INT TERM
  set +e
  clear_precommit_hook
  remove_recovery_authorization
  release_all_controls
  if [[ -n "${BASE_PID}" ]] && kill -0 "${BASE_PID}" >/dev/null 2>&1; then
    for attempt in $(seq 1 120); do kill -0 "${BASE_PID}" >/dev/null 2>&1 || break; sleep 0.5; done
    kill -TERM "${BASE_PID}" >/dev/null 2>&1 || true
    wait "${BASE_PID}" >/dev/null 2>&1 || true
  fi
  inherit_base_failure
  if [[ -f "${REPORT}" ]]; then
    assert_public_artifact_privacy && record_if_missing artifact.privacy pass
    finalize_report
  fi
  exit "${code}"
}

wait_for_control_ready() {
  local phase="$1" ready="${CONTROL_DIR}/$1.ready" attempt code
  for attempt in $(seq 1 1800); do
    [[ -f "${ready}" && ! -L "${ready}" ]] && return 0
    if [[ -z "${BASE_PID}" ]] || ! kill -0 "${BASE_PID}" >/dev/null 2>&1; then
      if [[ -n "${BASE_PID}" ]]; then set +e; wait "${BASE_PID}"; code=$?; set -e; BASE_PID=""; printf 'base exited before %s: %s\n' "${phase}" "${code}" >&2; fi
      inherit_base_failure
      return 1
    fi
    sleep 0.1
  done
  return 1
}

release_control() {
  local phase="$1" ready="${CONTROL_DIR}/$1.ready" continue="${CONTROL_DIR}/$1.continue" attempt
  [[ -f "${ready}" && ! -L "${ready}" && ! -e "${continue}" && ! -L "${continue}" ]] || return 1
  printf 'continue\n' >"${continue}"; chmod 0600 "${continue}"
  for attempt in $(seq 1 100); do [[ ! -e "${ready}" ]] && return 0; sleep 0.05; done
  return 1
}

run_base_scenario() {
  local candidate="$1"
  rm -rf "${PRIVATE_ROOT}"
  install -d -m 0700 "${PRIVATE_ROOT}" "${BASE_TMP_ROOT}" "${BASE_ARTIFACT_DIR}" "${CONTROL_DIR}"
  env E2E_TMP_ROOT="${BASE_TMP_ROOT}" E2E_ARTIFACT_DIR="${BASE_ARTIFACT_DIR}"     PODLAZ_E2E_CANDIDATE_COMMIT="${EXPECTED_COMMIT}" PODLAZ_E2E_HOSTED_CONTROL_DIR="${CONTROL_DIR}"     PODLAZ_E2E_HOSTED_CONTROL_PHASES="candidate-ready connect-failed" PODLAZ_E2E_HOSTED_CONTROL_TIMEOUT_SECONDS=180     PODLAZ_E2E_HOSTED_EXPECT_CONNECT_FAILURE=true     bash "${BASE_SCENARIO}" "${candidate}" >"${BASE_STDOUT}" 2>"${BASE_STDERR}" &
  BASE_PID=$!
}

wait_base_expected_failure() {
  local code
  [[ -n "${BASE_PID}" ]] || return 1
  set +e; wait "${BASE_PID}"; code=$?; set -e; BASE_PID=""
  (( code != 0 ))
}

install_recovery_authorization() {
  local rule_tmp
  rule_tmp="$(mktemp "${PRIVATE_ROOT}/recover-polkit.XXXXXX")"
  cat >"${rule_tmp}" <<'EOF_RULE'
polkit.addRule(function(action, subject) {
    if (subject.user == "e2e" && action.id == "io.github.aidarkhusainov.podlaz.recover-execute") return polkit.Result.YES;
});
EOF_RULE
  sudo -n install -D -m 0644 "${rule_tmp}" "${BASE_GUEST_ROOT}${RECOVERY_RULE}"
  rm -f -- "${rule_tmp}"
  RECOVERY_RULE_INSTALLED=true
  guest_exec systemctl restart polkit.service
  guest_exec systemctl is-active --quiet polkit.service
}

wait_for_daemon_socket() {
  local attempt
  for attempt in $(seq 1 200); do
    if guest_exec systemctl is-active --quiet podlazd.service >/dev/null 2>&1 && guest_exec test -S "${DAEMON_SOCKET}" >/dev/null 2>&1; then return 0; fi
    sleep 0.1
  done
  return 1
}

install_precommit_hook() {
  guest_exec /bin/bash -lc "rm -rf '${HOOK_DIR}'; install -d -m 0700 '${HOOK_DIR}' '${HOOK_DROPIN_DIR}'; printf '%s\n' '[Service]' 'Environment=PODLAZ_E2E_TUN_HOOKS=true' 'Environment=PODLAZ_E2E_TUN_HOOK_PHASE=before-commit-pause' 'Environment=PODLAZ_E2E_TUN_HOOK_DIR=${HOOK_DIR}' 'Environment=PODLAZ_E2E_TUN_HOOK_TIMEOUT_SECONDS=60' >'${HOOK_DROPIN}'; systemctl daemon-reload; systemctl restart podlazd.service"
  HOOK_INSTALLED=true
  wait_for_daemon_socket
}

wait_for_hook_ready() {
  local attempt
  for attempt in $(seq 1 600); do guest_exec test -f "${HOOK_READY}" >/dev/null 2>&1 && return 0; sleep 0.1; done
  return 1
}

capture_guest_network_snapshot() {
  local prefix="$1"
  guest_exec ip -4 -j addr show | jq -S . >"${prefix}.addr.json"
  guest_exec ip -4 -j route show table all | jq -S . >"${prefix}.routes.json"
  guest_exec ip -4 -j rule show | jq -S . >"${prefix}.rules.json"
  guest_exec nft -j list ruleset | jq -S . >"${prefix}.nft.json"
  guest_exec /bin/bash -lc 'nmcli -t -f NAME,UUID,TYPE,DEVICE connection show --active | LC_ALL=C sort' >"${prefix}.nm.txt"
  guest_exec /bin/bash -lc '{ resolvectl dns; resolvectl domain; resolvectl default-route; }' >"${prefix}.resolved.txt"
  chmod 0600 "${prefix}."*
}

assert_network_snapshot_equal() {
  local before="$1" after="$2" suffix
  for suffix in addr.json routes.json rules.json nft.json nm.txt resolved.txt; do cmp -s "${before}.${suffix}" "${after}.${suffix}" || return 1; done
}

assert_foreign_sentinel() { guest_exec nft list table inet pzsynt_foreign >/dev/null 2>&1; }

assert_precommit_transaction_only() {
  guest_exec python3 - /run/podlaz/transactions <<'PY'
import glob,json,sys
paths=glob.glob(sys.argv[1].rstrip("/")+"/*.json")
if len(paths)!=1: raise SystemExit(f"expected one pre-commit transaction, found {len(paths)}")
with open(paths[0],encoding="utf-8") as handle: tx=json.load(handle)
if tx.get("schema_version")!="podlaz.transaction.v1" or tx.get("owner")!="podlaz" or tx.get("mode")!="tun": raise SystemExit("invalid pre-commit transaction")
if tx.get("state")!="applying": raise SystemExit(f"unexpected pre-commit transaction state: {tx.get('state')!r}")
if tx.get("applied_steps"): raise SystemExit("pre-commit transaction has applied mutation authority")
PY
  guest_exec test ! -e /run/podlaz/network-session-continuation.json
  guest_exec test ! -e /run/podlaz/generated/xray.json
  guest_exec /bin/bash -lc '! ip link show dev podlaz0 >/dev/null 2>&1'
  guest_exec /bin/bash -lc '! nft list table inet podlaz >/dev/null 2>&1'
  guest_exec /bin/bash -lc "nft list tables >'${FOCUSED_GUEST_PRIVATE}/nft-precommit.txt'; ! grep -E 'table inet podlaz_pe_[0-9a-f]+' '${FOCUSED_GUEST_PRIVATE}/nft-precommit.txt'"
  # Guest shell expands daemon/child process variables.
  # shellcheck disable=SC2016
  guest_exec /bin/bash -lc 'daemon="$(systemctl show -p MainPID --value podlazd.service)"; for pid in $(pgrep -P "$daemon" 2>/dev/null || true); do [[ "$(readlink -f "/proc/$pid/exe" 2>/dev/null || true)" != /usr/lib/podlaz/xray ]] || exit 1; done'
  guest_exec /bin/bash -lc "curl --fail --silent --show-error --max-time 3 --unix-socket '${DAEMON_SOCKET}' http://localhost/v1/status >'${FOCUSED_GUEST_PRIVATE}/precommit-status.json'"
  guest_exec python3 - "${FOCUSED_GUEST_PRIVATE}/precommit-status.json" <<'PY'
import json,sys
with open(sys.argv[1],encoding="utf-8") as handle: status=json.load(handle)
connection=str(status.get("connection") or "")
if connection in {"active","reconnecting"}:
    raise SystemExit(f"pre-commit lifecycle published unsafe connection state: {connection!r}")
if str(status.get("active_transaction_id") or ""): raise SystemExit("pre-commit lifecycle published an active transaction")
PY
}

assert_no_published_or_resumed_authority() {
  guest_exec /bin/bash -lc "curl --fail --silent --show-error --max-time 3 --unix-socket '${DAEMON_SOCKET}' http://localhost/v1/status >'${FOCUSED_GUEST_PRIVATE}/post-restart-status.json'"
  guest_exec python3 - "${FOCUSED_GUEST_PRIVATE}/post-restart-status.json" <<'PY'
import json,sys
with open(sys.argv[1],encoding="utf-8") as handle: status=json.load(handle)
if status.get("connection") in {"active","connecting","reconnecting"}: raise SystemExit(f"interrupted lifecycle was published/resumed as {status.get('connection')!r}")
if str(status.get("active_transaction_id") or ""): raise SystemExit("interrupted lifecycle published active transaction authority")
PY
  guest_exec test ! -e /run/podlaz/network-session-continuation.json
  guest_exec test ! -e /run/podlaz/generated/xray.json
  guest_exec /bin/bash -lc '! ip link show dev podlaz0 >/dev/null 2>&1'
  guest_exec /bin/bash -lc '! nft list table inet podlaz >/dev/null 2>&1'
  guest_exec /bin/bash -lc "nft list tables >'${FOCUSED_GUEST_PRIVATE}/nft-post-restart.txt'; ! grep -E 'table inet podlaz_pe_[0-9a-f]+' '${FOCUSED_GUEST_PRIVATE}/nft-post-restart.txt'"
  # Guest shell expands daemon/child process variables.
  # shellcheck disable=SC2016
  guest_exec /bin/bash -lc 'daemon="$(systemctl show -p MainPID --value podlazd.service)"; for pid in $(pgrep -P "$daemon" 2>/dev/null || true); do [[ "$(readlink -f "/proc/$pid/exe" 2>/dev/null || true)" != /usr/lib/podlaz/xray ]] || exit 1; done'
}

wait_for_clean_inactive() {
  local attempt
  for attempt in $(seq 1 160); do
    if guest_exec /bin/bash -lc "curl --fail --silent --show-error --max-time 3 --unix-socket '${DAEMON_SOCKET}' http://localhost/v1/status >'${FOCUSED_GUEST_PRIVATE}/inactive-status.json' 2>/dev/null && python3 '${STATUS_HELPER}' clean-inactive '${FOCUSED_GUEST_PRIVATE}/inactive-status.json' >/dev/null" >/dev/null 2>&1; then return 0; fi
    sleep 0.25
  done
  return 1
}

assert_recovery_exact_transaction_only() {
  local before="${PRIVATE_ROOT}/recover-before.json"
  run_e2e_podlaz recover --json >"${before}"
  python3 - "${before}" <<'PY'
import json,sys
with open(sys.argv[1],encoding="utf-8") as handle: payload=json.load(handle)
candidates=(payload.get("recovery") or {}).get("candidates") or []
session=payload.get("network_session")
if isinstance(session,dict) and (session.get("cleanup_authority") or session.get("transaction_present") or session.get("next_action") not in (None,"","none")):
    raise SystemExit("pre-commit recovery fabricated Network Session authority")
for candidate in candidates:
    if not isinstance(candidate,dict) or not isinstance(candidate.get("transaction"),dict):
        raise SystemExit("pre-commit recovery exposed non-transaction cleanup authority")
PY
  guest_exec python3 - "${before}" /run/podlaz/transactions <<'PY'
import glob,json,sys
with open(sys.argv[1],encoding="utf-8") as handle: payload=json.load(handle)
paths=glob.glob(sys.argv[2].rstrip("/")+"/*.json")
candidates=(payload.get("recovery") or {}).get("candidates") or []
if paths and not candidates: raise SystemExit("durable pre-commit transaction exists without recovery evidence")
PY
  run_e2e_podlaz recover --execute --yes --json >"${PRIVATE_ROOT}/recover-execute.json"
  wait_for_clean_inactive
  guest_exec python3 -c 'import glob,sys; raise SystemExit(1 if glob.glob("/run/podlaz/transactions/*.json") else 0)'
  run_e2e_podlaz recover --json >"${PRIVATE_ROOT}/recover-clean.json"
  python3 - "${PRIVATE_ROOT}/recover-clean.json" <<'PY'
import json,sys
with open(sys.argv[1],encoding="utf-8") as handle: payload=json.load(handle)
if payload.get("status")!="ok" or payload.get("warnings"): raise SystemExit("post-interruption recovery is not clean")
recovery=payload.get("recovery") or {}
if recovery.get("candidates") or recovery.get("warnings"): raise SystemExit("post-interruption recovery retains candidates")
session=payload.get("network_session")
if isinstance(session,dict) and (session.get("cleanup_authority") or session.get("transaction_present") or session.get("next_action") not in (None,"","none")):
    raise SystemExit("post-interruption recovery retains session authority")
PY
}

assert_no_hidden_reconnect() {
  local attempt
  for attempt in $(seq 1 20); do
    assert_no_published_or_resumed_authority || return 1
    guest_exec python3 -c 'import glob,sys; raise SystemExit(1 if glob.glob("/run/podlaz/transactions/*.json") else 0)' || return 1
    sleep 0.25
  done
}

restart_daemon_cleanly() {
  local old_pid new_pid
  old_pid="$(guest_exec systemctl show -p MainPID --value podlazd.service | tr -d '[:space:]')"
  guest_exec systemctl restart podlazd.service
  wait_for_daemon_socket || return 1
  new_pid="$(guest_exec systemctl show -p MainPID --value podlazd.service | tr -d '[:space:]')"
  [[ "${old_pid}" =~ ^[1-9][0-9]*$ && "${new_pid}" =~ ^[1-9][0-9]*$ && "${new_pid}" != "${old_pid}" ]]
}

run_scenario() {
  local candidate="$1"
  local before_interrupt="${PRIVATE_ROOT}/before-interrupt" after_restart="${PRIVATE_ROOT}/after-restart"
  local after_recovery="${PRIVATE_ROOT}/after-recovery" after_second_restart="${PRIVATE_ROOT}/after-second-restart"
  local boot_before boot_after

  mark_failure diagnostic_unknown base.candidate_ready
  run_base_scenario "${candidate}"
  wait_for_control_ready candidate-ready || fail "base synthetic TUN did not reach candidate-ready"
  grep -Fx 'candidate.provenance=pass' "${BASE_REPORT}" >/dev/null || fail "candidate provenance did not pass"
  grep -Fx 'ordinary_user.boundary=pass' "${BASE_REPORT}" >/dev/null || fail "ordinary-user boundary did not pass"

  mark_failure fixture recovery.authorization
  install_recovery_authorization
  guest_exec install -d -m 0700 "${FOCUSED_GUEST_PRIVATE}"
  assert_foreign_sentinel || fail "base foreign sentinel is absent"

  mark_failure fixture hook.install
  install_precommit_hook || fail "could not install supported pre-commit hook"
  capture_guest_network_snapshot "${before_interrupt}"
  assert_network_snapshot_equal "${BASE_GUEST_BASELINE}" "${before_interrupt}" || fail "hook setup changed guest network baseline"
  boot_before="$(guest_exec cat /proc/sys/kernel/random/boot_id | tr -d '[:space:]')"

  mark_failure product precommit.connect
  release_control candidate-ready || fail "could not release candidate-ready boundary"
  wait_for_hook_ready || fail "connect did not reach before-commit-pause"
  assert_precommit_transaction_only || fail "pre-commit pause already published or mutated runtime authority"
  assert_foreign_sentinel || fail "foreign state changed before interruption"
  record_evidence hook.precommit_reached pass
  record_evidence precommit.no_network_mutation pass

  mark_failure product precommit.interrupt
  guest_exec systemctl kill --kill-whom=main --signal=SIGKILL podlazd.service
  wait_for_control_ready connect-failed || fail "interrupted connect did not return as failure"
  record_evidence connect.interrupted_not_success pass

  mark_failure product daemon.restart
  clear_precommit_hook
  guest_exec systemctl reset-failed podlazd.service >/dev/null 2>&1 || true
  guest_exec systemctl start podlazd.service >/dev/null 2>&1 || guest_exec systemctl restart podlazd.service
  wait_for_daemon_socket || fail "daemon did not return after pre-commit interruption"
  boot_after="$(guest_exec cat /proc/sys/kernel/random/boot_id | tr -d '[:space:]')"
  [[ "${boot_after}" == "${boot_before}" ]] || fail "pre-commit interruption crossed a boot boundary"
  assert_no_published_or_resumed_authority || fail "daemon restart fabricated active/reconnect authority"
  capture_guest_network_snapshot "${after_restart}"
  assert_network_snapshot_equal "${BASE_GUEST_BASELINE}" "${after_restart}" || fail "daemon restart changed foreign network state"
  assert_foreign_sentinel || fail "foreign state changed after restart"
  record_evidence restart.no_false_resume_authority pass

  mark_failure product recovery.exact
  assert_recovery_exact_transaction_only || fail "recovery exceeded exact pre-commit transaction authority"
  assert_no_published_or_resumed_authority || fail "recovery published active/reconnect authority"
  capture_guest_network_snapshot "${after_recovery}"
  assert_network_snapshot_equal "${BASE_GUEST_BASELINE}" "${after_recovery}" || fail "recovery changed foreign network state"
  record_evidence recovery.exact_transaction_only pass
  record_evidence foreign.state_preserved pass

  mark_failure product hidden_reconnect
  assert_no_hidden_reconnect || fail "interrupted lifecycle retried or reconnected after recovery"
  record_evidence hidden_reconnect.absent pass

  mark_failure product clean_restart
  restart_daemon_cleanly || fail "clean daemon restart did not replace daemon process"
  wait_for_clean_inactive || fail "clean restart did not remain inactive"
  assert_no_published_or_resumed_authority || fail "clean restart fabricated lifecycle authority"
  capture_guest_network_snapshot "${after_second_restart}"
  assert_network_snapshot_equal "${BASE_GUEST_BASELINE}" "${after_second_restart}" || fail "clean restart changed foreign network state"
  guest_exec timeout 20 getent ahostsv4 example.com >/dev/null
  guest_exec timeout 30 curl -4 -fsS -o /dev/null https://example.com/
  record_evidence terminal.clean pass

  mark_failure diagnostic_unknown base.teardown
  remove_recovery_authorization
  release_control connect-failed || fail "could not release expected connect-failed boundary"
  wait_base_expected_failure || fail "base scenario unexpectedly succeeded after daemon interruption"
  grep -Fx 'outer.cleanup=pass' "${BASE_REPORT}" >/dev/null || fail "base outer cleanup did not pass"
  grep -Fx 'artifact.privacy=pass' "${BASE_REPORT}" >/dev/null || fail "base artifact privacy did not pass"
  record_evidence base.outer_cleanup pass

  FAILURE_CLASS=none
  FAILURE_STEP=none
  assert_public_artifact_privacy || fail "hosted pre-commit public evidence is not privacy-safe"
  record_evidence artifact.privacy pass
}

main() {
  (($# == 1)) || fail "usage: $0 CANDIDATE.deb"
  require_cmd awk bash chmod cmp curl find grep install jq mktemp pgrep python3 readlink rm seq sleep sudo systemd-run timeout tr
  [[ "${PODLAZ_E2E_CANDIDATE_COMMIT:-}" =~ ^[0-9a-fA-F]{40}$ ]] || fail "PODLAZ_E2E_CANDIDATE_COMMIT must be exact"
  install -d -m 0700 "${E2E_ARTIFACT_DIR}"
  : >"${REPORT}"
  chmod 0600 "${REPORT}"
  trap cleanup EXIT INT TERM
  run_scenario "$1"
  finalize_report
  validate_report
}

if [[ "${1:-}" == validate-report ]]; then validate_report; exit 0; fi
main "$@"
