#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"

BASE_SCENARIO="${SCRIPT_DIR}/hosted-synthetic-tun.sh"
METRICS_TOOL="/workspace/scripts/e2e/lib/tun_soak_metrics.py"
STATUS_TOOL="${SCRIPT_DIR}/lib/tun_soak_status.py"
ACTIVE_AUTHORITY_HELPER="/workspace/scripts/e2e/hosted_synthetic_active_authority.py"
NETWORK_AUTHORITY_HELPER="/workspace/scripts/e2e/hosted_synthetic_network_authority.py"
RECOVERY_JSON_HELPER="/workspace/scripts/e2e/lib/recovery_json.sh"

MACHINE="podlaz-synthetic-tun"
GUEST_IF="host0"
FOREIGN_NFT_TABLE="pzsynt_foreign"
GUEST_XDG="/home/e2e/.local/share/podlaz-hosted-synthetic-tun"
BASE_GUEST_PRIVATE="/tmp/podlaz-hosted-synthetic-tun"
GUEST_PRIVATE="/tmp/podlaz-hosted-resource-soak"
TRANSACTION_DIR="/run/podlaz/transactions"
SESSION_STATE="/run/podlaz/network-session-continuation.json"

SOAK_DURATION_SECONDS=10800
SOAK_PRECONDITION_WARMUP_SECONDS=30
SOAK_WARMUP_SECONDS=120
SOAK_SAMPLE_INTERVAL_SECONDS=60
SOAK_DOCTOR_EVERY_SAMPLES=10
SOAK_RECONNECT_WARMUP_SECONDS=120
SOAK_RECONNECT_SAMPLES=3
SOAK_CLEANUP_SETTLE_SECONDS=10
TUN_HEALTH_TIMEOUT_SECONDS=75
TUN_HEALTH_POLL_SECONDS=1
TUN_STATUS_TIMEOUT_SECONDS=10
TUN_DIAGNOSTIC_TIMEOUT_SECONDS=90
SOAK_CLEANUP_ATTEMPTS=2
SOAK_CLEANUP_RETRY_SECONDS=2

EXPECTED_RUNTIME_MINUTES=210
JOB_TIMEOUT_MINUTES=300
MAX_PRIVATE_BYTES=6442450944
MAX_PUBLIC_BYTES=4194304

PRIVATE_ROOT="${E2E_TMP_ROOT}/hosted-resource-soak-private"
BASE_TMP_ROOT="${PRIVATE_ROOT}/base-private"
BASE_ARTIFACT_DIR="${PRIVATE_ROOT}/base-public"
BASE_REPORT="${BASE_ARTIFACT_DIR}/hosted-synthetic-tun.txt"
BASE_STDOUT="${PRIVATE_ROOT}/base.stdout"
BASE_STDERR="${PRIVATE_ROOT}/base.stderr"
CONTROL_DIR="${BASE_TMP_ROOT}/control"

ACTIVE_SAMPLES_PRIVATE="${PRIVATE_ROOT}/active-samples.ndjson"
RECONNECT_SAMPLES_PRIVATE="${PRIVATE_ROOT}/reconnect-samples.ndjson"
BASELINE_BOUNDARY_PRIVATE="${PRIVATE_ROOT}/warmed-inactive-baseline.json"
CLEANUP_BOUNDARY_PRIVATE="${PRIVATE_ROOT}/post-cleanup.json"
RECONNECT_CLEANUP_BOUNDARY_PRIVATE="${PRIVATE_ROOT}/post-reconnect-cleanup.json"
PROVENANCE_PRIVATE="${PRIVATE_ROOT}/provenance.json"
CONFIGURATION_PRIVATE="${PRIVATE_ROOT}/configuration.json"
PUBLIC_REPORT="${E2E_ARTIFACT_DIR}/hosted-resource-soak.json"
STATUS_REPORT="${E2E_ARTIFACT_DIR}/hosted-resource-soak-status.txt"

GUEST_ACTIVE_SAMPLES="${GUEST_PRIVATE}/active-samples.ndjson"
GUEST_RECONNECT_SAMPLES="${GUEST_PRIVATE}/reconnect-samples.ndjson"
GUEST_BASELINE_BOUNDARY="${GUEST_PRIVATE}/warmed-inactive-baseline.json"
GUEST_CLEANUP_BOUNDARY="${GUEST_PRIVATE}/post-cleanup.json"
GUEST_RECONNECT_CLEANUP_BOUNDARY="${GUEST_PRIVATE}/post-reconnect-cleanup.json"
GUEST_PRECONDITION_IDENTITY="${GUEST_PRIVATE}/precondition-identity.json"
GUEST_SESSION_ONE_IDENTITY="${GUEST_PRIVATE}/session-one-identity.json"
GUEST_SESSION_TWO_IDENTITY="${GUEST_PRIVATE}/session-two-identity.json"
GUEST_DAEMON_BASELINE_IDENTITY="${GUEST_PRIVATE}/daemon-baseline-identity.json"
GUEST_DAEMON_CLEANUP_IDENTITY="${GUEST_PRIVATE}/daemon-cleanup-identity.json"
GUEST_DAEMON_RECONNECT_CLEANUP_IDENTITY="${GUEST_PRIVATE}/daemon-reconnect-cleanup-identity.json"
GUEST_PRECONDITION_MANIFEST="${GUEST_PRIVATE}/precondition-network.json"
GUEST_SESSION_ONE_MANIFEST="${GUEST_PRIVATE}/session-one-network.json"
GUEST_SESSION_TWO_MANIFEST="${GUEST_PRIVATE}/session-two-network.json"

