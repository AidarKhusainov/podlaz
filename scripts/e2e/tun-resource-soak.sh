#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"
# shellcheck source=lib/tun_package_assertions.sh
source "${SCRIPT_DIR}/lib/tun_package_assertions.sh"
# shellcheck source=lib/tun_soak_health.sh
source "${SCRIPT_DIR}/lib/tun_soak_health.sh"
# shellcheck source=lib/tun_soak_cleanup.sh
source "${SCRIPT_DIR}/lib/tun_soak_cleanup.sh"

require_cmd apt awk bash cat cmp curl date dpkg dpkg-deb find getent git go grep hostname id install ip mktemp nmcli python3 readlink resolvectl runuser sed seq sha256sum sleep sort sudo systemctl timeout tr uname

CANONICAL_DNS_CHECK_HOST="github.com"
CANONICAL_PUBLIC_IP_CHECK_URL="https://api.ipify.org"
CANONICAL_SOAK_POLICY_FILE="${SCRIPT_DIR}/tun-resource-soak-policy.json"
CANONICAL_SOAK_POLICY_REPOSITORY_PATH="scripts/e2e/tun-resource-soak-policy.json"
CANONICAL_TRUSTED_HOST_FILE="/etc/podlaz-e2e/tun-resource-soak-trusted-host.json"

: "${PODLAZ_E2E_PROFILE_URI:=}"
: "${PODLAZ_E2E_PROFILE_URI_LIST:=}"
: "${PODLAZ_E2E_DNS_CHECK_HOST:=${CANONICAL_DNS_CHECK_HOST}}"
: "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL:=${CANONICAL_PUBLIC_IP_CHECK_URL}}"
: "${PODLAZ_DEB_ARCH:=$(dpkg --print-architecture)}"
: "${PODLAZ_E2E_SOAK_DURATION_SECONDS:=10800}"
: "${PODLAZ_E2E_SOAK_PRECONDITION_WARMUP_SECONDS:=30}"
: "${PODLAZ_E2E_SOAK_WARMUP_SECONDS:=120}"
: "${PODLAZ_E2E_SOAK_SAMPLE_INTERVAL_SECONDS:=60}"
: "${PODLAZ_E2E_SOAK_DOCTOR_EVERY_SAMPLES:=10}"
: "${PODLAZ_E2E_SOAK_RECONNECT_WARMUP_SECONDS:=120}"
: "${PODLAZ_E2E_SOAK_RECONNECT_SAMPLES:=3}"
: "${PODLAZ_E2E_SOAK_CLEANUP_SETTLE_SECONDS:=10}"
: "${PODLAZ_E2E_SOAK_POLICY_FILE:=${CANONICAL_SOAK_POLICY_FILE}}"
: "${PODLAZ_E2E_SOAK_TRUSTED_HOST_FILE:=${CANONICAL_TRUSTED_HOST_FILE}}"
: "${PODLAZ_E2E_TUN_HEALTH_TIMEOUT_SECONDS:=75}"
: "${PODLAZ_E2E_TUN_HEALTH_POLL_SECONDS:=1}"
: "${PODLAZ_E2E_TUN_STATUS_TIMEOUT_SECONDS:=10}"
: "${PODLAZ_E2E_TUN_DIAGNOSTIC_TIMEOUT_SECONDS:=90}"
: "${PODLAZ_E2E_SOAK_CLEANUP_ATTEMPTS:=2}"
: "${PODLAZ_E2E_SOAK_CLEANUP_RETRY_SECONDS:=2}"

if [[ -z "${PODLAZ_E2E_PROFILE_URI}" && -z "${PODLAZ_E2E_PROFILE_URI_LIST}" ]]; then
  fail "PODLAZ_E2E_PROFILE_URI or PODLAZ_E2E_PROFILE_URI_LIST is required"
fi
if [[ "${PODLAZ_DEB_ARCH}" != "$(dpkg --print-architecture)" ]]; then
  fail "TUN resource soak requires a native .deb"
fi

validate_positive_integer() {
  local name="$1" value="$2"
  [[ "${value}" =~ ^[1-9][0-9]*$ ]] || fail "${name} must be a positive integer"
}

for numeric_setting in \
  PODLAZ_E2E_SOAK_DURATION_SECONDS \
  PODLAZ_E2E_SOAK_PRECONDITION_WARMUP_SECONDS \
  PODLAZ_E2E_SOAK_WARMUP_SECONDS \
  PODLAZ_E2E_SOAK_SAMPLE_INTERVAL_SECONDS \
  PODLAZ_E2E_SOAK_DOCTOR_EVERY_SAMPLES \
  PODLAZ_E2E_SOAK_RECONNECT_WARMUP_SECONDS \
  PODLAZ_E2E_SOAK_RECONNECT_SAMPLES \
  PODLAZ_E2E_SOAK_CLEANUP_SETTLE_SECONDS \
  PODLAZ_E2E_TUN_HEALTH_TIMEOUT_SECONDS \
  PODLAZ_E2E_TUN_HEALTH_POLL_SECONDS \
  PODLAZ_E2E_TUN_STATUS_TIMEOUT_SECONDS \
  PODLAZ_E2E_TUN_DIAGNOSTIC_TIMEOUT_SECONDS \
  PODLAZ_E2E_SOAK_CLEANUP_ATTEMPTS; do
  validate_positive_integer "${numeric_setting}" "${!numeric_setting}"
done
[[ "${PODLAZ_E2E_SOAK_CLEANUP_RETRY_SECONDS}" =~ ^[0-9]+$ ]] || \
  fail "PODLAZ_E2E_SOAK_CLEANUP_RETRY_SECONDS must be a non-negative integer"
if ((PODLAZ_E2E_SOAK_DURATION_SECONDS < PODLAZ_E2E_SOAK_SAMPLE_INTERVAL_SECONDS * 5)); then
  fail "soak duration must produce at least six post-warm-up samples"
fi
if ((PODLAZ_E2E_SOAK_DURATION_SECONDS > 14400)); then
  fail "soak duration exceeds the bounded four-hour harness limit"
fi
[[ -f "${PODLAZ_E2E_SOAK_POLICY_FILE}" ]] || fail "soak policy file is missing"
sudo -n test -f "${PODLAZ_E2E_SOAK_TRUSTED_HOST_FILE}" || fail "trusted host fingerprint is missing"

