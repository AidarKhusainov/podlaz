#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"
# shellcheck source=lib/installed_client.sh
source "${SCRIPT_DIR}/lib/installed_client.sh"
# shellcheck source=lib/profile_input.sh
source "${SCRIPT_DIR}/lib/profile_input.sh"

require_cmd bash go python3 grep awk sed mktemp sudo runuser systemctl journalctl apt curl getent ip ss timeout dpkg dpkg-deb

: "${PODLAZ_E2E_PROFILE_URI:=}"
: "${PODLAZ_E2E_PROFILE_URI_LIST:=}"
: "${PODLAZ_E2E_EXPECTED_EGRESS_IP:=}"
: "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL:=https://api.ipify.org}"
: "${PODLAZ_E2E_RELIABILITY_CYCLES:=0}"
: "${PODLAZ_E2E_PACKAGE_PATH:=}"
: "${PODLAZ_DEB_ARCH:=$(dpkg --print-architecture)}"

if [[ -z "${PODLAZ_E2E_PROFILE_URI}" && -z "${PODLAZ_E2E_PROFILE_URI_LIST}" ]]; then
  fail "PODLAZ_E2E_PROFILE_URI or PODLAZ_E2E_PROFILE_URI_LIST is required for data-plane e2e"
fi
HOST_DEB_ARCH="$(dpkg --print-architecture)"
if [[ "${PODLAZ_DEB_ARCH}" != "${HOST_DEB_ARCH}" ]]; then
  fail "data-plane e2e must install a native package: PODLAZ_DEB_ARCH=${PODLAZ_DEB_ARCH}, host=${HOST_DEB_ARCH}"
fi
DEV_DEB="dist/podlaz_0.0.0~dev-1_linux_${PODLAZ_DEB_ARCH}.deb"
INSTALL_DEB=""
DAEMON_SOCKET="/run/podlaz/podlazd.sock"
DAEMON_RUNTIME_CONFIG_PATH="${DAEMON_SOCKET%/*}/generated/xray.json"
PACKAGE_INSTALLED=0
SERVICE_TOUCHED=0
ACTIVE_CONNECTION=0
ACTIVE_RUNTIME_CONFIG_PATH=""

mask_multiline_sensitive() {
  local value="${1:-}"
  [[ -n "${value}" ]] || return 0
  mask_value "${value}"
  while IFS= read -r line; do
    [[ -n "${line}" ]] || continue
    mask_value "${line}"
  done <<<"${value}"
}

for sensitive in "${PODLAZ_E2E_PROFILE_URI}" "${PODLAZ_E2E_PROFILE_URI_LIST}" "${PODLAZ_E2E_EXPECTED_EGRESS_IP}"; do
  mask_multiline_sensitive "${sensitive}"
done

build_podlaz_binary
setup_isolated_xdg "data-plane"
PODLAZ=("${PODLAZ_BIN}")

capture_sensitive_command() {
  local name="$1"
  shift
  local safe restore_errexit=0
  case $- in
    *e*) restore_errexit=1 ;;
  esac
  safe="$(safe_name "${name}")"
  E2E_STEP=$((E2E_STEP + 1))
  LAST_STDOUT="${E2E_ARTIFACT_DIR}/$(printf '%03d' "${E2E_STEP}")-${safe}.stdout"
  LAST_STDERR="${E2E_ARTIFACT_DIR}/$(printf '%03d' "${E2E_STEP}")-${safe}.stderr"
  log "${name}: command arguments and output are intentionally not printed"
  set +e
  "$@" >"${LAST_STDOUT}" 2>"${LAST_STDERR}"
  local code=$?
  if [[ "${restore_errexit}" == "1" ]]; then set -e; fi
  return "${code}"
}

expect_sensitive_success() {
  local name="$1"
  shift
  set +e
  capture_sensitive_command "${name}" "$@"
  local code=$?
  set -e
  [[ "${code}" == "0" ]] || fail "${name} failed with exit code ${code}"
}

