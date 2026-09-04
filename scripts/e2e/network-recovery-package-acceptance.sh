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

require_cmd apt awk curl dpkg dpkg-deb find getent grep mktemp nft python3 runuser sed sha256sum sleep stat sudo systemctl timeout

: "${PODLAZ_E2E_BASE_DEB:=}"
: "${PODLAZ_E2E_BASE_VERSION:=v0.2.29}"
: "${PODLAZ_E2E_PROFILE_URI:=}"
: "${PODLAZ_E2E_PROFILE_URI_LIST:=}"
: "${PODLAZ_E2E_DNS_CHECK_HOST:=github.com}"
: "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL:=https://api.ipify.org}"
: "${PODLAZ_DEB_ARCH:=$(dpkg --print-architecture)}"

[[ -n "${PODLAZ_E2E_BASE_DEB}" ]] || fail "PODLAZ_E2E_BASE_DEB is required"
[[ -f "${PODLAZ_E2E_BASE_DEB}" ]] || fail "released baseline package is missing"
[[ "${PODLAZ_E2E_BASE_VERSION}" == "v0.2.29" ]] || fail "network-recovery baseline must be exactly v0.2.29"
base_package_version="$(dpkg-deb --field "${PODLAZ_E2E_BASE_DEB}" Version 2>/dev/null || true)"
case "${base_package_version}" in 0.2.29|0.2.29-*) ;; *) fail "baseline package version is ${base_package_version:-unknown}, want v0.2.29" ;; esac
if [[ -z "${PODLAZ_E2E_PROFILE_URI}" && -z "${PODLAZ_E2E_PROFILE_URI_LIST}" ]]; then
  fail "PODLAZ_E2E_PROFILE_URI or PODLAZ_E2E_PROFILE_URI_LIST is required"
fi
if [[ "${PODLAZ_DEB_ARCH}" != "$(dpkg --print-architecture)" ]]; then
  fail "network-recovery package acceptance requires a native .deb"
fi

DEV_DEB="dist/podlaz_0.0.0~dev-1_linux_${PODLAZ_DEB_ARCH}.deb"
DAEMON_SOCKET="/run/podlaz/podlazd.sock"
CONTINUATION_PATH="/run/podlaz/network-session-continuation.json"
LEGACY_UPGRADE_MARKER="/run/podlaz/legacy-upgrade-continuation"
RESUME_DIAGNOSTIC_PATH="/run/podlaz/diagnostics/network-session-resume.json"
TRANSACTION_DIR="/run/podlaz/transactions"
CURRENT_BOOT_ID_PATH="/proc/sys/kernel/random/boot_id"
HOOK_DIR="/run/podlaz/network-recovery-e2e"
OVERRIDE_DIR="/etc/systemd/system/podlazd.service.d"
OVERRIDE_PATH="${OVERRIDE_DIR}/99-network-recovery-e2e.conf"
EVIDENCE="${E2E_ARTIFACT_DIR}/network-recovery-acceptance.txt"
FAILURE_EVIDENCE_DIR="${E2E_ARTIFACT_DIR}/network-recovery-failure"
PRE_UPGRADE_EVIDENCE="${E2E_ARTIFACT_DIR}/network-recovery-pre-upgrade.json"

ACTIVE_CONNECTION=0
CANDIDATE_INSTALLED=0
CANDIDATE_INSTALL_ATTEMPTED=0
FAILURE_CAPTURED=0
PROFILE_ID=""
CANDIDATE_INSTALL_STARTED_AT=""
CANDIDATE_INSTALL_STARTED_MONOTONIC=""
CANDIDATE_INSTALL_FINISHED_MONOTONIC=""
CANDIDATE_PREVIOUS_PID=""
CANDIDATE_CURRENT_PID=""

mask_multiline_sensitive() {
  local value="${1:-}"
  [[ -n "${value}" ]] || return 0
  mask_value "${value}"
  while IFS= read -r line; do
    [[ -n "${line}" ]] && mask_value "${line}"
  done <<<"${value}"
}
for sensitive in "${PODLAZ_E2E_PROFILE_URI}" "${PODLAZ_E2E_PROFILE_URI_LIST}"; do
  mask_multiline_sensitive "${sensitive}"
done

write_evidence() {
  append_evidence_pass "${EVIDENCE}" "$1"
}

daemon_status_json() {
  local output
  output="$(mktemp "${E2E_TMP_ROOT}/network-recovery-status.XXXXXX")"
  if ! sudo -n curl --fail --silent --show-error --max-time 5 \
      --unix-socket "${DAEMON_SOCKET}" http://localhost/v1/status >"${output}" 2>/dev/null; then
    rm -f -- "${output}"
    return 1
  fi
  if ! python3 - "${output}" <<'PY_STATUS_JSON'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    status = json.load(handle)
if not isinstance(status, dict):
    raise SystemExit(1)
print(json.dumps(status, separators=(",", ":")))
PY_STATUS_JSON
  then
    rm -f -- "${output}"
    return 2
  fi
  rm -f -- "${output}"
}

