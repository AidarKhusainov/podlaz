#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"

BASE_SCENARIO="${SCRIPT_DIR}/hosted-synthetic-tun.sh"
REPORT="${E2E_ARTIFACT_DIR}/hosted-protected-gateway-lifecycle.txt"
MACHINE="podlaz-synthetic-tun"
FOREIGN_NFT_TABLE="pzsynt_foreign"
GUEST_XDG="/home/e2e/.local/share/podlaz-hosted-synthetic-tun"
GUEST_PRIVATE="/tmp/podlaz-hosted-synthetic-tun"
Q17_PRIVATE="/tmp/podlaz-hosted-protected-gateway"
SESSION_STATE="/run/podlaz/network-session-continuation.json"
DAEMON_SOCKET="/run/podlaz/podlazd.sock"
STATUS_HELPER="/workspace/scripts/e2e/lib/daemon_status_semantics.py"
ACTIVE_AUTHORITY_HELPER="/workspace/scripts/e2e/hosted_synthetic_active_authority.py"
NETWORK_AUTHORITY_HELPER="/workspace/scripts/e2e/hosted_synthetic_network_authority.py"
PROTECTED_AUTHORITY_HELPER="/workspace/scripts/e2e/hosted_protected_gateway_authority.py"
RECOVERY_RULE="/etc/polkit-1/rules.d/50-podlaz-hosted-protected-gateway.rules"

PRIVATE_ROOT="${E2E_TMP_ROOT}/hosted-protected-gateway-private"
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
  candidate.positive_control
  recover.active_inspection_noop
  recover.active_execute_noop
  authority.protected_gateway_current
  authority.resolver_current
  active.reads_stable
  resolver.missing_link_converged
  authority.observation_never_authority
  first.terminal_cleanup
  reconnect.fresh_generation
  second.terminal_cleanup
  privacy.envelope_preserved
  foreign.state_preserved
  base.terminal_cleanup
  artifact.privacy
)

FAILURE_CLASS=diagnostic_unknown
FAILURE_STEP=bootstrap
REPORT_FINALIZED=false
BASE_PID=""
PROFILE_ID=""
RECOVERY_RULE_INSTALLED=false

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
  [[ -f "${REPORT}" && ! -L "${REPORT}" ]] || fail "hosted protected gateway report is missing"
  python3 - "${REPORT}" <<'PY'
import sys
required = {
    "candidate.positive_control",
    "recover.active_inspection_noop",
    "recover.active_execute_noop",
    "authority.protected_gateway_current",
    "authority.resolver_current",
    "active.reads_stable",
    "resolver.missing_link_converged",
    "authority.observation_never_authority",
    "first.terminal_cleanup",
    "reconnect.fresh_generation",
    "second.terminal_cleanup",
    "privacy.envelope_preserved",
    "foreign.state_preserved",
    "base.terminal_cleanup",
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
    raise SystemExit("unexpected hosted protected gateway report schema")
for key in required:
    if values[key] != "pass":
        raise SystemExit(f"required evidence is not pass: {key}={values[key]}")
if values["failure.class"] != "none" or values["failure.step"] != "none":
    raise SystemExit("hosted protected gateway report contains failure metadata")
PY
}

assert_public_artifact_privacy() {
  local extra
  extra="$(find "${E2E_ARTIFACT_DIR}" -mindepth 1 -maxdepth 1 ! -name 'hosted-protected-gateway-lifecycle.txt' -print -quit)"
  [[ -z "${extra}" ]] || return 1
  [[ -f "${REPORT}" && ! -L "${REPORT}" ]] || return 1
  ! grep -Eiq 'vless://|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|172\.31\.(253|254)\.|podlaz_pe_[0-9a-f]' "${REPORT}"
}

guest_exec() {
  sudo -n systemd-run --machine="${MACHINE}" --expand-environment=no --wait --pipe --collect --quiet -- "$@"
}

