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
  recovery.insufficient_authority_preserved
  foreign.state_preserved
  hidden_reconnect.absent
  direct.connectivity_preserved
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
"restart.no_false_resume_authority","recovery.exact_transaction_only","recovery.insufficient_authority_preserved",
"foreign.state_preserved","hidden_reconnect.absent","direct.connectivity_preserved","base.outer_cleanup","artifact.privacy"}
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
    if guest_exec systemctl is-active --quiet podlazd.service >/dev/null 2>&1 &&
      guest_exec test -S "${DAEMON_SOCKET}" >/dev/null 2>&1 &&
      guest_exec /bin/bash -lc "curl --fail --silent --show-error --max-time 3 --unix-socket '${DAEMON_SOCKET}' http://localhost/v1/status >'${FOCUSED_GUEST_PRIVATE}/readiness-status.json' 2>/dev/null" >/dev/null 2>&1 &&
      guest_exec python3 -c 'import json,sys; json.load(open(sys.argv[1],encoding="utf-8"))' "${FOCUSED_GUEST_PRIVATE}/readiness-status.json" >/dev/null 2>&1; then
      return 0
    fi
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

capture_transaction_fingerprint() {
  local output="$1"
  guest_exec python3 - /run/podlaz/transactions <<'PY' >"${output}" || return 1
import glob
import hashlib
import json
import sys

paths = glob.glob(sys.argv[1].rstrip("/") + "/*.json")
if len(paths) != 1:
    raise SystemExit(f"expected one preserved pre-commit transaction, found {len(paths)}")
raw = open(paths[0], "rb").read()
tx = json.loads(raw)
if tx.get("schema_version") != "podlaz.transaction.v1" or tx.get("owner") != "podlaz" or tx.get("mode") != "tun":
    raise SystemExit("preserved pre-commit transaction identity is invalid")
if tx.get("state") != "applying":
    raise SystemExit(f"preserved pre-commit transaction state is {tx.get('state')!r}")
if tx.get("applied_steps"):
    raise SystemExit("preserved pre-commit transaction gained applied mutation authority")
print(hashlib.sha256(raw).hexdigest())
PY
  [[ -s "${output}" ]] || return 1
  chmod 0600 "${output}" || return 1
}

capture_precommit_session_identity() {
  local output="$1"
  guest_exec python3 - /run/podlaz/network-session-continuation.json /proc/sys/kernel/random/boot_id <<'PY' >"${output}" || return 1
import hashlib
import json
import re
import sys

state_path, boot_path = sys.argv[1:]
with open(state_path, encoding="utf-8") as handle:
    state = json.load(handle)
with open(boot_path, encoding="utf-8") as handle:
    boot_id = handle.read().strip()

if state.get("schema_version") != "podlaz.network-session-state.v1" or state.get("owner") != "podlaz":
    raise SystemExit("pre-commit Network Session identity is invalid")
session_id = str(state.get("session_id") or "")
if not re.fullmatch(r"[0-9a-f]{32}", session_id):
    raise SystemExit("pre-commit Network Session ID is invalid")
if state.get("boot_id") != boot_id:
    raise SystemExit("pre-commit Network Session is not current-boot")
if state.get("intent") != "resume":
    raise SystemExit(f"pre-commit Network Session intent is {state.get('intent')!r}")
request = state.get("request")
if not isinstance(request, dict) or request.get("mode") != "tun":
    raise SystemExit("pre-commit Network Session request is not TUN")
if state.get("protection") is not None:
    raise SystemExit("pre-commit Network Session fabricated Privacy Envelope authority")
if state.get("replacement") is not None:
    raise SystemExit("pre-commit Network Session fabricated replacement authority")

stable = {
    "schema_version": state["schema_version"],
    "owner": state["owner"],
    "boot_id": state["boot_id"],
    "session_id": session_id,
    "intent": state["intent"],
    "request": request,
}
encoded = json.dumps(stable, sort_keys=True, separators=(",", ":")).encode()
print(hashlib.sha256(encoded).hexdigest())
PY
  [[ -s "${output}" ]] || return 1
  chmod 0600 "${output}" || return 1
}