EXPECTED_COMMIT="${PODLAZ_E2E_CANDIDATE_COMMIT:-${GITHUB_SHA:-}}"
FAILURE_CLASS=diagnostic_unknown
FAILURE_STEP=bootstrap
BASE_PID=""
BASE_EXIT_CODE=""
REPORT_FINALIZED=false
BASE_POSITIVE_CONTROL=fail
PRODUCT_CLEANUP=fail
ORDINARY_CONNECTIVITY=fail
POLICY_RESULT=unavailable
DOCTOR_RUNS=0
DOCTOR_UNHEALTHY_RUNS=0
WARMED_DAEMON_PID=""
RUN_STARTED_SECONDS="${SECONDS}"
OBSERVED_PRIVATE_BYTES=0
HOST_MEMORY_BYTES=0
HOST_FREE_DISK_BYTES=0

mark_failure() {
  FAILURE_CLASS="$1"
  FAILURE_STEP="$2"
}

guest_exec() {
  sudo -n systemd-run --machine="${MACHINE}" --expand-environment=no --wait --pipe --collect --quiet -- "$@"
}

run_guest_user() {
  guest_exec runuser -u e2e -- env \
    XDG_CONFIG_HOME="${GUEST_XDG}/config" \
    XDG_STATE_HOME="${GUEST_XDG}/state" \
    XDG_CACHE_HOME="${GUEST_XDG}/cache" \
    "$@"
}

main_pid() {
  guest_exec systemctl show -p MainPID --value podlazd.service | tr -d '[:space:]'
}

assert_same_daemon() {
  local current
  current="$(main_pid)"
  [[ "${current}" =~ ^[1-9][0-9]*$ && "${current}" == "${WARMED_DAEMON_PID}" ]]
}

assert_foreign_sentinel() {
  guest_exec nft list table inet "${FOREIGN_NFT_TABLE}" >/dev/null 2>&1
}

load_envelope_identity() {
  guest_exec python3 -c 'import json,re,sys
with open(sys.argv[1], encoding="utf-8") as handle:
    state=json.load(handle)
protection=state.get("protection") or {}
family=protection.get("family")
table=protection.get("table")
if protection.get("state") != "armed" or family != "inet" or not re.fullmatch(r"podlaz_pe_[0-9a-f]{12}(?:_[1-9][0-9]{0,2})?", str(table or "")):
    raise SystemExit(1)
print(f"{family} {table}")' "${SESSION_STATE}"
}

assert_privacy_envelope_present() {
  local family table
  read -r family table <<<"$(load_envelope_identity)"
  [[ "${family}" == inet && -n "${table}" ]] || return 1
  guest_exec nft list table "${family}" "${table}" >/dev/null 2>&1
}

wait_for_verified_cli_status() {
  local label="$1" deadline stdout_file stderr_file code verdict
  stdout_file="${PRIVATE_ROOT}/${label}-status.stdout"
  stderr_file="${PRIVATE_ROOT}/${label}-status.stderr"
  deadline=$((SECONDS + TUN_HEALTH_TIMEOUT_SECONDS))
  while ((SECONDS < deadline)); do
    set +e
    run_guest_user timeout --signal=TERM --kill-after=5s "${TUN_STATUS_TIMEOUT_SECONDS}s" /usr/bin/podlaz status \
      >"${stdout_file}" 2>"${stderr_file}"
    code=$?
    set -e
    if ((code == 124 || code == 137)); then
      return 1
    fi
    if verdict="$(python3 "${STATUS_TOOL}" classify --stdout-file "${stdout_file}" --exit-code "${code}" 2>/dev/null)"; then
      :
    else
      verdict=invalid-status
    fi
    case "${verdict}" in
      verified) return 0 ;;
      retry-revalidating|retry-degraded) sleep "${TUN_HEALTH_POLL_SECONDS}" ;;
      *) return 1 ;;
    esac
  done
  return 1
}

run_bounded_traffic() {
  guest_exec resolvectl flush-caches >/dev/null
  guest_exec timeout 20 getent ahostsv4 github.com >/dev/null
  guest_exec timeout 30 curl -4 -fsS --max-time 10 -o /dev/null https://api.ipify.org
}

run_bounded_doctor() {
  local label="$1" code stdout_file stderr_file
  stdout_file="${PRIVATE_ROOT}/${label}-doctor.stdout"
  stderr_file="${PRIVATE_ROOT}/${label}-doctor.stderr"
  set +e
  run_guest_user timeout --signal=TERM --kill-after=5s "${TUN_DIAGNOSTIC_TIMEOUT_SECONDS}s" \
    /usr/bin/podlaz doctor --tun >"${stdout_file}" 2>"${stderr_file}"
  code=$?
  set -e
  DOCTOR_RUNS=$((DOCTOR_RUNS + 1))
  case "${code}" in
    0) ;;
    3) DOCTOR_UNHEALTHY_RUNS=$((DOCTOR_UNHEALTHY_RUNS + 1)) ;;
    *) return 1 ;;
  esac
  wait_for_verified_cli_status "${label}-post-doctor"
}

connect_profile() {
  local label="$1"
  guest_exec /bin/bash -lc "id=\$(cat '${BASE_GUEST_PRIVATE}/profile-id'); runuser -u e2e -- env XDG_CONFIG_HOME='${GUEST_XDG}/config' XDG_STATE_HOME='${GUEST_XDG}/state' XDG_CACHE_HOME='${GUEST_XDG}/cache' /usr/bin/podlaz connect --mode tun \"${id}\" >'${GUEST_PRIVATE}/${label}-connect.stdout' 2>'${GUEST_PRIVATE}/${label}-connect.stderr'"
  wait_for_verified_cli_status "${label}"
}

