#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"

BASE_SCENARIO="${SCRIPT_DIR}/hosted-synthetic-tun.sh"
REPORT="${E2E_ARTIFACT_DIR}/hosted-orphan-routing-preflight.txt"
MACHINE="podlaz-synthetic-tun"
GUEST_XDG="/home/e2e/.local/share/podlaz-hosted-synthetic-tun"
GUEST_PRIVATE="/tmp/podlaz-hosted-synthetic-tun"
FOCUSED_GUEST_PRIVATE="/tmp/podlaz-hosted-orphan-routing"
DAEMON_SOCKET="/run/podlaz/podlazd.sock"
STATUS_HELPER="/workspace/scripts/e2e/lib/daemon_status_semantics.py"
NETWORK_AUTHORITY_HELPER="/workspace/scripts/e2e/hosted_synthetic_network_authority.py"
ORPHAN_FIXTURE_HELPER="/workspace/scripts/e2e/hosted_orphan_routing_fixture.py"
RECOVERY_RULE="/etc/polkit-1/rules.d/50-podlaz-hosted-orphan-recovery.rules"

PRIVATE_ROOT="${E2E_TMP_ROOT}/hosted-orphan-routing-private"
BASE_TMP_ROOT="${PRIVATE_ROOT}/base-private"
BASE_ARTIFACT_DIR="${PRIVATE_ROOT}/base-public"
BASE_REPORT="${BASE_ARTIFACT_DIR}/hosted-synthetic-tun.txt"
BASE_STDOUT="${PRIVATE_ROOT}/base.stdout"
BASE_STDERR="${PRIVATE_ROOT}/base.stderr"
CONTROL_DIR="${BASE_TMP_ROOT}/control"
BASE_GUEST_ROOT="${BASE_TMP_ROOT}/system-guest"
BASE_GUEST_BASELINE="${BASE_TMP_ROOT}/private/guest-baseline"
EXPECTED_COMMIT="${PODLAZ_E2E_CANDIDATE_COMMIT:-}"

AUTHORITY_MANIFEST="${FOCUSED_GUEST_PRIVATE}/current-authority.json"
ORPHAN_MANIFEST="${FOCUSED_GUEST_PRIVATE}/orphan-rules.json"

EVIDENCE_KEYS=(
  candidate.current_authority_seeded
  preflight.blocked_before_mutation
  ownership.observation_not_authority
  recovery.unauthorized_noop
  foreign.state_preserved
  terminal.clean_after_fixture_removal
  base.outer_cleanup
  artifact.privacy
)

FAILURE_CLASS=diagnostic_unknown
FAILURE_STEP=bootstrap
REPORT_FINALIZED=false
BASE_PID=""
PROFILE_ID=""
RECOVERY_RULE_INSTALLED=false
ORPHAN_FIXTURE_CREATED=false

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
  [[ -f "${REPORT}" && ! -L "${REPORT}" ]] || fail "hosted orphan routing report is missing"
  python3 - "${REPORT}" <<'PY'
import sys
required = {
    "candidate.current_authority_seeded",
    "preflight.blocked_before_mutation",
    "ownership.observation_not_authority",
    "recovery.unauthorized_noop",
    "foreign.state_preserved",
    "terminal.clean_after_fixture_removal",
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
    raise SystemExit("unexpected hosted orphan routing report schema")
for key in required:
    if values[key] != "pass":
        raise SystemExit(f"required evidence is not pass: {key}={values[key]}")
if values["failure.class"] != "none" or values["failure.step"] != "none":
    raise SystemExit("hosted orphan routing report contains failure metadata")
PY
}

assert_public_artifact_privacy() {
  local extra
  extra="$(find "${E2E_ARTIFACT_DIR}" -mindepth 1 -maxdepth 1 ! -name 'hosted-orphan-routing-preflight.txt' -print -quit)"
  [[ -z "${extra}" ]] || return 1
  [[ -f "${REPORT}" && ! -L "${REPORT}" ]] || return 1
  ! grep -Eiq 'vless://|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|172[.]31[.](253|254)[.]|session[_-]?id=|transaction[_-]?id=' "${REPORT}"
}

guest_exec() {
  sudo -n systemd-run --machine="${MACHINE}" --expand-environment=no --wait --pipe --collect --quiet -- "$@"
}

run_e2e_podlaz() {
  guest_exec runuser -u e2e -- env     XDG_CONFIG_HOME="${GUEST_XDG}/config"     XDG_STATE_HOME="${GUEST_XDG}/state"     XDG_CACHE_HOME="${GUEST_XDG}/cache"     /usr/bin/podlaz "$@"
}