DEV_DEB="dist/podlaz_0.0.0~dev-1_linux_${PODLAZ_DEB_ARCH}.deb"
DAEMON_SOCKET="/run/podlaz/podlazd.sock"
TRANSACTION_DIR="/run/podlaz/transactions"
METRICS_TOOL="${SCRIPT_DIR}/lib/tun_soak_metrics.py"
TUN_SOAK_STATUS_TOOL="${SCRIPT_DIR}/lib/tun_soak_status.py"
NETWORK_HELPER="${SCRIPT_DIR}/tun-package-fallback-network.py"
ISOLATION_TOOL="${SCRIPT_DIR}/lib/tun_soak_isolation.py"
ENVIRONMENT_TOOL="${SCRIPT_DIR}/lib/tun_soak_environment.py"

setup_isolated_xdg "tun-resource-soak"
SOAK_PRIVATE_DIR="${E2E_TMP_ROOT}/tun-resource-soak-private"
install -d -m 0700 "${SOAK_PRIVATE_DIR}"
SOAK_POLICY_SNAPSHOT="${SOAK_PRIVATE_DIR}/soak-policy.json"
install -m 0600 "${PODLAZ_E2E_SOAK_POLICY_FILE}" "${SOAK_POLICY_SNAPSHOT}"
SOAK_POLICY_SHA256="$(sha256sum "${SOAK_POLICY_SNAPSHOT}" | awk '{print $1}')"
SOAK_POLICY_MODE="$(python3 - "${SOAK_POLICY_SNAPSHOT}" <<'PY'
import json
import sys
from pathlib import Path

try:
    payload = json.loads(Path(sys.argv[1]).read_text(encoding="utf-8"))
except (OSError, json.JSONDecodeError) as exc:
    raise SystemExit("invalid soak policy") from exc
if payload.get("schema_version") != 2 or payload.get("mode") not in {"observe", "accept"}:
    raise SystemExit("unsupported soak policy")
print(payload["mode"])
PY
)" || fail "soak policy preflight failed"
ACTIVE_SAMPLES="${E2E_ARTIFACT_DIR}/tun-resource-active-samples.ndjson"
RECONNECT_SAMPLES="${E2E_ARTIFACT_DIR}/tun-resource-reconnect-samples.ndjson"
BASELINE_BOUNDARY="${E2E_ARTIFACT_DIR}/tun-resource-warmed-inactive-baseline.json"
CLEANUP_BOUNDARY="${E2E_ARTIFACT_DIR}/tun-resource-post-cleanup.json"
RECONNECT_CLEANUP_BOUNDARY="${E2E_ARTIFACT_DIR}/tun-resource-post-reconnect-cleanup.json"
PUBLIC_REPORT="${E2E_ARTIFACT_DIR}/tun-resource-soak-report.json"
FAILURE_REPORT="${E2E_ARTIFACT_DIR}/tun-resource-failure.json"
PROVENANCE_JSON="${SOAK_PRIVATE_DIR}/provenance.json"
CONFIGURATION_JSON="${SOAK_PRIVATE_DIR}/configuration.json"
RUNTIME_OS_JSON="${SOAK_PRIVATE_DIR}/runtime-os.json"
PACKAGE_BUILD_LOG="${SOAK_PRIVATE_DIR}/package-build.log"
PACKAGE_INSTALL_LOG="${SOAK_PRIVATE_DIR}/package-install.log"
PACKAGE_REINSTALL_LOG="${SOAK_PRIVATE_DIR}/package-reinstall.log"
DAEMON_BASELINE_IDENTITY="${SOAK_PRIVATE_DIR}/daemon-warmed-baseline.json"
NETWORK_ISOLATION_BASELINE="${SOAK_PRIVATE_DIR}/network-isolation-baseline.json"
PRECONDITION_IDENTITY="${SOAK_PRIVATE_DIR}/precondition-session.json"
PRECONDITION_NETWORK_MANIFEST="${SOAK_PRIVATE_DIR}/precondition-network.json"
DAEMON_CLEANUP_IDENTITY="${SOAK_PRIVATE_DIR}/daemon-cleanup.json"
DAEMON_RECONNECT_CLEANUP_IDENTITY="${SOAK_PRIVATE_DIR}/daemon-reconnect-cleanup.json"
SESSION_ONE_IDENTITY="${SOAK_PRIVATE_DIR}/session-one.json"
SESSION_TWO_IDENTITY="${SOAK_PRIVATE_DIR}/session-two.json"
SESSION_ONE_NETWORK_MANIFEST="${SOAK_PRIVATE_DIR}/session-one-network.json"
SESSION_TWO_NETWORK_MANIFEST="${SOAK_PRIVATE_DIR}/session-two-network.json"
PRECONNECT_NETWORK_MANIFEST="${SOAK_PRIVATE_DIR}/preconnect-network.json"
PROFILE_ID=""
HOST_SENSITIVE_VALUES=""
BUILD_COMMIT=""
SOAK_STARTED_SECONDS=0
SOAK_PHASE="initialization"
SOAK_COMMAND_EXIT=""
SOAK_COMMAND_CLASSIFICATION=""
SOAK_STATUS_VERDICT=""
DOCTOR_RUNS=0
DOCTOR_UNHEALTHY_RUNS=0
WARMED_DAEMON_PID=""

append_sensitive_value() {
  local value="${1:-}"
  [[ -n "${value}" ]] || return 0
  HOST_SENSITIVE_VALUES+="${value}"$'\n'
}