collect_daemon_startup_diagnostics() {
  sudo -n systemctl status podlazd.service --no-pager >"${E2E_ARTIFACT_DIR}/data-plane-podlazd.service.status" 2>&1 || true
  sudo -n systemctl cat podlazd.service >"${E2E_ARTIFACT_DIR}/data-plane-podlazd.service.cat" 2>&1 || true
  sudo -n journalctl -u podlazd.service -n 200 --no-pager >"${E2E_ARTIFACT_DIR}/data-plane-podlazd.service.journal" 2>&1 || true
  sudo -n ls -la /run/podlaz >"${E2E_ARTIFACT_DIR}/data-plane-run-podlaz.ls" 2>&1 || true
}

# Deliberately local: service terminal failure and diagnostics are checked before
# every retry and diagnostics are captured before the terminal failure.
wait_for_daemon_socket() {
  local attempt
  for attempt in $(seq 1 100); do
    if [[ -S "${DAEMON_SOCKET}" ]]; then
      return 0
    fi
    if ! sudo -n systemctl is-active --quiet podlazd.service; then
      collect_daemon_startup_diagnostics
      fail "podlazd.service stopped before daemon socket became ready: ${DAEMON_SOCKET}"
    fi
    sleep 0.1
  done
  collect_daemon_startup_diagnostics
  fail "podlazd.service did not create daemon socket within readiness timeout: ${DAEMON_SOCKET}"
}

cleanup_data_plane() {
  local code=$?
  if [[ "${ACTIVE_CONNECTION}" == "1" ]]; then
    run_installed_podlaz disconnect >"${E2E_ARTIFACT_DIR}/cleanup-disconnect.stdout" 2>"${E2E_ARTIFACT_DIR}/cleanup-disconnect.stderr" || true
    ACTIVE_CONNECTION=0
  fi
  ss -ltnp >"${E2E_ARTIFACT_DIR}/cleanup-ss-ltnp.txt" 2>&1 || true
  if [[ "${SERVICE_TOUCHED}" == "1" ]]; then
    sudo -n systemctl stop podlazd.service >/dev/null 2>&1 || true
  fi
  if [[ "${PACKAGE_INSTALLED}" == "1" && "${PODLAZ_E2E_KEEP_PACKAGE:-false}" != "true" ]]; then
    sudo -n apt remove -y podlaz >/dev/null 2>&1 || true
  fi
  exit "${code}"
}
trap cleanup_data_plane EXIT

assert_ipv4() {
  local value="$1" description="$2"
  [[ "${value}" =~ ^[0-9]{1,3}(\.[0-9]{1,3}){3}$ ]] || fail "${description}: expected IPv4 address, got ${value:-<empty>}"
}

assert_expected_egress() {
  local value="$1" description="$2"
  assert_ipv4 "${value}" "${description}"
  mask_value "${value}"
  if [[ -n "${PODLAZ_E2E_EXPECTED_EGRESS_IP}" && "${value}" != "${PODLAZ_E2E_EXPECTED_EGRESS_IP}" ]]; then
    fail "${description}: expected ${PODLAZ_E2E_EXPECTED_EGRESS_IP}, got ${value}"
  fi
}

curl_proxy_ip() {
  local proxy_kind="$1" output="$2" stderr="$3"
  case "${proxy_kind}" in
    socks)
      curl -4 -fsS --max-time 30 --socks5-hostname 127.0.0.1:1080 "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL}" >"${output}" 2>"${stderr}"
      ;;
    http)
      curl -4 -fsS --max-time 30 --proxy http://127.0.0.1:8080 "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL}" >"${output}" 2>"${stderr}"
      ;;
    *) fail "unsupported proxy kind: ${proxy_kind}" ;;
  esac
}

assert_proxy_egress() {
  local proxy_kind="$1" phase="$2"
  local dir="${E2E_ARTIFACT_DIR}/data-plane-${phase}-${proxy_kind}"
  mkdir -p "${dir}"
  curl_proxy_ip "${proxy_kind}" "${dir}/public-ipv4.txt" "${dir}/public-ipv4.stderr"
  local ip4
  ip4="$(tr -d '\r\n[:space:]' <"${dir}/public-ipv4.txt")"
  assert_expected_egress "${ip4}" "${phase} ${proxy_kind} egress"
}