disconnect_profile() {
  local label="$1"
  guest_exec /bin/bash -lc "runuser -u e2e -- env XDG_CONFIG_HOME='${GUEST_XDG}/config' XDG_STATE_HOME='${GUEST_XDG}/state' XDG_CACHE_HOME='${GUEST_XDG}/cache' /usr/bin/podlaz disconnect >'${GUEST_PRIVATE}/${label}-disconnect.stdout' 2>'${GUEST_PRIVATE}/${label}-disconnect.stderr'"
  guest_exec /bin/bash -lc "for _ in \$(seq 1 80); do curl --fail --silent --show-error --max-time 3 --unix-socket /run/podlaz/podlazd.sock http://localhost/v1/status >'${GUEST_PRIVATE}/clean-status.json' 2>/dev/null && python3 /workspace/scripts/e2e/lib/daemon_status_semantics.py clean-inactive '${GUEST_PRIVATE}/clean-status.json' >/dev/null 2>&1 && exit 0; sleep 1; done; exit 1"
}

capture_active_authority() {
  local label="$1" manifest="$2"
  guest_exec /bin/bash -lc "curl --fail --silent --show-error --max-time 5 --unix-socket /run/podlaz/podlazd.sock http://localhost/v1/status >'${GUEST_PRIVATE}/${label}-daemon-status.json'"
  guest_exec /bin/bash -lc "resolvectl dns >'${GUEST_PRIVATE}/${label}-resolved-dns.txt'; resolvectl domain >'${GUEST_PRIVATE}/${label}-resolved-domain.txt'; resolvectl default-route >'${GUEST_PRIVATE}/${label}-resolved-default-route.txt'; nft -j list ruleset >'${GUEST_PRIVATE}/${label}-nft.json'"
  guest_exec python3 "${ACTIVE_AUTHORITY_HELPER}" \
    --status "${GUEST_PRIVATE}/${label}-daemon-status.json" \
    --transactions "${TRANSACTION_DIR}" \
    --session "${SESSION_STATE}" \
    --boot-id /proc/sys/kernel/random/boot_id \
    --runtime-config /run/podlaz/generated/xray.json \
    --resolved-dns "${GUEST_PRIVATE}/${label}-resolved-dns.txt" \
    --resolved-domain "${GUEST_PRIVATE}/${label}-resolved-domain.txt" \
    --resolved-default-route "${GUEST_PRIVATE}/${label}-resolved-default-route.txt" \
    --nft-ruleset "${GUEST_PRIVATE}/${label}-nft.json" >/dev/null
  guest_exec python3 "${NETWORK_AUTHORITY_HELPER}" snapshot "${TRANSACTION_DIR}" "${manifest}" >/dev/null
  guest_exec python3 "${NETWORK_AUTHORITY_HELPER}" verify-present "${manifest}" >/dev/null
  assert_privacy_envelope_present
  assert_foreign_sentinel
}

assert_clean_runtime() {
  local label="$1" manifest="$2"
  guest_exec /bin/bash -lc "cd /workspace && source scripts/e2e/lib/e2e.sh && source scripts/e2e/lib/tun_package_assertions.sh && verify_tun_package_resources_absent '${label}' '${NETWORK_AUTHORITY_HELPER}' '${manifest}'"
  guest_exec test ! -e "${SESSION_STATE}"
  guest_exec /bin/bash -lc "! nft list tables | grep -E 'table inet podlaz_pe_[0-9a-f]+' >/dev/null"
  guest_exec /bin/bash -lc "! nmcli -t -f NAME,DEVICE connection show --active | grep -F ':podlaz0' >/dev/null"
  assert_foreign_sentinel
  guest_exec /bin/bash -lc "runuser -u e2e -- env XDG_CONFIG_HOME='${GUEST_XDG}/config' XDG_STATE_HOME='${GUEST_XDG}/state' XDG_CACHE_HOME='${GUEST_XDG}/cache' /usr/bin/podlaz recover --json >'${GUEST_PRIVATE}/${label}-recover.json' 2>'${GUEST_PRIVATE}/${label}-recover.stderr' && cd /workspace && source '${RECOVERY_JSON_HELPER}' && assert_clean_recovery_json_file '${GUEST_PRIVATE}/${label}-recover.json'"
}

discover_active_identity() {
  local output="$1" daemon
  daemon="$(main_pid)"
  [[ "${daemon}" =~ ^[1-9][0-9]*$ ]] || return 1
  guest_exec python3 "${METRICS_TOOL}" discover \
    --daemon-pid "${daemon}" \
    --transaction-dir "${TRANSACTION_DIR}" \
    --output "${output}"
}

sample_active() {
  local identity="$1" output="$2" phase="$3" session="$4" index="$5" elapsed="$6"
  guest_exec python3 "${METRICS_TOOL}" sample \
    --identity "${identity}" \
    --output "${output}" \
    --phase "${phase}" \
    --session "${session}" \
    --sample-index "${index}" \
    --elapsed-seconds "${elapsed}"
}

capture_daemon_boundary() {
  local identity="$1" output="$2" phase="$3" index="$4" daemon
  daemon="$(main_pid)"
  [[ "${daemon}" =~ ^[1-9][0-9]*$ ]] || return 1
  guest_exec python3 "${METRICS_TOOL}" discover-daemon --daemon-pid "${daemon}" --output "${identity}"
  guest_exec python3 "${METRICS_TOOL}" boundary-sample \
    --identity "${identity}" \
    --output "${output}" \
    --phase "${phase}" \
    --sample-index "${index}" \
    --elapsed-seconds 0
}

copy_guest_file() {
  local source="$1" target="$2"
  guest_exec cat "${source}" >"${target}"
  chmod 0600 "${target}"
}