assert_precommit_transaction_only() {
  local transaction_fingerprint="$1" session_fingerprint="$2"
  capture_transaction_fingerprint "${transaction_fingerprint}" || return 1
  capture_precommit_session_identity "${session_fingerprint}" || return 1
  guest_exec test ! -e /run/podlaz/generated/xray.json || return 1
  guest_exec /bin/bash -lc '! ip link show dev podlaz0 >/dev/null 2>&1' || return 1
  guest_exec /bin/bash -lc '! nft list table inet podlaz >/dev/null 2>&1' || return 1
  guest_exec /bin/bash -lc "nft list tables >'${FOCUSED_GUEST_PRIVATE}/nft-precommit.txt'; ! grep -E 'table inet podlaz_pe_[0-9a-f]+' '${FOCUSED_GUEST_PRIVATE}/nft-precommit.txt'" || return 1
  # Guest shell expands daemon/child process variables.
  # shellcheck disable=SC2016
  guest_exec /bin/bash -lc 'daemon="$(systemctl show -p MainPID --value podlazd.service)"; for pid in $(pgrep -P "$daemon" 2>/dev/null || true); do [[ "$(readlink -f "/proc/$pid/exe" 2>/dev/null || true)" != /usr/lib/podlaz/xray ]] || exit 1; done' || return 1
  guest_exec /bin/bash -lc "curl --fail --silent --show-error --max-time 3 --unix-socket '${DAEMON_SOCKET}' http://localhost/v1/status >'${FOCUSED_GUEST_PRIVATE}/precommit-status.json'" || return 1
  guest_exec python3 - "${FOCUSED_GUEST_PRIVATE}/precommit-status.json" <<'PY' || return 1
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    status = json.load(handle)
connection = str(status.get("connection") or "")
if connection in {"active", "reconnecting"}:
    raise SystemExit(f"pre-commit lifecycle published unsafe connection state: {connection!r}")
if str(status.get("active_transaction_id") or ""):
    raise SystemExit("pre-commit lifecycle published an active transaction")
PY
}

assert_no_published_or_resumed_authority() {
  local expected_session_fingerprint="$1" current_session_fingerprint="${PRIVATE_ROOT}/session-current.sha256"
  guest_exec /bin/bash -lc "curl --fail --silent --show-error --max-time 3 --unix-socket '${DAEMON_SOCKET}' http://localhost/v1/status >'${FOCUSED_GUEST_PRIVATE}/post-restart-status.json'" || return 1
  guest_exec python3 - "${FOCUSED_GUEST_PRIVATE}/post-restart-status.json" <<'PY' || return 1
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    status = json.load(handle)
if status.get("connection") in {"active", "connecting", "reconnecting"}:
    raise SystemExit(f"interrupted lifecycle was published/resumed as {status.get('connection')!r}")
if str(status.get("active_transaction_id") or ""):
    raise SystemExit("interrupted lifecycle published active transaction authority")
PY
  capture_precommit_session_identity "${current_session_fingerprint}" || return 1
  cmp -s "${expected_session_fingerprint}" "${current_session_fingerprint}" || return 1
  guest_exec test ! -e /run/podlaz/generated/xray.json || return 1
  guest_exec /bin/bash -lc '! ip link show dev podlaz0 >/dev/null 2>&1' || return 1
  guest_exec /bin/bash -lc '! nft list table inet podlaz >/dev/null 2>&1' || return 1
  guest_exec /bin/bash -lc "nft list tables >'${FOCUSED_GUEST_PRIVATE}/nft-post-restart.txt'; ! grep -E 'table inet podlaz_pe_[0-9a-f]+' '${FOCUSED_GUEST_PRIVATE}/nft-post-restart.txt'" || return 1
  # Guest shell expands daemon/child process variables.
  # shellcheck disable=SC2016
  guest_exec /bin/bash -lc 'daemon="$(systemctl show -p MainPID --value podlazd.service)"; for pid in $(pgrep -P "$daemon" 2>/dev/null || true); do [[ "$(readlink -f "/proc/$pid/exe" 2>/dev/null || true)" != /usr/lib/podlaz/xray ]] || exit 1; done' || return 1
}

