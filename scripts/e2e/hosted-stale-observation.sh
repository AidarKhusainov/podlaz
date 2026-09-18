#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"

BASE_SCENARIO="${SCRIPT_DIR}/hosted-synthetic-tun.sh"
MACHINE="podlaz-synthetic-tun"
FOREIGN_NFT_TABLE="pzsynt_foreign"
HOOK_DIR="/run/podlaz/e2e-hosted-stale-observation"
HOOK_EVENTS="${HOOK_DIR}/events.log"
HOOK_READY="${HOOK_DIR}/dns-missing-link.ready"
HOOK_CONTINUE="${HOOK_DIR}/dns-missing-link.continue"
DNS_ROLLBACK_EXIT_CODE="${HOOK_DIR}/dns-rollback.exit-code"
DNS_ROLLBACK_STDOUT="${HOOK_DIR}/dns-rollback.stdout"
DNS_ROLLBACK_STDERR="${HOOK_DIR}/dns-rollback.stderr"
HOOK_DROPIN_DIR="/run/systemd/system/podlazd.service.d"
HOOK_DROPIN="${HOOK_DROPIN_DIR}/99-hosted-stale-observation.conf"
DIAGNOSTIC_REPORT="/run/podlaz/diagnostics/tun-last.json"
DAEMON_SOCKET="/run/podlaz/podlazd.sock"
TRANSACTION_DIR="/run/podlaz/transactions"
GUEST_XDG="/home/e2e/.local/share/podlaz-hosted-synthetic-tun"
GUEST_PRIVATE="/tmp/podlaz-hosted-stale-observation"
STATUS_HELPER="/workspace/scripts/e2e/lib/daemon_status_semantics.py"
FALLBACK_NETWORK_HELPER="/workspace/scripts/e2e/tun-package-fallback-network.py"
NETWORK_MANIFEST="${GUEST_PRIVATE}/pre-fault-network.json"
RETRY_MANIFEST="${GUEST_PRIVATE}/retry-network.json"
EXPECTED_COMMIT="${PODLAZ_E2E_CANDIDATE_COMMIT:-}"

REPORT="${E2E_ARTIFACT_DIR}/hosted-stale-observation.txt"
PRIVATE_ROOT="${E2E_TMP_ROOT}/hosted-stale-observation-private"
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
HOOK_INSTALLED=false

EVIDENCE_KEYS=(
  candidate.provenance
  stale.fault_injected
  stale.resolved_missing_link
  stale.rollback_order
  stale.observation_not_authority
  stale.rollback_converged
  stale.retry_verified_active
  stale.retry_terminal_cleanup
  foreign.state_preserved
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
  [[ -f "${REPORT}" && ! -L "${REPORT}" ]] || fail "hosted stale observation report is missing"
  python3 - "${REPORT}" <<'PY'
import sys
path=sys.argv[1]
required={
"candidate.provenance","stale.fault_injected","stale.resolved_missing_link",
"stale.rollback_order","stale.observation_not_authority","stale.rollback_converged",
"stale.retry_verified_active","stale.retry_terminal_cleanup","foreign.state_preserved",
"base.outer_cleanup","artifact.privacy",
}
values={}
with open(path,encoding="utf-8") as handle:
    for raw in handle:
        line=raw.rstrip("\n")
        if not line or "=" not in line:
            raise SystemExit(f"invalid report line: {line!r}")
        key,value=line.split("=",1)
        if key in values:
            raise SystemExit(f"duplicate report key: {key}")
        values[key]=value
if set(values) != required | {"failure.class","failure.step"}:
    raise SystemExit("unexpected hosted stale observation report schema")
for key in required:
    if values[key] != "pass":
        raise SystemExit(f"required evidence is not pass: {key}={values[key]}")
if values["failure.class"] != "none" or values["failure.step"] != "none":
    raise SystemExit("hosted stale observation reports a failure")
PY
  grep -Fx 'stale.resolved_missing_link=pass' "${REPORT}" >/dev/null
  grep -Fx 'stale.rollback_converged=pass' "${REPORT}" >/dev/null
  grep -Fx 'stale.observation_not_authority=pass' "${REPORT}" >/dev/null
  grep -Fx 'stale.retry_verified_active=pass' "${REPORT}" >/dev/null
  grep -Fx 'stale.retry_terminal_cleanup=pass' "${REPORT}" >/dev/null
}

