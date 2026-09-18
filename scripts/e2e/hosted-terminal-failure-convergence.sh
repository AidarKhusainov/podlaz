#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"

BASE_SCENARIO="${SCRIPT_DIR}/hosted-synthetic-tun.sh"
REPORT="${E2E_ARTIFACT_DIR}/hosted-terminal-failure-convergence.txt"
MACHINE="podlaz-synthetic-tun"
GUEST_IF="host0"
FOREIGN_NFT_TABLE="pzsynt_foreign"
SESSION_STATE="/run/podlaz/network-session-continuation.json"
DAEMON_SOCKET="/run/podlaz/podlazd.sock"
HOOK_DIR="/run/podlaz/hosted-terminal-failure"
OVERRIDE_DIR="/run/systemd/system/podlazd.service.d"
OVERRIDE_PATH="${OVERRIDE_DIR}/99-hosted-terminal-failure.conf"
GUEST_PRIVATE="/tmp/podlaz-hosted-terminal-failure"
BASE_GUEST_PRIVATE="/tmp/podlaz-hosted-synthetic-tun"
GUEST_MANIFEST="${BASE_GUEST_PRIVATE}/network-manifest.json"
FALLBACK_NETWORK_HELPER="/workspace/scripts/e2e/tun-package-fallback-network.py"
STATUS_HELPER="/workspace/scripts/e2e/lib/daemon_status_semantics.py"
PRIVATE_ROOT="${E2E_TMP_ROOT}/hosted-terminal-failure-private"
BASE_TMP_ROOT="${PRIVATE_ROOT}/base-private"
BASE_ARTIFACT_DIR="${PRIVATE_ROOT}/base-public"
BASE_REPORT="${BASE_ARTIFACT_DIR}/hosted-synthetic-tun.txt"
BASE_STDOUT="${PRIVATE_ROOT}/base.stdout"
BASE_STDERR="${PRIVATE_ROOT}/base.stderr"
CONTROL_DIR="${BASE_TMP_ROOT}/control"
EXPECTED_COMMIT="${PODLAZ_E2E_CANDIDATE_COMMIT:-}"

EVIDENCE_KEYS=(
  candidate.active_control
  privacy.envelope_retained
  privacy.direct_uplink_blocked
  foreign.state_preserved
  terminal.data_plane_clean
  terminal.converged_once
  terminal.authority_clean
  connectivity.restored
  terminal.no_hidden_retry
  base.terminal_cleanup
  artifact.privacy
)

FAILURE_CLASS=diagnostic_unknown
FAILURE_STEP=bootstrap
REPORT_FINALIZED=false
BASE_PID=""
BASE_EXIT_CODE=""
SESSION_ID=""
PE_FAMILY=""
PE_TABLE=""
PROBE_IP=""
READY_FINGERPRINT=""

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
  [[ -f "${REPORT}" ]] || fail "hosted terminal failure report is missing"
  python3 - "${REPORT}" <<'PY'
import sys

path = sys.argv[1]
required = {
    "candidate.active_control",
    "privacy.envelope_retained",
    "privacy.direct_uplink_blocked",
    "foreign.state_preserved",
    "terminal.data_plane_clean",
    "terminal.converged_once",
    "terminal.authority_clean",
    "connectivity.restored",
    "terminal.no_hidden_retry",
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
    raise SystemExit("unexpected hosted terminal failure report schema")
for key in required:
    if values[key] != "pass":
        raise SystemExit(f"required evidence is not pass: {key}={values[key]}")
if values["failure.class"] != "none" or values["failure.step"] != "none":
    raise SystemExit("hosted terminal failure reports a failure")
PY
}