inherit_base_failure() {
  local class step
  [[ -f "${BASE_REPORT}" ]] || return 0
  class="$(awk -F= '$1 == "failure.class" {print $2; exit}' "${BASE_REPORT}" 2>/dev/null || true)"
  step="$(awk -F= '$1 == "failure.step" {print $2; exit}' "${BASE_REPORT}" 2>/dev/null || true)"
  case "${class}" in
    product|fixture|infrastructure|capability|diagnostic_unknown) FAILURE_CLASS="${class}" ;;
    *) ;;
  esac
  if [[ -n "${step}" && "${step}" != none ]]; then
    FAILURE_STEP="base.${step//[^A-Za-z0-9_.-]/_}"
  fi
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

remove_orphan_fixture() {
  [[ "${ORPHAN_FIXTURE_CREATED}" == true ]] || return 0
  guest_exec python3 "${ORPHAN_FIXTURE_HELPER}" remove "${ORPHAN_MANIFEST}"
  ORPHAN_FIXTURE_CREATED=false
}

cleanup() {
  local code=$? attempt
  trap - EXIT INT TERM
  set +e
  remove_orphan_fixture
  remove_recovery_authorization
  release_all_controls
  if [[ -n "${BASE_PID}" ]] && kill -0 "${BASE_PID}" >/dev/null 2>&1; then
    for attempt in $(seq 1 120); do
      kill -0 "${BASE_PID}" >/dev/null 2>&1 || break
      sleep 0.5
    done
    kill -TERM "${BASE_PID}" >/dev/null 2>&1 || true
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
  local phase="$1" ready="${CONTROL_DIR}/$1.ready" attempt code
  for attempt in $(seq 1 1800); do
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
      inherit_base_failure
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
  env     E2E_TMP_ROOT="${BASE_TMP_ROOT}"     E2E_ARTIFACT_DIR="${BASE_ARTIFACT_DIR}"     PODLAZ_E2E_CANDIDATE_COMMIT="${EXPECTED_COMMIT}"     PODLAZ_E2E_HOSTED_CONTROL_DIR="${CONTROL_DIR}"     PODLAZ_E2E_HOSTED_CONTROL_PHASES="candidate-ready connect-failed"     PODLAZ_E2E_HOSTED_CONTROL_TIMEOUT_SECONDS=180     PODLAZ_E2E_HOSTED_EXPECT_CONNECT_FAILURE=true     bash "${BASE_SCENARIO}" "${candidate}" >"${BASE_STDOUT}" 2>"${BASE_STDERR}" &
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

install_recovery_authorization() {
  local rule_tmp
  rule_tmp="$(mktemp "${PRIVATE_ROOT}/recover-polkit.XXXXXX")"
  cat >"${rule_tmp}" <<'EOF_RULE'
polkit.addRule(function(action, subject) {
    if (subject.user == "e2e" &&
        action.id == "io.github.aidarkhusainov.podlaz.recover-execute") {
        return polkit.Result.YES;
    }
});
EOF_RULE
  sudo -n install -D -m 0644 "${rule_tmp}" "${BASE_GUEST_ROOT}${RECOVERY_RULE}"
  rm -f -- "${rule_tmp}"
  RECOVERY_RULE_INSTALLED=true
  guest_exec systemctl restart polkit.service
  guest_exec systemctl is-active --quiet polkit.service
}

wait_guest_status() {
  local target="$1" attempt
  guest_exec install -d -m 0700 "${FOCUSED_GUEST_PRIVATE}" >/dev/null
  for attempt in $(seq 1 240); do
    if guest_exec /bin/bash -lc "curl --fail --silent --show-error --max-time 3 --unix-socket '${DAEMON_SOCKET}' http://localhost/v1/status >'${FOCUSED_GUEST_PRIVATE}/status.json' 2>/dev/null && python3 '${STATUS_HELPER}' '${target}' '${FOCUSED_GUEST_PRIVATE}/status.json' >/dev/null" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.5
  done
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
  for suffix in addr.json routes.json rules.json nft.json nm.txt resolved.txt; do
    cmp -s "${before}.${suffix}" "${after}.${suffix}" || return 1
  done
}

assert_clean_recovery() {
  local phase="$1"
  run_e2e_podlaz recover --json >"${PRIVATE_ROOT}/recover-${phase}.json"
  python3 - "${PRIVATE_ROOT}/recover-${phase}.json" <<'PY'
import json,sys
with open(sys.argv[1], encoding="utf-8") as handle:
    payload=json.load(handle)
if payload.get("status") != "ok" or payload.get("warnings"):
    raise SystemExit("recover inspection is not clean")
recovery=payload.get("recovery")
if not isinstance(recovery,dict) or recovery.get("candidates") or recovery.get("warnings"):
    raise SystemExit("recover inspection has cleanup candidates or warnings")
network_session=payload.get("network_session")
if isinstance(network_session,dict) and (
    network_session.get("cleanup_authority")
    or network_session.get("transaction_present")
    or network_session.get("next_action") not in (None,"","none")
):
    raise SystemExit("recover inspection published unexpected session authority")
PY
}

assert_clean_recovery_execute() {
  local phase="$1"
  run_e2e_podlaz recover --execute --yes --json >"${PRIVATE_ROOT}/recover-execute-${phase}.json"
  run_e2e_podlaz recover --json >"${PRIVATE_ROOT}/recover-after-execute-${phase}.json"
  python3 - "${PRIVATE_ROOT}/recover-after-execute-${phase}.json" <<'PY'
import json,sys
with open(sys.argv[1], encoding="utf-8") as handle:
    payload=json.load(handle)
if payload.get("status") != "ok" or payload.get("warnings"):
    raise SystemExit("recovery did not remain clean after execute")
recovery=payload.get("recovery") or {}
if recovery.get("candidates") or recovery.get("warnings"):
    raise SystemExit("recovery execute fabricated cleanup authority")
network_session=payload.get("network_session")
if isinstance(network_session,dict) and (
    network_session.get("cleanup_authority")
    or network_session.get("transaction_present")
    or network_session.get("next_action") not in (None,"","none")
):
    raise SystemExit("recovery execute fabricated session authority")
PY
}

assert_no_podlaz_authority_created() {
  guest_exec test ! -e /run/podlaz/network-session-continuation.json
  guest_exec test ! -e /run/podlaz/generated/xray.json
  guest_exec /bin/bash -lc '! ip link show dev podlaz0 >/dev/null 2>&1'
  guest_exec /bin/bash -lc '! nft list table inet podlaz >/dev/null 2>&1'
  guest_exec /bin/bash -lc "nft list tables >'${FOCUSED_GUEST_PRIVATE}/nft-tables.txt'; ! grep -E 'table inet podlaz_pe_[0-9a-f]+' '${FOCUSED_GUEST_PRIVATE}/nft-tables.txt' >/dev/null"
  guest_exec python3 -c 'import glob,sys; raise SystemExit(1 if glob.glob("/run/podlaz/transactions/*.json") else 0)'
}

assert_orphan_fixture_unchanged() {
  guest_exec python3 "${ORPHAN_FIXTURE_HELPER}" verify-present "${ORPHAN_MANIFEST}"
}

seed_orphan_routing_from_committed_generation() {
  mark_failure product seed.connect
  run_e2e_podlaz connect --mode tun "${PROFILE_ID}" >/dev/null
  wait_guest_status verified-active || return 1
  guest_exec python3 "${NETWORK_AUTHORITY_HELPER}" snapshot /run/podlaz/transactions "${AUTHORITY_MANIFEST}" >/dev/null
  guest_exec python3 "${NETWORK_AUTHORITY_HELPER}" verify-present "${AUTHORITY_MANIFEST}" >/dev/null

  mark_failure product seed.disconnect
  run_e2e_podlaz disconnect >/dev/null
  wait_guest_status clean-inactive || return 1
  assert_clean_recovery seed || return 1

  mark_failure fixture orphan.prepare
  guest_exec python3 "${ORPHAN_FIXTURE_HELPER}" prepare "${AUTHORITY_MANIFEST}" "${ORPHAN_MANIFEST}"
  guest_exec python3 "${ORPHAN_FIXTURE_HELPER}" apply "${ORPHAN_MANIFEST}"
  ORPHAN_FIXTURE_CREATED=true
  assert_orphan_fixture_unchanged
}

assert_base_baseline_restored() {
  local after="${PRIVATE_ROOT}/after-fixture-removal"
  capture_guest_network_snapshot "${after}"
  assert_network_snapshot_equal "${BASE_GUEST_BASELINE}" "${after}"
}

run_scenario() {
  local candidate="$1"
  local fixture_before="${PRIVATE_ROOT}/fixture-before"
  local fixture_after="${PRIVATE_ROOT}/fixture-after"

  mark_failure diagnostic_unknown base.candidate_ready
  run_base_scenario "${candidate}"
  wait_for_control_ready candidate-ready || fail "base synthetic TUN did not reach candidate-ready"
  grep -Fx 'candidate.provenance=pass' "${BASE_REPORT}" >/dev/null || fail "candidate provenance did not pass"
  grep -Fx 'ordinary_user.boundary=pass' "${BASE_REPORT}" >/dev/null || fail "ordinary-user boundary did not pass"

  mark_failure fixture recovery.authorization
  install_recovery_authorization
  PROFILE_ID="$(guest_exec cat "${GUEST_PRIVATE}/profile-id" | tr -d '[:space:]')"
  [[ -n "${PROFILE_ID}" ]] || fail "guest profile identity is unavailable"
  guest_exec install -d -m 0700 "${FOCUSED_GUEST_PRIVATE}"

  seed_orphan_routing_from_committed_generation || fail "could not derive orphan fixture from current committed authority"
  record_evidence candidate.current_authority_seeded pass
  capture_guest_network_snapshot "${fixture_before}"

  mark_failure product preflight.connect
  release_control candidate-ready || fail "could not release candidate-ready boundary"
  wait_for_control_ready connect-failed || fail "ambiguous orphan connect did not fail at the expected boundary"

  guest_exec grep -F "ambiguous stale routing state blocks TUN connect before network mutation" "${GUEST_PRIVATE}/connect.stderr" >/dev/null || fail "connect did not publish ambiguous routing preflight classification"
  guest_exec grep -F "ownership evidence is unavailable" "${GUEST_PRIVATE}/connect.stderr" >/dev/null || fail "connect did not explain missing durable ownership"
  if guest_exec grep -F "recover --execute" "${GUEST_PRIVATE}/connect.stderr" >/dev/null 2>&1; then
    fail "connect recommended unauthoritative recovery"
  fi
  assert_no_podlaz_authority_created || fail "ambiguous preflight created Podlaz authority"
  assert_orphan_fixture_unchanged || fail "ambiguous preflight mutated the foreign-looking fixture"
  record_evidence preflight.blocked_before_mutation pass
  record_evidence ownership.observation_not_authority pass

  mark_failure product recovery.unauthorized
  assert_clean_recovery orphan-before-execute || fail "recover inspection claimed orphan routing authority"
  assert_clean_recovery_execute orphan || fail "recover execute claimed orphan routing authority"
  assert_orphan_fixture_unchanged || fail "recover mutated the unowned orphan fixture"
  assert_no_podlaz_authority_created || fail "recover fabricated Podlaz authority"
  capture_guest_network_snapshot "${fixture_after}"
  assert_network_snapshot_equal "${fixture_before}" "${fixture_after}" || fail "foreign network state changed during blocked lifecycle/recovery"
  record_evidence recovery.unauthorized_noop pass
  record_evidence foreign.state_preserved pass

  mark_failure fixture orphan.remove
  remove_orphan_fixture
  assert_base_baseline_restored || fail "exact fixture removal did not restore the original guest network baseline"
  assert_clean_recovery after-fixture-removal || fail "recovery is not clean after test-owned fixture removal"
  assert_clean_recovery_execute after-fixture-removal || fail "recovery execute is not clean after test-owned fixture removal"
  guest_exec timeout 20 getent ahostsv4 example.com >/dev/null
  guest_exec timeout 30 curl -4 -fsS -o /dev/null https://example.com/
  record_evidence terminal.clean_after_fixture_removal pass

  mark_failure diagnostic_unknown base.teardown
  remove_recovery_authorization
  release_control connect-failed || fail "could not release expected connect-failed boundary"
  wait_base_expected_failure || fail "base scenario unexpectedly succeeded after required preflight rejection"
  grep -Fx 'outer.cleanup=pass' "${BASE_REPORT}" >/dev/null || fail "base outer cleanup did not pass"
  grep -Fx 'artifact.privacy=pass' "${BASE_REPORT}" >/dev/null || fail "base artifact privacy did not pass"
  record_evidence base.outer_cleanup pass

  FAILURE_CLASS=none
  FAILURE_STEP=none
  assert_public_artifact_privacy || fail "hosted orphan routing public evidence is not privacy-safe"
  record_evidence artifact.privacy pass
}

main() {
  (($# == 1)) || fail "usage: $0 CANDIDATE.deb"
  require_cmd awk bash chmod cmp curl find grep install jq mktemp python3 rm seq sleep sudo systemd-run timeout tr
  [[ "${PODLAZ_E2E_CANDIDATE_COMMIT:-}" =~ ^[0-9a-fA-F]{40}$ ]] || fail "PODLAZ_E2E_CANDIDATE_COMMIT must be exact"
  install -d -m 0700 "${E2E_ARTIFACT_DIR}"
  : >"${REPORT}"
  chmod 0600 "${REPORT}"
  trap cleanup EXIT INT TERM
  run_scenario "$1"
  finalize_report
  validate_report
}

if [[ "${1:-}" == validate-report ]]; then
  validate_report
  exit 0
fi

main "$@"