assert_proxy_cleanup() {
  local phase="$1"
  local dir="${E2E_ARTIFACT_DIR}/data-plane-${phase}-cleanup"
  mkdir -p "${dir}"
  if curl_proxy_ip socks "${dir}/socks.stdout" "${dir}/socks.stderr"; then
    fail "${phase}: SOCKS proxy still accepted traffic after disconnect"
  fi
  if curl_proxy_ip http "${dir}/http.stdout" "${dir}/http.stderr"; then
    fail "${phase}: HTTP proxy still accepted traffic after disconnect"
  fi
}

assert_loopback_listeners() {
  local phase="$1"
  local listeners="${E2E_ARTIFACT_DIR}/data-plane-${phase}-listeners.txt"
  local attempt
  for attempt in $(seq 1 100); do
    ss -ltnp >"${listeners}" 2>&1 || fail "${phase}: failed to inspect TCP listeners"
    if grep -E '(^|[[:space:]])127\.0\.0\.1:1080([[:space:]]|$)' "${listeners}" >/dev/null && grep -E '(^|[[:space:]])127\.0\.0\.1:8080([[:space:]]|$)' "${listeners}" >/dev/null; then
      break
    fi
    sleep 0.1
  done
  grep -E '(^|[[:space:]])127\.0\.0\.1:1080([[:space:]]|$)' "${listeners}" >/dev/null || fail "${phase}: SOCKS listener is not bound on 127.0.0.1:1080"
  grep -E '(^|[[:space:]])127\.0\.0\.1:8080([[:space:]]|$)' "${listeners}" >/dev/null || fail "${phase}: HTTP listener is not bound on 127.0.0.1:8080"
  if grep -E '(^|[[:space:]])(0\.0\.0\.0|\*):1080([[:space:]]|$)' "${listeners}" >/dev/null; then
    fail "${phase}: SOCKS listener is exposed beyond loopback"
  fi
  if grep -E '(^|[[:space:]])(0\.0\.0\.0|\*):8080([[:space:]]|$)' "${listeners}" >/dev/null; then
    fail "${phase}: HTTP listener is exposed beyond loopback"
  fi
}

assert_recovery_plan_empty() {
  local phase="$1"
  if ! python3 - "${LAST_STDOUT}" "${phase}" <<'PY'
import json
import sys

path, phase = sys.argv[1], sys.argv[2]
with open(path, encoding="utf-8") as handle:
    payload = json.load(handle)
if payload.get("status") != "ok":
    raise SystemExit(f"{phase}: recover JSON status is not ok")
if payload.get("warnings"):
    raise SystemExit(f"{phase}: top-level recover JSON warnings remain")
recovery = payload.get("recovery")
if not isinstance(recovery, dict):
    raise SystemExit(f"{phase}: recovery payload is missing")
if recovery.get("candidates"):
    raise SystemExit(f"{phase}: recovery candidates remain")
if recovery.get("warnings"):
    raise SystemExit(f"{phase}: recovery inspection warnings remain")
PY
  then
    fail "${phase}: recover --json is not clean"
  fi
}

assert_active_proxy_only_control_plane() {
  local phase="$1" pass
  for pass in 1 2; do
    expect_sensitive_success "status-${phase}-active-${pass}" run_installed_podlaz status
    grep -Fx "Status: Connected" "${LAST_STDOUT}" >/dev/null || fail "${phase}: active status pass ${pass} is not connected"
    grep -Fx "Mode: proxy-only" "${LAST_STDOUT}" >/dev/null || fail "${phase}: active status pass ${pass} is not proxy-only"
    if [[ "${pass}" == "1" ]]; then
      sudo -n test -f "${ACTIVE_RUNTIME_CONFIG_PATH}" || fail "${phase}: active runtime config is missing"
    fi
  done
  expect_sensitive_success "recover-${phase}-while-active-json" run_installed_podlaz recover --json
  assert_json_file "${LAST_STDOUT}"
  assert_recovery_plan_empty "${phase}-while-active"
}

