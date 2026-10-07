#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"
# Reuse the permanent hosted nspawn/TUN substrate. This scenario owns only
# ephemeral Remnawave material handling and compatibility assertions.
# shellcheck source=hosted-synthetic-tun.sh
source "${SCRIPT_DIR}/hosted-synthetic-tun.sh"

REPORT="${E2E_ARTIFACT_DIR}/hosted-remnawave-tun.txt"
MACHINE="podlaz-remnawave-tun"
HOST_VETH="pzremtun0"
HOST_ENDPOINT_DEV="pzremstub0"
GUEST_IF="host0"
NFT_TABLE="pzrem_hosted"
FOREIGN_NFT_TABLE="pzrem_foreign"
NETWORK_CIDR="172.31.250.0/30"
HOST_CIDR="172.31.250.1/30"
GUEST_CIDR="172.31.250.2/30"
HOST_IP="172.31.250.1"
ENDPOINT_CIDR="172.31.249.1/32"
ENDPOINT_IP="172.31.249.1"
GUEST_ROOT="${E2E_TMP_ROOT}/remnawave-system-guest"
PRIVATE_ROOT="${E2E_TMP_ROOT}/remnawave-private"
XRAY_ROOT="${PRIVATE_ROOT}/unused-synthetic-bind"
GUEST_CANDIDATE="/opt/podlaz-remnawave-candidate.deb"
GUEST_XDG="/home/e2e/.local/share/podlaz-hosted-remnawave-tun"
GUEST_PRIVATE="/tmp/podlaz-hosted-remnawave-tun"
GUEST_MANIFEST="${GUEST_PRIVATE}/network-manifest.json"
GUEST_PROVIDER_DIR="/tmp/podlaz-remnawave-material"
EXPECTED_COMMIT="${PODLAZ_E2E_CANDIDATE_COMMIT:-${GITHUB_SHA:-}}"
PUBLIC_IP_CHECK_URL="${PODLAZ_E2E_PUBLIC_IP_CHECK_URL:-https://api.ipify.org}"
EXPECTED_EGRESS_IP="${PODLAZ_E2E_EXPECTED_EGRESS_IP:-}"

EVIDENCE_KEYS=(
  candidate.provenance
  remnawave.material_private
  ordinary_user.boundary
  tun.verified_active
  tun.system_dns
  tun.ipv4_tcp
  tun.tls
  tun.https
  tun.remnawave_path
  tun.doctor
  privacy.direct_uplink_blocked
  foreign.state_preserved
  tun.clean_disconnect
  tun.terminal_cleanup
  tun.recovery_clean
  guest.baseline_restored
  guest.ordinary_connectivity_restored
  outer.cleanup
  artifact.privacy
  fixture.cleanup
)

FAILURE_CLASS=diagnostic_unknown
FAILURE_STEP=bootstrap
REPORT_FINALIZED=false
SYSTEM_GUEST_ACTIVE=false
TEARDOWN_RUNNING=false
NSPAWN_PID=""
XRAY_PID=""
CANDIDATE_DEB=""
CANDIDATE_SHA256=""
PROFILE_URI=""
ORDINARY_EGRESS=""
ACTIVE_EGRESS=""
PROBE_IP=""
REMNAWAVE_FIXTURE_TMP="${E2E_TMP_ROOT}/remnawave-tun-fixture"
REMNAWAVE_COMPOSE="${REMNAWAVE_FIXTURE_TMP}/remnawave-fixture/compose.yml"
REMNAWAVE_PROFILE_FILE="${PRIVATE_ROOT}/remnawave-profile-uri"
REMNAWAVE_ACTIVE=false
REMNAWAVE_ACCESS_BEFORE=0


mark_failure() {
  local class="$1" step="$2"
  case "${class}" in
    product|remnawave|fixture|infrastructure|capability|diagnostic_unknown|none) ;;
    *) class=diagnostic_unknown ;;
  esac
  FAILURE_CLASS="${class}"
  FAILURE_STEP="${step//[^A-Za-z0-9_.-]/_}"
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
  python3 - "${REPORT}" "${EXPECTED_COMMIT,,}" "${CANDIDATE_SHA256}" "${EVIDENCE_KEYS[@]}" <<'PY'