enforce_acceptance_inputs() {
  [[ "${SOAK_POLICY_MODE}" == "accept" ]] || return 0
  [[ "$(readlink -f "${PODLAZ_E2E_SOAK_POLICY_FILE}")" == "$(readlink -f "${CANONICAL_SOAK_POLICY_FILE}")" ]] ||
    fail "accept mode requires the checked-in soak policy"
  local checked_in_policy_sha256
  checked_in_policy_sha256="$(git show "HEAD:${CANONICAL_SOAK_POLICY_REPOSITORY_PATH}" | sha256sum | awk '{print $1}')" ||
    fail "accept mode could not read the checked-in HEAD policy"
  [[ "${SOAK_POLICY_SHA256}" == "${checked_in_policy_sha256}" ]] ||
    fail "accept mode requires the exact checked-in HEAD policy"
  [[ "${PODLAZ_E2E_SOAK_TRUSTED_HOST_FILE}" == "${CANONICAL_TRUSTED_HOST_FILE}" ]] ||
    fail "accept mode requires the canonical trusted-host path"
  [[ "${PODLAZ_E2E_DNS_CHECK_HOST}" == "${CANONICAL_DNS_CHECK_HOST}" ]] ||
    fail "accept mode requires the reviewed DNS workload"
  [[ "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL}" == "${CANONICAL_PUBLIC_IP_CHECK_URL}" ]] ||
    fail "accept mode requires the reviewed HTTPS workload"

  python3 - "${SOAK_POLICY_SNAPSHOT}" \
    "${PODLAZ_E2E_SOAK_DURATION_SECONDS}" \
    "${PODLAZ_E2E_SOAK_PRECONDITION_WARMUP_SECONDS}" \
    "${PODLAZ_E2E_SOAK_WARMUP_SECONDS}" \
    "${PODLAZ_E2E_SOAK_SAMPLE_INTERVAL_SECONDS}" \
    "${PODLAZ_E2E_SOAK_DOCTOR_EVERY_SAMPLES}" \
    "${PODLAZ_E2E_SOAK_RECONNECT_WARMUP_SECONDS}" \
    "${PODLAZ_E2E_SOAK_RECONNECT_SAMPLES}" \
    "${PODLAZ_E2E_SOAK_CLEANUP_SETTLE_SECONDS}" \
    "${PODLAZ_E2E_TUN_DIAGNOSTIC_TIMEOUT_SECONDS}" \
    "${PODLAZ_E2E_TUN_HEALTH_TIMEOUT_SECONDS}" \
    "${PODLAZ_E2E_TUN_HEALTH_POLL_SECONDS}" \
    "${PODLAZ_E2E_TUN_STATUS_TIMEOUT_SECONDS}" \
    "${PODLAZ_E2E_SOAK_CLEANUP_ATTEMPTS}" \
    "${PODLAZ_E2E_SOAK_CLEANUP_RETRY_SECONDS}" <<'PY'
import json
import sys
from pathlib import Path

policy = json.loads(Path(sys.argv[1]).read_text(encoding="utf-8"))
expected = policy.get("acceptance_configuration")
keys = (
    "duration_seconds",
    "precondition_warmup_seconds",
    "warmup_seconds",
    "sample_interval_seconds",
    "doctor_every_samples",
    "reconnect_warmup_seconds",
    "reconnect_samples",
    "cleanup_settle_seconds",
    "tun_diagnostic_timeout_seconds",
    "tun_health_timeout_seconds",
    "tun_health_poll_seconds",
    "tun_status_timeout_seconds",
    "cleanup_attempts",
    "cleanup_retry_seconds",
)
actual = {key: int(value) for key, value in zip(keys, sys.argv[2:], strict=True)}
if expected != actual:
    raise SystemExit("accept mode configuration differs from the reviewed policy")
PY
}

verify_runtime_environment() {
  python3 "${ENVIRONMENT_TOOL}" verify-os --output "${RUNTIME_OS_JSON}" ||
    fail "runtime host is not Ubuntu 24.04"
}

collect_host_sensitive_values() {
  local values
  values="$({
    hostname -f 2>/dev/null || true
    ip -o -4 addr show scope global 2>/dev/null | awk '{split($4, value, "/"); print value[1]; print $2}'
    ip -o -6 addr show scope global 2>/dev/null | awk '{split($4, value, "/"); print value[1]; print $2}'
    ip -4 route show default 2>/dev/null | awk '{for (i=1; i<=NF; i++) {if ($i=="via" || $i=="dev") print $(i+1)}}'
    nmcli --terse --escape no --fields UUID connection show --active 2>/dev/null || true
    resolvectl status --no-pager 2>/dev/null | awk -F: '/Current DNS Server|DNS Servers|DNS Domain/ {gsub(/^[[:space:]]+/, "", $2); for (i=1; i<=split($2, values, /[[:space:]]+/); i++) print values[i]}'
  } | sed '/^[[:space:]]*$/d' | sort -u)"
  append_sensitive_value "${values}"
}

first_profile_uri() {
  if [[ -n "${PODLAZ_E2E_PROFILE_URI}" ]]; then
    printf '%s\n' "${PODLAZ_E2E_PROFILE_URI}"
    return
  fi
  while IFS= read -r uri; do
    [[ -n "${uri}" ]] || continue
    printf '%s\n' "${uri}"
    return
  done <<<"${PODLAZ_E2E_PROFILE_URI_LIST}"
}

wait_for_daemon_socket() {
  local attempt
  for attempt in $(seq 1 150); do
    [[ -S "${DAEMON_SOCKET}" ]] && return 0
    sleep 0.1
  done
  fail "podlazd.service did not create its socket"
}

run_installed_podlaz() {
  sudo -n runuser -u "$(id -un)" -g podlaz -- env \
    XDG_CONFIG_HOME="${XDG_CONFIG_HOME}" \
    XDG_STATE_HOME="${XDG_STATE_HOME}" \
    XDG_CACHE_HOME="${XDG_CACHE_HOME}" \
    /usr/bin/podlaz "$@"
}

run_installed_podlaz_bounded() {
  local timeout_seconds="$1"
  shift
  timeout --signal=TERM --kill-after=5s "${timeout_seconds}s" \
    sudo -n runuser -u "$(id -un)" -g podlaz -- env \
      XDG_CONFIG_HOME="${XDG_CONFIG_HOME}" \
      XDG_STATE_HOME="${XDG_STATE_HOME}" \
      XDG_CACHE_HOME="${XDG_CACHE_HOME}" \
      /usr/bin/podlaz "$@"
}

capture_secret_import() {
  local uri="$1" stdout_file stderr_file
  stdout_file="${SOAK_PRIVATE_DIR}/profile-import.stdout"
  stderr_file="${SOAK_PRIVATE_DIR}/profile-import.stderr"
  if ! run_installed_podlaz profile import "${uri}" >"${stdout_file}" 2>"${stderr_file}"; then
    fail "profile import failed"
  fi
  PROFILE_ID="$(awk '/^Imported profile:/ {print $3; exit}' "${stdout_file}")"
  assert_nonempty "${PROFILE_ID}" "imported profile id"
  append_sensitive_value "${PROFILE_ID}"
  rm -f -- "${stdout_file}" "${stderr_file}"
}

daemon_main_pid() {
  local value
  value="$(systemctl show -p MainPID --value podlazd.service)"
  [[ "${value}" =~ ^[1-9][0-9]*$ ]] || fail "podlazd.service has no running MainPID"
  printf '%s\n' "${value}"
}

snapshot_network_manifest() {
  local target="$1"
  sudo -n python3 "${NETWORK_HELPER}" snapshot "${TRANSACTION_DIR}" "${target}" >/dev/null || \
    fail "exact transaction-backed route/rule snapshot failed"
}

