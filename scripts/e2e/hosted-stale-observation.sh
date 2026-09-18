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
HOOK_DROPIN_DIR="/run/systemd/system/podlazd.service.d"
HOOK_DROPIN="${HOOK_DROPIN_DIR}/99-hosted-stale-observation.conf"
HOOK_READY="${HOOK_DIR}/dns-missing-link.ready"
HOOK_CONTINUE="${HOOK_DIR}/dns-missing-link.continue"
DNS_ROLLBACK_EXIT_CODE="${HOOK_DIR}/dns-rollback.exit-code"
DNS_ROLLBACK_STDOUT="${HOOK_DIR}/dns-rollback.stdout"
DNS_ROLLBACK_STDERR="${HOOK_DIR}/dns-rollback.stderr"
DIAGNOSTIC_REPORT="/run/podlaz/diagnostics/tun-last.json"
SESSION_STATE="/run/podlaz/network-session-continuation.json"
GUEST_XDG="/home/e2e/.local/share/podlaz-hosted-synthetic-tun"
STALE_GUEST_PRIVATE="/tmp/podlaz-hosted-stale-observation"
STATUS_HELPER="/workspace/scripts/e2e/lib/daemon_status_semantics.py"
MISSING_LINK_HELPER="/workspace/scripts/e2e/verify_resolvectl_missing_link.py"
EXPECTED_COMMIT="${PODLAZ_E2E_CANDIDATE_COMMIT:-}"

PRIVATE_ROOT="${E2E_TMP_ROOT}/hosted-stale-observation-private"
BASE_TMP_ROOT="${PRIVATE_ROOT}/base-private"
BASE_ARTIFACT_DIR="${PRIVATE_ROOT}/base-public"
BASE_REPORT="${BASE_ARTIFACT_DIR}/hosted-synthetic-tun.txt"
BASE_STDOUT="${PRIVATE_ROOT}/base.stdout"
BASE_STDERR="${PRIVATE_ROOT}/base.stderr"
CONTROL_DIR="${BASE_TMP_ROOT}/control"
BASE_NETWORK_PREFIX="${BASE_TMP_ROOT}/private/guest-baseline"
REPORT="${E2E_ARTIFACT_DIR}/hosted-stale-observation.txt"
BASE_PID=""
FAILURE_CLASS=diagnostic_unknown
FAILURE_STEP=bootstrap
REPORT_FINALIZED=false
OBSERVATION_LINK_CREATED=false
OBSERVATION_LINK_INDEX=""