daemon_status_classify() {
  local target="${1:-active}" output diagnostic rc
  output="$(mktemp "${E2E_TMP_ROOT}/network-recovery-status.XXXXXX")"
  diagnostic="$(mktemp "${E2E_TMP_ROOT}/network-recovery-diagnostic.XXXXXX")"
  if ! daemon_status_json >"${output}"; then
    rm -f -- "${output}" "${diagnostic}"
    return 2
  fi
  if sudo -n test -f "${RESUME_DIAGNOSTIC_PATH}"; then
    sudo -n cat "${RESUME_DIAGNOSTIC_PATH}" >"${diagnostic}" 2>/dev/null || : >"${diagnostic}"
  else
    : >"${diagnostic}"
  fi
  set +e
  python3 - "${output}" "${diagnostic}" "${target}" <<'PY_STATUS_CLASSIFY'
import json
import sys

status_path, diagnostic_path, target = sys.argv[1:]
try:
    with open(status_path, encoding="utf-8") as handle:
        status = json.load(handle)
except Exception:
    print("INCOMPATIBLE")
    raise SystemExit(0)
if not isinstance(status, dict) or not isinstance(status.get("connection"), str):
    print("INCOMPATIBLE")
    raise SystemExit(0)
transactions = status.get("transactions", [])
if not isinstance(transactions, list) or any(not isinstance(tx, dict) for tx in transactions):
    print("INCOMPATIBLE")
    raise SystemExit(0)
tun_health = status.get("tun_health") or {}
if not isinstance(tun_health, dict):
    print("INCOMPATIBLE")
    raise SystemExit(0)
startup_scan = status.get("startup_scan") or {}
if not isinstance(startup_scan, dict):
    print("INCOMPATIBLE")
    raise SystemExit(0)
network_session = startup_scan.get("network_session") or {}
if not isinstance(network_session, dict):
    print("INCOMPATIBLE")
    raise SystemExit(0)
diagnostic = {}
try:
    with open(diagnostic_path, encoding="utf-8") as handle:
        raw = handle.read().strip()
    if raw:
        diagnostic = json.loads(raw)
except Exception:
    print("INCOMPATIBLE")
    raise SystemExit(0)
if not isinstance(diagnostic, dict):
    print("INCOMPATIBLE")
    raise SystemExit(0)
connection = status.get("connection", "")
mode = status.get("mode", "")
health = str(tun_health.get("state", ""))
terminal_reason = str(status.get("terminal_reason", ""))
lifecycle_phase = str(status.get("lifecycle_phase", ""))
resume_stage = str(network_session.get("resume_stage") or diagnostic.get("resume_stage") or "")
last_resume_outcome = str(network_session.get("last_resume_outcome") or diagnostic.get("last_resume_outcome") or "")
next_action = str(network_session.get("next_action") or "")
cleanup = any(bool(tx.get("requires_cleanup")) for tx in transactions)
committed = [tx for tx in transactions if tx.get("state") == "committed" and not bool(tx.get("requires_cleanup"))]
active_id = str(status.get("active_transaction_id") or "")
active_committed = not active_id or any(str(tx.get("id") or "") == active_id for tx in committed)
if cleanup or health in {"cleanup-required", "degraded"} or terminal_reason:
    print("TERMINAL_IMPOSSIBLE")
elif last_resume_outcome == "failed" or next_action in {"manual-diagnosis", "continue-teardown"}:
    print("TERMINAL_IMPOSSIBLE")
elif target == "active-legacy":
    if connection == "active" and mode == "tun" and committed and active_committed and health in {"", "verified"}:
        print("TARGET_REACHED")
    elif lifecycle_phase == "connecting" or health == "revalidating" or last_resume_outcome in {"not-attempted", "incomplete"} or resume_stage:
        print("PROGRESS_POSSIBLE")
    elif connection == "inactive":
        print("TERMINAL_IMPOSSIBLE")
    else:
        print("INCOMPATIBLE")
elif target == "active":
    if connection == "active" and mode == "tun" and len(committed) == 1 and active_committed and health == "verified":
        print("TARGET_REACHED")
    elif lifecycle_phase == "connecting":
        print("PROGRESS_POSSIBLE")
    elif connection == "active" and mode == "tun" and committed and health == "revalidating":
        print("PROGRESS_POSSIBLE")
    elif connection == "error (core exited)" and health == "revalidating":
        print("PROGRESS_POSSIBLE")
    elif last_resume_outcome in {"not-attempted", "incomplete"} and next_action in {"", "retry-resume"}:
        print("PROGRESS_POSSIBLE")
    elif connection == "inactive":
        print("TERMINAL_IMPOSSIBLE")
    else:
        print("INCOMPATIBLE")
elif target == "inactive":
    if connection == "inactive" and not active_id and not committed:
        print("TARGET_REACHED")
    elif lifecycle_phase == "connecting" or connection in {"active", "error (core exited)"}:
        print("PROGRESS_POSSIBLE")
    else:
        print("INCOMPATIBLE")
else:
    print("INCOMPATIBLE")
PY_STATUS_CLASSIFY
  rc=$?
  set -e
  rm -f -- "${output}" "${diagnostic}"
  return "${rc}"
}