assert_public_artifact_privacy() {
  local extra
  extra="$(find "${E2E_ARTIFACT_DIR}" -mindepth 1 -maxdepth 1 ! -name 'hosted-terminal-failure-convergence.txt' -print -quit)"
  [[ -z "${extra}" ]] || return 1
  [[ -f "${REPORT}" && ! -L "${REPORT}" ]] || return 1
  ! grep -Eiq 'vless://|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|172[.]31[.](253|254)[.]|session[_-]?id=|transaction[_-]?id=' "${REPORT}"
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

release_terminal_pause_if_needed() {
  if guest_exec test -f "${HOOK_DIR}/terminal-data-plane-clean.ready" >/dev/null 2>&1 &&
      ! guest_exec test -e "${HOOK_DIR}/terminal-data-plane-clean.continue" >/dev/null 2>&1; then
    guest_exec /bin/bash -lc "printf 'continue\\n' >'${HOOK_DIR}/terminal-data-plane-clean.continue'; chmod 0600 '${HOOK_DIR}/terminal-data-plane-clean.continue'" >/dev/null 2>&1 || true
  fi
}

remove_terminal_override() {
  if guest_exec test -e "${OVERRIDE_PATH}" >/dev/null 2>&1; then
    guest_exec rm -f "${OVERRIDE_PATH}" >/dev/null 2>&1 || return 1
    guest_exec systemctl daemon-reload >/dev/null 2>&1 || return 1
  fi
}

cleanup_hook_dir() {
  local names
  guest_exec test -d "${HOOK_DIR}" >/dev/null 2>&1 || return 0
  names="$(guest_exec find "${HOOK_DIR}" -mindepth 1 -maxdepth 1 -printf '%f\n' 2>/dev/null || true)"
  while IFS= read -r name; do
    [[ -n "${name}" ]] || continue
    case "${name}" in
      terminal-data-plane-clean.ready|terminal-data-plane-clean.continue) ;;
      *) return 1 ;;
    esac
  done <<<"${names}"
  guest_exec rm -f "${HOOK_DIR}/terminal-data-plane-clean.ready" "${HOOK_DIR}/terminal-data-plane-clean.continue" || return 1
  guest_exec rmdir "${HOOK_DIR}"
}

cleanup() {
  local saved=$? attempt cleanup_failed=0
  trap - EXIT INT TERM
  set +e
  release_terminal_pause_if_needed
  remove_terminal_override || cleanup_failed=1
  release_all_controls
  if [[ -n "${BASE_PID}" ]] && kill -0 "${BASE_PID}" >/dev/null 2>&1; then
    for attempt in $(seq 1 200); do
      kill -0 "${BASE_PID}" >/dev/null 2>&1 || break
      sleep 0.05
    done
  fi
  if [[ -n "${BASE_PID}" ]] && kill -0 "${BASE_PID}" >/dev/null 2>&1; then
    kill "${BASE_PID}" >/dev/null 2>&1 || true
    wait "${BASE_PID}" >/dev/null 2>&1 || true
  fi
  inherit_base_failure
  if assert_public_artifact_privacy; then
    record_if_missing artifact.privacy pass
  else
    record_if_missing artifact.privacy fail
    mark_failure fixture artifact.privacy
    cleanup_failed=1
  fi
  if (( saved == 0 && cleanup_failed != 0 )); then
    saved=1
  fi
  finalize_report
  set -e
  exit "${saved}"
}

wait_for_control_ready() {
  local phase="$1" ready attempt
  ready="${CONTROL_DIR}/${phase}.ready"
  for attempt in $(seq 1 1800); do
    if [[ -f "${ready}" && ! -L "${ready}" ]]; then
      return 0
    fi
    if [[ -n "${BASE_PID}" ]] && ! kill -0 "${BASE_PID}" >/dev/null 2>&1; then
      wait "${BASE_PID}" || true
      BASE_PID=""
      inherit_base_failure
      return 1
    fi
    sleep 0.1
  done
  return 1
}

release_control() {
  local phase="$1" ready continue
  ready="${CONTROL_DIR}/${phase}.ready"
  continue="${CONTROL_DIR}/${phase}.continue"
  [[ -f "${ready}" && ! -L "${ready}" ]] || return 1
  [[ ! -e "${continue}" && ! -L "${continue}" ]] || return 1
  printf 'continue\n' >"${continue}"
  chmod 0600 "${continue}"
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
    PODLAZ_E2E_HOSTED_EXPECT_EXTERNAL_TERMINAL=true \
    bash "${BASE_SCENARIO}" "${candidate}" >"${BASE_STDOUT}" 2>"${BASE_STDERR}" &
  BASE_PID=$!
}