EVIDENCE_KEYS=(
  candidate.provenance
  resolver.fault_injected
  resolver.missing_link_classified
  resolver.rollback_order
  resolver.bounded_convergence
  link.stale_classified
  observation.never_authority
  owned_state.absent
  foreign.state_preserved
  recovery.clean
  base.outer_cleanup
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
  [[ -f "${REPORT}" && ! -L "${REPORT}" ]] || fail "hosted stale observation report is missing or invalid"
  python3 - "${REPORT}" <<'PY'
import sys
required = {
    "candidate.provenance",
    "resolver.fault_injected",
    "resolver.missing_link_classified",
    "resolver.rollback_order",
    "resolver.bounded_convergence",
    "link.stale_classified",
    "observation.never_authority",
    "owned_state.absent",
    "foreign.state_preserved",
    "recovery.clean",
    "base.outer_cleanup",
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
    raise SystemExit("unexpected hosted stale observation report schema")
for key in required:
    if values[key] != "pass":
        raise SystemExit(f"required evidence is not pass: {key}={values[key]}")
if values["failure.class"] != "none" or values["failure.step"] != "none":
    raise SystemExit("hosted stale observation reports a failure")
PY
}

assert_public_artifact_privacy() {
  local extra
  extra="$(find "${E2E_ARTIFACT_DIR}" -mindepth 1 -maxdepth 1 ! -name 'hosted-stale-observation.txt' -print -quit)"
  [[ -z "${extra}" ]] || return 1
  [[ -f "${REPORT}" && ! -L "${REPORT}" ]] || return 1
  ! grep -Eiq 'vless://|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|172[.]31[.](253|254)[.]|session[_-]?id=|transaction[_-]?id=' "${REPORT}"
}

guest_exec() {
  sudo -n systemd-run --machine="${MACHINE}" --expand-environment=no --wait --pipe --collect --quiet -- "$@"
}

release_all_controls() {
  local phase path
  [[ -d "${CONTROL_DIR}" ]] || return 0
  for phase in candidate-ready connect-failed; do
    path="${CONTROL_DIR}/${phase}.continue"
    [[ -e "${path}" ]] || printf 'continue\n' >"${path}"
    chmod 0600 "${path}" >/dev/null 2>&1 || true
  done
  guest_exec /bin/bash -lc "if test -d '${HOOK_DIR}'; then test -e '${HOOK_CONTINUE}' || { printf 'continue\\n' >'${HOOK_CONTINUE}'; chmod 0600 '${HOOK_CONTINUE}'; }; fi" >/dev/null 2>&1 || true
}

remove_observation_link() {
  local current_index
  [[ "${OBSERVATION_LINK_CREATED}" == true ]] || return 0
  current_index="$(guest_exec /bin/bash -lc "ip -o link show dev podlaz0 2>/dev/null | awk -F: 'NR == 1 {gsub(/[[:space:]]/, \"\", \\$1); print \\$1}'" 2>/dev/null || true)"
  if [[ -z "${current_index}" ]]; then
    OBSERVATION_LINK_CREATED=false
    OBSERVATION_LINK_INDEX=""
    return 0
  fi
  [[ "${current_index}" == "${OBSERVATION_LINK_INDEX}" ]] || return 1
  guest_exec /bin/bash -lc "ip tuntap show dev podlaz0 2>/dev/null | grep -Eq '^podlaz0:[[:space:]]+tun([[:space:]]|$)'"
  guest_exec ip link del dev podlaz0
  OBSERVATION_LINK_CREATED=false
  OBSERVATION_LINK_INDEX=""
}

cleanup() {
  local saved=$? attempt cleanup_failed=0
  trap - EXIT INT TERM
  set +e
  remove_observation_link || cleanup_failed=1
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

wait_for_control_ready() {
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
        BASE_PID=""
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
    PODLAZ_E2E_HOSTED_CONTROL_PHASES="candidate-ready connect-failed" \
    PODLAZ_E2E_HOSTED_CONTROL_TIMEOUT_SECONDS=180 \
    PODLAZ_E2E_HOSTED_EXPECT_CONNECT_FAILURE=true \
    bash "${BASE_SCENARIO}" "${candidate}" >"${BASE_STDOUT}" 2>"${BASE_STDERR}" &
  BASE_PID=$!
}

wait_base_expected_failure() {
  local code
  [[ -n "${BASE_PID}" ]] || return 1
  set +e
  wait "${BASE_PID}"
  code=$?
  set -e
  BASE_PID=""
  (( code != 0 ))
}

wait_for_daemon_ready() {
  local attempt
  for attempt in $(seq 1 100); do
    if guest_exec systemctl is-active --quiet podlazd.service >/dev/null 2>&1 &&
       guest_exec test -S /run/podlaz/podlazd.sock >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.1
  done
  return 1
}

install_missing_link_hook() {
  guest_exec /bin/bash -lc "rm -rf '${HOOK_DIR}'; install -d -m 0700 '${HOOK_DIR}' '${HOOK_DROPIN_DIR}'; printf '%s\\n' '[Service]' 'Environment=PODLAZ_E2E_TUN_HOOKS=true' 'Environment=PODLAZ_E2E_TUN_HOOK_PHASE=dns-missing-link-rollback' 'Environment=PODLAZ_E2E_TUN_HOOK_DIR=${HOOK_DIR}' 'Environment=PODLAZ_E2E_TUN_HOOK_TIMEOUT_SECONDS=90' >'${HOOK_DROPIN}'; chmod 0644 '${HOOK_DROPIN}'; systemctl daemon-reload; systemctl restart podlazd.service"
  wait_for_daemon_ready
}

wait_for_missing_link_ready() {
  local attempt
  for attempt in $(seq 1 900); do
    if guest_exec test -f "${HOOK_READY}" >/dev/null 2>&1; then
      return 0
    fi
    [[ -n "${BASE_PID}" ]] && kill -0 "${BASE_PID}" >/dev/null 2>&1 || return 1
    sleep 0.1
  done
  return 1
}

assert_exact_fault_link_authority() {
  guest_exec python3 - /run/podlaz/transactions <<'PY'
import glob,json,socket,sys
paths=sorted(glob.glob(sys.argv[1].rstrip("/")+"/*.json"))
if len(paths)!=1:
    raise SystemExit(f"expected one transaction before link disappearance, found {len(paths)}")
with open(paths[0],encoding="utf-8") as handle:
    tx=json.load(handle)
address=(tx.get("desired_plan") or {}).get("tun_address") or {}
if tx.get("owner")!="podlaz" or tx.get("mode")!="tun" or tx.get("state") not in {"applying","applied","verifying"}:
    raise SystemExit("fault target transaction is not active apply authority")
if address.get("interface_name")!="podlaz0" or address.get("owner")!="podlaz:tun-address":
    raise SystemExit("fault target address is not exact Podlaz authority")
index=address.get("link_index")
if not isinstance(index,int) or index<=0 or address.get("link_kind")!="tun" or address.get("appeared_after_core") is not True:
    raise SystemExit("fault target link identity is incomplete")
if socket.if_nametoindex("podlaz0")!=index:
    raise SystemExit("fault target live link no longer matches persisted identity")
PY
}

inject_missing_link_fault() {
  assert_exact_fault_link_authority
  guest_exec resolvectl status podlaz0 --no-pager >/dev/null
  guest_exec ip link del dev podlaz0
  guest_exec /bin/bash -lc "test ! -e '${HOOK_CONTINUE}'; printf 'continue\\n' >'${HOOK_CONTINUE}'; chmod 0600 '${HOOK_CONTINUE}'"
}

capture_guest_file() {
  guest_exec cat "$1" >"$2"
  chmod 0600 "$2"
}

assert_event_order() {
  python3 - "$1" "$2" "$3" <<'PY'
import sys
path, first, second = sys.argv[1:]
with open(path, encoding="utf-8") as handle:
    events=[line.strip() for line in handle if line.strip()]
if first not in events or second not in events:
    raise SystemExit(f"missing lifecycle event: {first!r} or {second!r}")
if events.index(first) >= events.index(second):
    raise SystemExit(f"invalid lifecycle order: {first!r} >= {second!r}")
PY
}

assert_missing_link_classification() {
  local events="${PRIVATE_ROOT}/events.log" diagnostic="${PRIVATE_ROOT}/tun-last.json"
  capture_guest_file "${HOOK_EVENTS}" "${events}"
  capture_guest_file "${DIAGNOSTIC_REPORT}" "${diagnostic}"
  guest_exec grep -Fx 1 "${DNS_ROLLBACK_EXIT_CODE}" >/dev/null
  guest_exec test ! -s "${DNS_ROLLBACK_STDOUT}"
  guest_exec python3 "${MISSING_LINK_HELPER}" "${DNS_ROLLBACK_STDERR}"
  for event in dns-missing-link-ready dns-missing-link-released diagnostics-persisted rollback-started dns-rollback-started dns-rollback-result-captured rollback-completed; do
    grep -Fx "${event}" "${events}" >/dev/null || return 1
  done
  assert_event_order "${events}" diagnostics-persisted rollback-started
  assert_event_order "${events}" rollback-started dns-rollback-started
  assert_event_order "${events}" dns-rollback-started dns-rollback-result-captured
  assert_event_order "${events}" dns-rollback-result-captured rollback-completed
  python3 - "${diagnostic}" <<'PY'
import json,sys
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

assert_foreign_sentinel() {
  guest_exec nft list table inet "${FOREIGN_NFT_TABLE}" >/dev/null 2>&1
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
  local current="${PRIVATE_ROOT}/guest-after-rollback" suffix attempt matched
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

assert_owned_state_absent() {
  guest_exec test ! -e "${SESSION_STATE}" || return 1
  guest_exec test ! -e /run/podlaz/generated/xray.json || return 1
  guest_exec /bin/bash -lc '! ip link show dev podlaz0 >/dev/null 2>&1' || return 1
  guest_exec /bin/bash -lc '! nft list table inet podlaz >/dev/null 2>&1' || return 1
  guest_exec /bin/bash -lc "nft list tables >'${STALE_GUEST_PRIVATE}/nft-tables.txt'; ! grep -E 'table inet podlaz_pe_[0-9a-f]+' '${STALE_GUEST_PRIVATE}/nft-tables.txt' >/dev/null" || return 1
  guest_exec python3 -c 'import glob,sys; raise SystemExit(1 if glob.glob("/run/podlaz/transactions/*.json") else 0)' || return 1
  wait_for_guest_network_baseline
}

create_observation_only_foreign_link() {
  guest_exec /bin/bash -lc '! ip link show dev podlaz0 >/dev/null 2>&1'
  guest_exec ip tuntap add dev podlaz0 mode tun
  OBSERVATION_LINK_INDEX="$(guest_exec /bin/bash -lc "ip -o link show dev podlaz0 | awk -F: 'NR == 1 {gsub(/[[:space:]]/, \"\", \\$1); print \\$1}'")"
  [[ "${OBSERVATION_LINK_INDEX}" =~ ^[1-9][0-9]*$ ]]
  OBSERVATION_LINK_CREATED=true
}

assert_observation_only_foreign_link() {
  local doctor="${STALE_GUEST_PRIVATE}/doctor.txt" recovery="${STALE_GUEST_PRIVATE}/recover-execute.json" before after
  guest_exec install -d -m 0700 "${STALE_GUEST_PRIVATE}"
  guest_exec /bin/bash -lc "runuser -u e2e -- env XDG_CONFIG_HOME='${GUEST_XDG}/config' XDG_STATE_HOME='${GUEST_XDG}/state' XDG_CACHE_HOME='${GUEST_XDG}/cache' /usr/bin/podlaz doctor >'${doctor}' 2>&1"
  guest_exec grep -F '[WARN] stale-resources: found interface podlaz0 exists' "${doctor}" >/dev/null
  before="$(guest_exec stat -c '%d:%i:%y:%z' /sys/class/net/podlaz0)"
  guest_exec /bin/bash -lc "/usr/bin/podlaz recover --execute --yes --json >'${recovery}'"
  guest_exec python3 - "${recovery}" <<'PY'
import json,sys
with open(sys.argv[1],encoding="utf-8") as handle:
    payload=json.load(handle)
if payload.get("status")!="ok" or payload.get("mode")!="execute":
    raise SystemExit("recovery execute did not complete with observation-only skip")
results=payload.get("recovery") or []
matches=[
    item for item in results
    if isinstance(item,dict)
    and (item.get("candidate") or {}).get("kind")=="tun-interface"
    and (item.get("candidate") or {}).get("target")=="podlaz0"
]
if len(matches)!=1 or matches[0].get("status")!="skipped":
    raise SystemExit(f"standalone podlaz0 observation was not skipped exactly: {results!r}")
message=str(matches[0].get("message") or "").lower()
if "name alone" not in message or "ownership proof" not in message:
    raise SystemExit("standalone podlaz0 skip lacks ownership-authority reason")
PY
  after="$(guest_exec stat -c '%d:%i:%y:%z' /sys/class/net/podlaz0)"
  [[ "${before}" == "${after}" ]]
  guest_exec /bin/bash -lc "ip tuntap show dev podlaz0 | grep -Eq '^podlaz0:[[:space:]]+tun([[:space:]]|$)'"
}

assert_clean_recovery() {
  guest_exec /bin/bash -lc "/usr/bin/podlaz recover --execute --yes --json >'${STALE_GUEST_PRIVATE}/recover-clean.json'"
  guest_exec python3 - "${STALE_GUEST_PRIVATE}/recover-clean.json" <<'PY'
import json,sys
with open(sys.argv[1],encoding="utf-8") as handle:
    payload=json.load(handle)
if payload.get("status")!="ok" or payload.get("mode")!="execute":
    raise SystemExit("clean recovery execute did not report ok")
if payload.get("recovery") or payload.get("warnings") or payload.get("errors"):
    raise SystemExit("clean recovery execute retained cleanup evidence")
PY
}

run_scenario() {
  local candidate="$1"

  mark_failure diagnostic_unknown base.candidate_ready
  run_base_scenario "${candidate}"
  wait_for_control_ready candidate-ready || fail "base synthetic TUN did not reach candidate-ready boundary"
  grep -Fx 'candidate.provenance=pass' "${BASE_REPORT}" >/dev/null || fail "base candidate provenance did not pass"
  record_evidence candidate.provenance pass

  mark_failure fixture stale.hook_install
  install_missing_link_hook || fail "could not install supported missing-link hook"
  release_control candidate-ready || fail "could not release candidate-ready boundary"

  mark_failure product stale.wait_missing_link
  wait_for_missing_link_ready || fail "connect did not reach supported DNS missing-link boundary"
  inject_missing_link_fault || fail "could not inject exact link-disappearance fault"
  record_evidence resolver.fault_injected pass

  mark_failure product stale.connect_failure
  wait_for_control_ready connect-failed || fail "faulted connect did not reach expected failure boundary"
  assert_missing_link_classification || fail "missing-link resolver observation was not classified exactly"
  record_evidence resolver.missing_link_classified pass
  record_evidence resolver.rollback_order pass

  mark_failure product stale.convergence
  assert_owned_state_absent || fail "missing-link rollback did not boundedly converge to exact pre-connect state"
  record_evidence owned_state.absent pass
  record_evidence resolver.bounded_convergence pass
  assert_foreign_sentinel || fail "base foreign network state changed during missing-link rollback"
  record_evidence foreign.state_preserved pass

  mark_failure product stale.observation_authority
  create_observation_only_foreign_link || fail "could not create exact observation-only podlaz0 fixture"
  assert_observation_only_foreign_link || fail "standalone podlaz0 observation gained cleanup authority or lost precise classification"
  record_evidence link.stale_classified pass
  record_evidence observation.never_authority pass
  remove_observation_link || fail "could not safely remove exact observation-only fixture"

  mark_failure product stale.recovery_clean
  assert_clean_recovery || fail "recovery did not return to clean after observation fixture removal"
  record_evidence recovery.clean pass
  assert_foreign_sentinel || fail "foreign sentinel changed after stale-observation recovery"

  mark_failure diagnostic_unknown base.teardown
  release_control connect-failed || fail "could not release expected connect-failed boundary"
  wait_base_expected_failure || fail "base synthetic scenario unexpectedly succeeded after missing-link fault"
  grep -Fx 'outer.cleanup=pass' "${BASE_REPORT}" >/dev/null || fail "base outer hosted plumbing did not cleanly restore"
  grep -Fx 'artifact.privacy=pass' "${BASE_REPORT}" >/dev/null || fail "base private/public evidence boundary failed"
  record_evidence base.outer_cleanup pass

  FAILURE_CLASS=none
  FAILURE_STEP=none
  assert_public_artifact_privacy || fail "hosted stale observation public evidence is not privacy-safe"
  record_evidence artifact.privacy pass
}

main() {
  (($# == 1)) || fail "usage: $0 CANDIDATE.deb"
  [[ -f "$1" && ! -L "$1" ]] || fail "candidate package must be a regular file"
  [[ "${EXPECTED_COMMIT}" =~ ^[0-9a-fA-F]{40}$ ]] || fail "PODLAZ_E2E_CANDIDATE_COMMIT must be an exact 40-hex commit"
  require_cmd awk bash chmod cmp find grep install jq kill python3 rm seq sleep sudo systemd-run
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