wait_for_semantic_status() {
  local phase="$1" timeout_seconds="$2" target="$3" attempts attempt classification
  [[ "${timeout_seconds}" =~ ^[1-9][0-9]*$ ]] || fail "status timeout must be a positive whole number of seconds"
  attempts="$((timeout_seconds * 5))"
  for ((attempt = 0; attempt < attempts; attempt++)); do
    classification="$(daemon_status_classify "${target}" 2>/dev/null || true)"
    case "${classification}" in
      TARGET_REACHED) return 0 ;;
      PROGRESS_POSSIBLE|"") ;;
      TERMINAL_IMPOSSIBLE)
        capture_failure_evidence "${phase}: terminal/impossible status" || true
        fail "${phase}: daemon reached terminal/impossible state while waiting for ${target}"
        ;;
      INCOMPATIBLE)
        capture_failure_evidence "${phase}: incompatible status contract" || true
        fail "${phase}: daemon status contract is incompatible"
        ;;
      *)
        capture_failure_evidence "${phase}: unknown status classification" || true
        fail "${phase}: unknown status classification ${classification}"
        ;;
    esac
    sleep "${E2E_STATUS_POLL_INTERVAL_SECONDS}"
  done
  capture_failure_evidence "${phase}: semantic status timeout" || true
  fail "${phase} did not converge within ${timeout_seconds}s"
}

wait_for_active_tun() {
  local phase="$1"
  wait_for_daemon_ready "${DAEMON_SOCKET}" podlazd.service 20
  wait_for_semantic_status "${phase}" 60 active
  write_evidence "${phase}"
}

wait_for_released_active_tun() {
  local phase="$1"
  wait_for_daemon_ready "${DAEMON_SOCKET}" podlazd.service 20
  wait_for_semantic_status "${phase}" 60 active-legacy
  write_evidence "${phase}"
}

wait_for_inactive() {
  local phase="$1"
  wait_for_daemon_ready "${DAEMON_SOCKET}" podlazd.service 20
  wait_for_semantic_status "${phase}" 30 inactive
  write_evidence "${phase}"
}