import re
import sys
from pathlib import Path

path = Path(sys.argv[1])
expected_commit = sys.argv[2]
expected_digest = sys.argv[3]
required = sys.argv[4:]
if not path.is_file() or path.is_symlink():
    raise SystemExit("ephemeral Remnawave TUN report is missing")
values = {}
meta = {}
for line in path.read_text(encoding="utf-8").splitlines():
    match = re.fullmatch(r"([a-z0-9_.-]+)=(pass|fail|observed|unavailable)", line)
    if match:
        key, value = match.groups()
        if key in values:
            raise SystemExit(f"duplicate evidence key: {key}")
        values[key] = value
        continue
    match = re.fullmatch(r"candidate\.(commit|package_sha256)=([0-9a-f]+)", line)
    if match:
        key, value = match.groups()
        if key in meta:
            raise SystemExit(f"duplicate candidate metadata: {key}")
        meta[key] = value
        continue
    match = re.fullmatch(r"failure\.(class|step)=([A-Za-z0-9_.-]+)", line)
    if match:
        key, value = match.groups()
        if f"failure.{key}" in meta:
            raise SystemExit(f"duplicate failure metadata: {key}")
        meta[f"failure.{key}"] = value
        continue
    raise SystemExit("ephemeral Remnawave TUN report contains non-normalized data")
if set(values) != set(required):
    raise SystemExit("ephemeral Remnawave TUN evidence schema mismatch")
if meta.get("commit") != expected_commit or meta.get("package_sha256") != expected_digest:
    raise SystemExit("ephemeral Remnawave TUN provenance metadata mismatch")
if meta.get("failure.class") not in {
    "none", "product", "remnawave", "fixture", "infrastructure", "capability", "diagnostic_unknown"
}:
    raise SystemExit("ephemeral Remnawave TUN failure class is invalid")
if not re.fullmatch(r"[A-Za-z0-9_.-]+", meta.get("failure.step", "")):
    raise SystemExit("ephemeral Remnawave TUN failure step is invalid")
for key in required:
    allowed = {"pass", "observed"} if key == "tun.doctor" else {"pass"}
    if values[key] not in allowed:
        raise SystemExit(f"required evidence is not successful: {key}={values[key]}")
if meta.get("failure.class") != "none" or meta.get("failure.step") != "none":
    raise SystemExit("ephemeral Remnawave TUN report contains a failure")
PY
}

assert_public_artifact_privacy() {
  local extra
  extra="$(find "${E2E_ARTIFACT_DIR}" -mindepth 1 -maxdepth 1 ! -name 'hosted-remnawave-tun.txt' -print -quit)"
  [[ -z "${extra}" ]] || return 1
  [[ -f "${REPORT}" && ! -L "${REPORT}" ]] || return 1
  python3 - "${REPORT}" <<'PY'
import re
import sys
from pathlib import Path

allowed = [
    re.compile(r"candidate\.commit=[0-9a-f]{40}"),
    re.compile(r"candidate\.package_sha256=[0-9a-f]{64}"),
    re.compile(r"[a-z0-9_.-]+=(?:pass|fail|observed|unavailable)"),
    re.compile(r"failure\\.class=(?:none|product|remnawave|fixture|infrastructure|capability|diagnostic_unknown)"),
    re.compile(r"failure\.step=[A-Za-z0-9_.-]+"),
]
for raw in Path(sys.argv[1]).read_text(encoding="utf-8").splitlines():
    if not any(pattern.fullmatch(raw) for pattern in allowed):
        raise SystemExit("public Remnawave TUN evidence contains non-normalized data")
PY
}

mask_multiline_sensitive() {
  local value="${1:-}"
  [[ -n "${value}" ]] || return 0
  mask_value "${value}"
  while IFS= read -r line; do
    [[ -n "${line}" ]] || continue
    mask_value "${line}"
  done <<<"${value}"
}