assert_runtime_config_removed() {
  local phase="$1" path="$2"
  [[ -n "${path}" ]] || fail "${phase}: runtime config path was not captured before disconnect"
  if sudo -n test -e "${path}"; then
    fail "${phase}: generated runtime config still exists after disconnect"
  fi
}

assert_no_stale_state() {
  local phase="$1"
  expect_sensitive_success "status-${phase}-after-disconnect" run_installed_podlaz status
  grep -Fx "Status: Disconnected" "${LAST_STDOUT}" >/dev/null || fail "${phase}: status is not disconnected after disconnect"
  expect_sensitive_success "recover-${phase}-dry-run-json" run_installed_podlaz recover --json
  assert_json_file "${LAST_STDOUT}"
  assert_recovery_plan_empty "${phase}"
}

assert_current_runtime_config_artifacts_safe() {
  local phase="$1"
  local status_json
  [[ -n "${ACTIVE_RUNTIME_CONFIG_PATH}" ]] || fail "${phase}: active runtime config path is empty"
  sudo -n test -f "${ACTIVE_RUNTIME_CONFIG_PATH}" || fail "${phase}: active runtime config is missing"
  status_json="$(mktemp "${E2E_TMP_ROOT}/$(safe_name "status-${phase}").runtime-config-status.XXXXXX.json")"
  chmod 600 "${status_json}"
  python3 - "${ACTIVE_RUNTIME_CONFIG_PATH}" "${status_json}" <<'PY'
import json
import sys

runtime_config_path, output_path = sys.argv[1], sys.argv[2]
with open(output_path, "w", encoding="utf-8") as handle:
    json.dump({"runtime_config_path": runtime_config_path}, handle)
PY
  assert_json_file "${status_json}"
  assert_active_runtime_config_artifacts_safe "runtime-config-${phase}" "${status_json}"
  rm -f -- "${status_json}"
}

connect_profile() {
  local label="$1" id="$2"
  shift 2
  expect_sensitive_success "connect-${label}" run_installed_podlaz connect "$@" "${id}"
  ACTIVE_CONNECTION=1
  ACTIVE_RUNTIME_CONFIG_PATH="${DAEMON_RUNTIME_CONFIG_PATH}"
  capture_sensitive_command "status-${label}" run_installed_podlaz status || true
}

disconnect_profile() {
  local label="$1"
  expect_sensitive_success "disconnect-${label}" run_installed_podlaz disconnect
  ACTIVE_CONNECTION=0
}

log "import primary real profile for data-plane checks"
PRIMARY_URI="$(first_configured_profile_uri)"
assert_nonempty "${PRIMARY_URI}" "primary real profile URI"
expect_sensitive_success import-primary-profile "${PODLAZ[@]}" profile import "${PRIMARY_URI}"
PROFILE_ID="$(awk '/^Imported profile:/ {print $3}' "${LAST_STDOUT}")"
assert_nonempty "${PROFILE_ID}" "primary profile id"
assert_not_contains "${LAST_STDOUT}" "${PRIMARY_URI}"
expect_sensitive_success validate-primary-proxy "${PODLAZ[@]}" profile validate "${PROFILE_ID}" --mode proxy-only

if [[ -n "${PODLAZ_E2E_PACKAGE_PATH}" ]]; then
  log "use prebuilt package for data-plane checks"
  [[ -f "${PODLAZ_E2E_PACKAGE_PATH}" ]] || fail "configured data-plane package is missing: ${PODLAZ_E2E_PACKAGE_PATH}"
  package_arch="$(dpkg-deb --field "${PODLAZ_E2E_PACKAGE_PATH}" Architecture 2>/dev/null || true)"
  [[ "${package_arch}" == "${HOST_DEB_ARCH}" ]] || fail "configured data-plane package architecture mismatch: package=${package_arch:-unknown}, host=${HOST_DEB_ARCH}"
  INSTALL_DEB="${PODLAZ_E2E_PACKAGE_PATH}"