capture_failure_evidence() {
  local reason="${1:-failure}" dir status_tmp since boot canonical boundary pid
  [[ "${FAILURE_CAPTURED}" == "0" ]] || return 0
  FAILURE_CAPTURED=1
  dir="${FAILURE_EVIDENCE_DIR}"
  umask 0077
  mkdir -p -- "${dir}" || return 1
  printf '%s\n' "${reason}" >"${dir}/reason.txt" || true
  systemd --version >"${dir}/systemd-version.txt" 2>&1 || true
  uname -a >"${dir}/kernel.txt" 2>&1 || true
  dpkg-query -W '-f=${Status}\t${Package}\t${Version}\t${Architecture}\n' podlaz >"${dir}/package-state.txt" 2>&1 || true
  sudo -n systemctl show podlazd.service \
    -p MainPID -p ActiveState -p SubState -p Result -p ExecMainCode -p ExecMainStatus \
    -p KillSignal -p RestartKillSignal -p KillMode -p TimeoutStopUSec -p FragmentPath \
    >"${dir}/systemd-unit-properties.txt" 2>&1 || true
  since="${CANDIDATE_INSTALL_STARTED_AT:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"
  boot="$(cat "${CURRENT_BOOT_ID_PATH}" 2>/dev/null || true)"
  canonical="${boot//-/}"
  if [[ "${canonical}" =~ ^[0-9a-fA-F]{32}$ ]]; then
    sudo -n journalctl -u podlazd.service "_BOOT_ID=${canonical,,}" --since "${since}" --no-pager -o short-iso -n 2000 \
      >"${dir}/journal.txt" 2>&1 || true
  fi
  boundary="$(date -u -d "${since}" '+%Y-%m-%d %H:%M:%S' 2>/dev/null || true)"
  if [[ -n "${boundary}" && -f /var/log/dpkg.log ]]; then
    sudo -n awk -v boundary="${boundary}" '$0 ~ /podlaz/ {ts=$1" "$2; if (ts>=boundary) print substr($0,1,4096)}' /var/log/dpkg.log \
      | tail -n 500 >"${dir}/dpkg-log.txt" 2>&1 || true
  fi
  status_tmp="$(mktemp "${E2E_TMP_ROOT}/network-recovery-failure-status.XXXXXX")"
  if daemon_status_json >"${status_tmp}" 2>/dev/null; then
    python3 - "${status_tmp}" >"${dir}/status.json" <<'PY_SAFE_STATUS' || true
import json
import sys
with open(sys.argv[1], encoding="utf-8") as handle:
    status = json.load(handle)
safe_transactions = []
for tx in status.get("transactions") or []:
    if isinstance(tx, dict):
        safe_transactions.append({key: tx.get(key) for key in ("id", "state", "rollback_available", "requires_cleanup")})
out = {key: status.get(key) for key in (
    "daemon", "service", "connection", "lifecycle_phase", "terminal_reason", "mode",
    "runtime_directory", "active_transaction_id", "proxy", "tun", "tun_health", "startup_scan",
    "inspection_warnings",
)}
out["transactions"] = safe_transactions
print(json.dumps(out, indent=2, sort_keys=True))
PY_SAFE_STATUS
  fi
  rm -f -- "${status_tmp}"
  if sudo -n test -f "/run/podlaz/network-session-continuation.json"; then
    sudo -n python3 - "/run/podlaz/network-session-continuation.json" >"${dir}/network-session-continuation.json" <<'PY_SAFE_CONTINUATION' || true
import json
import sys
with open(sys.argv[1], encoding="utf-8") as handle:
    state = json.load(handle)
protection = state.get("protection") or {}
out = {
    "schema_version": state.get("schema_version"),
    "owner": state.get("owner"),
    "boot_id": state.get("boot_id"),
    "session_id": state.get("session_id"),
    "intent": state.get("intent"),
    "protection": {key: protection.get(key) for key in ("state", "composition_version", "family", "table", "tun_interface")},
}
print(json.dumps(out, indent=2, sort_keys=True))
PY_SAFE_CONTINUATION
  fi
  if sudo -n test -f "/run/podlaz/diagnostics/network-session-resume.json"; then
    sudo -n python3 - "/run/podlaz/diagnostics/network-session-resume.json" >"${dir}/network-session-resume.json" <<'PY_SAFE_DIAGNOSTIC' || true
import json
import sys
with open(sys.argv[1], encoding="utf-8") as handle:
    record = json.load(handle)
keys = ("schema_version", "owner", "boot_id", "resume_stage", "last_resume_outcome", "tun_failure_phase", "rollback_status", "transaction_present", "legacy_migration")
print(json.dumps({key: record.get(key) for key in keys}, indent=2, sort_keys=True))
PY_SAFE_DIAGNOSTIC
  fi
  sudo -n python3 - "${TRANSACTION_DIR}" >"${dir}/transactions.json" <<'PY_SAFE_TRANSACTIONS' || true
import glob
import json
import os
import sys
summaries = []
for path in sorted(glob.glob(os.path.join(sys.argv[1], "*.json"))):
    try:
        with open(path, encoding="utf-8") as handle:
            tx = json.load(handle)
    except Exception:
        continue
    failure = tx.get("failure") or {}
    summaries.append({
        "id": tx.get("id"),
        "owner": tx.get("owner"),
        "mode": tx.get("mode"),
        "state": tx.get("state"),
        "requires_recovery": tx.get("requires_recovery"),
        "failure_phase": failure.get("phase"),
        "rollback_status": failure.get("rollback_status"),
    })
print(json.dumps(summaries, indent=2, sort_keys=True))
PY_SAFE_TRANSACTIONS
  pid="$(main_pid 2>/dev/null || true)"
  if [[ "${pid}" =~ ^[0-9]+$ ]] && (( pid > 1 )); then
    sudo -n python3 - "${pid}" >"${dir}/daemon-process-identity.json" <<'PY_PROCESS_IDENTITY' || true
import json
import os
import sys
pid = int(sys.argv[1])
identity = {"pid": pid}
try:
    with open(f"/proc/{pid}/stat", encoding="utf-8") as handle:
        identity["start_time_ticks"] = handle.read().split()[21]
except Exception:
    identity["start_time_ticks"] = None
identity["exe"] = os.path.realpath(f"/proc/{pid}/exe")
try:
    with open(f"/proc/{pid}/cgroup", encoding="utf-8") as handle:
        identity["cgroup"] = handle.read().strip()
except Exception:
    identity["cgroup"] = None
print(json.dumps(identity, indent=2, sort_keys=True))
PY_PROCESS_IDENTITY
  fi
  {
    printf 'baseline_path=%s\n' "${PODLAZ_E2E_BASE_DEB}"
    printf 'baseline_version=%s\n' "${PODLAZ_E2E_BASE_VERSION}"
    printf 'baseline_sha256=%s\n' "$(sha256sum "${PODLAZ_E2E_BASE_DEB}" 2>/dev/null | awk '{print $1}')"
    printf 'candidate_path=%s\n' "${DEV_DEB}"
    printf 'candidate_sha256=%s\n' "$(sha256sum "${DEV_DEB}" 2>/dev/null | awk '{print $1}')"
    printf 'candidate_previous_pid=%s\n' "${CANDIDATE_PREVIOUS_PID}"
    printf 'candidate_current_pid=%s\n' "${CANDIDATE_CURRENT_PID}"
    printf 'candidate_install_started_monotonic=%s\n' "${CANDIDATE_INSTALL_STARTED_MONOTONIC}"
    printf 'candidate_install_finished_monotonic=%s\n' "${CANDIDATE_INSTALL_FINISHED_MONOTONIC}"
  } >"${dir}/package-provenance.txt" 2>&1 || true
}

capture_pre_upgrade_snapshot() {
  local status_tmp pid
  status_tmp="$(mktemp "${E2E_TMP_ROOT}/network-recovery-pre-status.XXXXXX")"
  daemon_status_json >"${status_tmp}" || { rm -f -- "${status_tmp}"; fail "could not capture pre-upgrade status"; }
  pid="$(main_pid)"
  sudo -n python3 - "${status_tmp}" "${TRANSACTION_DIR}" "${pid}" >"${PRE_UPGRADE_EVIDENCE}" <<'PY_PRE_UPGRADE'
import glob
import hashlib
import json
import os
import sys
status_path, tx_dir, pid_text = sys.argv[1:]
with open(status_path, encoding="utf-8") as handle:
    status = json.load(handle)
transactions = []
for path in sorted(glob.glob(os.path.join(tx_dir, "*.json"))):
    try:
        with open(path, encoding="utf-8") as handle:
            tx = json.load(handle)
    except Exception:
        continue
    if tx.get("owner") != "podlaz" or tx.get("state") != "committed":
        continue
    config_path = (((tx.get("desired_plan") or {}).get("core") or {}).get("runtime_config_path") or "")
    config_sha256 = None
    if config_path and os.path.isfile(config_path):
        with open(config_path, "rb") as handle:
            config_sha256 = hashlib.sha256(handle.read()).hexdigest()
    transactions.append({
        "id": tx.get("id"),
        "owner": tx.get("owner"),
        "mode": tx.get("mode"),
        "state": tx.get("state"),
        "runtime_config_path": config_path,
        "runtime_config_sha256": config_sha256,
    })
if len(transactions) != 1:
    raise SystemExit(f"expected exactly one committed pre-upgrade transaction, found {len(transactions)}")
pid = int(pid_text)
with open(f"/proc/{pid}/stat", encoding="utf-8") as handle:
    start_time_ticks = handle.read().split()[21]
out = {
    "daemon_pid": pid,
    "daemon_start_time_ticks": start_time_ticks,
    "connection": status.get("connection"),
    "mode": status.get("mode"),
    "active_transaction_id": status.get("active_transaction_id"),
    "tun_health": status.get("tun_health"),
    "committed_transaction": transactions[0],
}
print(json.dumps(out, indent=2, sort_keys=True))
PY_PRE_UPGRADE
  rm -f -- "${status_tmp}"
  write_evidence pre_upgrade_exact_authority_captured
}