assert_resources_absent() {
  local phase="$1" manifest="$2"
  verify_tun_package_resources_absent "${phase}" "${NETWORK_HELPER}" "${manifest}" || \
    fail "${phase}: installed-package networking or runtime resources did not converge"
}

assert_no_recovery_candidates() {
  local phase="$1" output
  output="${SOAK_PRIVATE_DIR}/recover-${phase}.json"
  run_installed_podlaz recover --json >"${output}" 2>/dev/null || fail "${phase}: recovery inspection failed"
  python3 - "${output}" <<'PY'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as handle:
    payload = json.load(handle)
if payload.get("recovery", {}).get("candidates"):
    raise SystemExit("recovery candidates remain")
PY
  rm -f -- "${output}"
}

write_configuration() {
  python3 - "${CONFIGURATION_JSON}" \
    "${PODLAZ_E2E_SOAK_DURATION_SECONDS}" \
    "${PODLAZ_E2E_SOAK_PRECONDITION_WARMUP_SECONDS}" \
    "${PODLAZ_E2E_SOAK_WARMUP_SECONDS}" \
    "${PODLAZ_E2E_SOAK_SAMPLE_INTERVAL_SECONDS}" \
    "${PODLAZ_E2E_SOAK_DOCTOR_EVERY_SAMPLES}" \
    "${DOCTOR_RUNS}" \
    "${DOCTOR_UNHEALTHY_RUNS}" \
    "${PODLAZ_E2E_SOAK_RECONNECT_WARMUP_SECONDS}" \
    "${PODLAZ_E2E_SOAK_RECONNECT_SAMPLES}" \
    "${PODLAZ_E2E_SOAK_CLEANUP_SETTLE_SECONDS}" \
    "${PODLAZ_E2E_TUN_DIAGNOSTIC_TIMEOUT_SECONDS}" \
    "${PODLAZ_E2E_TUN_HEALTH_TIMEOUT_SECONDS}" \
    "${PODLAZ_E2E_TUN_HEALTH_POLL_SECONDS}" \
    "${PODLAZ_E2E_TUN_STATUS_TIMEOUT_SECONDS}" \
    "${PODLAZ_E2E_SOAK_CLEANUP_ATTEMPTS}" \
    "${PODLAZ_E2E_SOAK_CLEANUP_RETRY_SECONDS}" <<'PY'
import json
import os
import sys
path = sys.argv[1]
keys = (
    "duration_seconds",
    "precondition_warmup_seconds",
    "warmup_seconds",
    "sample_interval_seconds",
    "doctor_every_samples",
    "doctor_runs",
    "doctor_unhealthy_runs",
    "reconnect_warmup_seconds",
    "reconnect_samples",
    "cleanup_settle_seconds",
    "tun_diagnostic_timeout_seconds",
    "tun_health_timeout_seconds",
    "tun_health_poll_seconds",
    "tun_status_timeout_seconds",
    "cleanup_attempts",
    "cleanup_retry_seconds",
)
payload = {key: int(value) for key, value in zip(keys, sys.argv[2:], strict=True)}
temporary = path + ".tmp"
with open(temporary, "w", encoding="utf-8") as handle:
    json.dump(payload, handle, sort_keys=True)
    handle.write("\n")
os.chmod(temporary, 0o600)
os.replace(temporary, path)
PY
}

verify_package_provenance() {
  local extract_dir expected_cli expected_daemon expected_xray installed_cli installed_daemon installed_xray
  local main_pid running_exe running_hash version_output package_hash xray_binary_hash xray_artifact_hash
  local kernel_release systemd_version
  extract_dir="$(mktemp -d "${E2E_TMP_ROOT}/soak-package-extract.XXXXXX")"
  dpkg-deb -x "${DEV_DEB}" "${extract_dir}"
  expected_cli="$(sha256sum "${extract_dir}/usr/bin/podlaz" | awk '{print $1}')"
  expected_daemon="$(sha256sum "${extract_dir}/usr/bin/podlazd" | awk '{print $1}')"
  expected_xray="$(sha256sum "${extract_dir}/usr/lib/podlaz/xray" | awk '{print $1}')"
  installed_cli="$(sha256sum /usr/bin/podlaz | awk '{print $1}')"
  installed_daemon="$(sha256sum /usr/bin/podlazd | awk '{print $1}')"
  installed_xray="$(sudo -n sha256sum /usr/lib/podlaz/xray | awk '{print $1}')"
  [[ "${installed_cli}" == "${expected_cli}" ]] || fail "installed CLI does not match the built package"
  [[ "${installed_daemon}" == "${expected_daemon}" ]] || fail "installed daemon does not match the built package"
  [[ "${installed_xray}" == "${expected_xray}" ]] || fail "installed Xray does not match the built package"

  main_pid="$(daemon_main_pid)"
  running_exe="$(sudo -n readlink -f "/proc/$main_pid/exe")"
  [[ "${running_exe}" == "/usr/bin/podlazd" ]] || fail "running service executable is not the packaged daemon"
  running_hash="$(sudo -n sha256sum "/proc/$main_pid/exe" | awk '{print $1}')"
  [[ "${running_hash}" == "${expected_daemon}" ]] || fail "running daemon does not match the built package"

  version_output="${SOAK_PRIVATE_DIR}/installed-version.txt"
  /usr/bin/podlaz version >"${version_output}" 2>/dev/null || fail "installed CLI version command failed"
  grep -Fx "commit: ${BUILD_COMMIT}" "${version_output}" >/dev/null || fail "installed package does not identify the tested commit"

  # shellcheck disable=SC1091
  . packaging/runtime-helpers.env
  case "${PODLAZ_DEB_ARCH}" in
    amd64) xray_artifact_hash="${XRAY_AMD64_SHA256}" ;;
    arm64) xray_artifact_hash="${XRAY_ARM64_SHA256}" ;;
    *) fail "unsupported package architecture" ;;
  esac
  package_hash="$(sha256sum "${DEV_DEB}" | awk '{print $1}')"
  xray_binary_hash="${installed_xray}"
  kernel_release="$(uname -r)"
  systemd_version="$(systemctl --version | awk 'NR == 1 {print $2; exit}')"

  python3 - "${version_output}" "${PROVENANCE_JSON}" "${RUNTIME_OS_JSON}" \
    "${XRAY_VERSION}" "${xray_artifact_hash}" "${xray_binary_hash}" \
    "${kernel_release}" "${systemd_version}" "${package_hash}" "${PODLAZ_DEB_ARCH}" <<'PY'