else
  log "build package for data-plane checks"
  # shellcheck disable=SC1091
  . packaging/package-toolchain.env
  go install github.com/goreleaser/nfpm/v2/cmd/nfpm@"${NFPM_VERSION}"
  export PATH="$(go env GOPATH)/bin:${PATH}"
  PODLAZ_COMMIT="${GITHUB_SHA:-e2e-data-plane}" PODLAZ_BUILT="${PODLAZ_E2E_BUILT:-$(date -u '+%b %d %Y')}" PODLAZ_DEB_ARCH="${PODLAZ_DEB_ARCH}" bash scripts/build-deb.sh 2>&1 | tee "${E2E_ARTIFACT_DIR}/data-plane-build-deb.log"
  [[ -f "${DEV_DEB}" ]] || fail "expected package not found: ${DEV_DEB}"
  INSTALL_DEB="./${DEV_DEB}"
fi

sudo -n apt install -y "${INSTALL_DEB}" 2>&1 | tee "${E2E_ARTIFACT_DIR}/data-plane-apt-install.log"
PACKAGE_INSTALLED=1
sudo -n systemctl daemon-reload
sudo -n systemctl reset-failed podlazd.service || true
sudo -n systemctl start podlazd.service
SERVICE_TOUCHED=1
wait_for_daemon_socket

log "proxy-only explicit data-plane lifecycle"
connect_profile "proxy-only-explicit" "${PROFILE_ID}" --mode proxy-only
assert_active_proxy_only_control_plane "proxy-only-explicit"
PROXY_ONLY_RUNTIME_CONFIG_PATH="${ACTIVE_RUNTIME_CONFIG_PATH}"
assert_loopback_listeners "proxy-only-explicit"
assert_proxy_egress socks "proxy-only-explicit"
assert_proxy_egress http "proxy-only-explicit"
assert_current_runtime_config_artifacts_safe "proxy-only-explicit"
disconnect_profile "proxy-only-explicit"
assert_runtime_config_removed "proxy-only-explicit" "${PROXY_ONLY_RUNTIME_CONFIG_PATH}"
assert_proxy_cleanup "proxy-only-explicit"
assert_no_stale_state "proxy-only-explicit"

log "default connect mode data-plane lifecycle"
connect_profile "default-mode" "${PROFILE_ID}"
assert_active_proxy_only_control_plane "default-mode"
DEFAULT_RUNTIME_CONFIG_PATH="${ACTIVE_RUNTIME_CONFIG_PATH}"
assert_loopback_listeners "default-mode"
assert_proxy_egress socks "default-mode"
assert_proxy_egress http "default-mode"
assert_current_runtime_config_artifacts_safe "default-mode"
disconnect_profile "default-mode"
assert_runtime_config_removed "default-mode" "${DEFAULT_RUNTIME_CONFIG_PATH}"
assert_proxy_cleanup "default-mode"
assert_no_stale_state "default-mode"

if [[ ! "${PODLAZ_E2E_RELIABILITY_CYCLES}" =~ ^[0-9]+$ ]]; then
  fail "PODLAZ_E2E_RELIABILITY_CYCLES must be a non-negative integer"
fi
if [[ "${PODLAZ_E2E_RELIABILITY_CYCLES}" -gt 0 ]]; then
  log "proxy-only reliability cycle gate: ${PODLAZ_E2E_RELIABILITY_CYCLES} cycles"
  for cycle in $(seq 1 "${PODLAZ_E2E_RELIABILITY_CYCLES}"); do
    connect_profile "reliability-${cycle}" "${PROFILE_ID}" --mode proxy-only
    assert_loopback_listeners "reliability-${cycle}"
    assert_proxy_egress socks "reliability-${cycle}"
    assert_proxy_egress http "reliability-${cycle}"
    assert_current_runtime_config_artifacts_safe "reliability-${cycle}"
    disconnect_profile "reliability-${cycle}"
    assert_proxy_cleanup "reliability-${cycle}"
    assert_no_stale_state "reliability-${cycle}"
  done
else
  log "proxy-only reliability cycle gate is disabled; set PODLAZ_E2E_RELIABILITY_CYCLES=100 for release/manual evidence"
fi

assert_artifacts_do_not_contain_sensitive_values "data-plane" "${PODLAZ_E2E_PROFILE_URI}" "${PODLAZ_E2E_PROFILE_URI_LIST}"

log "data-plane e2e completed"
