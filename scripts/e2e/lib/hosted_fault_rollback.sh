#!/usr/bin/env bash

HOSTED_FAULT_LIB_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HOSTED_FAULT_E2E_DIR="$(cd "${HOSTED_FAULT_LIB_DIR}/.." && pwd)"
# shellcheck source=e2e.sh
source "${HOSTED_FAULT_LIB_DIR}/e2e.sh"

BASE_SCENARIO="${HOSTED_FAULT_E2E_DIR}/hosted-synthetic-tun.sh"
MACHINE="podlaz-synthetic-tun"
FOREIGN_NFT_TABLE="pzsynt_foreign"
HOOK_DIR="/run/podlaz/e2e-hosted-fault-rollback"
HOOK_EVENTS="${HOOK_DIR}/events.log"
HOOK_DROPIN_DIR="/run/systemd/system/podlazd.service.d"
HOOK_DROPIN="${HOOK_DROPIN_DIR}/99-hosted-fault-rollback.conf"
DIAGNOSTIC_REPORT="/run/podlaz/diagnostics/tun-last.json"
DAEMON_SOCKET="/run/podlaz/podlazd.sock"
SESSION_STATE="/run/podlaz/network-session-continuation.json"
GUEST_XDG="/home/e2e/.local/share/podlaz-hosted-synthetic-tun"
FAULT_GUEST_PRIVATE="/tmp/podlaz-hosted-fault-rollback"
STATUS_HELPER="/workspace/scripts/e2e/lib/daemon_status_semantics.py"

FAULT_PRIVATE_ROOT=""
BASE_TMP_ROOT=""
BASE_ARTIFACT_DIR=""
BASE_REPORT=""
BASE_STDOUT=""
BASE_STDERR=""
CONTROL_DIR=""
BASE_NETWORK_PREFIX=""
REPORT=""
BASE_PID=""
BASE_EXIT_CODE=""
FAILURE_CLASS="diagnostic_unknown"
FAILURE_STEP="bootstrap"
REPORT_FINALIZED=false

FAULT_EVIDENCE_KEYS=(
  candidate.provenance
  fault.injected
  connect.failed
  diagnostic.classification
  diagnostic.rollback_order
  owned_state.absent
  foreign_state.preserved
  recovery.clean
  base.outer_cleanup
  artifact.privacy
)

hosted_fault_record() {
  local key="$1" value="$2"
  printf '%s=%s\n' "${key}" "${value}" >>"${REPORT}"
}

hosted_fault_recorded() {
  local key="$1"
  grep -Eq "^${key}=" "${REPORT}" 2>/dev/null
}

hosted_fault_record_if_missing() {
  local key="$1" value="$2"
  hosted_fault_recorded "${key}" || hosted_fault_record "${key}" "${value}"
}

hosted_fault_mark_failure() {
  local class="$1" step="$2"
  case "${class}" in
    product|fixture|infrastructure|capability|diagnostic_unknown) ;;
    *) class=infrastructure ;;
  esac
  FAILURE_CLASS="${class}"
  FAILURE_STEP="${step//[^A-Za-z0-9_.-]/_}"
}

hosted_fault_finalize_report() {
  local key
  [[ "${REPORT_FINALIZED}" == false ]] || return 0
  for key in "${FAULT_EVIDENCE_KEYS[@]}"; do
    hosted_fault_record_if_missing "${key}" fail
  done
  printf 'failure.class=%s\n' "${FAILURE_CLASS}" >>"${REPORT}"
  printf 'failure.step=%s\n' "${FAILURE_STEP}" >>"${REPORT}"
  REPORT_FINALIZED=true
}