import json
import os
import re
import sys
version_file, target, runtime_os_file, xray_version, xray_artifact, xray_binary, kernel, systemd, package, architecture = sys.argv[1:]
with open(version_file, encoding="utf-8") as handle:
    lines = [line.rstrip("\n") for line in handle]
if len(lines) < 2:
    raise SystemExit("installed version output is incomplete")
version_match = re.fullmatch(r"podlaz version ([A-Za-z0-9.+~_-]+)", lines[0])
commit_match = re.fullmatch(r"commit: ([0-9a-f]{7,64})", lines[1])
if version_match is None or commit_match is None:
    raise SystemExit("installed version output is malformed")
with open(runtime_os_file, encoding="utf-8") as handle:
    runtime_os = json.load(handle)
if runtime_os != {"id": "ubuntu", "version_id": "24.04"}:
    raise SystemExit("runtime OS provenance is invalid")
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
    "runtime_os": runtime_os,
}
temporary = target + ".tmp"
with open(temporary, "w", encoding="utf-8") as handle:
    json.dump(payload, handle, sort_keys=True)
    handle.write("\n")
os.chmod(temporary, 0o600)
os.replace(temporary, target)
PY
  rm -rf -- "${extract_dir}" "${version_output}"
}

capture_network_isolation_baseline() {
  local stderr_file
  stderr_file="${SOAK_PRIVATE_DIR}/network-isolation-capture.stderr"
  sudo -n python3 "${ISOLATION_TOOL}" capture \
    --output "${NETWORK_ISOLATION_BASELINE}" \
    --trusted-host "${PODLAZ_E2E_SOAK_TRUSTED_HOST_FILE}" \
    >/dev/null 2>"${stderr_file}" || fail "clean structural network isolation cannot be proved"
}

assert_network_isolation() {
  local label="$1" manifest="${2:-}" stderr_file
  local -a args
  [[ "${label}" =~ ^[a-z0-9-]+$ ]] || fail "network isolation label is invalid"
  stderr_file="${SOAK_PRIVATE_DIR}/network-isolation-${label}.stderr"
  args=(verify --baseline "${NETWORK_ISOLATION_BASELINE}" --trusted-host "${PODLAZ_E2E_SOAK_TRUSTED_HOST_FILE}")
  if [[ -n "${manifest}" ]]; then
    args+=(--manifest "${manifest}")
  fi
  sudo -n python3 "${ISOLATION_TOOL}" "${args[@]}" \
    >/dev/null 2>"${stderr_file}" || fail "${label}: structural network isolation cannot be proved"
}

run_bounded_data_plane_probe() {
  local label="$1" dns_stdout dns_stderr curl_stdout curl_stderr
  [[ "${label}" =~ ^[a-z0-9-]+$ ]] || fail "data-plane probe label is invalid"
  dns_stdout="${SOAK_PRIVATE_DIR}/${label}-dns.stdout"
  dns_stderr="${SOAK_PRIVATE_DIR}/${label}-dns.stderr"
  timeout --signal=TERM --kill-after=5s 30s \
    sudo -n resolvectl --cache=no --interface=podlaz0 -4 query "${PODLAZ_E2E_DNS_CHECK_HOST}" \
    >"${dns_stdout}" 2>"${dns_stderr}" || fail "${label}: bounded DNS probe failed"
  curl_stdout="${SOAK_PRIVATE_DIR}/${label}-https.stdout"
  curl_stderr="${SOAK_PRIVATE_DIR}/${label}-https.stderr"
  curl -4 -fsS --max-time 30 "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL}" \
    >"${curl_stdout}" 2>"${curl_stderr}" || fail "${label}: bounded HTTPS probe failed"
  append_sensitive_value "$(cat "${curl_stdout}")"
  wait_for_verified_tun_status "${label}"
}

precondition_warmed_inactive_baseline() {
  local daemon_pid after_pid
  SOAK_PHASE="precondition-connect"
  run_installed_podlaz connect --mode tun "${PROFILE_ID}" \
    >"${SOAK_PRIVATE_DIR}/precondition-connect.stdout" \
    2>"${SOAK_PRIVATE_DIR}/precondition-connect.stderr" || fail "TUN preconditioning connect failed"
  assert_tun_package_address_present precondition || fail "preconditioning TUN address is not authoritative"
  wait_for_verified_tun_status precondition

  SOAK_PHASE="precondition-attribution"
  daemon_pid="$(daemon_main_pid)"
  sudo -n python3 "${METRICS_TOOL}" discover \
    --daemon-pid "${daemon_pid}" \
    --transaction-dir "${TRANSACTION_DIR}" \
    --output "${PRECONDITION_IDENTITY}" || fail "preconditioning process attribution failed"
  snapshot_network_manifest "${PRECONDITION_NETWORK_MANIFEST}"
  assert_network_isolation precondition-active "${PRECONDITION_NETWORK_MANIFEST}"

  SOAK_PHASE="precondition-warmup"
  sleep "${PODLAZ_E2E_SOAK_PRECONDITION_WARMUP_SECONDS}"
  run_bounded_data_plane_probe precondition-probe
  run_bounded_tun_diagnostic precondition
  assert_network_isolation precondition-post-probes "${PRECONDITION_NETWORK_MANIFEST}"

  SOAK_PHASE="precondition-disconnect"
  run_installed_podlaz disconnect \
    >"${SOAK_PRIVATE_DIR}/precondition-disconnect.stdout" \
    2>"${SOAK_PRIVATE_DIR}/precondition-disconnect.stderr" || fail "TUN preconditioning disconnect failed"
  sudo -n python3 "${METRICS_TOOL}" assert-gone \
    --identity "${PRECONDITION_IDENTITY}" || fail "preconditioning supervised child did not terminate"
  sleep "${PODLAZ_E2E_SOAK_CLEANUP_SETTLE_SECONDS}"
  assert_resources_absent precondition-cleanup "${PRECONDITION_NETWORK_MANIFEST}"
  assert_no_recovery_candidates precondition-cleanup
  assert_network_isolation precondition-cleanup

  SOAK_PHASE="warmed-inactive-baseline"
  after_pid="$(daemon_main_pid)"
  [[ "${after_pid}" == "${daemon_pid}" ]] || fail "podlazd restarted during TUN preconditioning"
  WARMED_DAEMON_PID="${after_pid}"
  sudo -n python3 "${METRICS_TOOL}" discover-daemon \
    --daemon-pid "${after_pid}" \
    --output "${DAEMON_BASELINE_IDENTITY}" || fail "warmed inactive daemon attribution failed"
  sudo -n python3 "${METRICS_TOOL}" boundary-sample \
    --identity "${DAEMON_BASELINE_IDENTITY}" \
    --output "${BASELINE_BOUNDARY}" \
    --phase inactive-baseline \
    --sample-index 0 \
    --elapsed-seconds 0 || fail "warmed inactive resource baseline failed"
}