assert_package_replacement_transition() {
  local properties result exec_main_code exec_main_status restart_signal kill_mode timeout_stop fragment
  properties="$(sudo -n systemctl show podlazd.service \
    -p Result -p ExecMainCode -p ExecMainStatus -p RestartKillSignal -p KillMode -p TimeoutStopUSec -p FragmentPath)" || \
    fail "candidate package replacement systemd properties are unavailable"
  result="$(awk -F= '$1=="Result"{print $2}' <<<"${properties}")"
  exec_main_code="$(awk -F= '$1=="ExecMainCode"{print $2}' <<<"${properties}")"
  exec_main_status="$(awk -F= '$1=="ExecMainStatus"{print $2}' <<<"${properties}")"
  restart_signal="$(awk -F= '$1=="RestartKillSignal"{print $2}' <<<"${properties}")"
  kill_mode="$(awk -F= '$1=="KillMode"{print $2}' <<<"${properties}")"
  timeout_stop="$(awk -F= '$1=="TimeoutStopUSec"{print $2}' <<<"${properties}")"
  fragment="$(awk -F= '$1=="FragmentPath"{print $2}' <<<"${properties}")"
  [[ "${result}" != "timeout" ]] || fail "candidate package replacement fell through TimeoutStopSec"
  [[ "${result}" == "success" ]] || fail "candidate package replacement service Result=${result:-unknown}"
  [[ "${restart_signal}" == "10" ]] || fail "candidate RestartKillSignal=${restart_signal:-unknown}, want SIGUSR1/10"
  [[ "${kill_mode}" == "mixed" ]] || fail "candidate KillMode=${kill_mode:-unknown}, want mixed"
  [[ -n "${timeout_stop}" && -n "${fragment}" ]] || fail "candidate package replacement systemd contract is incomplete"
  write_evidence "candidate_package_transition_result_success exec_main_code=${exec_main_code:-unknown} exec_main_status=${exec_main_status:-unknown}"
}

assert_legacy_upgrade_converged() {
  local legacy_marker="/run/podlaz/legacy-upgrade-continuation" continuation_path="/run/podlaz/network-session-continuation.json"
  local current_boot status_tmp
  [[ ! -e "${legacy_marker}" ]] || fail "legacy-upgrade-continuation authority was not consumed"
  current_boot="$(cat "${CURRENT_BOOT_ID_PATH}")"
  sudo -n python3 - "${continuation_path}" "${current_boot}" <<'PY_LEGACY_CONTINUATION'
import json
import sys
path, current_boot = sys.argv[1:]
with open(path, encoding="utf-8") as handle:
    state = json.load(handle)
if state.get("schema_version") != "podlaz.network-session-state.v1" or state.get("owner") != "podlaz":
    raise SystemExit("network-session-continuation.json is not current schema/owner")
if state.get("boot_id") != current_boot:
    raise SystemExit("network-session-continuation.json is not current_boot authority")
if state.get("intent") != "resume" or not state.get("session_id"):
    raise SystemExit("current Network Session is not resumable reconstructed authority")
protection = state.get("protection") or {}
if protection.get("state") != "armed":
    raise SystemExit("reconstructed Network Session Privacy Envelope is not armed")
PY_LEGACY_CONTINUATION
  status_tmp="$(mktemp "${E2E_TMP_ROOT}/network-recovery-legacy-status.XXXXXX")"
  daemon_status_json >"${status_tmp}" || { rm -f -- "${status_tmp}"; fail "candidate status unavailable after legacy migration"; }
  python3 - "${status_tmp}" <<'PY_LEGACY_STATUS'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as handle:
    status = json.load(handle)
transactions = status.get("transactions") or []
committed = [tx for tx in transactions if isinstance(tx, dict) and tx.get("state") == "committed" and not bool(tx.get("requires_cleanup"))]
health = status.get("tun_health") or {}
if status.get("connection") != "active" or status.get("mode") != "tun" or health.get("state") != "verified":
    raise SystemExit("candidate did not converge to verified active TUN")
if len(committed) != 1:
    raise SystemExit(f"candidate has {len(committed)} clean committed transactions, want 1")
active = status.get("active_transaction_id") or ""
if active and committed[0].get("id") != active:
    raise SystemExit("active transaction does not match the one committed candidate transaction")
PY_LEGACY_STATUS
  rm -f -- "${status_tmp}"
  if sudo -n test -e "${RESUME_DIAGNOSTIC_PATH}"; then
    fail "successful legacy migration retained network-session-resume failure diagnostic"
  fi
  write_evidence legacy_upgrade_reconstructed_current_boot_session
}