run_e2e_podlaz() {
  guest_exec runuser -u e2e -- env \
    XDG_CONFIG_HOME="${GUEST_XDG}/config" \
    XDG_STATE_HOME="${GUEST_XDG}/state" \
    XDG_CACHE_HOME="${GUEST_XDG}/cache" \
    /usr/bin/podlaz "$@"
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

release_candidate_control() {
  local ready="${CONTROL_DIR}/candidate-ready.ready" continue="${CONTROL_DIR}/candidate-ready.continue" attempt
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

release_control_best_effort() {
  local ready="${CONTROL_DIR}/candidate-ready.ready" continue="${CONTROL_DIR}/candidate-ready.continue"
  [[ -d "${CONTROL_DIR}" ]] || return 0
  if [[ -f "${ready}" && ! -e "${continue}" ]]; then
    printf 'continue\n' >"${continue}" 2>/dev/null || true
    chmod 0600 "${continue}" >/dev/null 2>&1 || true
  fi
}

remove_recovery_authorization() {
  [[ "${RECOVERY_RULE_INSTALLED}" == true ]] || return 0
  sudo -n rm -f -- "${BASE_GUEST_ROOT}${RECOVERY_RULE}" >/dev/null 2>&1 || true
  guest_exec systemctl restart polkit.service >/dev/null 2>&1 || true
  RECOVERY_RULE_INSTALLED=false
}

cleanup() {
  local code=$? attempt
  trap - EXIT INT TERM
  set +e
  remove_recovery_authorization
  release_control_best_effort
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
  local ready="${CONTROL_DIR}/candidate-ready.ready" attempt code
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
        BASE_PID=""
      fi
      inherit_base_failure
      return 1
    fi
    sleep 0.1
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
    PODLAZ_E2E_HOSTED_CONTROL_PHASES="candidate-ready" \
    PODLAZ_E2E_HOSTED_CONTROL_TIMEOUT_SECONDS=180 \
    bash "${BASE_SCENARIO}" "${candidate}" >"${BASE_STDOUT}" 2>"${BASE_STDERR}" &
  BASE_PID=$!
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
  guest_exec install -d -m 0700 "${Q17_PRIVATE}" >/dev/null
  for attempt in $(seq 1 240); do
    if guest_exec /bin/bash -lc "curl --fail --silent --show-error --max-time 3 --unix-socket '${DAEMON_SOCKET}' http://localhost/v1/status >'${Q17_PRIVATE}/status.json' 2>/dev/null && python3 '${STATUS_HELPER}' '${target}' '${Q17_PRIVATE}/status.json' >/dev/null" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.5
  done
  return 1
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

assert_guest_baseline_unchanged() {
  local current="${PRIVATE_ROOT}/after-focused-lifecycle" suffix
  capture_guest_network_snapshot "${current}"
  for suffix in addr.json routes.json rules.json nft.json nm.txt resolved.txt; do
    cmp -s "${BASE_GUEST_BASELINE}.${suffix}" "${current}.${suffix}" || return 1
  done
}

assert_active_status_reads() {
  local phase="$1" read_name output
  for read_name in first second; do
    output="${PRIVATE_ROOT}/${phase}-${read_name}-status.txt"
    run_e2e_podlaz status >"${output}"
    grep -Fx 'Connection: active' "${output}" >/dev/null || return 1
    grep -Fx 'Transaction: committed' "${output}" >/dev/null || return 1
    grep -Fx 'Stale state: none' "${output}" >/dev/null || return 1
    grep -Fx 'Startup recovery scan: clean for active connection' "${output}" >/dev/null || return 1
    ! grep -F 'Inspection warnings:' "${output}" >/dev/null || return 1
  done
}

assert_inactive_status() {
  local phase="$1" output="${PRIVATE_ROOT}/${phase}-inactive-status.txt"
  wait_guest_status clean-inactive || return 1
  run_e2e_podlaz status >"${output}"
  grep -Fx 'Connection: inactive' "${output}" >/dev/null || return 1
  grep -Fx 'Stale state: none' "${output}" >/dev/null || return 1
  grep -Fx 'Startup recovery scan: clean inactive state' "${output}" >/dev/null || return 1
  ! grep -F 'Recovery candidates:' "${output}" >/dev/null || return 1
  ! grep -F 'Inspection warnings:' "${output}" >/dev/null || return 1
}

assert_recover_dry_run_noop() {
  local phase="$1" output="${PRIVATE_ROOT}/${phase}-recover-dry.json"
  run_e2e_podlaz recover --json >"${output}"
  python3 - "${output}" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as handle:
    payload = json.load(handle)
if payload.get("schema_version") != "v1" or payload.get("status") != "ok" or payload.get("mode") != "dry-run":
    raise SystemExit("recovery inspection is not a clean dry-run")
if payload.get("warnings"):
    raise SystemExit("recovery inspection published top-level warnings")
recovery = payload.get("recovery")
if not isinstance(recovery, dict):
    raise SystemExit("recovery inspection payload is missing")
if recovery.get("candidates") or recovery.get("warnings") or recovery.get("network_session"):
    raise SystemExit("recovery inspection published cleanup/reconnect authority")
PY
}

assert_recover_execute_noop() {
  local phase="$1" output="${PRIVATE_ROOT}/${phase}-recover-execute.json"
  run_e2e_podlaz recover --execute --yes --json >"${output}"
  python3 - "${output}" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as handle:
    payload = json.load(handle)
if payload.get("schema_version") != "v1" or payload.get("status") != "ok" or payload.get("mode") != "execute":
    raise SystemExit("active recovery execution did not report a successful typed no-op")
if payload.get("errors"):
    raise SystemExit("active recovery execution reported errors")
results = payload.get("recovery")
warnings = payload.get("warnings")
if not isinstance(results, list) or not results:
    raise SystemExit("active recovery execution did not explicitly classify skipped candidates")
if not isinstance(warnings, list) or not warnings:
    raise SystemExit("active recovery execution did not publish its mutation-free warning")
if any(not isinstance(item, dict) or item.get("status") != "skipped" for item in results):
    raise SystemExit("active recovery execution reported a non-skipped cleanup result")
PY
}

assert_recover_execute_clean() {
  local phase="$1" output="${PRIVATE_ROOT}/${phase}-recover-execute-clean.json"
  run_e2e_podlaz recover --execute --yes --json >"${output}"
  python3 - "${output}" <<'PY'
import json, sys
with open(sys.argv[1], encoding="utf-8") as handle:
    payload = json.load(handle)
if payload.get("schema_version") != "v1" or payload.get("status") != "ok" or payload.get("mode") != "execute":
    raise SystemExit("inactive recovery execution is not clean")
if payload.get("warnings") or payload.get("errors") or payload.get("recovery"):
    raise SystemExit("inactive recovery execution unexpectedly found authority")
if payload.get("network_session"):
    raise SystemExit("inactive recovery execution unexpectedly found Network Session authority")
PY
}

assert_active_authority() {
  local phase="$1" manifest="$2" identity="$3"
  local status="${Q17_PRIVATE}/${phase}-status.json"
  local dns="${Q17_PRIVATE}/${phase}-resolved-dns.txt"
  local domains="${Q17_PRIVATE}/${phase}-resolved-domain.txt"
  local default_route="${Q17_PRIVATE}/${phase}-resolved-default-route.txt"
  local nft="${Q17_PRIVATE}/${phase}-nft-ruleset.json"

  guest_exec /bin/bash -lc "curl --fail --silent --show-error --max-time 3 --unix-socket '${DAEMON_SOCKET}' http://localhost/v1/status >'${status}'"
  guest_exec /bin/bash -lc "resolvectl dns >'${dns}'; resolvectl domain >'${domains}'; resolvectl default-route >'${default_route}'; nft -j list ruleset >'${nft}'"
  guest_exec python3 "${ACTIVE_AUTHORITY_HELPER}" \
    --status "${status}" \
    --transactions /run/podlaz/transactions \
    --session "${SESSION_STATE}" \
    --boot-id /proc/sys/kernel/random/boot_id \
    --runtime-config /run/podlaz/generated/xray.json \
    --resolved-dns "${dns}" \
    --resolved-domain "${domains}" \
    --resolved-default-route "${default_route}" \
    --nft-ruleset "${nft}" >/dev/null
  guest_exec python3 "${NETWORK_AUTHORITY_HELPER}" snapshot /run/podlaz/transactions "${manifest}" >/dev/null
  guest_exec python3 "${NETWORK_AUTHORITY_HELPER}" verify-present "${manifest}" >/dev/null
  guest_exec python3 "${PROTECTED_AUTHORITY_HELPER}" verify-current "${manifest}" >/dev/null
  guest_exec python3 "${PROTECTED_AUTHORITY_HELPER}" identity \
    "${status}" /run/podlaz/transactions "${SESSION_STATE}" /run/podlaz/generated/xray.json "${manifest}" "${identity}" >/dev/null
}

assert_same_generation() {
  guest_exec python3 "${PROTECTED_AUTHORITY_HELPER}" same "$1" "$2" >/dev/null
}

assert_fresh_generation() {
  guest_exec python3 "${PROTECTED_AUTHORITY_HELPER}" fresh "$1" "$2" >/dev/null
}

assert_terminal_clean() {
  local phase="$1" manifest="$2" identity="$3"
  assert_inactive_status "${phase}" || return 1
  guest_exec /bin/bash -lc "cd /workspace && source scripts/e2e/lib/e2e.sh && source scripts/e2e/lib/tun_package_assertions.sh && verify_tun_package_resources_absent '${phase}' '${NETWORK_AUTHORITY_HELPER}' '${manifest}'" >/dev/null
  guest_exec test ! -e "${SESSION_STATE}"
  guest_exec python3 "${PROTECTED_AUTHORITY_HELPER}" privacy-absent "${identity}" >/dev/null
  if guest_exec /bin/bash -lc "nft list tables | grep -E 'table inet podlaz_pe_[0-9a-f]+'" >/dev/null 2>&1; then
    return 1
  fi
  if guest_exec nmcli -t -f NAME,DEVICE connection show --active | grep -F ':podlaz0' >/dev/null; then
    return 1
  fi
  assert_foreign_sentinel
}

wait_resolved_missing_link() {
  local phase="$1" stdout_file="${PRIVATE_ROOT}/${phase}-resolved.stdout" stderr_file="${PRIVATE_ROOT}/${phase}-resolved.stderr"
  local exit_code classification attempt
  for attempt in $(seq 1 100); do
    set +e
    guest_exec timeout --signal=TERM --kill-after=1s 3s resolvectl status podlaz0 --no-pager >"${stdout_file}" 2>"${stderr_file}"
    exit_code=$?
    set -e
    classification="$(python3 - "${exit_code}" "${stdout_file}" "${stderr_file}" <<'PY'
import re
import sys

exit_code = int(sys.argv[1])
stdout = open(sys.argv[2], "rb").read()
stderr = open(sys.argv[3], "rb").read()
expected = b'Failed to resolve interface "podlaz0", ignoring: No such device'

if exit_code == 0 and stdout == b"" and stderr in (expected + b"\n", expected + b"\r\n"):
    print("exact")
    raise SystemExit(0)
if exit_code != 0 or stderr:
    print("unexpected")
    raise SystemExit(0)
try:
    text = stdout.decode("utf-8")
except UnicodeDecodeError:
    print("unexpected")
    raise SystemExit(0)

seen_header = False
seen_fields = set()
last_field = ""
current_scopes = []
protocols = []
current_dns_server = ""
dns_servers = []
dns_domains = []

def tokens(value):
    out = []
    for item in value.split():
        if item and item not in out:
            out.append(item)
    return out

def reject():
    print("unexpected")
    raise SystemExit(0)

for raw in text.split("\n"):
    line = raw.strip()
    if not line:
        continue
    if line.startswith("Link "):
        if seen_header or re.fullmatch(r"Link [0-9]+ \(podlaz0\)", line) is None:
            reject()
        seen_header = True
        last_field = ""
        continue
    if not seen_header:
        reject()
    if ":" not in line:
        if last_field == "DNS Servers":
            for item in tokens(line):
                if item not in dns_servers:
                    dns_servers.append(item)
            continue
        if last_field == "DNS Domain":
            for item in tokens(line):
                if item not in dns_domains:
                    dns_domains.append(item)
            continue
        reject()
    key, value = (part.strip() for part in line.split(":", 1))
    if not key or key in seen_fields:
        reject()
    if key == "Current Scopes":
        if not value:
            reject()
        current_scopes = tokens(value)
    elif key == "Protocols":
        if not value:
            reject()
        protocols = tokens(value)
    elif key == "Current DNS Server":
        fields = value.split()
        if len(fields) != 1:
            reject()
        current_dns_server = fields[0]
    elif key == "DNS Servers":
        if not value:
            reject()
        dns_servers = tokens(value)
    elif key == "DNS Domain":
        if not value:
            reject()
        dns_domains = tokens(value)
    else:
        reject()
    seen_fields.add(key)
    last_field = key

if not seen_header or "Current Scopes" not in seen_fields or "Protocols" not in seen_fields:
    reject()
is_empty = (
    current_scopes == ["none"]
    and current_dns_server == ""
    and not dns_servers
    and not dns_domains
    and "-DefaultRoute" in protocols
    and "+DefaultRoute" not in protocols
)
print("transient" if is_empty else "unexpected")
PY
)"
    case "${classification}" in
      exact) return 0 ;;
      transient) ;;
      *) return 1 ;;
    esac
    sleep 0.1
  done
  return 1
}