hosted_fault_validate_report() {
  [[ -f "${REPORT}" && ! -L "${REPORT}" ]] || fail "hosted fault rollback report is missing or invalid"
  python3 - "${REPORT}" <<'PY'
import sys

path = sys.argv[1]
required = {
    "candidate.provenance",
    "fault.injected",
    "connect.failed",
    "diagnostic.classification",
    "diagnostic.rollback_order",
    "owned_state.absent",
    "foreign_state.preserved",
    "recovery.clean",
    "base.outer_cleanup",
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
    raise SystemExit("unexpected hosted fault rollback report schema")
for key in required:
    if values[key] != "pass":
        raise SystemExit(f"required evidence is not pass: {key}={values[key]}")
if values["failure.class"] != "none" or values["failure.step"] != "none":
    raise SystemExit("hosted fault rollback reports a failure")
PY
}

hosted_fault_public_artifact_privacy() {
  local extra
  extra="$(find "${E2E_ARTIFACT_DIR}" -mindepth 1 -maxdepth 1 ! -name "${REPORT_BASENAME}" -print -quit)"
  [[ -z "${extra}" ]] || return 1
  [[ -f "${REPORT}" && ! -L "${REPORT}" ]] || return 1
  ! grep -Eiq 'vless://|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|172\.31\.(253|254)\.' "${REPORT}"
}

guest_exec() {
  sudo -n systemd-run --machine="${MACHINE}" --expand-environment=no --wait --pipe --collect --quiet -- "$@"
}

release_all_fault_controls() {
  local phase continue
  [[ -d "${CONTROL_DIR}" ]] || return 0
  for phase in candidate-ready connect-failed; do
    continue="${CONTROL_DIR}/${phase}.continue"
    [[ -e "${continue}" ]] || printf 'continue\n' >"${continue}"
    chmod 0600 "${continue}" >/dev/null 2>&1 || true
  done
}

hosted_fault_cleanup() {
  local code=$? attempt
  trap - EXIT INT TERM
  set +e
  release_all_fault_controls
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
  if [[ -f "${REPORT}" ]]; then
    if hosted_fault_public_artifact_privacy; then
      hosted_fault_record_if_missing artifact.privacy pass
    fi
    hosted_fault_finalize_report
  fi
  exit "${code}"
}

wait_for_fault_control_ready() {
  local phase="$1" ready attempt code
  ready="${CONTROL_DIR}/${phase}.ready"
  for attempt in $(seq 1 2400); do
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
      return 1
    fi
    sleep 0.1
  done
  return 1
}

release_fault_control() {
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

wait_base_expected_failure() {
  local code
  [[ -n "${BASE_PID}" ]] || return 1
  set +e
  wait "${BASE_PID}"
  code=$?
  set -e
  BASE_EXIT_CODE="${code}"
  BASE_PID=""
  (( code != 0 ))
}

run_fault_base_scenario() {
  local candidate="$1"
  rm -rf "${FAULT_PRIVATE_ROOT}"
  install -d -m 0700 "${FAULT_PRIVATE_ROOT}" "${BASE_TMP_ROOT}" "${BASE_ARTIFACT_DIR}" "${CONTROL_DIR}"
  env \
    E2E_TMP_ROOT="${BASE_TMP_ROOT}" \
    E2E_ARTIFACT_DIR="${BASE_ARTIFACT_DIR}" \
    PODLAZ_E2E_CANDIDATE_COMMIT="${PODLAZ_E2E_CANDIDATE_COMMIT}" \
    PODLAZ_E2E_HOSTED_CONTROL_DIR="${CONTROL_DIR}" \
    PODLAZ_E2E_HOSTED_CONTROL_PHASES="candidate-ready connect-failed" \
    PODLAZ_E2E_HOSTED_CONTROL_TIMEOUT_SECONDS=180 \
    PODLAZ_E2E_HOSTED_EXPECT_CONNECT_FAILURE=true \
    bash "${BASE_SCENARIO}" "${candidate}" >"${BASE_STDOUT}" 2>"${BASE_STDERR}" &
  BASE_PID=$!
}

wait_for_fault_daemon_ready() {
  local attempt
  for attempt in $(seq 1 100); do
    if guest_exec systemctl is-active --quiet podlazd.service >/dev/null 2>&1 && \
      guest_exec test -S "${DAEMON_SOCKET}" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.1
  done
  return 1
}

install_fault_hook() {
  guest_exec /bin/bash -lc "rm -rf '${HOOK_DIR}'; install -d -m 0700 '${HOOK_DIR}' '${HOOK_DROPIN_DIR}'; printf '%s\\n' '[Service]' 'Environment=PODLAZ_E2E_TUN_HOOKS=true' 'Environment=PODLAZ_E2E_TUN_HOOK_PHASE=${FAULT_PHASE}' 'Environment=PODLAZ_E2E_TUN_HOOK_DIR=${HOOK_DIR}' 'Environment=PODLAZ_E2E_TUN_HOOK_TIMEOUT_SECONDS=60' >'${HOOK_DROPIN}'; systemctl daemon-reload; systemctl restart podlazd.service" >/dev/null
  wait_for_fault_daemon_ready || return 1
}

assert_foreign_sentinel() {
  guest_exec nft list table inet "${FOREIGN_NFT_TABLE}" >/dev/null 2>&1
}

capture_fault_file() {
  local guest_path="$1" host_path="$2"
  guest_exec cat "${guest_path}" >"${host_path}"
  chmod 0600 "${host_path}"
}

assert_event_present() {
  local path="$1" event="$2"
  grep -Fx "${event}" "${path}" >/dev/null
}

assert_event_order() {
  local path="$1" first="$2" second="$3"
  python3 - "${path}" "${first}" "${second}" <<'PY'
import sys

path, first, second = sys.argv[1:]
with open(path, encoding="utf-8") as handle:
    events = [line.strip() for line in handle if line.strip()]
try:
    first_index = events.index(first)
    second_index = events.index(second)
except ValueError as exc:
    raise SystemExit(f"missing lifecycle event: {exc}; events={events}")
if first_index >= second_index:
    raise SystemExit(
        f"invalid lifecycle event order: {first}={first_index}, {second}={second_index}, events={events}"
    )
PY
}

assert_failure_report() {
  local path="$1"
  python3 - "${path}" "${FAULT_FAILURE_PHASE}" "${FAULT_CLASSIFICATION}" <<'PY'
import json
import sys

path, phase, classification = sys.argv[1:]
with open(path, encoding="utf-8") as handle:
    report = json.load(handle)
expected = {
    "failure_phase": phase,
    "primary_classification": classification,
    "rollback_status": "completed",
}
for key, value in expected.items():
    if report.get(key) != value:
        raise SystemExit(f"{key}={report.get(key)!r}, expected {value!r}")
if report.get("primary_classification") in {"healthy", "degraded", "unhealthy", "unavailable"}:
    raise SystemExit("overall health state leaked into failure classification")
PY
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

wait_for_guest_network_baseline() {
  local current="${FAULT_PRIVATE_ROOT}/guest-after-fault" suffix attempt matched
  for attempt in $(seq 1 100); do
    capture_guest_network_snapshot "${current}"
    matched=true
    for suffix in addr.json routes.json rules.json nft.json nm.txt resolved.txt; do
      if ! cmp -s "${BASE_NETWORK_PREFIX}.${suffix}" "${current}.${suffix}"; then
        matched=false
        break
      fi
    done
    [[ "${matched}" == true ]] && return 0
    sleep 0.25
  done
  return 1
}

wait_for_clean_inactive() {
  local attempt
  guest_exec install -d -m 0700 "${FAULT_GUEST_PRIVATE}" >/dev/null
  for attempt in $(seq 1 160); do
    if guest_exec /bin/bash -lc "curl --fail --silent --show-error --max-time 3 --unix-socket '${DAEMON_SOCKET}' http://localhost/v1/status >'${FAULT_GUEST_PRIVATE}/status.json' 2>/dev/null && python3 '${STATUS_HELPER}' clean-inactive '${FAULT_GUEST_PRIVATE}/status.json' >/dev/null" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.25
  done
  return 1
}

assert_owned_state_absent() {
  wait_for_clean_inactive || return 1
  guest_exec /bin/bash -lc "test ! -e '${SESSION_STATE}' && test ! -e /run/podlaz/generated/xray.json && ! ip link show dev podlaz0 >/dev/null 2>&1 && ! nft list table inet podlaz >/dev/null 2>&1 && nft list tables >'${FAULT_GUEST_PRIVATE}/nft-tables.txt' && ! grep -E 'table inet podlaz_pe_[0-9a-f]+' '${FAULT_GUEST_PRIVATE}/nft-tables.txt' >/dev/null && python3 -c 'import glob,sys; raise SystemExit(1 if glob.glob(\"/run/podlaz/transactions/*.json\") else 0)'" || return 1
  wait_for_guest_network_baseline
}

assert_clean_recovery() {
  guest_exec /bin/bash -lc "runuser -u e2e -- env XDG_CONFIG_HOME='${GUEST_XDG}/config' XDG_STATE_HOME='${GUEST_XDG}/state' XDG_CACHE_HOME='${GUEST_XDG}/cache' /usr/bin/podlaz recover --json >'${FAULT_GUEST_PRIVATE}/recover.json' 2>'${FAULT_GUEST_PRIVATE}/recover.stderr'"
  guest_exec /bin/bash -lc "cd /workspace && source scripts/e2e/lib/e2e.sh && source scripts/e2e/lib/recovery_json.sh && assert_clean_recovery_json_file '${FAULT_GUEST_PRIVATE}/recover.json'"
}

run_fault_scenario() {
  local candidate="$1" events diagnostic

  hosted_fault_mark_failure diagnostic_unknown base.candidate_ready
  run_fault_base_scenario "${candidate}"
  wait_for_fault_control_ready candidate-ready || fail "base synthetic TUN did not reach candidate-ready boundary"
  grep -Fx 'candidate.provenance=pass' "${BASE_REPORT}" >/dev/null || fail "base candidate provenance did not pass"
  hosted_fault_record candidate.provenance pass

  hosted_fault_mark_failure fixture fault.hook_install
  install_fault_hook || fail "could not install supported TUN fault hook"

  hosted_fault_mark_failure product fault.connect
  release_fault_control candidate-ready || fail "could not release candidate-ready boundary"
  wait_for_fault_control_ready connect-failed || fail "faulted connect did not reach expected failed-connect boundary"
  hosted_fault_record connect.failed pass

  events="${FAULT_PRIVATE_ROOT}/events.log"
  diagnostic="${FAULT_PRIVATE_ROOT}/tun-last.json"
  capture_fault_file "${HOOK_EVENTS}" "${events}" || fail "fault hook events are unavailable"
  capture_fault_file "${DIAGNOSTIC_REPORT}" "${diagnostic}" || fail "TUN failure diagnostic is unavailable"
  assert_event_present "${events}" "${FAULT_EVENT}" || fail "expected fault injection event is absent"
  hosted_fault_record fault.injected pass
  assert_failure_report "${diagnostic}" || fail "failure diagnostic phase/classification/rollback status is incorrect"
  hosted_fault_record diagnostic.classification pass
  assert_event_present "${events}" diagnostics-persisted || fail "diagnostic persistence event is absent"
  assert_event_present "${events}" rollback-started || fail "rollback start event is absent"
  assert_event_present "${events}" rollback-completed || fail "rollback completion event is absent"
  assert_event_order "${events}" diagnostics-persisted rollback-started || fail "diagnostics were not persisted before rollback"
  assert_event_order "${events}" rollback-started rollback-completed || fail "rollback lifecycle order is incorrect"
  hosted_fault_record diagnostic.rollback_order pass

  hosted_fault_mark_failure product fault.post_rollback
  assert_foreign_sentinel || fail "foreign nft state changed during rollback"
  hosted_fault_record foreign_state.preserved pass
  assert_owned_state_absent || fail "owned network/runtime state did not return to exact pre-connect baseline"
  hosted_fault_record owned_state.absent pass
  assert_clean_recovery || fail "post-rollback recovery inspection is not clean"
  hosted_fault_record recovery.clean pass

  hosted_fault_mark_failure diagnostic_unknown base.teardown
  release_fault_control connect-failed || fail "could not release expected failed-connect boundary"
  wait_base_expected_failure || fail "base synthetic scenario unexpectedly succeeded after injected connect failure"
  grep -Fx 'outer.cleanup=pass' "${BASE_REPORT}" >/dev/null || fail "base outer hosted plumbing did not cleanly restore"
  grep -Fx 'artifact.privacy=pass' "${BASE_REPORT}" >/dev/null || fail "base private/public evidence boundary failed"
  hosted_fault_record base.outer_cleanup pass

  FAILURE_CLASS=none
  FAILURE_STEP=none
  hosted_fault_public_artifact_privacy || fail "hosted fault rollback public evidence is not privacy-safe"
  hosted_fault_record artifact.privacy pass
}

run_hosted_fault_rollback() {
  if [[ "${1:-}" == validate-report ]]; then
    FAULT_PRIVATE_ROOT="${E2E_TMP_ROOT}/hosted-fault-rollback-private"
    REPORT="${E2E_ARTIFACT_DIR}/${REPORT_BASENAME}"
    hosted_fault_validate_report
    return 0
  fi

  (($# == 1)) || fail "usage: $0 CANDIDATE.deb"
  [[ -f "$1" && ! -L "$1" ]] || fail "candidate package must be a regular file"
  [[ "${PODLAZ_E2E_CANDIDATE_COMMIT:-}" =~ ^[0-9a-fA-F]{40}$ ]] || fail "PODLAZ_E2E_CANDIDATE_COMMIT must be an exact 40-hex commit"
  [[ "${FAULT_PHASE}" =~ ^[a-z0-9-]+$ ]] || fail "invalid fault phase"
  [[ "${FAULT_FAILURE_PHASE}" =~ ^[a-z0-9-]+$ ]] || fail "invalid failure phase"
  [[ "${FAULT_CLASSIFICATION}" =~ ^[a-z0-9_]+$ ]] || fail "invalid fault classification"
  [[ "${FAULT_EVENT}" =~ ^[a-z0-9-]+$ ]] || fail "invalid fault event"
  [[ "${REPORT_BASENAME}" =~ ^[a-z0-9.-]+$ ]] || fail "invalid report basename"

  require_cmd awk bash chmod cmp find grep install jq python3 rm seq sleep sudo systemd-run
  FAULT_PRIVATE_ROOT="${E2E_TMP_ROOT}/hosted-fault-rollback-private"
  BASE_TMP_ROOT="${FAULT_PRIVATE_ROOT}/base-private"
  BASE_ARTIFACT_DIR="${FAULT_PRIVATE_ROOT}/base-public"
  BASE_REPORT="${BASE_ARTIFACT_DIR}/hosted-synthetic-tun.txt"
  BASE_STDOUT="${FAULT_PRIVATE_ROOT}/base.stdout"
  BASE_STDERR="${FAULT_PRIVATE_ROOT}/base.stderr"
  CONTROL_DIR="${BASE_TMP_ROOT}/control"
  BASE_NETWORK_PREFIX="${BASE_TMP_ROOT}/private/guest-baseline"
  REPORT="${E2E_ARTIFACT_DIR}/${REPORT_BASENAME}"

  install -d -m 0700 "${E2E_TMP_ROOT}" "${E2E_ARTIFACT_DIR}"
  : >"${REPORT}"
  chmod 0600 "${REPORT}"
  trap hosted_fault_cleanup EXIT INT TERM
  run_fault_scenario "$1"
  trap - EXIT INT TERM
  hosted_fault_finalize_report
  hosted_fault_validate_report
}