mask_provider_material() {
  return 0
}

start_remnawave_fixture() {
  install -d -m 0700 "${REMNAWAVE_FIXTURE_TMP}" "${PRIVATE_ROOT}"
  E2E_TMP_ROOT="${REMNAWAVE_FIXTURE_TMP}" \
  E2E_ARTIFACT_DIR="${REMNAWAVE_FIXTURE_TMP}/private-artifacts" \
  PODLAZ_REMNAWAVE_NODE_BIND_IP="${HOST_IP}" \
  PODLAZ_REMNAWAVE_PROFILE_ADDRESS="${HOST_IP}" \
    bash "${SCRIPT_DIR}/remnawave-fixture.sh" export-profile "${REMNAWAVE_PROFILE_FILE}"
  [[ -f "${REMNAWAVE_PROFILE_FILE}" && ! -L "${REMNAWAVE_PROFILE_FILE}" ]] ||     fail "Remnawave fixture did not export provider material"
  REMNAWAVE_ACTIVE=true
}

stop_remnawave_fixture() {
  local failed=0
  [[ "${REMNAWAVE_ACTIVE}" == true ]] || return 0
  docker compose -f "${REMNAWAVE_COMPOSE}" down -v --remove-orphans >/dev/null 2>&1 || failed=1
  if docker ps -aq --filter 'label=com.docker.compose.project=remnawave-fixture' | grep -q .; then failed=1; fi
  if docker network inspect podlaz-remnawave-fixture >/dev/null 2>&1; then failed=1; fi
  if docker volume inspect podlaz-remnawave-db >/dev/null 2>&1; then failed=1; fi
  rm -rf "${REMNAWAVE_FIXTURE_TMP}"
  REMNAWAVE_ACTIVE=false
  (( failed == 0 ))
}

remnawave_node_access_count() {
  local value
  value="$(docker compose -f "${REMNAWAVE_COMPOSE}" exec -T remnanode sh -c \
    'if [ -f /tmp/podlaz-remnawave-access.log ]; then wc -l </tmp/podlaz-remnawave-access.log; else printf 0; fi' \
    2>/dev/null | tr -d '[:space:]')"
  [[ "${value}" =~ ^[0-9]+$ ]] || return 1
  printf '%s' "${value}"
}

prepare_provider_material() {
  local host_uri="${PRIVATE_ROOT}/provider-uri"
  [[ -f "${REMNAWAVE_PROFILE_FILE}" && ! -L "${REMNAWAVE_PROFILE_FILE}" ]] || return 1
  PROFILE_URI="$(cat "${REMNAWAVE_PROFILE_FILE}")"
  [[ "${PROFILE_URI}" == vless://* ]] || return 1
  mask_value "${PROFILE_URI}"
  printf '%s\n' "${PROFILE_URI}" >"${host_uri}"
  chmod 0600 "${host_uri}"
  guest_exec install -d -o e2e -g e2e -m 0700 "${GUEST_PROVIDER_DIR}"
  sudo -n machinectl copy-to "${MACHINE}" "${host_uri}" "${GUEST_PROVIDER_DIR}/profile-uri" >/dev/null
  guest_exec chown e2e:e2e "${GUEST_PROVIDER_DIR}/profile-uri"
  guest_exec chmod 0600 "${GUEST_PROVIDER_DIR}/profile-uri"
  rm -f -- "${host_uri}"
}

remove_provider_material() {
  rm -f -- "${PRIVATE_ROOT}/provider-uri" "${PRIVATE_ROOT}/profile-id" "${REMNAWAVE_PROFILE_FILE}"
  if [[ "${SYSTEM_GUEST_ACTIVE}" == true ]]; then
    guest_exec rm -rf "${GUEST_PROVIDER_DIR}" >/dev/null 2>&1 || true
  fi
  PROFILE_URI=""
}

classify_provider_tun_connect_failure() {
  local exit_code="$1" classification="" domain=diagnostic_unknown
  case "${exit_code}" in
    4)
      mark_failure fixture provider_tun.authorization
      return 0
      ;;
    5)
      mark_failure infrastructure provider_tun.daemon_unavailable
      return 0
      ;;
  esac

  if guest_exec test -f /run/podlaz/diagnostics/tun-last.json >/dev/null 2>&1; then
    classification="$(guest_exec python3 - /run/podlaz/diagnostics/tun-last.json <<'PY'
import json
import re
import sys

try:
    with open(sys.argv[1], encoding="utf-8") as handle:
        report = json.load(handle)
except (OSError, json.JSONDecodeError):
    raise SystemExit(1)
value = str(report.get("primary_classification") or "").strip().lower()
value = re.sub(r"[^a-z0-9_.-]+", "-", value).strip("-.")
if value:
    print(value)
PY
)" || classification=""
  fi

  case "${classification}" in
    network_apply_failure|network_verify_failure|ownership_invalid|owned_state_invalid)
      domain=product
      ;;
    server_bypass*|dns_*|tcp_*|tls_*|https_*|doh_*|ipv6_*|likely_pmtu_blackhole|timeout)
      domain=remnawave
      ;;
    *)
      domain=diagnostic_unknown
      ;;
  esac
  mark_failure "${domain}" "provider_tun.connect.${classification:-unclassified}"
}