assert_public_artifact_privacy() {
  local extra
  extra="$(find "${E2E_ARTIFACT_DIR}" -mindepth 1 -maxdepth 1 ! -name 'hosted-stale-observation.txt' -print -quit)"
  [[ -z "${extra}" && -f "${REPORT}" && ! -L "${REPORT}" ]] || return 1
  ! grep -Eiq 'vless://|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|172[.]31[.](253|254)[.]|session[_-]?id=|transaction[_-]?id=' "${REPORT}"
}

guest_exec() {
  sudo -n systemd-run --machine="${MACHINE}" --expand-environment=no --wait --pipe --collect --quiet -- "$@"
}

release_all_controls() {
  local phase continue
  [[ -d "${CONTROL_DIR}" ]] || return 0
  for phase in candidate-ready connect-failed; do
    continue="${CONTROL_DIR}/${phase}.continue"
    [[ -e "${continue}" ]] || printf 'continue\n' >"${continue}"
    chmod 0600 "${continue}" >/dev/null 2>&1 || true
  done
  guest_exec /bin/bash -lc "if test -d '${HOOK_DIR}'; then test -e '${HOOK_CONTINUE}' || { printf 'continue\\n' >'${HOOK_CONTINUE}'; chmod 0600 '${HOOK_CONTINUE}'; }; fi" >/dev/null 2>&1 || true
}

remove_hook_override() {
  [[ "${HOOK_INSTALLED}" == true ]] || return 0
  guest_exec /bin/bash -lc "rm -f '${HOOK_DROPIN}'; systemctl daemon-reload; systemctl restart podlazd.service" >/dev/null
  HOOK_INSTALLED=false
  wait_for_guest_status clean-inactive 80
}

cleanup_hook_dir() {
  guest_exec test -d "${HOOK_DIR}" >/dev/null 2>&1 || return 0
  guest_exec /bin/bash -lc "find '${HOOK_DIR}' -mindepth 1 -maxdepth 1 -type f \
    \( -name 'events.log' -o -name 'dns-missing-link.ready' -o -name 'dns-missing-link.continue' \
       -o -name 'dns-rollback.capture-claimed' -o -name 'dns-rollback.exit-code' \
       -o -name 'dns-rollback.stdout' -o -name 'dns-rollback.stderr' \) -delete; \
    test -z \"\$(find '${HOOK_DIR}' -mindepth 1 -maxdepth 1 -print -quit)\"; rmdir '${HOOK_DIR}'"
}

cleanup() {
  local code=$? attempt
  trap - EXIT INT TERM
  set +e
  release_all_controls
  remove_hook_override >/dev/null 2>&1 || true
  cleanup_hook_dir >/dev/null 2>&1 || true
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
  local phase="$1" attempt code ready="${CONTROL_DIR}/$1.ready"
  for attempt in $(seq 1 2400); do
    [[ -f "${ready}" && ! -L "${ready}" ]] && return 0
    if [[ -z "${BASE_PID}" ]] || ! kill -0 "${BASE_PID}" >/dev/null 2>&1; then
      if [[ -n "${BASE_PID}" ]]; then
        set +e; wait "${BASE_PID}"; code=$?; set -e
        BASE_PID=""
        (( code != 0 )) || true
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
    PODLAZ_E2E_HOSTED_CONTROL_PHASES="candidate-ready connect-failed" \
    PODLAZ_E2E_HOSTED_CONTROL_TIMEOUT_SECONDS=180 \
    PODLAZ_E2E_HOSTED_EXPECT_CONNECT_FAILURE=true \
    bash "${BASE_SCENARIO}" "${candidate}" >"${BASE_STDOUT}" 2>"${BASE_STDERR}" &
  BASE_PID=$!
}