assert_recovery_exact_transaction_only() {
  local expected_fingerprint="$1" expected_session_fingerprint="$2"
  local before="${PRIVATE_ROOT}/recover-before.json"
  local execute="${PRIVATE_ROOT}/recover-execute.json"
  local execute_stderr="${PRIVATE_ROOT}/recover-execute.stderr"
  local after_dry_run="${PRIVATE_ROOT}/transaction-after-dry-run.sha256"
  local after_execute="${PRIVATE_ROOT}/transaction-after-execute.sha256"
  local session_after_dry_run="${PRIVATE_ROOT}/session-after-dry-run.sha256"
  local session_after_execute="${PRIVATE_ROOT}/session-after-execute.sha256"
  local execute_code

  run_e2e_podlaz recover --json >"${before}" 2>"${PRIVATE_ROOT}/recover-before.stderr" || return 1
  python3 - "${before}" <<'PY' || return 1
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    payload = json.load(handle)
if payload.get("status") != "warn" or payload.get("mode") != "dry-run":
    raise SystemExit("pre-commit dry-run did not publish pending fail-closed recovery")
recovery = payload.get("recovery")
if not isinstance(recovery, dict):
    raise SystemExit("pre-commit dry-run recovery projection is missing")
candidates = recovery.get("candidates") or []
if not candidates:
    raise SystemExit("durable pre-commit transaction exists without recovery evidence")
session = recovery.get("network_session")
if not isinstance(session, dict):
    raise SystemExit("pre-commit dry-run lost current Network Session recovery projection")
expected_session = {
    "authority": "present",
    "intent": "resume",
    "startup_gate": "blocked",
    "resume_stage": "exact-recovery",
    "last_resume_outcome": "incomplete",
    "transaction_present": True,
    "cleanup_authority": "none",
    "next_action": "retry-resume",
}
for key, value in expected_session.items():
    if session.get(key) != value:
        raise SystemExit(f"pre-commit dry-run Network Session {key}={session.get(key)!r}, expected {value!r}")
if session.get("replay_disposition") or session.get("network_apply_subphase"):
    raise SystemExit("pre-commit dry-run advanced into connect replay")
for candidate in candidates:
    transaction = candidate.get("transaction") if isinstance(candidate, dict) else None
    if not isinstance(transaction, dict):
        raise SystemExit("pre-commit dry-run exposed non-transaction cleanup authority")
    if transaction.get("state") != "applying" or not transaction.get("requires_cleanup"):
        raise SystemExit("pre-commit dry-run lost applying transaction evidence")
PY

  capture_transaction_fingerprint "${after_dry_run}" || return 1
  cmp -s "${expected_fingerprint}" "${after_dry_run}" || return 1
  capture_precommit_session_identity "${session_after_dry_run}" || return 1
  cmp -s "${expected_session_fingerprint}" "${session_after_dry_run}" || return 1

  set +e
  run_e2e_podlaz recover --execute --yes --json >"${execute}" 2>"${execute_stderr}"
  execute_code=$?
  set -e
  (( execute_code == 1 )) || return 1
  python3 - "${execute}" <<'PY' || return 1
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    payload = json.load(handle)
if payload.get("status") != "warn" or payload.get("mode") != "execute":
    raise SystemExit("pre-commit execute did not fail closed as incomplete")
if "recover completed with incomplete cleanup" not in (payload.get("errors") or []):
    raise SystemExit("pre-commit execute lost incomplete-cleanup classification")
session = payload.get("network_session")
if not isinstance(session, dict):
    raise SystemExit("pre-commit execute lost current Network Session recovery projection")
expected_session = {
    "authority": "present",
    "intent": "resume",
    "startup_gate": "blocked",
    "resume_stage": "exact-recovery",
    "last_resume_outcome": "incomplete",
    "transaction_present": True,
    "cleanup_authority": "none",
    "next_action": "retry-resume",
}
for key, value in expected_session.items():
    if session.get(key) != value:
        raise SystemExit(f"pre-commit execute Network Session {key}={session.get(key)!r}, expected {value!r}")
if session.get("replay_disposition") or session.get("network_apply_subphase"):
    raise SystemExit("pre-commit execute advanced into connect replay")
results = payload.get("recovery")
if not isinstance(results, list):
    raise SystemExit("pre-commit execute recovery result has invalid shape")
if results:
    raise SystemExit("blocked Network Session execute unexpectedly ran standalone generic cleanup")
PY

  capture_transaction_fingerprint "${after_execute}" || return 1
  cmp -s "${expected_fingerprint}" "${after_execute}" || return 1
  capture_precommit_session_identity "${session_after_execute}" || return 1
  cmp -s "${expected_session_fingerprint}" "${session_after_execute}" || return 1
}