assert_inactive_observation_clean() {
  local phase="$1"
  assert_recover_dry_run_noop "${phase}-before-resolved" || return 1
  wait_resolved_missing_link "${phase}" || return 1
  assert_recover_execute_clean "${phase}-after-resolved" || return 1
  assert_recover_dry_run_noop "${phase}-final" || return 1
  assert_inactive_status "${phase}-final" || return 1
}

connect_once() {
  run_e2e_podlaz connect --mode tun "${PROFILE_ID}" >/dev/null
  wait_guest_status verified-active
}

disconnect_once() {
  run_e2e_podlaz disconnect >/dev/null
  wait_guest_status clean-inactive
}

validate_base_positive_control() {
  env E2E_TMP_ROOT="${BASE_TMP_ROOT}" E2E_ARTIFACT_DIR="${BASE_ARTIFACT_DIR}" \
    bash "${BASE_SCENARIO}" validate-report >/dev/null
  grep -Fx 'candidate.provenance=pass' "${BASE_REPORT}" >/dev/null
  grep -Fx 'tun.terminal_cleanup=pass' "${BASE_REPORT}" >/dev/null
  grep -Fx 'tun.recovery_clean=pass' "${BASE_REPORT}" >/dev/null
  grep -Fx 'guest.baseline_restored=pass' "${BASE_REPORT}" >/dev/null
  grep -Fx 'outer.cleanup=pass' "${BASE_REPORT}" >/dev/null
}