precondition_warmed_baseline() {
  local daemon
  mark_failure product precondition.connect
  connect_profile precondition
  capture_active_authority precondition "${GUEST_PRECONDITION_MANIFEST}"
  discover_active_identity "${GUEST_PRECONDITION_IDENTITY}"
  daemon="$(main_pid)"
  [[ "${daemon}" =~ ^[1-9][0-9]*$ ]] || return 1

  mark_failure diagnostic_unknown precondition.warmup
  sleep "${SOAK_PRECONDITION_WARMUP_SECONDS}"
  run_bounded_traffic
  run_bounded_doctor precondition
  assert_privacy_envelope_present
  assert_foreign_sentinel

  mark_failure product precondition.cleanup
  disconnect_profile precondition
  guest_exec python3 "${METRICS_TOOL}" assert-gone --identity "${GUEST_PRECONDITION_IDENTITY}"
  sleep "${SOAK_CLEANUP_SETTLE_SECONDS}"
  assert_clean_runtime precondition-cleanup "${GUEST_PRECONDITION_MANIFEST}"
  [[ "$(main_pid)" == "${daemon}" ]] || return 1

  WARMED_DAEMON_PID="${daemon}"
  capture_daemon_boundary "${GUEST_DAEMON_BASELINE_IDENTITY}" "${GUEST_BASELINE_BOUNDARY}" inactive-baseline 0
}

run_active_measurement() {
  local index=0 elapsed=0 started
  mark_failure product measured.attribution
  assert_same_daemon
  capture_active_authority measured "${GUEST_SESSION_ONE_MANIFEST}"
  discover_active_identity "${GUEST_SESSION_ONE_IDENTITY}"
  guest_exec python3 "${METRICS_TOOL}" assert-replaced \
    --before "${GUEST_PRECONDITION_IDENTITY}" \
    --after "${GUEST_SESSION_ONE_IDENTITY}"

  mark_failure diagnostic_unknown measured.warmup
  sleep "${SOAK_WARMUP_SECONDS}"
  assert_privacy_envelope_present
  assert_foreign_sentinel

  : >"${ACTIVE_SAMPLES_PRIVATE}"
  started="${SECONDS}"
  sample_active "${GUEST_SESSION_ONE_IDENTITY}" "${GUEST_ACTIVE_SAMPLES}" active 1 0 0

  mark_failure product measured.active
  while ((elapsed < SOAK_DURATION_SECONDS)); do
    wait_for_verified_cli_status active
    assert_privacy_envelope_present
    assert_foreign_sentinel
    run_bounded_traffic
    if ((index % SOAK_DOCTOR_EVERY_SAMPLES == 0)); then
      run_bounded_doctor active
    fi
    sleep "${SOAK_SAMPLE_INTERVAL_SECONDS}"
    elapsed=$((SECONDS - started))
    index=$((index + 1))
    sample_active "${GUEST_SESSION_ONE_IDENTITY}" "${GUEST_ACTIVE_SAMPLES}" active 1 "${index}" "${elapsed}"
  done
}

capture_first_cleanup_boundary() {
  mark_failure product measured.cleanup
  guest_exec python3 "${METRICS_TOOL}" assert-gone --identity "${GUEST_SESSION_ONE_IDENTITY}"
  assert_same_daemon
  capture_daemon_boundary "${GUEST_DAEMON_CLEANUP_IDENTITY}" "${GUEST_CLEANUP_BOUNDARY}" post-cleanup 0
  assert_foreign_sentinel
}

run_reconnect_measurement() {
  local index elapsed
  mark_failure product reconnect.connect
  connect_profile reconnect
  assert_same_daemon
  capture_active_authority reconnect "${GUEST_SESSION_TWO_MANIFEST}"
  discover_active_identity "${GUEST_SESSION_TWO_IDENTITY}"
  guest_exec python3 "${METRICS_TOOL}" assert-replaced \
    --before "${GUEST_SESSION_ONE_IDENTITY}" \
    --after "${GUEST_SESSION_TWO_IDENTITY}"

  mark_failure diagnostic_unknown reconnect.warmup
  sleep "${SOAK_RECONNECT_WARMUP_SECONDS}"

  mark_failure product reconnect.sampling
  for index in $(seq 0 $((SOAK_RECONNECT_SAMPLES - 1))); do
    elapsed=$((index * SOAK_SAMPLE_INTERVAL_SECONDS))
    sample_active "${GUEST_SESSION_TWO_IDENTITY}" "${GUEST_RECONNECT_SAMPLES}" reconnect 2 "${index}" "${elapsed}"
    wait_for_verified_cli_status reconnect
    assert_privacy_envelope_present
    assert_foreign_sentinel
    run_bounded_traffic
    if ((index + 1 < SOAK_RECONNECT_SAMPLES)); then
      sleep "${SOAK_SAMPLE_INTERVAL_SECONDS}"
    fi
  done

  mark_failure product reconnect.cleanup
  disconnect_profile reconnect
  guest_exec python3 "${METRICS_TOOL}" assert-gone --identity "${GUEST_SESSION_TWO_IDENTITY}"
  sleep "${SOAK_CLEANUP_SETTLE_SECONDS}"
  assert_clean_runtime reconnect-cleanup "${GUEST_SESSION_TWO_MANIFEST}"
  assert_same_daemon
  capture_daemon_boundary "${GUEST_DAEMON_RECONNECT_CLEANUP_IDENTITY}" "${GUEST_RECONNECT_CLEANUP_BOUNDARY}" post-cleanup 1
  PRODUCT_CLEANUP=pass

  mark_failure diagnostic_unknown reconnect.ordinary_connectivity
  guest_exec resolvectl flush-caches >/dev/null
  guest_exec timeout 20 getent ahostsv4 github.com >/dev/null
  guest_exec timeout 30 curl -4 -fsS --max-time 10 -o /dev/null https://api.ipify.org
  ORDINARY_CONNECTIVITY=pass
}

