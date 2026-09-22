#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"
# Reuse the permanent hosted nspawn/TUN infrastructure. This file owns only
# resource-soak orchestration and evidence.
# shellcheck source=hosted-synthetic-tun.sh
source "${SCRIPT_DIR}/hosted-synthetic-tun.sh"

SUMMARY="${E2E_ARTIFACT_DIR}/hosted-resource-soak.txt"
SOAK_REPORT="${E2E_ARTIFACT_DIR}/tun-resource-soak-report.json"
SOAK_FAILURE="${E2E_ARTIFACT_DIR}/tun-resource-failure.json"

MACHINE="podlaz-resource-soak"
HOST_VETH="pzsoak0"
HOST_ENDPOINT_DEV="pzsoaksrv"
GUEST_IF="host0"
NETWORK_CIDR="172.31.252.0/30"
HOST_CIDR="172.31.252.1/30"
GUEST_CIDR="172.31.252.2/30"
HOST_IP="172.31.252.1"
ENDPOINT_CIDR="172.31.251.1/32"
ENDPOINT_IP="172.31.251.1"
GUEST_ROOT="${E2E_TMP_ROOT}/resource-soak-system-guest"
PRIVATE_ROOT="${E2E_TMP_ROOT}/hosted-resource-soak-private"
XRAY_ROOT="${PRIVATE_ROOT}/synthetic-xray"
GUEST_CANDIDATE="/opt/podlaz-resource-soak-candidate.deb"
GUEST_SOAK_TMP="/tmp/podlaz-resource-soak-private"
GUEST_SOAK_ARTIFACTS="/tmp/podlaz-resource-soak-public"
TRUSTED_HOST="/etc/podlaz-e2e/tun-resource-soak-trusted-host.json"
EXPECTED_COMMIT="${PODLAZ_E2E_CANDIDATE_COMMIT:-${GITHUB_SHA:-}}"

EVIDENCE_KEYS=(
  candidate.provenance
  environment.clean_baseline
  soak.full_window
  soak.lifecycle_thresholds
  soak.terminal_cleanup
  guest.baseline_restored
  guest.ordinary_connectivity_restored
  outer.cleanup
  artifact.privacy
)

FAILURE_CLASS=diagnostic_unknown
FAILURE_STEP=bootstrap
REPORT_FINALIZED=false
SYSTEM_GUEST_ACTIVE=false
TEARDOWN_RUNNING=false
NSPAWN_PID=""
XRAY_PID=""
CANDIDATE_DEB=""
SOAK_URI=""

evidence_recorded() {
  local key="$1"
  [[ -f "${SUMMARY}" ]] && grep -Eq "^${key}=" "${SUMMARY}"
}

record_evidence() {
  local key="$1" state="$2"
  [[ "${key}" =~ ^[a-z0-9_.-]+$ ]] || fail "invalid hosted resource-soak evidence key"
  case "${state}" in
    pass|fail) ;;
    *) fail "invalid hosted resource-soak evidence state" ;;
  esac
  evidence_recorded "${key}" && fail "duplicate hosted resource-soak evidence key: ${key}"
  printf '%s=%s\n' "${key}" "${state}" >>"${SUMMARY}"
}

record_if_missing() {
  local key="$1" state="$2"
  evidence_recorded "${key}" || record_evidence "${key}" "${state}"
}

mark_failure() {
  local class="$1" step="$2"
  case "${class}" in
    product|fixture|infrastructure|capability|diagnostic_unknown) ;;
    *) class=diagnostic_unknown ;;
  esac
  FAILURE_CLASS="${class}"
  FAILURE_STEP="${step//[^A-Za-z0-9_.-]/_}"
}

finalize_summary() {
  local key
  [[ "${REPORT_FINALIZED}" == false ]] || return 0
  for key in "${EVIDENCE_KEYS[@]}"; do
    record_if_missing "${key}" fail
  done
  printf 'failure.class=%s\n' "${FAILURE_CLASS}" >>"${SUMMARY}"
  printf 'failure.step=%s\n' "${FAILURE_STEP}" >>"${SUMMARY}"
  REPORT_FINALIZED=true
}