validate_base_terminal_control() {
  env \
    E2E_TMP_ROOT="${BASE_TMP_ROOT}" \
    E2E_ARTIFACT_DIR="${BASE_ARTIFACT_DIR}" \
    PODLAZ_E2E_HOSTED_EXPECT_EXTERNAL_TERMINAL=true \
    bash "${BASE_SCENARIO}" validate-report >/dev/null
  grep -Fx 'candidate.provenance=pass' "${BASE_REPORT}" >/dev/null
  grep -Fx 'tun.verified_active=pass' "${BASE_REPORT}" >/dev/null
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

install_terminal_override() {
  guest_exec test ! -e "${HOOK_DIR}"
  guest_exec test ! -e "${OVERRIDE_PATH}"
  guest_exec install -d -m 0700 "${HOOK_DIR}"
  guest_exec install -d -m 0755 "${OVERRIDE_DIR}"
  guest_exec /bin/bash -lc "printf '%s\\n' '[Service]' 'Environment=PODLAZ_E2E_TUN_TERMINAL_FAILURE=true' 'Environment=PODLAZ_E2E_TUN_TERMINAL_FAILURE_DIR=${HOOK_DIR}' 'Environment=PODLAZ_E2E_PRIVACY_TEARDOWN_PAUSE=true' 'Environment=PODLAZ_E2E_PRIVACY_TEARDOWN_PAUSE_DIR=${HOOK_DIR}' 'Environment=PODLAZ_E2E_PRIVACY_TEARDOWN_PAUSE_TIMEOUT_SECONDS=120' >'${OVERRIDE_PATH}'; chmod 0644 '${OVERRIDE_PATH}'; systemctl daemon-reload; systemctl restart podlazd.service"
  wait_for_guest_status clean-inactive 80
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
if state.get("intent") != "resume":
    raise SystemExit("active Network Session does not have resume intent")
if not re.fullmatch(r"[0-9a-f]{32}", session):
    raise SystemExit("invalid active Network Session identity")
if protection.get("state") != "armed" or family != "inet":
    raise SystemExit("Privacy Envelope is not armed")
if not re.fullmatch(r"podlaz_pe_[0-9a-f]{12}(?:_[1-9][0-9]{0,2})?", table):
    raise SystemExit("invalid Privacy Envelope identity")
print(session, family, table)
PY
)"
  [[ -n "${SESSION_ID}" && -n "${PE_FAMILY}" && -n "${PE_TABLE}" ]]
}