write_configuration() {
  python3 - "${CONFIGURATION_PRIVATE}" \
    "${SOAK_DURATION_SECONDS}" "${SOAK_PRECONDITION_WARMUP_SECONDS}" "${SOAK_WARMUP_SECONDS}" \
    "${SOAK_SAMPLE_INTERVAL_SECONDS}" "${SOAK_DOCTOR_EVERY_SAMPLES}" "${DOCTOR_RUNS}" "${DOCTOR_UNHEALTHY_RUNS}" \
    "${SOAK_RECONNECT_WARMUP_SECONDS}" "${SOAK_RECONNECT_SAMPLES}" "${SOAK_CLEANUP_SETTLE_SECONDS}" \
    "${TUN_DIAGNOSTIC_TIMEOUT_SECONDS}" "${TUN_HEALTH_TIMEOUT_SECONDS}" "${TUN_HEALTH_POLL_SECONDS}" \
    "${TUN_STATUS_TIMEOUT_SECONDS}" "${SOAK_CLEANUP_ATTEMPTS}" "${SOAK_CLEANUP_RETRY_SECONDS}" <<'PY'
import json, os, sys
path = sys.argv[1]
keys = (
    "duration_seconds", "precondition_warmup_seconds", "warmup_seconds",
    "sample_interval_seconds", "doctor_every_samples", "doctor_runs",
    "doctor_unhealthy_runs", "reconnect_warmup_seconds", "reconnect_samples",
    "cleanup_settle_seconds", "tun_diagnostic_timeout_seconds",
    "tun_health_timeout_seconds", "tun_health_poll_seconds",
    "tun_status_timeout_seconds", "cleanup_attempts", "cleanup_retry_seconds",
)
payload = {key: int(value) for key, value in zip(keys, sys.argv[2:], strict=True)}
with open(path, "w", encoding="utf-8") as handle:
    json.dump(payload, handle, sort_keys=True)
    handle.write("\n")
os.chmod(path, 0o600)
PY
}

write_provenance() {
  local version_file="${PRIVATE_ROOT}/version.txt" xray_version xray_artifact xray_binary
  local kernel systemd package_hash arch os_id os_version
  guest_exec /usr/bin/podlaz version >"${version_file}"
  # shellcheck disable=SC1091
  . "${REPO_ROOT}/packaging/runtime-helpers.env"
  xray_version="${XRAY_VERSION}"
  xray_artifact="${XRAY_AMD64_SHA256}"
  xray_binary="$(guest_exec sha256sum /usr/lib/podlaz/xray | awk '{print $1}')"
  kernel="$(guest_exec uname -r | tr -d '[:space:]')"
  systemd="$(guest_exec /bin/bash -lc "systemctl --version | awk 'NR == 1 {print \\\$2; exit}'" | tr -d '[:space:]')"
  package_hash="$(sha256sum "${CANDIDATE_DEB}" | awk '{print $1}')"
  arch="$(dpkg-deb --field "${CANDIDATE_DEB}" Architecture)"
  os_id="$(guest_exec /bin/bash -lc ". /etc/os-release; printf '%s' \"\\\$ID\"")"
  os_version="$(guest_exec /bin/bash -lc ". /etc/os-release; printf '%s' \"\\\$VERSION_ID\"")"

  python3 - "${version_file}" "${PROVENANCE_PRIVATE}" "${xray_version}" "${xray_artifact}" \
    "${xray_binary}" "${kernel}" "${systemd}" "${package_hash}" "${arch}" "${os_id}" "${os_version}" <<'PY'
import json, os, re, sys
version_file, target, xray_version, xray_artifact, xray_binary, kernel, systemd, package, architecture, os_id, os_version = sys.argv[1:]
lines = [line.rstrip("\n") for line in open(version_file, encoding="utf-8")]
if len(lines) < 2:
    raise SystemExit("installed version output is incomplete")
version_match = re.fullmatch(r"podlaz version ([A-Za-z0-9.+~_-]+)", lines[0])
commit_match = re.fullmatch(r"commit: ([0-9a-f]{7,64})", lines[1])
if version_match is None or commit_match is None:
    raise SystemExit("installed version output is malformed")
payload = {
    "podlaz_version": version_match.group(1),
    "podlaz_commit": commit_match.group(1),
    "xray_version": xray_version,
    "xray_artifact_sha256": xray_artifact,
    "xray_binary_sha256": xray_binary,
    "kernel_release": kernel,
    "systemd_version": systemd,
    "package_sha256": package,
    "package_architecture": architecture,
    "runtime_os": {"id": os_id, "version_id": os_version},
}
with open(target, "w", encoding="utf-8") as handle:
    json.dump(payload, handle, sort_keys=True)
    handle.write("\n")
os.chmod(target, 0o600)
PY
}