disconnect_and_sample_cleanup() {
  SOAK_PHASE="session-one-disconnect"
  run_installed_podlaz disconnect >"${SOAK_PRIVATE_DIR}/session-one-disconnect.stdout" 2>"${SOAK_PRIVATE_DIR}/session-one-disconnect.stderr" || \
    fail "normal disconnect failed after the active soak"
  sudo -n python3 "${SCRIPT_DIR}/lib/tun_soak_metrics.py" assert-gone \
    --identity "${SESSION_ONE_IDENTITY}" || fail "exact supervised child did not terminate"
  sleep "${PODLAZ_E2E_SOAK_CLEANUP_SETTLE_SECONDS}"
  SOAK_PHASE="post-cleanup"
  assert_resources_absent post-cleanup "${SESSION_ONE_NETWORK_MANIFEST}"
  assert_no_recovery_candidates post-cleanup
  assert_network_isolation post-cleanup
  local daemon_pid
  daemon_pid="$(daemon_main_pid)"
  [[ "${daemon_pid}" == "${WARMED_DAEMON_PID}" ]] || fail "podlazd restarted during the first measured lifecycle"
  sudo -n python3 "${METRICS_TOOL}" discover-daemon \
    --daemon-pid "${daemon_pid}" \
    --output "${DAEMON_CLEANUP_IDENTITY}" || fail "post-cleanup daemon attribution failed"
  sudo -n python3 "${SCRIPT_DIR}/lib/tun_soak_metrics.py" boundary-sample \
    --identity "${DAEMON_CLEANUP_IDENTITY}" \
    --output "${CLEANUP_BOUNDARY}" \
    --phase post-cleanup \
    --sample-index 0 \
    --elapsed-seconds 0 || fail "post-cleanup resource sample failed"
}

run_active_soak() {
  local sample_index=0 elapsed_seconds=0
  SOAK_PHASE="active-soak"
  while ((elapsed_seconds < PODLAZ_E2E_SOAK_DURATION_SECONDS)); do
    run_bounded_data_plane_probe active
    if ((sample_index % PODLAZ_E2E_SOAK_DOCTOR_EVERY_SAMPLES == 0)); then
      run_bounded_tun_diagnostic active
    fi
    assert_network_isolation active "${SESSION_ONE_NETWORK_MANIFEST}"
    sleep "${PODLAZ_E2E_SOAK_SAMPLE_INTERVAL_SECONDS}"
    elapsed_seconds=$((SECONDS - SOAK_STARTED_SECONDS))
    sample_index=$((sample_index + 1))
    sudo -n python3 "${METRICS_TOOL}" sample \
      --identity "${SESSION_ONE_IDENTITY}" \
      --output "${ACTIVE_SAMPLES}" \
      --phase active \
      --session 1 \
      --sample-index "${sample_index}" \
      --elapsed-seconds "${elapsed_seconds}" || fail "active structural resource sample failed"
  done
}

run_reconnect_probe() {
  local daemon_pid sample_index elapsed_seconds
  SOAK_PHASE="reconnect-connect"
  run_installed_podlaz connect --mode tun "${PROFILE_ID}" >"${SOAK_PRIVATE_DIR}/reconnect.stdout" 2>"${SOAK_PRIVATE_DIR}/reconnect.stderr" || \
    fail "immediate reconnect failed"
  assert_tun_package_address_present reconnect || fail "reconnect TUN address is not authoritative"
  wait_for_verified_tun_status reconnect
  SOAK_PHASE="reconnect-attribution"
  daemon_pid="$(daemon_main_pid)"
  [[ "${daemon_pid}" == "${WARMED_DAEMON_PID}" ]] || fail "podlazd restarted before reconnect attribution"
  sudo -n python3 "${METRICS_TOOL}" discover \
    --daemon-pid "${daemon_pid}" \
    --transaction-dir "${TRANSACTION_DIR}" \
    --output "${SESSION_TWO_IDENTITY}" || fail "reconnect process attribution failed"
  sudo -n python3 "${SCRIPT_DIR}/lib/tun_soak_metrics.py" assert-replaced \
    --before "${SESSION_ONE_IDENTITY}" \
    --after "${SESSION_TWO_IDENTITY}" || fail "reconnect did not replace the exact supervised child"
  snapshot_network_manifest "${SESSION_TWO_NETWORK_MANIFEST}"
  assert_network_isolation reconnect-attributed "${SESSION_TWO_NETWORK_MANIFEST}"
  SOAK_PHASE="reconnect-warmup"
  sleep "${PODLAZ_E2E_SOAK_RECONNECT_WARMUP_SECONDS}"
  SOAK_PHASE="reconnect-sampling"
  for sample_index in $(seq 0 $((PODLAZ_E2E_SOAK_RECONNECT_SAMPLES - 1))); do
    elapsed_seconds=$((sample_index * PODLAZ_E2E_SOAK_SAMPLE_INTERVAL_SECONDS))
    sudo -n python3 "${METRICS_TOOL}" sample \
      --identity "${SESSION_TWO_IDENTITY}" \
      --output "${RECONNECT_SAMPLES}" \
      --phase reconnect \
      --session 2 \
      --sample-index "${sample_index}" \
      --elapsed-seconds "${elapsed_seconds}" || fail "reconnect structural resource sample failed"
    run_bounded_data_plane_probe reconnect
    assert_network_isolation reconnect "${SESSION_TWO_NETWORK_MANIFEST}"
    if ((sample_index + 1 < PODLAZ_E2E_SOAK_RECONNECT_SAMPLES)); then
      sleep "${PODLAZ_E2E_SOAK_SAMPLE_INTERVAL_SECONDS}"
    fi
  done
  SOAK_PHASE="reconnect-disconnect"
  run_installed_podlaz disconnect >"${SOAK_PRIVATE_DIR}/reconnect-disconnect.stdout" 2>"${SOAK_PRIVATE_DIR}/reconnect-disconnect.stderr" || \
    fail "normal disconnect failed after reconnect"
  sudo -n python3 "${SCRIPT_DIR}/lib/tun_soak_metrics.py" assert-gone \
    --identity "${SESSION_TWO_IDENTITY}" || fail "replacement supervised child did not terminate"
  sleep "${PODLAZ_E2E_SOAK_CLEANUP_SETTLE_SECONDS}"
  SOAK_PHASE="reconnect-cleanup"
  assert_resources_absent reconnect-cleanup "${SESSION_TWO_NETWORK_MANIFEST}"
  assert_no_recovery_candidates reconnect-cleanup
  assert_network_isolation reconnect-cleanup
  daemon_pid="$(daemon_main_pid)"
  [[ "${daemon_pid}" == "${WARMED_DAEMON_PID}" ]] || fail "podlazd restarted during reconnect lifecycle"
  sudo -n python3 "${METRICS_TOOL}" discover-daemon \
    --daemon-pid "${daemon_pid}" \
    --output "${DAEMON_RECONNECT_CLEANUP_IDENTITY}" || fail "reconnect cleanup daemon attribution failed"
  sudo -n python3 "${METRICS_TOOL}" boundary-sample \
    --identity "${DAEMON_RECONNECT_CLEANUP_IDENTITY}" \
    --output "${RECONNECT_CLEANUP_BOUNDARY}" \
    --phase post-cleanup \
    --sample-index 1 \
    --elapsed-seconds 0 || fail "reconnect post-cleanup resource sample failed"
}