validate_report() {
  python3 - "${SUMMARY}" "${SOAK_REPORT}" <<'PY'
import json
import sys
from pathlib import Path

summary = Path(sys.argv[1])
soak_report = Path(sys.argv[2])
required = {
    "candidate.provenance",
    "environment.clean_baseline",
    "soak.full_window",
    "soak.lifecycle_thresholds",
    "soak.terminal_cleanup",
    "guest.baseline_restored",
    "guest.ordinary_connectivity_restored",
    "outer.cleanup",
    "artifact.privacy",
}
if not summary.is_file() or summary.is_symlink():
    raise SystemExit("hosted resource-soak summary is missing")
values = {}
for raw in summary.read_text(encoding="utf-8").splitlines():
    if not raw or "=" not in raw:
        raise SystemExit("hosted resource-soak summary has an invalid line")
    key, value = raw.split("=", 1)
    if key in values:
        raise SystemExit(f"duplicate hosted resource-soak summary key: {key}")
    values[key] = value
if set(values) != required | {"failure.class", "failure.step"}:
    raise SystemExit("unexpected hosted resource-soak summary schema")
if any(values[key] != "pass" for key in required):
    raise SystemExit("hosted resource-soak required evidence did not pass")
if values["failure.class"] != "none" or values["failure.step"] != "none":
    raise SystemExit("hosted resource-soak summary reports a failure")
if not soak_report.is_file() or soak_report.is_symlink():
    raise SystemExit("resource-soak report is missing")
report = json.loads(soak_report.read_text(encoding="utf-8"))
if report.get("schema_version") != 1 or report.get("ok") is not True:
    raise SystemExit("resource-soak report is not successful")
if report.get("verdict") not in {"observation_complete", "acceptance_passed"}:
    raise SystemExit("resource-soak verdict is not a successful reviewed-policy outcome")
configuration = report.get("configuration") or {}
if configuration.get("duration_seconds") != 10800:
    raise SystemExit("resource-soak did not preserve the three-hour measurement window")
if configuration.get("warmup_seconds") != 120 or configuration.get("sample_interval_seconds") != 60:
    raise SystemExit("resource-soak changed the reviewed warmup/sample cadence")
trend = report.get("trend") or {}
if trend.get("observed_duration_seconds", 0) < 10800:
    raise SystemExit("resource-soak evidence did not span the full measurement window")
lifecycle = report.get("lifecycle") or {}
if (lifecycle.get("cleanup") or {}).get("ok") is not True:
    raise SystemExit("resource-soak cleanup thresholds failed")
if (lifecycle.get("reconnect") or {}).get("ok") is not True:
    raise SystemExit("resource-soak reconnect thresholds failed")
PY
}