assert_terminal_intent_protection_retained() {
  guest_exec python3 - "${SESSION_STATE}" "${SESSION_ID}" "${PE_FAMILY}" "${PE_TABLE}" <<'PY'
import json,sys
with open(sys.argv[1], encoding="utf-8") as handle:
    state=json.load(handle)
protection=state.get("protection") or {}
if state.get("session_id") != sys.argv[2]:
    raise SystemExit("Network Session identity changed")
if state.get("intent") != "terminal":
    raise SystemExit(f"Network Session intent={state.get('intent')!r}, expected terminal")
if protection.get("state") != "armed":
    raise SystemExit("Privacy Envelope authority is not armed")
if protection.get("family") != sys.argv[3] or protection.get("table") != sys.argv[4]:
    raise SystemExit("Privacy Envelope authority identity changed")
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

capture_foreign_sentinel() {
  local target="$1"
  guest_exec /bin/bash -lc "nft -j list table inet '${FOREIGN_NFT_TABLE}' >'${target}'"
}

assert_foreign_sentinel_unchanged() {
  local before="${GUEST_PRIVATE}/foreign-before.json" current="${GUEST_PRIVATE}/foreign-current.json"
  capture_foreign_sentinel "${current}"
  guest_exec cmp -s "${before}" "${current}"
  guest_exec nmcli -t -f NAME,DEVICE connection show --active | grep -Fx "synthetic-uplink:${GUEST_IF}" >/dev/null
}

assert_terminal_data_plane_clean() {
  guest_exec /bin/bash -lc "cd /workspace && source scripts/e2e/lib/e2e.sh && source scripts/e2e/lib/tun_package_assertions.sh && verify_tun_package_resources_absent terminal-failure '${FALLBACK_NETWORK_HELPER}' '${GUEST_MANIFEST}'"
}

wait_for_terminal_cleanup_boundary() {
  local marker="${HOOK_DIR}/terminal-data-plane-clean.ready" attempt
  for attempt in $(seq 1 800); do
    if guest_exec test -f "${marker}" >/dev/null 2>&1; then
      guest_exec /bin/bash -lc "[[ ! -L '${marker}' && \"\$(stat -c '%a' '${marker}')\" == 600 && \"\$(cat '${marker}')\" == 'phase=terminal-data-plane-clean' ]]"
      return 0
    fi
    sleep 0.1
  done
  return 1
}

terminal_ready_fingerprint() {
  guest_exec stat -c '%d:%i:%y:%z:%s' "${HOOK_DIR}/terminal-data-plane-clean.ready"
}

release_terminal_cleanup_boundary() {
  guest_exec test ! -e "${HOOK_DIR}/terminal-data-plane-clean.continue"
  guest_exec /bin/bash -lc "printf 'continue\\n' >'${HOOK_DIR}/terminal-data-plane-clean.continue'; chmod 0600 '${HOOK_DIR}/terminal-data-plane-clean.continue'"
}

assert_terminal_authority_clean() {
  assert_terminal_data_plane_clean || return 1
  guest_exec test ! -e "${SESSION_STATE}" || return 1
  if guest_exec nft list table "${PE_FAMILY}" "${PE_TABLE}" >/dev/null 2>&1; then
    return 1
  fi
  if guest_exec /bin/bash -lc "nft list tables | grep -E 'table inet podlaz_pe_[0-9a-f]+'" >/dev/null 2>&1; then
    return 1
  fi
  assert_foreign_sentinel_unchanged
}

assert_terminal_stable_sample() {
  capture_status >/dev/null || return 1
  guest_exec python3 "${STATUS_HELPER}" terminal-inactive "${GUEST_PRIVATE}/status.json" >/dev/null || return 1
  assert_terminal_authority_clean || return 1
  guest_exec test ! -e "${HOOK_DIR}/terminal-failure.trigger" || return 1
  [[ "$(terminal_ready_fingerprint)" == "${READY_FINGERPRINT}" ]]
}

watch_terminal_stability() {
  local samples="$1" attempt
  for attempt in $(seq 1 "${samples}"); do
    assert_terminal_stable_sample || return 1
    sleep 0.25
  done
}

restart_daemon_without_fault_hooks() {
  remove_terminal_override
  guest_exec systemctl restart podlazd.service
  guest_exec systemctl is-active --quiet podlazd.service
  wait_for_guest_status terminal-inactive 60
}

assert_ordinary_connectivity() {
  guest_exec resolvectl flush-caches
  guest_exec timeout 20 getent ahostsv4 example.com >/dev/null
  guest_exec timeout 30 curl -4 -fsS -o /dev/null https://example.com/
}

run_scenario() {
  local candidate="$1"

  mark_failure diagnostic_unknown base.candidate_ready
  run_base_scenario "${candidate}"
  wait_for_control_ready candidate-ready || fail "base synthetic TUN did not reach candidate-ready boundary"
  grep -Fx 'candidate.provenance=pass' "${BASE_REPORT}" >/dev/null || fail "base candidate provenance did not pass"

  mark_failure fixture terminal.hook_install
  install_terminal_override || fail "could not install supported terminal-failure hooks"
  release_control candidate-ready || fail "could not release candidate-ready boundary"

  mark_failure diagnostic_unknown base.verified_active
  wait_for_control_ready verified-active || fail "base synthetic TUN did not reach verified-active boundary"
  grep -Fx 'tun.verified_active=pass' "${BASE_REPORT}" >/dev/null || fail "base exact active authority did not pass"
  record_evidence candidate.active_control pass

  mark_failure product terminal.active_authority
  load_active_identity || fail "active Network Session/Privacy Envelope authority is invalid"
  assert_privacy_envelope_present || fail "exact Privacy Envelope is absent before terminal injection"
  prepare_direct_probe || fail "could not prepare direct-uplink leak probe"
  guest_exec install -d -m 0700 "${GUEST_PRIVATE}"
  capture_foreign_sentinel "${GUEST_PRIVATE}/foreign-before.json" || fail "could not capture foreign nftables sentinel"

  mark_failure product terminal.inject
  guest_exec test ! -e "${HOOK_DIR}/terminal-failure.trigger" || fail "terminal-failure trigger is already present"
  guest_exec touch "${HOOK_DIR}/terminal-failure.trigger"
  wait_for_terminal_cleanup_boundary || fail "terminal failure did not reach exact data-plane-clean boundary"

  mark_failure product terminal.fail_closed_window
  assert_terminal_data_plane_clean || fail "terminal boundary did not clean exact data-plane authority first"
  record_evidence terminal.data_plane_clean pass
  assert_terminal_intent_protection_retained || fail "terminal boundary lost Network Session/Privacy Envelope authority"
  assert_privacy_envelope_present || fail "Privacy Envelope disappeared before safe terminal cleanup completed"
  record_evidence privacy.envelope_retained pass
  assert_direct_uplink_blocked || fail "ordinary direct uplink escaped while terminal teardown remained protected"
  record_evidence privacy.direct_uplink_blocked pass
  assert_foreign_sentinel_unchanged || fail "foreign network state changed during terminal teardown"
  record_evidence foreign.state_preserved pass
  READY_FINGERPRINT="$(terminal_ready_fingerprint)"
  [[ -n "${READY_FINGERPRINT}" ]] || fail "terminal cleanup boundary identity is unavailable"

  mark_failure product terminal.release
  release_terminal_cleanup_boundary || fail "could not release supported terminal cleanup boundary"
  wait_for_guest_status terminal-inactive 160 || fail "terminal failure did not converge to inactive terminal outcome"
  wait_for_control_ready terminal-clean || fail "base synthetic TUN did not prove terminal cleanup"

  mark_failure product terminal.authority
  assert_terminal_authority_clean || fail "terminal convergence retained Podlaz authority"
  record_evidence terminal.authority_clean pass
  assert_ordinary_connectivity || fail "ordinary connectivity was not restored after terminal convergence"
  record_evidence connectivity.restored pass

  mark_failure product terminal.single_convergence
  watch_terminal_stability 20 || fail "terminal outcome changed or authority reappeared after convergence"
  [[ "$(terminal_ready_fingerprint)" == "${READY_FINGERPRINT}" ]] || fail "terminal cleanup boundary was entered more than once"

  mark_failure product terminal.restart_no_retry
  restart_daemon_without_fault_hooks || fail "daemon restart after terminal convergence failed"
  watch_terminal_stability 12 || fail "terminal outcome retried or reconnected after same-boot daemon restart"
  [[ "$(terminal_ready_fingerprint)" == "${READY_FINGERPRINT}" ]] || fail "terminal cleanup repeated after same-boot daemon restart"
  record_evidence terminal.converged_once pass
  record_evidence terminal.no_hidden_retry pass

  mark_failure fixture terminal.hook_cleanup
  cleanup_hook_dir || fail "terminal E2E hook cleanup failed"

  mark_failure diagnostic_unknown base.complete
  release_control terminal-clean || fail "could not release terminal-clean boundary"
  wait_base_completion || fail "base synthetic TUN scenario failed after terminal convergence"
  validate_base_terminal_control || fail "base terminal-control report is not clean"
  record_evidence base.terminal_cleanup pass

  FAILURE_CLASS=none
  FAILURE_STEP=none
  assert_public_artifact_privacy || fail "hosted terminal failure public evidence is not privacy-safe"
  record_evidence artifact.privacy pass
}

main() {
  (($# == 1)) || fail "usage: $0 CANDIDATE.deb"
  [[ -f "$1" && ! -L "$1" ]] || fail "candidate package must be a regular file"
  [[ "${EXPECTED_COMMIT}" =~ ^[0-9a-fA-F]{40}$ ]] || fail "PODLAZ_E2E_CANDIDATE_COMMIT must be an exact 40-hex commit"
  require_cmd awk bash chmod cmp find grep install kill python3 rm seq sleep sudo systemd-run
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