wait_base_expected_failure() {
  local code
  [[ -n "${BASE_PID}" ]] || return 1
  set +e; wait "${BASE_PID}"; code=$?; set -e
  BASE_PID=""
  (( code != 0 ))
}

capture_status() {
  guest_exec install -d -m 0700 "${GUEST_PRIVATE}" >/dev/null
  guest_exec /bin/bash -lc "curl --fail --silent --show-error --max-time 3 --unix-socket '${DAEMON_SOCKET}' http://localhost/v1/status >'${GUEST_PRIVATE}/status.json'"
}

wait_for_guest_status() {
  local target="$1" attempts="${2:-160}" attempt
  for attempt in $(seq 1 "${attempts}"); do
    if capture_status >/dev/null 2>&1 &&
       guest_exec python3 "${STATUS_HELPER}" "${target}" "${GUEST_PRIVATE}/status.json" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.5
  done
  return 1
}

wait_for_hook_ready() {
  local attempt
  for attempt in $(seq 1 900); do
    guest_exec test -f "${HOOK_READY}" >/dev/null 2>&1 && return 0
    [[ -n "${BASE_PID}" ]] && kill -0 "${BASE_PID}" >/dev/null 2>&1 || return 1
    sleep 0.1
  done
  return 1
}

install_stale_hook() {
  guest_exec test ! -e "${HOOK_DIR}"
  guest_exec install -d -m 0700 "${HOOK_DIR}" "${HOOK_DROPIN_DIR}"
  guest_exec /bin/bash -lc "printf '%s\\n' '[Service]' \
    'Environment=PODLAZ_E2E_TUN_HOOKS=true' \
    'Environment=PODLAZ_E2E_TUN_HOOK_PHASE=dns-missing-link-rollback' \
    'Environment=PODLAZ_E2E_TUN_HOOK_DIR=${HOOK_DIR}' \
    'Environment=PODLAZ_E2E_TUN_HOOK_TIMEOUT_SECONDS=90' \
    >'${HOOK_DROPIN}'; chmod 0644 '${HOOK_DROPIN}'; systemctl daemon-reload; systemctl restart podlazd.service"
  HOOK_INSTALLED=true
  wait_for_guest_status clean-inactive 80
}

assert_exact_pre_fault_authority() {
  guest_exec python3 - "${TRANSACTION_DIR}" <<'PY'
import glob
import json
import sys
paths=glob.glob(sys.argv[1].rstrip("/")+"/*.json")
if len(paths)!=1:
    raise SystemExit(f"expected one transaction, found {len(paths)}")
with open(paths[0],encoding="utf-8") as handle:
    tx=json.load(handle)
if tx.get("owner")!="podlaz" or tx.get("mode")!="tun" or tx.get("state") not in {"applying","applied","verifying","rolling_back"}:
    raise SystemExit("transaction is not exact in-flight Podlaz TUN authority")
rollback=tx.get("rollback") or {}
addresses=rollback.get("tun_addresses") or []
if len(addresses)!=1:
    raise SystemExit("missing exact TUN-address rollback authority")
item=addresses[0]
if item.get("owner")!="podlaz:tun-address" or item.get("interface_name")!="podlaz0" or item.get("link_kind")!="tun":
    raise SystemExit("invalid TUN-address rollback identity")
if not isinstance(item.get("link_index"),int) or item["link_index"]<=0 or item.get("appeared_after_core") is not True:
    raise SystemExit("TUN-address rollback identity is not bound to the created link")
dns=[x for x in rollback.get("dns") or [] if isinstance(x,dict) and x.get("owner")=="podlaz:dns-link" and x.get("link")=="podlaz0"]
if len(dns)!=1:
    raise SystemExit("missing exact DNS rollback authority")
PY
}

assert_missing_link_capture() {
  guest_exec grep -Fx "1" "${DNS_ROLLBACK_EXIT_CODE}" >/dev/null
  guest_exec test ! -s "${DNS_ROLLBACK_STDOUT}"
  guest_exec python3 /workspace/scripts/e2e/verify_resolvectl_missing_link.py "${DNS_ROLLBACK_STDERR}"
}