import_provider_profile() {
  local import_stdout="${PRIVATE_ROOT}/profile-import.stdout" import_stderr="${PRIVATE_ROOT}/profile-import.stderr"
  set +e
  guest_exec runuser -u e2e -- env \
    XDG_CONFIG_HOME="${GUEST_XDG}/config" \
    XDG_STATE_HOME="${GUEST_XDG}/state" \
    XDG_CACHE_HOME="${GUEST_XDG}/cache" \
    /bin/bash -lc "uri=\$(cat '${GUEST_PROVIDER_DIR}/profile-uri'); exec /usr/bin/podlaz import \"\${uri}\"" \
    >"${import_stdout}" 2>"${import_stderr}"
  local code=$?
  set -e
  (( code == 0 )) || return 1
  grep -F 'Next: podlaz connect' "${import_stdout}" >/dev/null || return 1
  guest_exec rm -rf "${GUEST_PROVIDER_DIR}"
  PROFILE_URI=""
}

capture_ordinary_egress() {
  guest_exec timeout 20 getent ahostsv4 example.com >/dev/null
  guest_exec timeout 30 curl -4 -fsS -o /dev/null https://example.com/
  ORDINARY_EGRESS="$(guest_exec timeout 30 curl -4 -fsS --max-time 15 "${PUBLIC_IP_CHECK_URL}" | tr -d '[:space:]')"
  python3 - "${ORDINARY_EGRESS}" <<'PY'
import ipaddress,sys
value=ipaddress.ip_address(sys.argv[1])
if value.version != 4:
    raise SystemExit("ordinary egress is not IPv4")
PY
  mask_value "${ORDINARY_EGRESS}"
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

run_provider_traffic_checks() {
  local before after
  before="$(remnawave_node_access_count)" || return 1

  guest_exec resolvectl flush-caches
  guest_exec timeout 20 getent ahostsv4 example.com >/dev/null
  record_evidence tun.system_dns pass

  PROBE_IP="$(guest_exec getent ahostsv4 example.com | awk 'NR == 1 {print $1}')"
  [[ -n "${PROBE_IP}" ]] || return 1
  guest_exec timeout 15 /bin/bash -lc "exec 3<>/dev/tcp/${PROBE_IP}/443; exec 3>&-"
  record_evidence tun.ipv4_tcp pass

  guest_exec timeout 20 openssl s_client -connect "${PROBE_IP}:443" -servername example.com -brief </dev/null \
    >"${PRIVATE_ROOT}/tls.stdout" 2>"${PRIVATE_ROOT}/tls.stderr"
  record_evidence tun.tls pass

  guest_exec timeout 30 curl -4 -fsS -o /dev/null https://example.com/
  record_evidence tun.https pass

  ACTIVE_EGRESS="$(guest_exec timeout 30 curl -4 -fsS --max-time 15 "${PUBLIC_IP_CHECK_URL}" | tr -d '[:space:]')"
  python3 - "${ACTIVE_EGRESS}" <<'PY'
import ipaddress,sys
value=ipaddress.ip_address(sys.argv[1])
if value.version != 4:
    raise SystemExit("active Remnawave egress is not IPv4")
PY
  mask_value "${ACTIVE_EGRESS}"

  after="$(remnawave_node_access_count)" || return 1
  (( after > before )) || {
    mark_failure remnawave remnawave.path_not_observed
    return 1
  }
  record_evidence tun.remnawave_path pass
}

assert_ordinary_connectivity_restored() {
  guest_exec resolvectl flush-caches
  guest_exec timeout 20 getent ahostsv4 example.com >/dev/null
  guest_exec timeout 30 curl -4 -fsS -o /dev/null https://example.com/
  guest_exec timeout 30 curl -4 -fsS --max-time 15 "${PUBLIC_IP_CHECK_URL}" >/dev/null
}

remove_guest_private_state() {
  guest_exec rm -rf "${GUEST_PROVIDER_DIR}" "${GUEST_PRIVATE}" "${GUEST_XDG}" >/dev/null
}

run_provider_scenario() {
  mark_failure infrastructure outer.baseline
  capture_outer_baseline || fail "could not establish clean hosted runner baseline"
  install -d -m 0700 "${XRAY_ROOT}"

  mark_failure infrastructure guest.prepare
  prepare_system_guest || fail "could not prepare isolated provider guest"
  install_tun_authorization || fail "could not install production authorization fixture"
  start_system_guest || fail "could not start isolated Remnawave guest"

  mark_failure fixture remnawave.bootstrap
  start_remnawave_fixture || fail "could not start ephemeral Remnawave fixture"

  mark_failure infrastructure guest.ordinary_connectivity
  capture_ordinary_egress || fail "isolated guest ordinary connectivity is unavailable"
  mark_failure fixture candidate.provenance
  install_candidate_in_guest || fail "could not install candidate package in provider guest"
  assert_guest_package_provenance || fail "candidate package/runtime provenance mismatch"
  record_evidence candidate.provenance pass

  mark_failure fixture provider.material
  guest_exec install -d -o e2e -g e2e -m 0700 "${GUEST_PRIVATE}"
  prepare_provider_material || fail "ephemeral Remnawave material is unavailable"
  import_provider_profile || fail "ephemeral Remnawave profile import/validation failed"
  remove_provider_material
  record_evidence remnawave.material_private pass

  mark_failure product ordinary_user.boundary
  assert_ordinary_user_boundary || fail "Remnawave TUN did not use the ordinary-user authorization boundary"
  record_evidence ordinary_user.boundary pass

  mark_failure fixture foreign.state
  create_foreign_sentinel || fail "could not establish foreign guest state"
  capture_guest_network_baseline

  mark_failure diagnostic_unknown provider_tun.connect
  local connect_code
  set +e
  run_guest_user /usr/bin/podlaz connect \
    >"${PRIVATE_ROOT}/tun-connect.stdout" 2>"${PRIVATE_ROOT}/tun-connect.stderr"
  connect_code=$?
  set -e
  if (( connect_code != 0 )); then
    classify_provider_tun_connect_failure "${connect_code}"
    fail "ephemeral Remnawave TUN connect failed"
  fi
  if ! wait_guest_status verified-active 90; then
    classify_provider_tun_connect_failure 1
    fail "ephemeral Remnawave TUN did not reach verified-active state"
  fi

  mark_failure product tun.active_authority
  assert_verified_active_authority || fail "ephemeral Remnawave TUN active authority is incomplete"
  record_evidence tun.verified_active pass

  mark_failure remnawave provider_tun.data_plane
  run_provider_traffic_checks || fail "ephemeral Remnawave TUN data plane failed"

  mark_failure product privacy.direct_uplink
  prepare_direct_probe || fail "could not prepare direct-uplink privacy probe"
  assert_direct_uplink_blocked || fail "ordinary direct uplink bypassed active Privacy Envelope"
  record_evidence privacy.direct_uplink_blocked pass
  assert_foreign_sentinel || fail "foreign guest state changed while Remnawave TUN was active"
  record_evidence foreign.state_preserved pass

  mark_failure product tun.doctor
  run_tun_doctor || fail "doctor --tun failed against active Remnawave TUN"

  mark_failure product tun.disconnect
  run_guest_user /usr/bin/podlaz disconnect >"${PRIVATE_ROOT}/disconnect.stdout" 2>"${PRIVATE_ROOT}/disconnect.stderr" || \
    fail "normal Remnawave TUN disconnect failed"
  wait_guest_status clean-inactive 90 || fail "Remnawave TUN did not converge to clean-inactive"
  record_evidence tun.clean_disconnect pass

  mark_failure product tun.terminal_cleanup
  assert_terminal_authority_clean || fail "Remnawave TUN left owned authority or runtime state"
  record_evidence tun.terminal_cleanup pass

  mark_failure product tun.recovery
  run_clean_recovery || fail "Remnawave TUN recovery plan is not clean after disconnect"
  record_evidence tun.recovery_clean pass

  mark_failure product guest.baseline_restore
  assert_guest_network_baseline_restored || fail "Remnawave TUN did not restore guest network baseline"
  record_evidence guest.baseline_restored pass
  assert_ordinary_connectivity_restored || fail "ordinary guest connectivity did not recover after Remnawave TUN"
  record_evidence guest.ordinary_connectivity_restored pass

  mark_failure fixture guest.private_cleanup
  remove_guest_private_state || fail "Remnawave private guest state could not be removed"

  mark_failure fixture remnawave.cleanup
  stop_remnawave_fixture || fail "ephemeral Remnawave fixture cleanup failed"
  record_evidence fixture.cleanup pass

  FAILURE_CLASS=none
  FAILURE_STEP=none
}

main() {
  (($# == 1)) || fail "usage: $0 CANDIDATE.deb"
  require_cmd awk bash chmod curl dpkg dpkg-deb find grep install ip jq machinectl openssl python3 rm sed seq sha256sum sudo systemd-nspawn systemd-run timeout tr
  [[ "${EXPECTED_COMMIT}" =~ ^[0-9a-fA-F]{40}$ ]] || fail "PODLAZ_E2E_CANDIDATE_COMMIT must be an exact 40-hex commit"
  install -d -m 0700 "${E2E_TMP_ROOT}" "${E2E_ARTIFACT_DIR}"
  validate_candidate "$1"
  CANDIDATE_SHA256="$(sha256sum "${CANDIDATE_DEB}" | awk '{print $1}')"
  : >"${REPORT}"
  chmod 0600 "${REPORT}"
  printf 'candidate.commit=%s\n' "${EXPECTED_COMMIT,,}" >>"${REPORT}"
  printf 'candidate.package_sha256=%s\n' "${CANDIDATE_SHA256}" >>"${REPORT}"
  trap 'remove_provider_material; stop_remnawave_fixture; teardown_all' EXIT INT TERM
  mask_provider_material
  run_provider_scenario
  remove_provider_material
  stop_remnawave_fixture
  teardown_all
}

if [[ "${1:-}" == validate-report ]]; then
  require_cmd python3 sha256sum
  EXPECTED_COMMIT="${PODLAZ_E2E_CANDIDATE_COMMIT:-${GITHUB_SHA:-}}"
  CANDIDATE_SHA256="${PODLAZ_E2E_CANDIDATE_SHA256:-}"
  [[ "${EXPECTED_COMMIT}" =~ ^[0-9a-fA-F]{40}$ ]] || fail "PODLAZ_E2E_CANDIDATE_COMMIT must be an exact 40-hex commit"
  [[ "${CANDIDATE_SHA256}" =~ ^[0-9a-f]{64}$ ]] || fail "PODLAZ_E2E_CANDIDATE_SHA256 must be an exact package digest"
  validate_report
  exit 0
fi

main "$@"