run_scenario() {
  local candidate="$1"
  local first_before_manifest="${Q17_PRIVATE}/first-before-network.json"
  local first_before_identity="${Q17_PRIVATE}/first-before-identity.json"
  local first_after_dry_manifest="${Q17_PRIVATE}/first-after-dry-network.json"
  local first_after_dry_identity="${Q17_PRIVATE}/first-after-dry-identity.json"
  local first_after_execute_manifest="${Q17_PRIVATE}/first-after-execute-network.json"
  local first_after_execute_identity="${Q17_PRIVATE}/first-after-execute-identity.json"
  local second_manifest="${Q17_PRIVATE}/second-network.json"
  local second_identity="${Q17_PRIVATE}/second-identity.json"

  mark_failure diagnostic_unknown base.candidate_ready
  run_base_scenario "${candidate}"
  wait_for_control_ready || fail "base synthetic TUN did not reach candidate-ready control boundary"
  grep -Fx 'candidate.provenance=pass' "${BASE_REPORT}" >/dev/null || fail "base candidate provenance did not pass"
  grep -Fx 'ordinary_user.boundary=pass' "${BASE_REPORT}" >/dev/null || fail "base ordinary-user boundary did not pass"
  record_evidence candidate.positive_control pass

  mark_failure fixture recovery.authorization
  install_recovery_authorization
  PROFILE_ID="$(guest_exec cat "${GUEST_PRIVATE}/profile-id" | tr -d '[:space:]')"
  [[ -n "${PROFILE_ID}" ]] || fail "guest profile identity is unavailable"
  guest_exec install -d -m 0700 "${Q17_PRIVATE}"
  assert_foreign_sentinel || fail "base foreign sentinel is absent before protected-gateway lifecycle"

  mark_failure product first.connect
  connect_once || fail "first protected-gateway connect did not reach verified-active"
  assert_active_status_reads first || fail "first active status reads are not stable"
  record_evidence active.reads_stable pass

  mark_failure product first.active_authority
  assert_active_authority first-before "${first_before_manifest}" "${first_before_identity}" || fail "first active protected authority is not exact/current"
  record_evidence authority.protected_gateway_current pass
  record_evidence authority.resolver_current pass
  assert_foreign_sentinel || fail "foreign state changed before active recovery observation"

  mark_failure product recover.active_inspection
  assert_recover_dry_run_noop active || fail "active recovery inspection published cleanup/reconnect authority"
  assert_active_authority first-after-dry "${first_after_dry_manifest}" "${first_after_dry_identity}" || fail "active authority changed after recovery inspection"
  assert_same_generation "${first_before_identity}" "${first_after_dry_identity}" || fail "recovery inspection changed active authority"
  record_evidence recover.active_inspection_noop pass

  mark_failure product recover.active_execute
  assert_recover_execute_noop active || fail "active recover execute was not an explicit mutation-free no-op"
  assert_active_authority first-after-execute "${first_after_execute_manifest}" "${first_after_execute_identity}" || fail "active authority is not exact after recover execute"
  assert_same_generation "${first_before_identity}" "${first_after_execute_identity}" || fail "recover execute changed active authority"
  assert_active_status_reads first-after-recover || fail "active status became unstable after recover execute"
  assert_foreign_sentinel || fail "foreign state changed during active recover execute"
  record_evidence recover.active_execute_noop pass
  record_evidence privacy.envelope_preserved pass

  mark_failure product first.disconnect
  disconnect_once || fail "first disconnect did not reach clean inactive"
  assert_terminal_clean first "${first_before_manifest}" "${first_before_identity}" || fail "first lifecycle did not reach exact terminal cleanup"
  record_evidence first.terminal_cleanup pass

  mark_failure product first.resolver_convergence
  assert_inactive_observation_clean first || fail "first resolver/recovery observation did not converge cleanly"
  record_evidence resolver.missing_link_converged pass
  record_evidence authority.observation_never_authority pass

  mark_failure product reconnect.connect
  connect_once || fail "immediate reconnect did not reach verified-active"
  assert_active_authority second "${second_manifest}" "${second_identity}" || fail "reconnect active authority is not exact/current"
  assert_fresh_generation "${first_before_identity}" "${second_identity}" || fail "immediate reconnect reused stale active authority"
  assert_active_status_reads reconnect || fail "reconnect active status reads are not stable"
  assert_recover_dry_run_noop reconnect || fail "reconnect recovery inspection published authority"
  assert_foreign_sentinel || fail "foreign state changed during immediate reconnect"
  record_evidence reconnect.fresh_generation pass

  mark_failure product second.disconnect
  disconnect_once || fail "second disconnect did not reach clean inactive"
  assert_terminal_clean second "${second_manifest}" "${second_identity}" || fail "second lifecycle did not reach exact terminal cleanup"
  assert_inactive_observation_clean second || fail "second resolver/recovery observation did not converge cleanly"
  record_evidence second.terminal_cleanup pass

  mark_failure product focused.baseline
  assert_guest_baseline_unchanged || fail "focused protected-gateway lifecycle did not restore the exact guest network baseline"
  assert_foreign_sentinel || fail "foreign sentinel was not preserved across focused lifecycle"
  record_evidence foreign.state_preserved pass

  mark_failure fixture recovery.authorization_remove
  remove_recovery_authorization
  release_candidate_control || fail "could not release candidate-ready control boundary"

  mark_failure diagnostic_unknown base.completion
  wait_base_completion || fail "base synthetic TUN positive control failed after focused lifecycle"
  validate_base_positive_control || fail "base synthetic TUN terminal evidence is incomplete"
  record_evidence base.terminal_cleanup pass

  FAILURE_CLASS=none
  FAILURE_STEP=none
  assert_public_artifact_privacy || fail "hosted protected gateway public evidence is not privacy-safe"
  record_evidence artifact.privacy pass
}

main() {
  (($# == 1)) || fail "usage: $0 CANDIDATE.deb"
  require_cmd awk bash chmod cmp find grep install jq mktemp python3 rm seq sleep sudo systemd-run timeout
  install -d -m 0700 "${E2E_ARTIFACT_DIR}"
  : >"${REPORT}"
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