capture_events() {
  guest_exec cat "${HOOK_EVENTS}" >"${PRIVATE_ROOT}/events.log"
}

assert_event_order() {
  python3 - "${PRIVATE_ROOT}/events.log" "$1" "$2" <<'PY'
import sys
path,first,second=sys.argv[1:]
events=[line.strip() for line in open(path,encoding="utf-8") if line.strip()]
if first not in events or second not in events or events.index(first)>=events.index(second):
    raise SystemExit(f"invalid event order {first!r} -> {second!r}: {events!r}")
PY
}

assert_rollback_events() {
  local event
  capture_events
  for event in dns-missing-link-ready dns-missing-link-released diagnostics-persisted rollback-started dns-rollback-started dns-rollback-result-captured rollback-completed; do
    grep -Fx "${event}" "${PRIVATE_ROOT}/events.log" >/dev/null
  done
  assert_event_order diagnostics-persisted rollback-started
  assert_event_order rollback-started dns-rollback-started
  assert_event_order dns-rollback-started dns-rollback-result-captured
  assert_event_order dns-rollback-result-captured rollback-completed
}

assert_failure_classification() {
  guest_exec python3 - "${DIAGNOSTIC_REPORT}" <<'PY'
import json
import sys
with open(sys.argv[1],encoding="utf-8") as handle:
    report=json.load(handle)
expected={
    "failure_phase":"network-verify",
    "primary_classification":"network_verify_failure",
    "rollback_status":"completed",
}
for key,value in expected.items():
    if report.get(key)!=value:
        raise SystemExit(f"{key}={report.get(key)!r}, expected {value!r}")
PY
}

assert_owned_state_absent() {
  guest_exec /bin/bash -lc "cd /workspace && source scripts/e2e/lib/e2e.sh && source scripts/e2e/lib/tun_package_assertions.sh && verify_tun_package_resources_absent stale '${FALLBACK_NETWORK_HELPER}' '${NETWORK_MANIFEST}'"
  guest_exec nft list table inet "${FOREIGN_NFT_TABLE}" >/dev/null
}

run_guest_user() {
  guest_exec runuser -u e2e -- env XDG_CONFIG_HOME="${GUEST_XDG}/config" XDG_STATE_HOME="${GUEST_XDG}/state" XDG_CACHE_HOME="${GUEST_XDG}/cache" "$@"
}

run_clean_recovery() {
  run_guest_user /usr/bin/podlaz recover --json >"${PRIVATE_ROOT}/recover.json"
  guest_exec /bin/bash -lc "cat >'${GUEST_PRIVATE}/recover.json'" <"${PRIVATE_ROOT}/recover.json"
  guest_exec /bin/bash -lc "cd /workspace && source scripts/e2e/lib/e2e.sh && source scripts/e2e/lib/recovery_json.sh && assert_clean_recovery_json_file '${GUEST_PRIVATE}/recover.json'"
}

run_retry_cycle() {
  guest_exec /bin/bash -lc "id=\$(cat /tmp/podlaz-hosted-synthetic-tun/profile-id); runuser -u e2e -- env XDG_CONFIG_HOME='${GUEST_XDG}/config' XDG_STATE_HOME='${GUEST_XDG}/state' XDG_CACHE_HOME='${GUEST_XDG}/cache' /usr/bin/podlaz connect --mode tun \"\${id}\" >/dev/null"
  wait_for_guest_status verified-active 160
  guest_exec nft list table inet "${FOREIGN_NFT_TABLE}" >/dev/null
  guest_exec python3 "${FALLBACK_NETWORK_HELPER}" snapshot "${TRANSACTION_DIR}" "${RETRY_MANIFEST}" >/dev/null
  record_evidence stale.retry_verified_active pass
  run_guest_user /usr/bin/podlaz disconnect >/dev/null
  wait_for_guest_status clean-inactive 160
  guest_exec /bin/bash -lc "cd /workspace && source scripts/e2e/lib/e2e.sh && source scripts/e2e/lib/tun_package_assertions.sh && verify_tun_package_resources_absent retry '${FALLBACK_NETWORK_HELPER}' '${RETRY_MANIFEST}'"
  guest_exec nft list table inet "${FOREIGN_NFT_TABLE}" >/dev/null
  run_clean_recovery
  record_evidence stale.retry_terminal_cleanup pass
}