assert_privacy_envelope_active() {
  local table tun rules_tmp
  read -r table tun < <(sudo -n python3 - "${CONTINUATION_PATH}" <<'PY_PROTECTION_ID'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as handle:
    state = json.load(handle)
protection = state.get("protection") or {}
if protection.get("state") != "armed" or protection.get("composition_version") != 1 or protection.get("family") != "inet":
    raise SystemExit(1)
print(protection.get("table", ""), protection.get("tun_interface", ""))
PY_PROTECTION_ID
  )
  [[ -n "${table}" && -n "${tun}" ]] || fail "Privacy Envelope identity is unavailable"
  rules_tmp="$(mktemp "${E2E_TMP_ROOT}/network-recovery-nft.XXXXXX")"
  sudo -n nft -j list table inet "${table}" >"${rules_tmp}" || { rm -f -- "${rules_tmp}"; fail "Privacy Envelope nft table is unavailable"; }
  python3 - "${rules_tmp}" "${table}" "${tun}" <<'PY_PROTECTION_RULES'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as handle:
    doc = json.load(handle)
table, tun = sys.argv[2:]
rules = [item.get("rule") for item in doc.get("nftables", []) if isinstance(item, dict) and isinstance(item.get("rule"), dict)]
block = [rule for rule in rules if rule.get("table") == table and rule.get("comment") == "podlaz:privacy-envelope:block-direct"]
tun_rules = [rule for rule in rules if rule.get("table") == table and rule.get("comment") == "podlaz:privacy-envelope:tun-egress"]
if len(block) != 1 or "reject" not in json.dumps(block[0].get("expr", [])):
    raise SystemExit("Privacy Envelope block-direct reject rule is missing")
if len(tun_rules) != 1 or tun not in json.dumps(tun_rules[0].get("expr", [])):
    raise SystemExit("Privacy Envelope TUN egress rule is missing")
PY_PROTECTION_RULES
  rm -f -- "${rules_tmp}"
  write_evidence privacy_envelope_active
}

assert_active_connectivity() {
  getent hosts "${PODLAZ_E2E_DNS_CHECK_HOST}" >/dev/null 2>&1 || fail "DNS failed through verified candidate TUN"
  curl -4 -fsS --max-time 30 "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL}" >/dev/null || fail "HTTPS failed through verified candidate TUN"
  write_evidence active_dns_https
}

run_bounded_continuity_soak() {
  local attempt
  for attempt in $(seq 1 60); do
    wait_for_semantic_status "network_recovery_10m_soak_${attempt}" 10 active
    sleep 10
  done
  assert_privacy_envelope_active
  assert_active_connectivity
  write_evidence network_recovery_10m_soak_complete
}

run_private_profile_import() {
  local uri out err
  uri="$(first_configured_profile_uri)"
  [[ -n "${uri}" ]] || fail "no profile URI available"
  out="$(mktemp "${E2E_TMP_ROOT}/network-recovery-import.stdout.XXXXXX")"
  err="$(mktemp "${E2E_TMP_ROOT}/network-recovery-import.stderr.XXXXXX")"
  if ! run_installed_podlaz profile import "${uri}" >"${out}" 2>"${err}"; then
    rm -f -- "${out}" "${err}"
    fail "released-package profile import failed"
  fi
  PROFILE_ID="$(awk '/^Imported profile:/ {print $3}' "${out}")"
  [[ -n "${PROFILE_ID}" ]] || {
    rm -f -- "${out}" "${err}"
    fail "released-package profile import returned no profile ID"
  }
  mask_multiline_sensitive "${PROFILE_ID}"
  rm -f -- "${out}" "${err}"
}

connect_once_on_released_package() {
  local out err
  out="$(mktemp "${E2E_TMP_ROOT}/network-recovery-connect.stdout.XXXXXX")"
  err="$(mktemp "${E2E_TMP_ROOT}/network-recovery-connect.stderr.XXXXXX")"
  if ! run_installed_podlaz connect --mode tun "${PROFILE_ID}" >"${out}" 2>"${err}"; then
    rm -f -- "${out}" "${err}"
    fail "released-package TUN connect failed"
  fi
  rm -f -- "${out}" "${err}"
  ACTIVE_CONNECTION=1
  wait_for_released_active_tun released_package_connected
}

# Setup and cleanup may explicitly start the service because they are creating or
# restoring the test fixture, not validating candidate package replacement.
install_setup_package() {
  local package_path="$1"
  sudo -n apt install --allow-downgrades -y "${package_path}" >/dev/null
  sudo -n systemctl daemon-reload >/dev/null
  sudo -n systemctl start podlazd.service >/dev/null
  wait_for_daemon_ready "${DAEMON_SOCKET}" podlazd.service 20
}