assert_public_artifact_privacy() {
  local extra
  extra="$(find "${E2E_ARTIFACT_DIR}" -mindepth 1 -maxdepth 1 \
    ! -name 'hosted-resource-soak.txt' \
    ! -name 'tun-resource-soak-report.json' \
    ! -name 'tun-resource-failure.json' -print -quit)"
  [[ -z "${extra}" ]] || return 1
  [[ -f "${SUMMARY}" && ! -L "${SUMMARY}" ]] || return 1
  ! grep -Eiq 'vless://|vmess://|trojan://|ss://|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|172[.]31[.](251|252)[.]' "${E2E_ARTIFACT_DIR}"/*
}

provision_trusted_host() {
  guest_exec install -d -m 0700 "$(dirname "${TRUSTED_HOST}")"
  guest_exec python3 - "${GUEST_IF}" "${TRUSTED_HOST}" <<'PY'
import copy
import json
import os
import sys
from pathlib import Path

sys.path.insert(0, "/workspace")
from scripts.e2e.lib import tun_soak_isolation as isolation

uplink = sys.argv[1]
target = Path(sys.argv[2])
snapshot = isolation.collect_snapshot()
isolation.validate_clean_baseline(snapshot, uplink_environment="hosted-guest")

links = [copy.deepcopy(link) for link in snapshot["links"] if link.get("ifname") == uplink]
if len(links) != 1:
    raise SystemExit("hosted soak trusted uplink link is ambiguous")
defaults = [
    copy.deepcopy(route)
    for route in snapshot["routes_v4"] + snapshot["routes_v6"]
    if route.get("table") == "main"
    and route.get("dst") == "default"
    and route.get("type") == "unicast"
]
if not defaults or {route.get("dev") for route in defaults} != {uplink}:
    raise SystemExit("hosted soak trusted default route is ambiguous")
defaults.sort(key=lambda item: json.dumps(item, sort_keys=True, separators=(",", ":")))
inventories = [entry for entry in snapshot["addresses"] if entry.get("ifname") == uplink]
if len(inventories) != 1:
    raise SystemExit("hosted soak trusted uplink address inventory is ambiguous")
addresses = [
    copy.deepcopy(address)
    for address in inventories[0].get("addresses", [])
    if address.get("scope") == "global" and address.get("family") in {"inet", "inet6"}
]
if not addresses:
    raise SystemExit("hosted soak trusted uplink has no global address")
addresses.sort(key=lambda item: json.dumps(item, sort_keys=True, separators=(",", ":")))
connections = [
    copy.deepcopy(connection)
    for connection in snapshot["network_manager"]
    if connection.get("device") == uplink
]
if len(connections) != 1:
    raise SystemExit("hosted soak trusted NetworkManager ownership is ambiguous")
trusted = {
    "schema_version": "podlaz.e2e.trusted-host.v2",
    "runtime_os": copy.deepcopy(snapshot["runtime_os"]),
    "uplink": {
        "link": links[0],
        "default_routes": defaults,
        "global_addresses": addresses,
        "network_manager_connection": connections[0],
    },
    "resolved": copy.deepcopy(snapshot["resolved"]),
}
isolation.validate_trusted_host(snapshot, trusted, uplink_environment="hosted-guest")
temporary = target.with_name(target.name + ".tmp")
temporary.write_text(json.dumps(trusted, sort_keys=True, separators=(",", ":")) + "\n", encoding="utf-8")
os.chmod(temporary, 0o600)
os.replace(temporary, target)
PY
  [[ "$(guest_exec stat -c '%U:%G:%a' "${TRUSTED_HOST}" | tr -d '[:space:]')" == "root:root:600" ]]
}

copy_guest_evidence() {
  guest_exec test -f "${GUEST_SOAK_ARTIFACTS}/tun-resource-soak-report.json" &&
    guest_exec cat "${GUEST_SOAK_ARTIFACTS}/tun-resource-soak-report.json" >"${SOAK_REPORT}" || true
  guest_exec test -f "${GUEST_SOAK_ARTIFACTS}/tun-resource-failure.json" &&
    guest_exec cat "${GUEST_SOAK_ARTIFACTS}/tun-resource-failure.json" >"${SOAK_FAILURE}" || true
  chmod 0600 "${SOAK_REPORT}" "${SOAK_FAILURE}" 2>/dev/null || true
}

classify_soak_failure() {
  local phase=""
  if [[ -f "${SOAK_REPORT}" ]] && jq -e '.lifecycle.cleanup.ok == false or .lifecycle.reconnect.ok == false or (.policy.evaluated == true and .policy.ok == false)' "${SOAK_REPORT}" >/dev/null 2>&1; then
    mark_failure product resource.policy
    return
  fi
  if [[ -f "${SOAK_FAILURE}" ]]; then
    phase="$(jq -r '.phase // empty' "${SOAK_FAILURE}" 2>/dev/null || true)"
  fi
  case "${phase}" in
    host-attestation|cleanup-preflight|configuration|isolation-baseline)
      mark_failure infrastructure "soak.${phase}"
      ;;
    profile-import)
      mark_failure fixture "soak.${phase}"
      ;;
    package-provenance|inactive-*|precondition-*|warmed-inactive-baseline|session-one-*|active-*|warmup|post-cleanup|reconnect-*|report)
      mark_failure product "soak.${phase}"
      ;;
    *)
      mark_failure diagnostic_unknown "soak.${phase:-runtime}"
      ;;
  esac
}

run_hosted_soak() {
  local soak_code
  mark_failure infrastructure outer.baseline
  capture_outer_baseline

  mark_failure infrastructure guest.prepare
  prepare_system_guest
  start_system_guest

  mark_failure infrastructure guest.clean_baseline
  provision_trusted_host
  capture_guest_network_baseline
  record_evidence environment.clean_baseline pass

  mark_failure fixture synthetic.endpoint
  start_synthetic_xray_endpoint
  guest_exec test -s /run/podlaz-synthetic-xray/client-uri

  mark_failure product candidate.provenance
  set +e
  guest_exec /bin/bash -lc "
    set -Eeuo pipefail
    uri=\$(cat /run/podlaz-synthetic-xray/client-uri)
    exec runuser -u e2e -- env \\
      E2E_TMP_ROOT='${GUEST_SOAK_TMP}' \\
      E2E_ARTIFACT_DIR='${GUEST_SOAK_ARTIFACTS}' \\
      PODLAZ_E2E_PROFILE_URI=\"\${uri}\" \\
      PODLAZ_E2E_PREBUILT_DEB='${GUEST_CANDIDATE}' \\
      PODLAZ_E2E_CANDIDATE_COMMIT='${EXPECTED_COMMIT}' \\
      PODLAZ_E2E_SOAK_TRUSTED_HOST_FILE='${TRUSTED_HOST}' \\
      PODLAZ_E2E_SOAK_UPLINK_ENVIRONMENT=hosted-guest \\
      bash /workspace/scripts/e2e/tun-resource-soak.sh
  "
  soak_code=$?
  set -e
  copy_guest_evidence
  if (( soak_code != 0 )); then
    classify_soak_failure
    return "${soak_code}"
  fi

  jq -e --arg commit "${EXPECTED_COMMIT,,}" '.provenance.podlaz_commit == $commit' "${SOAK_REPORT}" >/dev/null
  record_evidence candidate.provenance pass
  jq -e '
    .ok == true and
    .configuration.duration_seconds == 10800 and
    .configuration.precondition_warmup_seconds == 30 and
    .configuration.warmup_seconds == 120 and
    .configuration.sample_interval_seconds == 60 and
    .configuration.reconnect_warmup_seconds == 120 and
    .configuration.reconnect_samples == 3 and
    .trend.observed_duration_seconds >= 10800
  ' "${SOAK_REPORT}" >/dev/null
  record_evidence soak.full_window pass
  jq -e '.lifecycle.cleanup.ok == true and .lifecycle.reconnect.ok == true' "${SOAK_REPORT}" >/dev/null
  record_evidence soak.lifecycle_thresholds pass

  mark_failure product soak.terminal_cleanup
  guest_exec /bin/bash -lc '! dpkg-query -W -f="\${db:Status-Abbrev}" podlaz 2>/dev/null | grep -q "^ii"'
  guest_exec test ! -e /run/podlaz/network-session-continuation.json
  guest_exec /bin/bash -lc '! ip link show dev podlaz0 >/dev/null 2>&1'
  guest_exec /bin/bash -lc '! find /run/podlaz/transactions -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null | grep -q .'
  record_evidence soak.terminal_cleanup pass

  mark_failure infrastructure guest.baseline_restore
  assert_guest_network_baseline_restored
  record_evidence guest.baseline_restored pass
  guest_exec resolvectl flush-caches
  guest_exec timeout 20 getent ahostsv4 example.com >/dev/null
  guest_exec timeout 30 curl -4 -fsS -o /dev/null https://example.com/
  record_evidence guest.ordinary_connectivity_restored pass

  FAILURE_CLASS=none
  FAILURE_STEP=none
}

cleanup() {
  local saved=$? cleanup_failed=0
  [[ "${TEARDOWN_RUNNING}" == false ]] || return
  TEARDOWN_RUNNING=true
  trap - EXIT INT TERM
  set +e
  stop_synthetic_xray_endpoint || cleanup_failed=1
  stop_system_guest || cleanup_failed=1
  cleanup_outer_plumbing || cleanup_failed=1
  if [[ -n "${OUTER_DEFAULT_ROUTE}" ]] && assert_outer_baseline_restored; then
    record_if_missing outer.cleanup pass
  else
    cleanup_failed=1
    record_if_missing outer.cleanup fail
    [[ "${FAILURE_CLASS}" != none ]] || mark_failure infrastructure outer.cleanup
  fi
  if assert_public_artifact_privacy; then
    record_if_missing artifact.privacy pass
  else
    cleanup_failed=1
    record_if_missing artifact.privacy fail
    [[ "${FAILURE_CLASS}" != none ]] || mark_failure fixture artifact.privacy
  fi
  if (( saved != 0 )) && [[ "${FAILURE_CLASS}" == none ]]; then
    mark_failure diagnostic_unknown scenario
  fi
  finalize_summary
  set -e
  if (( saved == 0 && cleanup_failed != 0 )); then saved=1; fi
  exit "${saved}"
}

main() {
  (($# == 1)) || fail "usage: $0 CANDIDATE.deb"
  [[ "${EXPECTED_COMMIT}" =~ ^[0-9a-fA-F]{40}$ ]] || fail "PODLAZ_E2E_CANDIDATE_COMMIT must be an exact 40-hex commit"
  require_cmd awk bash chmod cmp curl debootstrap dpkg dpkg-deb find grep install ip iptables jq mktemp nft python3 readlink rm seq sha256sum sleep ss sudo systemd-nspawn systemd-run timeout
  validate_candidate "$1"
  install -d -m 0700 "${PRIVATE_ROOT}" "${E2E_ARTIFACT_DIR}" "${XRAY_ROOT}"
  rm -f -- "${SUMMARY}" "${SOAK_REPORT}" "${SOAK_FAILURE}"
  : >"${SUMMARY}"
  chmod 0600 "${SUMMARY}"
  trap cleanup EXIT INT TERM
  run_hosted_soak
}

if [[ "${1:-}" == validate-report ]]; then
  validate_report
  exit 0
fi

main "$@"
