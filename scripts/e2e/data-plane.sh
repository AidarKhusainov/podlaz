#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"

require_cmd bash go python3 grep awk sed mktemp sudo systemctl journalctl apt curl getent ip ss timeout dpkg

: "${PODLAZ_E2E_PROFILE_URI:=}"
: "${PODLAZ_E2E_PROFILE_URI_LIST:=}"
: "${PODLAZ_E2E_EXPECTED_EGRESS_IP:=}"
: "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL:=https://api.ipify.org}"
: "${PODLAZ_E2E_RELIABILITY_CYCLES:=0}"
: "${PODLAZ_DEB_ARCH:=$(dpkg --print-architecture)}"

if [[ -z "${PODLAZ_E2E_PROFILE_URI}" && -z "${PODLAZ_E2E_PROFILE_URI_LIST}" ]]; then
  fail "PODLAZ_E2E_PROFILE_URI or PODLAZ_E2E_PROFILE_URI_LIST is required for data-plane e2e"
fi
HOST_DEB_ARCH="$(dpkg --print-architecture)"
if [[ "${PODLAZ_DEB_ARCH}" != "${HOST_DEB_ARCH}" ]]; then
  fail "data-plane e2e must install a native package: PODLAZ_DEB_ARCH=${PODLAZ_DEB_ARCH}, host=${HOST_DEB_ARCH}"
fi
DEV_DEB="dist/podlaz_0.0.0~dev-1_linux_${PODLAZ_DEB_ARCH}.deb"
DAEMON_SOCKET="/run/podlaz/podlazd.sock"
PACKAGE_INSTALLED=0
SERVICE_TOUCHED=0
ACTIVE_CONNECTION=0

mask_multiline_secret() {
  local value="${1:-}"
  [[ -n "${value}" ]] || return 0
  mask_value "${value}"
  while IFS= read -r line; do
    [[ -n "${line}" ]] || continue
    mask_value "${line}"
  done <<<"${value}"
}

for secret in "${PODLAZ_E2E_PROFILE_URI}" "${PODLAZ_E2E_PROFILE_URI_LIST}" "${PODLAZ_E2E_EXPECTED_EGRESS_IP}"; do
  mask_multiline_secret "${secret}"
done

build_podlaz_binary
setup_isolated_xdg "data-plane"
PODLAZ=("${PODLAZ_BIN}")

run_podlaz_as_socket_user() {
  sudo -n -u "$(id -un)" -g podlaz env \
    XDG_CONFIG_HOME="${XDG_CONFIG_HOME}" \
    XDG_STATE_HOME="${XDG_STATE_HOME}" \
    XDG_CACHE_HOME="${XDG_CACHE_HOME}" \
    /usr/bin/podlaz "$@"
}

capture_secret_command() {
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
  log "${name}: command contains secret material; arguments are intentionally not printed"
  set +e
  "$@" >"${LAST_STDOUT}" 2>"${LAST_STDERR}"
  local code=$?
  if [[ -s "${LAST_STDOUT}" ]]; then sed -e 's/^/stdout: /' "${LAST_STDOUT}"; fi
  if [[ -s "${LAST_STDERR}" ]]; then sed -e 's/^/stderr: /' "${LAST_STDERR}" >&2; fi
  if [[ "${restore_errexit}" == "1" ]]; then set -e; fi
  return "${code}"
}

expect_secret_success() {
  local name="$1"
  shift
  set +e
  capture_secret_command "${name}" "$@"
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
    run_podlaz_as_socket_user disconnect >"${E2E_ARTIFACT_DIR}/cleanup-disconnect.stdout" 2>"${E2E_ARTIFACT_DIR}/cleanup-disconnect.stderr" || true
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

first_profile_uri() {
  if [[ -n "${PODLAZ_E2E_PROFILE_URI}" ]]; then
    printf '%s\n' "${PODLAZ_E2E_PROFILE_URI}"
    return 0
  fi
  while IFS= read -r uri; do
    [[ -n "${uri}" ]] || continue
    printf '%s\n' "${uri}"
    return 0
  done <<<"${PODLAZ_E2E_PROFILE_URI_LIST}"
}

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
  ss -ltnp >"${listeners}" 2>&1 || fail "${phase}: failed to inspect TCP listeners"
  grep -E '127\.0\.0\.1:1080\b' "${listeners}" >/dev/null || fail "${phase}: SOCKS listener is not bound on 127.0.0.1:1080"
  grep -E '127\.0\.0\.1:8080\b' "${listeners}" >/dev/null || fail "${phase}: HTTP listener is not bound on 127.0.0.1:8080"
  if grep -E '(^|[[:space:]])(0\.0\.0\.0|\*):1080\b' "${listeners}" >/dev/null; then
    fail "${phase}: SOCKS listener is exposed beyond loopback"
  fi
  if grep -E '(^|[[:space:]])(0\.0\.0\.0|\*):8080\b' "${listeners}" >/dev/null; then
    fail "${phase}: HTTP listener is exposed beyond loopback"
  fi
}