build_resource_report() {
  local code expected_policy actual_policy
  copy_guest_file "${GUEST_ACTIVE_SAMPLES}" "${ACTIVE_SAMPLES_PRIVATE}"
  copy_guest_file "${GUEST_RECONNECT_SAMPLES}" "${RECONNECT_SAMPLES_PRIVATE}"
  copy_guest_file "${GUEST_BASELINE_BOUNDARY}" "${BASELINE_BOUNDARY_PRIVATE}"
  copy_guest_file "${GUEST_CLEANUP_BOUNDARY}" "${CLEANUP_BOUNDARY_PRIVATE}"
  copy_guest_file "${GUEST_RECONNECT_CLEANUP_BOUNDARY}" "${RECONNECT_CLEANUP_BOUNDARY_PRIVATE}"
  write_configuration
  write_provenance

  expected_policy="$(git -C "${REPO_ROOT}" show "HEAD:scripts/e2e/tun-resource-soak-policy.json" | sha256sum | awk '{print $1}')"
  actual_policy="$(sha256sum "${SCRIPT_DIR}/tun-resource-soak-policy.json" | awk '{print $1}')"
  [[ "${expected_policy}" == "${actual_policy}" ]] || return 1

  set +e
  python3 "${SCRIPT_DIR}/lib/tun_soak_metrics.py" report \
    --samples "${ACTIVE_SAMPLES_PRIVATE}" \
    --reconnect-samples "${RECONNECT_SAMPLES_PRIVATE}" \
    --baseline-boundary "${BASELINE_BOUNDARY_PRIVATE}" \
    --cleanup-boundary "${CLEANUP_BOUNDARY_PRIVATE}" \
    --reconnect-cleanup-boundary "${RECONNECT_CLEANUP_BOUNDARY_PRIVATE}" \
    --provenance "${PROVENANCE_PRIVATE}" \
    --configuration "${CONFIGURATION_PRIVATE}" \
    --policy "${SCRIPT_DIR}/tun-resource-soak-policy.json" \
    --output "${PUBLIC_REPORT}"
  code=$?
  set -e
  chmod 0644 "${PUBLIC_REPORT}"
  ((code == 0)) || return 1
  POLICY_RESULT=pass
}

validate_resource_report() {
  python3 - "${PUBLIC_REPORT}" "${EXPECTED_COMMIT}" "${SOAK_DURATION_SECONDS}" "${SOAK_WARMUP_SECONDS}" \
    "${SOAK_SAMPLE_INTERVAL_SECONDS}" "${SOAK_RECONNECT_SAMPLES}" <<'PY'
import json, sys
path, expected_commit, duration, warmup, interval, reconnect_samples = sys.argv[1:]
with open(path, encoding="utf-8") as handle:
    report = json.load(handle)
if report.get("schema_version") != 1 or report.get("ok") is not True:
    raise SystemExit("resource soak report is not successful")
if report.get("verdict") != "observation_complete":
    raise SystemExit("checked-in observe policy did not complete")
policy = report.get("policy") or {}
if policy.get("mode") != "observe" or policy.get("evaluated") is not False or policy.get("violations") != {}:
    raise SystemExit("resource soak policy result changed semantics")
configuration = report.get("configuration") or {}
expected = {
    "duration_seconds": int(duration),
    "warmup_seconds": int(warmup),
    "sample_interval_seconds": int(interval),
    "reconnect_samples": int(reconnect_samples),
}
for key, value in expected.items():
    if configuration.get(key) != value:
        raise SystemExit(f"resource soak configuration mismatch: {key}")
if report.get("provenance", {}).get("podlaz_commit") != expected_commit:
    raise SystemExit("resource soak candidate commit mismatch")
lifecycle = report.get("lifecycle") or {}
if lifecycle.get("cleanup", {}).get("ok") is not True or lifecycle.get("reconnect", {}).get("ok") is not True:
    raise SystemExit("resource soak lifecycle threshold failed")
trend = report.get("trend") or {}
if trend.get("observed_duration_seconds", 0) < int(duration):
    raise SystemExit("resource soak measurement window is shorter than required")
PY
}

inherit_base_failure() {
  local class step
  [[ -f "${BASE_REPORT}" ]] || return 0
  class="$(awk -F= '$1 == "failure.class" {print $2; exit}' "${BASE_REPORT}" 2>/dev/null || true)"
  step="$(awk -F= '$1 == "failure.step" {print $2; exit}' "${BASE_REPORT}" 2>/dev/null || true)"
  if [[ "${FAILURE_CLASS}" == diagnostic_unknown ]]; then
    case "${class}" in
      product|fixture|infrastructure|capability|diagnostic_unknown) FAILURE_CLASS="${class}" ;;
    esac
    [[ -n "${step}" && "${step}" != none ]] && FAILURE_STEP="base.${step}"
  fi
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

wait_for_control_ready() {
  local phase="$1" ready="${CONTROL_DIR}/$1.ready" attempt code
  for attempt in $(seq 1 9000); do
    [[ -f "${ready}" && ! -L "${ready}" ]] && return 0
    if [[ -z "${BASE_PID}" ]] || ! kill -0 "${BASE_PID}" >/dev/null 2>&1; then
      if [[ -n "${BASE_PID}" ]]; then
        set +e
        wait "${BASE_PID}"
        code=$?
        set -e
        BASE_EXIT_CODE="${code}"
        BASE_PID=""
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
  install -d -m 0700 "${PRIVATE_ROOT}" "${BASE_TMP_ROOT}" "${BASE_ARTIFACT_DIR}" "${CONTROL_DIR}" "${E2E_ARTIFACT_DIR}"
  env \
    E2E_TMP_ROOT="${BASE_TMP_ROOT}" \
    E2E_ARTIFACT_DIR="${BASE_ARTIFACT_DIR}" \
    PODLAZ_E2E_CANDIDATE_COMMIT="${EXPECTED_COMMIT}" \
    PODLAZ_E2E_HOSTED_CONTROL_DIR="${CONTROL_DIR}" \
    PODLAZ_E2E_HOSTED_CONTROL_PHASES="candidate-ready verified-active terminal-clean" \
    PODLAZ_E2E_HOSTED_CONTROL_TIMEOUT_SECONDS=15600 \
    bash "${BASE_SCENARIO}" "${candidate}" >"${BASE_STDOUT}" 2>"${BASE_STDERR}" &
  BASE_PID=$!
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
  if ((code != 0)); then
    inherit_base_failure
    return 1
  fi
}