write_public_report() {
  SOAK_PHASE="report"
  write_configuration
  sudo -n python3 "${SCRIPT_DIR}/lib/tun_soak_metrics.py" report \
    --samples "${ACTIVE_SAMPLES}" \
    --reconnect-samples "${RECONNECT_SAMPLES}" \
    --baseline-boundary "${BASELINE_BOUNDARY}" \
    --cleanup-boundary "${CLEANUP_BOUNDARY}" \
    --reconnect-cleanup-boundary "${RECONNECT_CLEANUP_BOUNDARY}" \
    --provenance "${PROVENANCE_JSON}" \
    --configuration "${CONFIGURATION_JSON}" \
    --policy "${SOAK_POLICY_SNAPSHOT}" \
    --output "${PUBLIC_REPORT}" || fail "resource soak trend or lifecycle policy failed"
  assert_json_file "${PUBLIC_REPORT}"
}

write_failure_evidence() {
  local harness_exit_code="$1"
  python3 - "${FAILURE_REPORT}" "${SOAK_PHASE}" "${SOAK_COMMAND_EXIT}" "${SOAK_COMMAND_CLASSIFICATION}" "${SOAK_STATUS_VERDICT}" "${harness_exit_code}" <<'PY'
import json
import os
import re
import sys

path, phase, command_exit_text, command_classification_text, status_verdict_text, harness_exit_text = sys.argv[1:]
allowed_phases = {
    "initialization",
    "cleanup-preflight",
    "host-attestation",
    "configuration",
    "package-build",
    "package-install",
    "package-provenance",
    "profile-import",
    "inactive-disconnect",
    "inactive-recovery",
    "inactive-network",
    "isolation-baseline",
    "precondition-connect",
    "precondition-attribution",
    "precondition-warmup",
    "precondition-disconnect",
    "warmed-inactive-baseline",
    "session-one-connect",
    "active-attribution",
    "warmup",
    "active-soak",
    "session-one-disconnect",
    "post-cleanup",
    "reconnect-connect",
    "reconnect-attribution",
    "reconnect-warmup",
    "reconnect-sampling",
    "reconnect-disconnect",
    "reconnect-cleanup",
    "report",
    "redaction",
    "completed",
}
if phase not in allowed_phases:
    raise SystemExit("unrecognized resource-soak phase")
if re.fullmatch(r"[0-9]+", harness_exit_text) is None:
    raise SystemExit("invalid harness exit code")
harness_exit_code = int(harness_exit_text)
if not 0 <= harness_exit_code <= 255:
    raise SystemExit("harness exit code is out of range")
command_exit_code = None
if command_exit_text:
    if re.fullmatch(r"[0-9]+", command_exit_text) is None:
        raise SystemExit("invalid command exit code")
    command_exit_code = int(command_exit_text)
    if not 0 <= command_exit_code <= 255:
        raise SystemExit("command exit code is out of range")
allowed_classifications = {
    "authorization-denied",
    "authorization-unavailable",
    "daemon-internal",
    "daemon-unavailable",
    "unclassified",
}
command_classification = None
if command_classification_text:
    if command_classification_text not in allowed_classifications:
        raise SystemExit("invalid command classification")
    command_classification = command_classification_text
allowed_status_verdicts = {
    "command-error",
    "command-timeout",
    "invalid-status",
    "retry-degraded-timeout",
    "retry-revalidating-timeout",
    "terminal-cleanup-required",
    "terminal-inactive",
}
status_verdict = None
if status_verdict_text:
    if status_verdict_text not in allowed_status_verdicts:
        raise SystemExit("invalid TUN status verdict")
    status_verdict = status_verdict_text
payload = {
    "schema_version": 1,
    "phase": phase,
    "harness_exit_code": harness_exit_code,
    "command_exit_code": command_exit_code,
    "command_classification": command_classification,
    "status_verdict": status_verdict,
}
temporary = path + ".tmp"
os.makedirs(os.path.dirname(path), mode=0o700, exist_ok=True)
with open(temporary, "w", encoding="utf-8") as handle:
    json.dump(payload, handle, sort_keys=True, separators=(",", ":"))
    handle.write("\n")
os.chmod(temporary, 0o644)
os.replace(temporary, path)
PY
}

cleanup() {
  local code=$? cleanup_code=0
  set +e
  if [[ -x /usr/bin/podlaz && -n "${XDG_CONFIG_HOME:-}" ]]; then
    run_installed_podlaz disconnect >/dev/null 2>/dev/null
  fi
  if [[ "${code}" != "0" ]]; then
    write_failure_evidence "${code}" || cleanup_code=1
  fi
  run_tun_soak_cleanup final || cleanup_code=1
  sudo -n rm -rf -- "${SOAK_PRIVATE_DIR}" || cleanup_code=1
  if [[ "${code}" == "0" && "${cleanup_code}" != "0" ]]; then
    code="${cleanup_code}"
  fi
  exit "${code}"
}

trap cleanup EXIT