assert_no_stale_state() {
  local phase="$1"
  expect_secret_success "status-${phase}-after-disconnect" run_podlaz_as_socket_user status
  expect_secret_success "recover-${phase}-dry-run" run_podlaz_as_socket_user recover
  grep -F "No podlaz-owned recovery candidates found." "${LAST_STDOUT}" >/dev/null || fail "${phase}: recovery dry-run found podlaz-owned cleanup candidates"
}

connect_profile() {
  local label="$1" id="$2"
  shift 2
  expect_secret_success "connect-${label}" run_podlaz_as_socket_user connect "$@" "${id}"
  ACTIVE_CONNECTION=1
  capture_secret_command "status-${label}" run_podlaz_as_socket_user status || true
}

disconnect_profile() {
  local label="$1"
  expect_secret_success "disconnect-${label}" run_podlaz_as_socket_user disconnect
  ACTIVE_CONNECTION=0
}

log "import primary real profile for data-plane checks"
PRIMARY_URI="$(first_profile_uri)"
assert_nonempty "${PRIMARY_URI}" "primary real profile URI"
expect_secret_success import-primary-profile "${PODLAZ[@]}" profile import "${PRIMARY_URI}"
PROFILE_ID="$(awk '/^Imported profile:/ {print $3}' "${LAST_STDOUT}")"
assert_nonempty "${PROFILE_ID}" "primary profile id"
assert_not_contains "${LAST_STDOUT}" "${PRIMARY_URI}"
expect_success validate-primary-proxy "${PODLAZ[@]}" profile validate "${PROFILE_ID}" --mode proxy-only

log "build and install package for data-plane checks"
# shellcheck disable=SC1091
. packaging/package-toolchain.env
go install github.com/goreleaser/nfpm/v2/cmd/nfpm@"${NFPM_VERSION}"
export PATH="$(go env GOPATH)/bin:${PATH}"
PODLAZ_COMMIT="${GITHUB_SHA:-e2e-data-plane}" PODLAZ_BUILT="${PODLAZ_E2E_BUILT:-$(date -u '+%b %d %Y')}" PODLAZ_DEB_ARCH="${PODLAZ_DEB_ARCH}" bash scripts/build-deb.sh 2>&1 | tee "${E2E_ARTIFACT_DIR}/data-plane-build-deb.log"
test -f "${DEV_DEB}" || fail "expected package not found: ${DEV_DEB}"
sudo -n apt install -y "./${DEV_DEB}" 2>&1 | tee "${E2E_ARTIFACT_DIR}/data-plane-apt-install.log"
PACKAGE_INSTALLED=1
sudo -n systemctl daemon-reload
sudo -n systemctl reset-failed podlazd.service || true
sudo -n systemctl start podlazd.service
SERVICE_TOUCHED=1
wait_for_daemon_socket

log "proxy-only explicit data-plane lifecycle"
connect_profile "proxy-only-explicit" "${PROFILE_ID}" --mode proxy-only
assert_loopback_listeners "proxy-only-explicit"
assert_proxy_egress socks "proxy-only-explicit"
assert_proxy_egress http "proxy-only-explicit"
disconnect_profile "proxy-only-explicit"
assert_proxy_cleanup "proxy-only-explicit"
assert_no_stale_state "proxy-only-explicit"

log "default connect mode data-plane lifecycle"
connect_profile "default-mode" "${PROFILE_ID}"
assert_loopback_listeners "default-mode"
assert_proxy_egress socks "default-mode"
assert_proxy_egress http "default-mode"
disconnect_profile "default-mode"
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
    disconnect_profile "reliability-${cycle}"
    assert_proxy_cleanup "reliability-${cycle}"
    assert_no_stale_state "reliability-${cycle}"
  done
else
  log "proxy-only reliability cycle gate is disabled; set PODLAZ_E2E_RELIABILITY_CYCLES=100 for release/manual evidence"
fi

log "data-plane e2e completed"