# Candidate installation must stand on the package's own service lifecycle. No
# daemon-reload/start/restart repair is allowed here: postinstall must replace
# the active daemon and leave the service usable itself.
install_candidate_package() {
  local package_path="$1" previous_pid="$2" current_pid
  CANDIDATE_INSTALL_ATTEMPTED=1
  CANDIDATE_PREVIOUS_PID="${previous_pid}"
  CANDIDATE_INSTALL_STARTED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  CANDIDATE_INSTALL_STARTED_MONOTONIC="$(python3 -c 'import time; print(time.monotonic_ns())')"
  sudo -n apt install --allow-downgrades -y "${package_path}" >/dev/null
  CANDIDATE_INSTALLED=1
  CANDIDATE_INSTALL_FINISHED_MONOTONIC="$(python3 -c 'import time; print(time.monotonic_ns())')"
  if ! sudo -n systemctl is-active --quiet podlazd.service; then
    fail "candidate package installation did not leave podlazd.service active"
  fi
  current_pid="$(main_pid)"
  CANDIDATE_CURRENT_PID="${current_pid}"
  if ! [[ "${current_pid}" =~ ^[0-9]+$ ]] || (( current_pid <= 1 )); then
    fail "candidate package installation left an invalid daemon MainPID"
  fi
  if [[ "${current_pid}" == "${previous_pid}" ]]; then
    fail "candidate package installation did not replace the released daemon process"
  fi
  wait_for_daemon_ready "${DAEMON_SOCKET}" podlazd.service 20
  assert_package_replacement_transition
  write_evidence candidate_package_replaced_daemon
}

main_pid() {
  sudo -n systemctl show -p MainPID --value podlazd.service | tr -d '[:space:]'
}

wait_for_new_main_pid() {
  local previous="$1" attempt current
  for attempt in $(seq 1 300); do
    current="$(main_pid)"
    if [[ "${current}" =~ ^[0-9]+$ ]] && (( current > 1 )) && [[ "${current}" != "${previous}" ]] && sudo -n systemctl is-active --quiet podlazd.service; then
      return 0
    fi
    sleep 0.1
  done
  fail "podlazd.service did not replace the daemon process"
}

assert_exact_rolling_back_authority() {
  sudo -n python3 - "${TRANSACTION_DIR}" <<'PY'
import glob
import json
import os
import sys

paths = glob.glob(os.path.join(sys.argv[1], "*.json"))
found = False
for path in paths:
    with open(path, encoding="utf-8") as handle:
        tx = json.load(handle)
    if tx.get("owner") != "podlaz" or tx.get("state") != "rolling_back":
        continue
    rollback = tx.get("rollback") or {}
    owned = False
    for key in (
        "tun_addresses", "routes", "policy_rules", "dns", "nftables",
        "generated_configs", "child_processes",
    ):
        values = rollback.get(key) or []
        if any(isinstance(item, dict) and item.get("owner") == "podlaz" for item in values):
            owned = True
            break
    if owned:
        found = True
        break
if not found:
    raise SystemExit("no exact transaction-owned rolling_back authority found")
PY
  write_evidence forced_rollback_authority_persisted
}

wait_for_file() {
  local path="$1" phase="$2" attempt
  for attempt in $(seq 1 400); do
    if sudo -n test -f "${path}"; then
      return 0
    fi
    sleep 0.1
  done
  fail "${phase}: marker did not appear"
}

install_rollback_pause_override() {
  sudo -n mkdir -p "${OVERRIDE_DIR}"
  sudo -n tee "${OVERRIDE_PATH}" >/dev/null <<EOF2
[Service]
Environment=PODLAZ_E2E_TUN_ROLLBACK_PAUSE=true
Environment=PODLAZ_E2E_TUN_ROLLBACK_PAUSE_DIR=${HOOK_DIR}
Environment=PODLAZ_E2E_TUN_ROLLBACK_PAUSE_TIMEOUT_SECONDS=120
EOF2
  sudo -n systemctl daemon-reload >/dev/null
}

remove_rollback_pause_override() {
  sudo -n rm -f -- "${OVERRIDE_PATH}"
  sudo -n systemctl daemon-reload >/dev/null 2>&1 || true
}

force_kill_inside_durable_rollback() {
  local old_pid restart_log restart_pid
  install_rollback_pause_override

  # Restart once so the replacement daemon inherits the gated E2E hook. This
  # restart itself remains an ordinary continuation scenario because the old
  # daemon does not have the hook environment.
  sudo -n systemctl restart podlazd.service >/dev/null
  wait_for_active_tun hook_enabled_restart_reconnected

  sudo -n rm -rf -- "${HOOK_DIR}"
  sudo -n mkdir -m 0700 "${HOOK_DIR}"
  sudo -n sh -c 'umask 077; printf "%s\n" armed > "$1"' sh "${HOOK_DIR}/rollback-pause.arm"

  old_pid="$(main_pid)"
  if ! [[ "${old_pid}" =~ ^[0-9]+$ ]] || (( old_pid <= 1 )); then
    fail "invalid daemon PID before forced rollback interruption"
  fi
  restart_log="$(mktemp "${E2E_TMP_ROOT}/network-recovery-forced-restart.XXXXXX")"
  sudo -n systemctl restart podlazd.service >"${restart_log}" 2>&1 &
  restart_pid=$!

  wait_for_file "${HOOK_DIR}/rollback-pause.ready" forced_rollback_pause
  assert_exact_rolling_back_authority
  sudo -n kill -KILL "${old_pid}"

  set +e
  wait "${restart_pid}"
  local restart_code=$?
  set -e
  rm -f -- "${restart_log}"
  if [[ "${restart_code}" != "0" ]]; then
    # A concurrent SIGKILL may make the original systemctl restart client
    # observe a failed stop job even though Restart=on-failure starts the next
    # daemon. Product success is determined by the converged service/session.
    sudo -n systemctl reset-failed podlazd.service >/dev/null 2>&1 || true
    sudo -n systemctl start podlazd.service >/dev/null 2>&1 || true
  fi

  wait_for_new_main_pid "${old_pid}"
  wait_for_active_tun forced_rollback_crash_recovered
  remove_rollback_pause_override
}