run_scenario() {
  local candidate="$1"
  mark_failure diagnostic_unknown base.candidate_ready
  run_base_scenario "${candidate}"
  wait_for_control_ready candidate-ready || fail "base synthetic TUN did not reach candidate-ready"
  grep -Fx 'candidate.provenance=pass' "${BASE_REPORT}" >/dev/null || fail "candidate provenance did not pass"
  record_evidence candidate.provenance pass

  mark_failure fixture stale.hook
  install_stale_hook || fail "could not install supported stale-observation hook"
  release_control candidate-ready || fail "could not release candidate-ready"

  mark_failure product stale.pre_fault_authority
  wait_for_hook_ready || fail "connect did not reach DNS missing-link boundary"
  guest_exec ip link show dev podlaz0 >/dev/null
  guest_exec resolvectl status podlaz0 --no-pager >/dev/null
  assert_exact_pre_fault_authority || fail "pre-fault cleanup authority is not exact"
  guest_exec install -d -m 0700 "${GUEST_PRIVATE}"
  guest_exec python3 "${FALLBACK_NETWORK_HELPER}" snapshot "${TRANSACTION_DIR}" "${NETWORK_MANIFEST}" >/dev/null
  record_evidence stale.observation_not_authority pass

  mark_failure product stale.inject
  guest_exec ip link del dev podlaz0
  guest_exec /bin/bash -lc "printf 'continue\\n' >'${HOOK_CONTINUE}'; chmod 0600 '${HOOK_CONTINUE}'"
  record_evidence stale.fault_injected pass

  mark_failure product stale.rollback
  wait_for_control_ready connect-failed || fail "failed connect did not reach bounded inspection boundary"
  assert_missing_link_capture || fail "production DNS rollback did not classify exact missing-link envelope"
  record_evidence stale.resolved_missing_link pass
  assert_rollback_events || fail "stale-observation rollback event ordering is invalid"
  record_evidence stale.rollback_order pass
  assert_failure_classification || fail "stale-observation diagnostic classification is incorrect"
  assert_owned_state_absent || fail "stale-observation rollback did not converge cleanly"
  record_evidence stale.rollback_converged pass
  record_evidence foreign.state_preserved pass

  mark_failure fixture stale.hook_cleanup
  remove_hook_override || fail "could not remove stale-observation hook override"
  cleanup_hook_dir || fail "could not remove stale-observation hook markers"

  mark_failure product stale.retry
  run_retry_cycle || fail "clean retry after stale observation did not converge"

  mark_failure diagnostic_unknown base.finish
  release_control connect-failed || fail "could not release failed-connect control"
  wait_base_expected_failure || fail "base expected-connect-failure scenario did not fail as expected"
  grep -Fx 'outer.cleanup=pass' "${BASE_REPORT}" >/dev/null || fail "base outer cleanup did not pass"
  record_evidence base.outer_cleanup pass

  FAILURE_CLASS=none
  FAILURE_STEP=none
  assert_public_artifact_privacy || fail "hosted stale observation public evidence is not privacy-safe"
  record_evidence artifact.privacy pass
}

main() {
  (($# == 1)) || fail "usage: $0 CANDIDATE.deb"
  [[ -f "$1" && ! -L "$1" ]] || fail "candidate package must be a regular file"
  [[ "${EXPECTED_COMMIT}" =~ ^[0-9a-fA-F]{40}$ ]] || fail "PODLAZ_E2E_CANDIDATE_COMMIT must be exact"
  require_cmd bash chmod find grep install ip nft python3 rm seq sleep sudo systemd-run
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