assert_no_hidden_reconnect() {
  local expected_fingerprint="$1" expected_session_fingerprint="$2"
  local attempt current="${PRIVATE_ROOT}/transaction-hidden-reconnect.sha256"
  for attempt in $(seq 1 20); do
    assert_no_published_or_resumed_authority "${expected_session_fingerprint}" || return 1
    capture_transaction_fingerprint "${current}" || return 1
    cmp -s "${expected_fingerprint}" "${current}" || return 1
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
  local transaction_fingerprint="${PRIVATE_ROOT}/transaction-precommit.sha256"
  local transaction_after_restart="${PRIVATE_ROOT}/transaction-after-restart.sha256"
  local transaction_after_second_restart="${PRIVATE_ROOT}/transaction-after-second-restart.sha256"
  local session_fingerprint="${PRIVATE_ROOT}/session-precommit.sha256"
  local session_after_restart="${PRIVATE_ROOT}/session-after-restart.sha256"
  local session_after_second_restart="${PRIVATE_ROOT}/session-after-second-restart.sha256"
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
  assert_precommit_transaction_only "${transaction_fingerprint}" "${session_fingerprint}" || fail "pre-commit pause already published or mutated runtime authority"
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
  assert_no_published_or_resumed_authority "${session_fingerprint}" || fail "daemon restart fabricated active/reconnect authority"
  capture_transaction_fingerprint "${transaction_after_restart}" || fail "daemon restart lost exact pre-commit transaction evidence"
  cmp -s "${transaction_fingerprint}" "${transaction_after_restart}" || fail "daemon restart changed exact pre-commit transaction authority"
  capture_precommit_session_identity "${session_after_restart}" || fail "daemon restart lost admitted Network Session identity"
  cmp -s "${session_fingerprint}" "${session_after_restart}" || fail "daemon restart fabricated a different Network Session"
  capture_guest_network_snapshot "${after_restart}"
  assert_network_snapshot_equal "${BASE_GUEST_BASELINE}" "${after_restart}" || fail "daemon restart changed foreign network state"
  assert_foreign_sentinel || fail "foreign state changed after restart"
  record_evidence restart.no_false_resume_authority pass

  mark_failure product recovery.exact
  assert_recovery_exact_transaction_only "${transaction_fingerprint}" "${session_fingerprint}" || fail "recovery exceeded exact pre-commit transaction authority"
  assert_no_published_or_resumed_authority "${session_fingerprint}" || fail "recovery published active/reconnect authority"
  capture_guest_network_snapshot "${after_recovery}"
  assert_network_snapshot_equal "${BASE_GUEST_BASELINE}" "${after_recovery}" || fail "recovery changed foreign network state"
  record_evidence recovery.exact_transaction_only pass
  record_evidence recovery.insufficient_authority_preserved pass
  record_evidence foreign.state_preserved pass

  mark_failure product hidden_reconnect
  assert_no_hidden_reconnect "${transaction_fingerprint}" "${session_fingerprint}" || fail "interrupted lifecycle retried, reconnected, or changed preserved transaction authority"
  record_evidence hidden_reconnect.absent pass

  mark_failure product preserved_restart
  restart_daemon_cleanly || fail "same-boot daemon restart did not replace daemon process"
  assert_no_published_or_resumed_authority "${session_fingerprint}" || fail "same-boot restart fabricated lifecycle authority"
  capture_transaction_fingerprint "${transaction_after_second_restart}" || fail "same-boot restart lost preserved pre-commit transaction"
  cmp -s "${transaction_fingerprint}" "${transaction_after_second_restart}" || fail "same-boot restart changed preserved pre-commit transaction"
  capture_precommit_session_identity "${session_after_second_restart}" || fail "same-boot restart lost admitted Network Session"
  cmp -s "${session_fingerprint}" "${session_after_second_restart}" || fail "same-boot restart fabricated a different Network Session"
  capture_guest_network_snapshot "${after_second_restart}"
  assert_network_snapshot_equal "${BASE_GUEST_BASELINE}" "${after_second_restart}" || fail "same-boot restart changed foreign network state"
  guest_exec timeout 20 getent ahostsv4 example.com >/dev/null || fail "direct DNS connectivity was not preserved"
  guest_exec timeout 30 curl -4 -fsS -o /dev/null https://example.com/ || fail "direct HTTPS connectivity was not preserved"
  record_evidence direct.connectivity_preserved pass

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