validate_base_positive_control() {
  env E2E_TMP_ROOT="${BASE_TMP_ROOT}" E2E_ARTIFACT_DIR="${BASE_ARTIFACT_DIR}" \
    bash "${BASE_SCENARIO}" validate-report >/dev/null
  grep -Fx 'candidate.provenance=pass' "${BASE_REPORT}" >/dev/null
  grep -Fx 'tun.terminal_cleanup=pass' "${BASE_REPORT}" >/dev/null
  grep -Fx 'tun.recovery_clean=pass' "${BASE_REPORT}" >/dev/null
  grep -Fx 'guest.baseline_restored=pass' "${BASE_REPORT}" >/dev/null
  grep -Fx 'outer.cleanup=pass' "${BASE_REPORT}" >/dev/null
  BASE_POSITIVE_CONTROL=pass
}

assert_public_privacy_and_size() {
  local bytes
  [[ -f "${STATUS_REPORT}" && ! -L "${STATUS_REPORT}" ]] || return 1
  if [[ -f "${PUBLIC_REPORT}" ]]; then
    [[ ! -L "${PUBLIC_REPORT}" ]] || return 1
  fi
  if grep -REiq 'vless://|vmess://|trojan://|ss://|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|172[.]31[.](253|254)[.]' "${E2E_ARTIFACT_DIR}"; then
    return 1
  fi
  bytes="$(du -sb "${E2E_ARTIFACT_DIR}" | awk '{print $1}')"
  [[ "${bytes}" =~ ^[0-9]+$ ]] && ((bytes <= MAX_PUBLIC_BYTES))
}

write_status_report() {
  local code="$1" outcome=fail elapsed
  ((code == 0)) && outcome=pass
  elapsed=$((SECONDS - RUN_STARTED_SECONDS))
  cat >"${STATUS_REPORT}" <<EOF
schema_version=1
outcome=${outcome}
failure.class=${FAILURE_CLASS}
failure.step=${FAILURE_STEP}
measurement.duration_seconds=${SOAK_DURATION_SECONDS}
measurement.warmup_seconds=${SOAK_WARMUP_SECONDS}
measurement.sample_interval_seconds=${SOAK_SAMPLE_INTERVAL_SECONDS}
measurement.reconnect_samples=${SOAK_RECONNECT_SAMPLES}
policy.result=${POLICY_RESULT}
product.cleanup=${PRODUCT_CLEANUP}
ordinary.connectivity=${ORDINARY_CONNECTIVITY}
base.positive_control=${BASE_POSITIVE_CONTROL}
budget.expected_runtime_minutes=${EXPECTED_RUNTIME_MINUTES}
budget.job_timeout_minutes=${JOB_TIMEOUT_MINUTES}
budget.max_private_bytes=${MAX_PRIVATE_BYTES}
budget.max_public_bytes=${MAX_PUBLIC_BYTES}
observed.runtime_seconds=${elapsed}
observed.private_bytes=${OBSERVED_PRIVATE_BYTES}
observed.host_memory_bytes=${HOST_MEMORY_BYTES}
observed.initial_free_disk_bytes=${HOST_FREE_DISK_BYTES}
EOF
  chmod 0644 "${STATUS_REPORT}"
}

cleanup() {
  local code=$? attempt
  trap - EXIT INT TERM
  set +e
  release_all_controls
  if [[ -n "${BASE_PID}" ]] && kill -0 "${BASE_PID}" >/dev/null 2>&1; then
    for attempt in $(seq 1 600); do
      kill -0 "${BASE_PID}" >/dev/null 2>&1 || break
      sleep 0.5
    done
    if kill -0 "${BASE_PID}" >/dev/null 2>&1; then
      kill -TERM "${BASE_PID}" >/dev/null 2>&1 || true
      sleep 1
    fi
    wait "${BASE_PID}" >/dev/null 2>&1 || true
  fi
  inherit_base_failure
  if [[ -d "${PRIVATE_ROOT}" ]]; then
    OBSERVED_PRIVATE_BYTES="$(du -sb "${PRIVATE_ROOT}" 2>/dev/null | awk '{print $1}' || printf '0')"
  fi
  if [[ ! "${OBSERVED_PRIVATE_BYTES}" =~ ^[0-9]+$ ]]; then OBSERVED_PRIVATE_BYTES=0; fi
  if ((OBSERVED_PRIVATE_BYTES > MAX_PRIVATE_BYTES)); then
    code=1
    FAILURE_CLASS=infrastructure
    FAILURE_STEP=budget.private_disk
  fi
  if ((code == 0)); then
    FAILURE_CLASS=none
    FAILURE_STEP=none
  fi
  write_status_report "${code}"
  if ! assert_public_privacy_and_size; then
    code=1
    FAILURE_CLASS=fixture
    FAILURE_STEP=artifact.privacy_or_size
    write_status_report "${code}"
  fi
  set -e
  exit "${code}"
}