check_direct_connectivity() {
  getent hosts "${PODLAZ_E2E_DNS_CHECK_HOST}" >/dev/null 2>&1 || fail "ordinary DNS did not recover after explicit service stop"
  curl -4 -fsS --max-time 30 "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL}" >/dev/null || fail "ordinary IPv4 egress did not recover after explicit service stop"
  write_evidence ordinary_network_after_explicit_stop
}

restore_candidate() {
  [[ -f "${DEV_DEB}" ]] || return 1
  install_setup_package "./${DEV_DEB}" || return 1
  CANDIDATE_INSTALLED=1
  if [[ "$(daemon_status_classify active 2>/dev/null || true)" == "TARGET_REACHED" ]]; then
    run_installed_podlaz disconnect >/dev/null 2>&1 || return 1
  fi
  return 0
}

cleanup() {
  local cleanup_code=0
  remove_rollback_pause_override
  sudo -n rm -rf -- "${HOOK_DIR}" >/dev/null 2>&1 || true
  if [[ "${ACTIVE_CONNECTION}" == "1" ]] && [[ -x /usr/bin/podlaz ]]; then
    run_installed_podlaz disconnect >/dev/null 2>&1 || cleanup_code=1
    ACTIVE_CONNECTION=0
  fi
  if [[ "${CANDIDATE_INSTALLED}" != "1" && "${CANDIDATE_INSTALL_ATTEMPTED}" != "1" ]]; then
    restore_candidate || cleanup_code=1
  fi
  return "${cleanup_code}"
}

finish() {
  local code=$? cleanup_code=0
  trap - EXIT
  if [[ "${code}" != "0" ]]; then
    capture_failure_evidence "exit-${code}" || true
  fi
  cleanup || cleanup_code=$?
  if [[ "${code}" == "0" && "${cleanup_code}" != "0" ]]; then
    code="${cleanup_code}"
  fi
  exit "${code}"
}
trap finish EXIT

setup_isolated_xdg "network-recovery-package"
: >"${EVIDENCE}"

[[ -f "${DEV_DEB}" ]] || fail "candidate package is missing: ${DEV_DEB}"

# Enter the lower released package from a deliberately disconnected service
# boundary so the only connection action in this acceptance belongs to it.
sudo -n systemctl stop podlazd.service >/dev/null 2>&1 || true
install_setup_package "${PODLAZ_E2E_BASE_DEB}"
run_private_profile_import
connect_once_on_released_package

# Upgrade to the PR package while the released TUN session is active. No CLI
# connect or service-start repair is issued after this point.
capture_pre_upgrade_snapshot
candidate_previous_pid="$(main_pid)"
if ! [[ "${candidate_previous_pid}" =~ ^[0-9]+$ ]] || (( candidate_previous_pid <= 1 )); then
  fail "invalid released daemon MainPID before candidate package upgrade"
fi
install_candidate_package "./${DEV_DEB}" "${candidate_previous_pid}"
wait_for_active_tun released_to_candidate_upgrade_reconnected
assert_legacy_upgrade_converged
assert_privacy_envelope_active
assert_active_connectivity

# Graceful restart must preserve intent and automatically converge/reconnect.
sudo -n systemctl restart podlazd.service >/dev/null
wait_for_active_tun graceful_restart_reconnected

# Unexpected main-process death must be recovered by Restart=on-failure.
old_pid="$(main_pid)"
if ! [[ "${old_pid}" =~ ^[0-9]+$ ]] || (( old_pid <= 1 )); then
  fail "invalid daemon PID before crash test"
fi
sudo -n kill -KILL "${old_pid}"
wait_for_new_main_pid "${old_pid}"
wait_for_active_tun daemon_crash_reconnected

# Kill the daemon after it has durably recorded rolling_back but before host
# cleanup begins. The next daemon must use that exact authority and reconnect.
force_kill_inside_durable_rollback

run_bounded_continuity_soak

# Explicit service stop is a different intent: disarm before teardown, restore
# ordinary networking, and remain disconnected after a later manual start.
sudo -n systemctl stop podlazd.service >/dev/null
ACTIVE_CONNECTION=0
if sudo -n test -e "${CONTINUATION_PATH}"; then
  fail "explicit service stop left reconnect continuation armed"
fi
write_evidence explicit_stop_disarmed_continuation
check_direct_connectivity
sudo -n rm -rf -- "${HOOK_DIR}" >/dev/null 2>&1 || true
sudo -n systemctl start podlazd.service >/dev/null
wait_for_inactive explicit_stop_then_start_stays_disconnected

assert_artifacts_do_not_contain_sensitive_values \
  network-recovery-package \
  "${PODLAZ_E2E_PROFILE_URI}" \
  "${PODLAZ_E2E_PROFILE_URI_LIST}" \
  "${PROFILE_ID}"

write_evidence network_recovery_acceptance_complete
log "network-recovery installed-package acceptance completed"