enforce_acceptance_inputs
SOAK_PHASE="host-attestation"
verify_runtime_environment
collect_host_sensitive_values
SOAK_PHASE="cleanup-preflight"
run_tun_soak_cleanup preflight
rm -f -- "${ACTIVE_SAMPLES}" "${RECONNECT_SAMPLES}" "${BASELINE_BOUNDARY}" "${CLEANUP_BOUNDARY}" "${RECONNECT_CLEANUP_BOUNDARY}" "${PUBLIC_REPORT}" "${FAILURE_REPORT}"
SOAK_PHASE="configuration"
write_configuration

SOAK_PHASE="package-build"
log "build exact release-like package for resource soak"
# shellcheck disable=SC1091
. packaging/package-toolchain.env
go install github.com/goreleaser/nfpm/v2/cmd/nfpm@"${NFPM_VERSION}"
export PATH="$(go env GOPATH)/bin:${PATH}"
BUILD_COMMIT="$(git rev-parse HEAD)"
PODLAZ_COMMIT="${BUILD_COMMIT}" \
  PODLAZ_BUILT="${PODLAZ_E2E_BUILT:-$(date -u '+%b %d %Y')}" \
  PODLAZ_DEB_ARCH="${PODLAZ_DEB_ARCH}" \
  bash scripts/build-deb.sh >"${PACKAGE_BUILD_LOG}" 2>&1
test -f "${DEV_DEB}" || fail "expected resource-soak package was not built"

SOAK_PHASE="package-install"
sudo -n apt install -y "./${DEV_DEB}" >"${PACKAGE_INSTALL_LOG}" 2>&1
sudo -n apt install --reinstall -y "./${DEV_DEB}" >"${PACKAGE_REINSTALL_LOG}" 2>&1
sudo -n systemctl daemon-reload
sudo -n systemctl restart podlazd.service
wait_for_daemon_socket
require_cmd nft
SOAK_PHASE="package-provenance"
verify_package_provenance

SOAK_PHASE="profile-import"
PRIMARY_URI="$(first_profile_uri)"
assert_nonempty "${PRIMARY_URI}" "primary profile URI"
capture_secret_import "${PRIMARY_URI}"

SOAK_PHASE="inactive-disconnect"
SOAK_COMMAND_EXIT=""
if run_installed_podlaz disconnect >"${SOAK_PRIVATE_DIR}/preconnect-disconnect.stdout" 2>"${SOAK_PRIVATE_DIR}/preconnect-disconnect.stderr"; then
  :
else
  SOAK_COMMAND_EXIT="$?"
  SOAK_COMMAND_CLASSIFICATION="$(
    python3 "${METRICS_TOOL}" classify-cli-error \
      --stderr-file "${SOAK_PRIVATE_DIR}/preconnect-disconnect.stderr"
  )" || SOAK_COMMAND_CLASSIFICATION="unclassified"
  fail "clean inactive preflight disconnect failed with exit code ${SOAK_COMMAND_EXIT}"
fi
SOAK_COMMAND_EXIT=""
SOAK_COMMAND_CLASSIFICATION=""
SOAK_PHASE="inactive-recovery"
assert_no_recovery_candidates preconnect
SOAK_PHASE="inactive-network"
snapshot_network_manifest "${PRECONNECT_NETWORK_MANIFEST}"
assert_resources_absent preconnect "${PRECONNECT_NETWORK_MANIFEST}"
SOAK_PHASE="isolation-baseline"
capture_network_isolation_baseline
precondition_warmed_inactive_baseline

SOAK_PHASE="session-one-connect"
SOAK_COMMAND_EXIT=""
run_installed_podlaz connect --mode tun "${PROFILE_ID}" >"${SOAK_PRIVATE_DIR}/session-one-connect.stdout" 2>"${SOAK_PRIVATE_DIR}/session-one-connect.stderr" || \
  fail "resource-soak TUN connect failed"
assert_tun_package_address_present active || fail "active TUN address is not authoritative"
wait_for_verified_tun_status post-connect
SOAK_PHASE="active-attribution"
DAEMON_PID="$(daemon_main_pid)"
[[ "${DAEMON_PID}" == "${WARMED_DAEMON_PID}" ]] || fail "podlazd identity changed after warmed baseline"
sudo -n python3 "${METRICS_TOOL}" discover \
  --daemon-pid "${DAEMON_PID}" \
  --transaction-dir "${TRANSACTION_DIR}" \
  --output "${SESSION_ONE_IDENTITY}" || fail "exact process attribution failed before warm-up"
sudo -n python3 "${METRICS_TOOL}" assert-replaced \
  --before "${PRECONDITION_IDENTITY}" \
  --after "${SESSION_ONE_IDENTITY}" || fail "measured session did not replace the preconditioning child on the same daemon"
snapshot_network_manifest "${SESSION_ONE_NETWORK_MANIFEST}"
assert_network_isolation active-attributed "${SESSION_ONE_NETWORK_MANIFEST}"
SOAK_PHASE="warmup"
sleep "${PODLAZ_E2E_SOAK_WARMUP_SECONDS}"
assert_network_isolation post-warmup "${SESSION_ONE_NETWORK_MANIFEST}"
SOAK_STARTED_SECONDS="${SECONDS}"
sudo -n python3 "${SCRIPT_DIR}/lib/tun_soak_metrics.py" sample \
  --identity "${SESSION_ONE_IDENTITY}" \
  --output "${ACTIVE_SAMPLES}" \
  --phase active \
  --session 1 \
  --sample-index 0 \
  --elapsed-seconds 0 || fail "post-warm-up resource baseline failed"

run_active_soak
disconnect_and_sample_cleanup
run_reconnect_probe
write_public_report
SOAK_PHASE="redaction"
collect_host_sensitive_values
sudo -n rm -rf -- "${SOAK_PRIVATE_DIR}"
assert_artifacts_do_not_contain_sensitive_values \
  "tun-resource-soak" \
  "${PODLAZ_E2E_PROFILE_URI}" \
  "${PODLAZ_E2E_PROFILE_URI_LIST}" \
  "${PROFILE_ID}" \
  "${PODLAZ_E2E_DNS_CHECK_HOST}" \
  "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL}" \
  "${HOST_SENSITIVE_VALUES}"

SOAK_PHASE="completed"
SOAK_COMMAND_EXIT=""
SOAK_COMMAND_CLASSIFICATION=""
SOAK_STATUS_VERDICT=""
rm -f -- "${FAILURE_REPORT}"
log "installed-package TUN resource soak completed"