validate_public_evidence() {
  [[ -f "${STATUS_REPORT}" && ! -L "${STATUS_REPORT}" ]] || fail "hosted resource soak status report is missing"
  python3 - "${STATUS_REPORT}" "${PUBLIC_REPORT}" <<'PY'
import json, re, sys
from pathlib import Path
status_path, report_path = map(Path, sys.argv[1:])
values = {}
for raw in status_path.read_text(encoding="utf-8").splitlines():
    if not raw or "=" not in raw:
        raise SystemExit("invalid status report line")
    key, value = raw.split("=", 1)
    if key in values:
        raise SystemExit("duplicate status report key")
    values[key] = value
required = {
    "schema_version", "outcome", "failure.class", "failure.step",
    "measurement.duration_seconds", "measurement.warmup_seconds",
    "measurement.sample_interval_seconds", "measurement.reconnect_samples",
    "policy.result", "product.cleanup", "ordinary.connectivity",
    "base.positive_control", "budget.expected_runtime_minutes",
    "budget.job_timeout_minutes", "budget.max_private_bytes",
    "budget.max_public_bytes", "observed.runtime_seconds",
    "observed.private_bytes", "observed.host_memory_bytes",
    "observed.initial_free_disk_bytes",
}
if set(values) != required or values["schema_version"] != "1":
    raise SystemExit("unexpected status report schema")
if values["outcome"] not in {"pass", "fail"}:
    raise SystemExit("invalid status outcome")
if values["failure.class"] not in {"none", "product", "fixture", "infrastructure", "capability", "diagnostic_unknown"}:
    raise SystemExit("invalid failure class")
if not re.fullmatch(r"[A-Za-z0-9_.-]+", values["failure.step"]):
    raise SystemExit("invalid failure step")
for key in required:
    if key.startswith(("measurement.", "budget.", "observed.")) and not values[key].isdigit():
        raise SystemExit(f"non-numeric bounded evidence: {key}")
if values["outcome"] == "pass":
    for key in ("policy.result", "product.cleanup", "ordinary.connectivity", "base.positive_control"):
        if values[key] != "pass":
            raise SystemExit(f"successful run lacks required evidence: {key}")
    if values["failure.class"] != "none" or values["failure.step"] != "none":
        raise SystemExit("successful run reports a failure")
    if not report_path.is_file() or report_path.is_symlink():
        raise SystemExit("successful run lacks resource report")
    report = json.loads(report_path.read_text(encoding="utf-8"))
    if report.get("ok") is not True:
        raise SystemExit("successful status has failed resource report")
else:
    if values["failure.class"] == "none" or values["failure.step"] == "none":
        raise SystemExit("failed run lacks classified failure")
PY
  assert_public_privacy_and_size
}

run_scenario() {
  local candidate="$1"

  HOST_MEMORY_BYTES="$(awk '/^MemTotal:/ {print $2 * 1024; exit}' /proc/meminfo | awk '{printf "%.0f\n", $1}')"
  HOST_FREE_DISK_BYTES="$(df --output=avail -B1 "${E2E_TMP_ROOT}" | awk 'NR == 2 {print $1}')"

  mark_failure infrastructure base.candidate_ready
  run_base_scenario "${candidate}"
  wait_for_control_ready candidate-ready || fail "base synthetic TUN did not reach candidate-ready"
  grep -Fx 'candidate.provenance=pass' "${BASE_REPORT}" >/dev/null || fail "candidate provenance did not pass"
  guest_exec install -d -m 0700 "${GUEST_PRIVATE}"
  guest_exec rm -f "${GUEST_ACTIVE_SAMPLES}" "${GUEST_RECONNECT_SAMPLES}"

  precondition_warmed_baseline

  mark_failure infrastructure base.measured_connect
  release_control candidate-ready || fail "could not release candidate-ready"
  wait_for_control_ready verified-active || fail "base synthetic TUN did not reach verified-active"
  grep -Fx 'tun.verified_active=pass' "${BASE_REPORT}" >/dev/null || fail "base active authority did not pass"

  run_active_measurement

  mark_failure infrastructure base.measured_cleanup
  release_control verified-active || fail "could not release verified-active"
  wait_for_control_ready terminal-clean || fail "base synthetic TUN did not reach terminal-clean"
  grep -Fx 'tun.terminal_cleanup=pass' "${BASE_REPORT}" >/dev/null || fail "base terminal cleanup did not pass"
  capture_first_cleanup_boundary

  run_reconnect_measurement

  mark_failure product resource.policy
  build_resource_report || fail "resource soak policy or lifecycle thresholds failed"
  validate_resource_report || fail "resource soak report validation failed"

  mark_failure infrastructure base.finalize
  release_control terminal-clean || fail "could not release terminal-clean"
  wait_base_completion || fail "base synthetic TUN scenario failed"
  validate_base_positive_control || fail "base positive control report is not clean"

  OBSERVED_PRIVATE_BYTES="$(du -sb "${PRIVATE_ROOT}" | awk '{print $1}')"
  ((OBSERVED_PRIVATE_BYTES <= MAX_PRIVATE_BYTES)) || fail "resource soak private working set exceeded budget"
  FAILURE_CLASS=none
  FAILURE_STEP=none
}

main() {
  (($# == 1)) || fail "usage: $0 CANDIDATE.deb"
  require_cmd awk bash chmod curl df dpkg-deb du git grep install jq kill python3 sha256sum sleep sudo systemd-run timeout tr
  [[ -f "$1" && ! -L "$1" ]] || fail "candidate package must be a regular file"
  [[ "${EXPECTED_COMMIT}" =~ ^[0-9a-fA-F]{40}$ ]] || fail "candidate commit must be exact 40-hex"
  CANDIDATE_DEB="$(readlink -f -- "$1")"
  install -d -m 0700 "${E2E_TMP_ROOT}" "${E2E_ARTIFACT_DIR}"
  rm -f "${PUBLIC_REPORT}" "${STATUS_REPORT}"
  trap cleanup EXIT INT TERM
  run_scenario "${CANDIDATE_DEB}"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  if [[ "${1:-}" == validate-report ]]; then
    validate_public_evidence
    exit 0
  fi
  main "$@"
fi
