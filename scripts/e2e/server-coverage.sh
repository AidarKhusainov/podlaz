#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"

require_cmd bash go python3 grep awk sed mktemp sudo systemctl journalctl apt curl getent ip ss timeout dpkg

: "${PODLAZ_E2E_PROFILE_URI:=}"
: "${PODLAZ_E2E_PROFILE_URI_2:=}"
: "${PODLAZ_E2E_PROFILE_URI_3:=}"
: "${PODLAZ_E2E_PROFILE_URI_4:=}"
: "${PODLAZ_E2E_PROFILE_URI_LIST:=}"
: "${PODLAZ_E2E_SUBSCRIPTION_URL:=}"
: "${PODLAZ_E2E_EXPECTED_EGRESS_IP:=}"
: "${PODLAZ_E2E_EXPECTED_EGRESS_IPV6:=}"
: "${PODLAZ_E2E_EXPECT_IPV6:=observe}"
: "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL:=https://api.ipify.org}"
: "${PODLAZ_E2E_PUBLIC_IPV6_CHECK_URL:=https://api6.ipify.org}"
: "${PODLAZ_E2E_DNS_CHECK_HOST:=github.com}"
: "${PODLAZ_E2E_ENABLE_TUN:=true}"
: "${PODLAZ_E2E_ENABLE_CRASH_TESTS:=true}"
: "${PODLAZ_E2E_ENABLE_HOST_DISRUPTION:=auto}"
: "${PODLAZ_E2E_STABILITY_MINUTES:=5}"
: "${PODLAZ_E2E_STATUS_CONCURRENCY:=6}"
: "${PODLAZ_E2E_HOST_WRAPPER_DIR:=/usr/local/libexec/podlaz-e2e}"
: "${PODLAZ_E2E_HOST_WRAPPER_TIMEOUT_SECONDS:=180}"
: "${PODLAZ_E2E_HOST_DISRUPTION_MODE:=}"
: "${PODLAZ_DEB_ARCH:=$(dpkg --print-architecture)}"

if [[ -z "${PODLAZ_E2E_PROFILE_URI}" && -z "${PODLAZ_E2E_PROFILE_URI_LIST}" ]]; then
  fail "PODLAZ_E2E_PROFILE_URI or PODLAZ_E2E_PROFILE_URI_LIST is required for server coverage e2e"
fi

HOST_DEB_ARCH="$(dpkg --print-architecture)"
if [[ "${PODLAZ_DEB_ARCH}" != "${HOST_DEB_ARCH}" ]]; then
  fail "server coverage e2e must install a native package: PODLAZ_DEB_ARCH=${PODLAZ_DEB_ARCH}, host=${HOST_DEB_ARCH}"
fi
DEV_DEB="dist/podlaz_0.0.0~dev-1_linux_${PODLAZ_DEB_ARCH}.deb"
PACKAGE_INSTALLED=0
SERVICE_TOUCHED=0
ACTIVE_CONNECTION=0
ACTIVE_MODE=""

mask_multiline_secret() {
  local value="${1:-}"
  [[ -n "${value}" ]] || return 0
  mask_value "${value}"
  while IFS= read -r line; do
    [[ -n "${line}" ]] || continue
    mask_value "${line}"
  done <<<"${value}"
}

for secret in \
  "${PODLAZ_E2E_PROFILE_URI}" \
  "${PODLAZ_E2E_PROFILE_URI_2}" \
  "${PODLAZ_E2E_PROFILE_URI_3}" \
  "${PODLAZ_E2E_PROFILE_URI_4}" \
  "${PODLAZ_E2E_PROFILE_URI_LIST}" \
  "${PODLAZ_E2E_SUBSCRIPTION_URL}" \
  "${PODLAZ_E2E_EXPECTED_EGRESS_IP}" \
  "${PODLAZ_E2E_EXPECTED_EGRESS_IPV6}"; do
  mask_multiline_secret "${secret}"
done

build_podlaz_binary
setup_isolated_xdg "server-coverage"
PODLAZ=("${PODLAZ_BIN}")

run_podlaz_as_socket_user() {
  sudo -n -u "$(id -un)" -g podlaz env \
    XDG_CONFIG_HOME="${XDG_CONFIG_HOME}" \
    XDG_STATE_HOME="${XDG_STATE_HOME}" \
    XDG_CACHE_HOME="${XDG_CACHE_HOME}" \
    /usr/bin/podlaz "$@"
}

run_podlaz_with_xdg_root() {
  local xdg_root="$1"
  shift
  sudo -n -u "$(id -un)" -g podlaz env \
    XDG_CONFIG_HOME="${xdg_root}/config" \
    XDG_STATE_HOME="${xdg_root}/state" \
    XDG_CACHE_HOME="${xdg_root}/cache" \
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

collect_host_snapshot() {
  local name="$1"
  local dir="${E2E_ARTIFACT_DIR}/host-${name}"
  mkdir -p "${dir}"
  date -u '+%Y-%m-%dT%H:%M:%SZ' >"${dir}/timestamp.txt"
  hostname >"${dir}/hostname.txt" 2>&1 || true
  id >"${dir}/id.txt" 2>&1 || true
  uname -a >"${dir}/uname.txt" 2>&1 || true
  ip addr >"${dir}/ip-addr.txt" 2>&1 || true
  ip route >"${dir}/ip-route.txt" 2>&1 || true
  ip -6 route >"${dir}/ip-route6.txt" 2>&1 || true
  ip rule >"${dir}/ip-rule.txt" 2>&1 || true
  ss -ltnup >"${dir}/ss-ltnup.txt" 2>&1 || true
  if command -v resolvectl >/dev/null 2>&1; then
    resolvectl status >"${dir}/resolvectl-status.txt" 2>&1 || true
    resolvectl query "${PODLAZ_E2E_DNS_CHECK_HOST}" >"${dir}/resolvectl-query.txt" 2>&1 || true
  fi
  getent hosts "${PODLAZ_E2E_DNS_CHECK_HOST}" >"${dir}/getent-hosts.txt" 2>&1 || true
  if command -v nmcli >/dev/null 2>&1; then
    nmcli general status >"${dir}/nmcli-general-status.txt" 2>&1 || true
    nmcli general permissions >"${dir}/nmcli-general-permissions.txt" 2>&1 || true
    nmcli networking connectivity check >"${dir}/nmcli-connectivity-check.txt" 2>&1 || true
    nmcli device status >"${dir}/nmcli-device-status.txt" 2>&1 || true
    nmcli connection show --active >"${dir}/nmcli-active-connections.txt" 2>&1 || true
  fi
  sudo -n systemctl status podlazd.service --no-pager >"${dir}/podlazd.service.status" 2>&1 || true
  sudo -n journalctl -u podlazd.service -n 300 --no-pager >"${dir}/podlazd.service.journal" 2>&1 || true
  sudo -n nft list ruleset >"${dir}/nft-ruleset.txt" 2>&1 || true
}

cleanup_server_coverage() {
  local code=$?
  if [[ "${ACTIVE_CONNECTION}" == "1" ]]; then
    run_podlaz_as_socket_user disconnect >"${E2E_ARTIFACT_DIR}/cleanup-disconnect.stdout" 2>"${E2E_ARTIFACT_DIR}/cleanup-disconnect.stderr" || true
    run_podlaz_as_socket_user recover --execute --yes >"${E2E_ARTIFACT_DIR}/cleanup-recover-execute.stdout" 2>"${E2E_ARTIFACT_DIR}/cleanup-recover-execute.stderr" || true
  fi
  collect_host_snapshot cleanup || true
  if [[ "${SERVICE_TOUCHED}" == "1" ]]; then
    sudo -n systemctl stop podlazd.service >/dev/null 2>&1 || true
  fi
  if [[ "${PACKAGE_INSTALLED}" == "1" && "${PODLAZ_E2E_KEEP_PACKAGE:-false}" != "true" ]]; then
    sudo -n apt remove -y podlaz >/dev/null 2>&1 || true
  fi
  exit "${code}"
}
trap cleanup_server_coverage EXIT

# The rest of this suite is intentionally implemented in reusable helpers below.
# It imports every provided real profile, validates proxy-only and TUN plans, installs the native package,
# runs proxy-only and optional TUN lifecycles, checks DNS/public egress/IPv6, runs concurrent status/connect/disconnect,
# optionally kills supervised core and daemon processes, optionally invokes host-owned disruption wrappers,
# and optionally performs a long-running stability probe.

profile_uri_candidates() {
  local uri
  for uri in "${PODLAZ_E2E_PROFILE_URI}" "${PODLAZ_E2E_PROFILE_URI_2}" "${PODLAZ_E2E_PROFILE_URI_3}" "${PODLAZ_E2E_PROFILE_URI_4}"; do
    [[ -n "${uri}" ]] || continue
    printf '%s\n' "${uri}"
  done
  if [[ -n "${PODLAZ_E2E_PROFILE_URI_LIST}" ]]; then
    while IFS= read -r uri; do
      [[ -n "${uri}" ]] || continue
      printf '%s\n' "${uri}"
    done <<<"${PODLAZ_E2E_PROFILE_URI_LIST}"
  fi
}

import_profile_uri() {
  local label="$1" uri="$2" out err id
  out="${E2E_ARTIFACT_DIR}/${label}-profile-import.stdout"
  err="${E2E_ARTIFACT_DIR}/${label}-profile-import.stderr"
  log "import ${label} real profile"
  set +e
  "${PODLAZ[@]}" profile import "${uri}" >"${out}" 2>"${err}"
  local code=$?
  set -e
  if [[ -s "${out}" ]]; then sed -e 's/^/stdout: /' "${out}"; fi
  if [[ -s "${err}" ]]; then sed -e 's/^/stderr: /' "${err}" >&2; fi
  [[ "${code}" == "0" ]] || fail "${label} profile import failed with exit code ${code}"
  assert_not_contains "${out}" "${uri}"
  id="$(awk '/^Imported profile:/ {print $3}' "${out}")"
  assert_nonempty "${id}" "${label} imported profile id"
  printf '%s\n' "${id}" >>"${E2E_ARTIFACT_DIR}/profile-ids.txt"
}

validate_and_plan_profile() {
  local id="$1"
  expect_success "show-${id}" "${PODLAZ[@]}" profile show "${id}"
  expect_success "show-json-${id}" "${PODLAZ[@]}" profile show "${id}" --json
  assert_json_file "${LAST_STDOUT}"
  expect_success "validate-proxy-${id}" "${PODLAZ[@]}" profile validate "${id}" --mode proxy-only
  expect_success "validate-tun-${id}" "${PODLAZ[@]}" profile validate "${id}" --mode tun
  expect_success "plan-proxy-${id}" "${PODLAZ[@]}" plan --mode proxy-only "${id}"
  expect_success "plan-tun-${id}" "${PODLAZ[@]}" plan --mode tun "${id}"
  assert_contains "${LAST_STDOUT}" "No changes were applied."
}

check_dns_resolution() {
  local phase="$1" dir="${E2E_ARTIFACT_DIR}/dns-${phase}"
  mkdir -p "${dir}"
  getent hosts "${PODLAZ_E2E_DNS_CHECK_HOST}" >"${dir}/getent-hosts.txt" 2>"${dir}/getent-hosts.stderr" || fail "${phase}: DNS resolution failed for ${PODLAZ_E2E_DNS_CHECK_HOST}"
  if command -v resolvectl >/dev/null 2>&1; then
    resolvectl query "${PODLAZ_E2E_DNS_CHECK_HOST}" >"${dir}/resolvectl-query.txt" 2>"${dir}/resolvectl-query.stderr" || true
  fi
}

check_public_egress() {
  local phase="$1" dir="${E2E_ARTIFACT_DIR}/egress-${phase}"
  mkdir -p "${dir}"
  local ip4="" ip6=""
  ip4="$(curl -4 -fsS --max-time 30 "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL}" 2>"${dir}/public-ipv4.stderr" || true)"
  mask_value "${ip4}"
  printf '%s\n' "${ip4}" >"${dir}/public-ipv4.txt"
  [[ -n "${ip4}" ]] || fail "${phase}: IPv4 egress check returned an empty response"
  if [[ -n "${PODLAZ_E2E_EXPECTED_EGRESS_IP}" && "${ip4}" != "${PODLAZ_E2E_EXPECTED_EGRESS_IP}" ]]; then fail "${phase}: unexpected IPv4 egress IP"; fi
  set +e
  ip6="$(curl -6 -fsS --max-time 30 "${PODLAZ_E2E_PUBLIC_IPV6_CHECK_URL}" 2>"${dir}/public-ipv6.stderr")"
  local ipv6_code=$?
  set -e
  mask_value "${ip6}"
  printf '%s\n' "${ip6}" >"${dir}/public-ipv6.txt"
  printf '%s\n' "${ipv6_code}" >"${dir}/public-ipv6.exit"
  case "${PODLAZ_E2E_EXPECT_IPV6}" in
    observe|"") ;;
    blocked) [[ "${ipv6_code}" != "0" ]] || fail "${phase}: IPv6 egress succeeded but PODLAZ_E2E_EXPECT_IPV6=blocked" ;;
    egress)
      [[ "${ipv6_code}" == "0" ]] || fail "${phase}: IPv6 egress failed but PODLAZ_E2E_EXPECT_IPV6=egress"
      if [[ -n "${PODLAZ_E2E_EXPECTED_EGRESS_IPV6}" && "${ip6}" != "${PODLAZ_E2E_EXPECTED_EGRESS_IPV6}" ]]; then fail "${phase}: unexpected IPv6 egress IP"; fi
      ;;
    *) fail "unsupported PODLAZ_E2E_EXPECT_IPV6=${PODLAZ_E2E_EXPECT_IPV6}" ;;
  esac
}

status_concurrency_probe() {
  local label="$1" workers="${PODLAZ_E2E_STATUS_CONCURRENCY}" i
  mkdir -p "${E2E_ARTIFACT_DIR}/concurrency-${label}"
  for i in $(seq 1 "${workers}"); do
    ( set +e; for _ in $(seq 1 10); do run_podlaz_as_socket_user status >>"${E2E_ARTIFACT_DIR}/concurrency-${label}/status-${i}.log" 2>&1; sleep 0.2; done ) &
  done
  wait
}

connect_profile() {
  local mode="$1" id="$2"
  expect_secret_success "connect-${mode}-${id}" run_podlaz_as_socket_user connect --mode "${mode}" "${id}"
  ACTIVE_CONNECTION=1
  ACTIVE_MODE="${mode}"
}

disconnect_active() {
  local label="$1"
  expect_secret_success "disconnect-${label}" run_podlaz_as_socket_user disconnect
  ACTIVE_CONNECTION=0
  ACTIVE_MODE=""
}

expect_second_connect_rejected() {
  local mode="$1" id="$2"
  set +e
  capture_secret_command "second-connect-rejected-${mode}-${id}" run_podlaz_as_socket_user connect --mode "${mode}" "${id}"
  local code=$?
  set -e
  [[ "${code}" != "0" ]] || fail "second connect unexpectedly succeeded while another connection was active"
}

concurrent_lifecycle_probe() {
  local mode="$1" id="$2" dir="${E2E_ARTIFACT_DIR}/concurrent-lifecycle-${mode}"
  mkdir -p "${dir}"
  connect_profile "${mode}" "${id}"
  ( set +e; for _ in $(seq 1 12); do run_podlaz_as_socket_user status >>"${dir}/status-loop.log" 2>&1; sleep 0.2; done ) &
  local status_pid=$!
  ( set +e; run_podlaz_as_socket_user connect --mode "${mode}" "${id}" >"${dir}/overlap-connect.stdout" 2>"${dir}/overlap-connect.stderr"; printf '%s\n' "$?" >"${dir}/overlap-connect.exit" ) &
  local connect_pid=$!
  sleep 0.5
  ( set +e; run_podlaz_as_socket_user disconnect >"${dir}/disconnect.stdout" 2>"${dir}/disconnect.stderr"; printf '%s\n' "$?" >"${dir}/disconnect.exit" ) &
  local disconnect_pid=$!
  wait "${status_pid}" "${connect_pid}" "${disconnect_pid}"
  ACTIVE_CONNECTION=0
  ACTIVE_MODE=""
  [[ "$(cat "${dir}/overlap-connect.exit")" != "0" ]] || fail "overlap connect unexpectedly succeeded during ${mode} concurrency probe"
  [[ "$(cat "${dir}/disconnect.exit")" == "0" ]] || fail "disconnect failed during ${mode} concurrency probe"
  capture_secret_command "status-after-concurrent-${mode}" run_podlaz_as_socket_user status || true
  capture_secret_command "disconnect-idempotent-concurrent-${mode}" run_podlaz_as_socket_user disconnect || true
}

find_xray_pids() { pgrep -u podlaz -f 'xray.*run.*-config' || true; }
kill_supervised_core() { local pids; pids="$(find_xray_pids)"; [[ -n "${pids}" ]] || fail "no supervised xray process found to kill"; printf '%s\n' "${pids}" >"${E2E_ARTIFACT_DIR}/killed-xray-pids.txt"; sudo -n kill -KILL ${pids}; sleep 3; }
restart_daemon_after_crash() { sudo -n systemctl reset-failed podlazd.service || true; sudo -n systemctl start podlazd.service || sudo -n systemctl restart podlazd.service; for _ in $(seq 1 20); do if sudo -n systemctl is-active --quiet podlazd.service; then return 0; fi; sleep 1; done; sudo -n systemctl status podlazd.service --no-pager || true; fail "podlazd.service did not become active after crash/restart"; }

run_lifecycle_for_profile() {
  local mode="$1" id="$2"
  connect_profile "${mode}" "${id}"
  collect_host_snapshot "active-${mode}-${id}"
  status_concurrency_probe "${mode}-${id}"
  expect_second_connect_rejected "${mode}" "${id}"
  capture_secret_command "status-${mode}-${id}" run_podlaz_as_socket_user status || true
  check_dns_resolution "${mode}-${id}"
  if [[ "${mode}" == "tun" ]]; then check_public_egress "tun-${id}"; fi
  disconnect_active "${mode}-${id}"
  capture_secret_command "disconnect-idempotent-${mode}-${id}" run_podlaz_as_socket_user disconnect || true
  if [[ "${mode}" == "tun" ]]; then capture_secret_command "recover-after-tun-${id}" run_podlaz_as_socket_user recover || true; capture_secret_command "recover-execute-after-tun-${id}" run_podlaz_as_socket_user recover --execute --yes || true; fi
}

run_core_crash_probe() {
  local mode="$1" id="$2"
  log "core crash probe (${mode})"
  connect_profile "${mode}" "${id}"
  collect_host_snapshot "before-core-crash-${mode}"
  kill_supervised_core
  capture_secret_command "status-after-core-crash-${mode}" run_podlaz_as_socket_user status || true
  capture_secret_command "doctor-after-core-crash-${mode}" run_podlaz_as_socket_user doctor || true
  capture_secret_command "disconnect-after-core-crash-${mode}" run_podlaz_as_socket_user disconnect || true
  ACTIVE_CONNECTION=0
  ACTIVE_MODE=""
  capture_secret_command "recover-after-core-crash-${mode}" run_podlaz_as_socket_user recover || true
  capture_secret_command "recover-execute-after-core-crash-${mode}" run_podlaz_as_socket_user recover --execute --yes || true
  collect_host_snapshot "after-core-crash-${mode}"
}

run_daemon_crash_probe() {
  local mode="$1" id="$2"
  log "daemon crash probe (${mode})"
  connect_profile "${mode}" "${id}"
  collect_host_snapshot "before-daemon-crash-${mode}"
  sudo -n systemctl kill --signal=SIGKILL podlazd.service
  sleep 4
  restart_daemon_after_crash
  capture_secret_command "status-after-daemon-crash-${mode}" run_podlaz_as_socket_user status || true
  capture_secret_command "recover-after-daemon-crash-${mode}" run_podlaz_as_socket_user recover || true
  capture_secret_command "recover-execute-after-daemon-crash-${mode}" run_podlaz_as_socket_user recover --execute --yes || true
  capture_secret_command "disconnect-after-daemon-crash-${mode}" run_podlaz_as_socket_user disconnect || true
  ACTIVE_CONNECTION=0
  ACTIVE_MODE=""
  collect_host_snapshot "after-daemon-crash-${mode}"
}

run_stability_probe() {
  local mode="$1" id="$2" minutes="$3"
  [[ "${minutes}" =~ ^[0-9]+$ ]] || fail "PODLAZ_E2E_STABILITY_MINUTES must be an integer"
  [[ "${minutes}" -gt 0 ]] || return 0
  local end now iter=0
  end=$(( $(date +%s) + minutes * 60 ))
  log "long-running stability probe (${mode}, ${minutes} min)"
  connect_profile "${mode}" "${id}"
  while true; do now=$(date +%s); [[ "${now}" -lt "${end}" ]] || break; iter=$((iter + 1)); capture_secret_command "stability-status-${mode}-${iter}" run_podlaz_as_socket_user status || true; check_dns_resolution "stability-${mode}-${iter}"; if [[ "${mode}" == "tun" ]]; then check_public_egress "stability-${mode}-${iter}"; fi; sleep 30; done
  disconnect_active "stability-${mode}"
}

safe_wrapper_path() { local name="$1"; case "${name}" in suspend-resume|network-reconnect|dhcp-renew|dns-change|polkit-gui-auth|polkit-tty-auth) ;; *) fail "unsupported host wrapper name: ${name}" ;; esac; printf '%s/%s\n' "${PODLAZ_E2E_HOST_WRAPPER_DIR}" "${name}"; }

run_host_wrapper_probe() {
  local name="$1" mode="$2" wrapper
  wrapper="$(safe_wrapper_path "${name}")"
  [[ -x "${wrapper}" ]] || return 1
  log "host disruption wrapper: ${name}"
  collect_host_snapshot "before-${name}"
  set +e
  sudo -n env PODLAZ_E2E_DNS_CHECK_HOST="${PODLAZ_E2E_DNS_CHECK_HOST}" PODLAZ_E2E_ACTIVE_MODE="${mode}" timeout "${PODLAZ_E2E_HOST_WRAPPER_TIMEOUT_SECONDS}" "${wrapper}" >"${E2E_ARTIFACT_DIR}/host-wrapper-${name}.stdout" 2>"${E2E_ARTIFACT_DIR}/host-wrapper-${name}.stderr"
  local code=$?
  set -e
  collect_host_snapshot "after-${name}"
  capture_secret_command "status-after-${name}" run_podlaz_as_socket_user status || true
  capture_secret_command "doctor-after-${name}" run_podlaz_as_socket_user doctor || true
  check_dns_resolution "after-${name}"
  if [[ "${mode}" == "tun" ]]; then check_public_egress "after-${name}"; fi
  [[ "${code}" == "0" ]] || fail "host wrapper ${name} failed with exit code ${code}"
  return 0
}

run_host_disruption_probes() {
  case "${PODLAZ_E2E_ENABLE_HOST_DISRUPTION}" in
    false|"") log "host disruption wrappers are disabled"; return 0 ;;
    true|auto) ;;
    *) fail "unsupported PODLAZ_E2E_ENABLE_HOST_DISRUPTION=${PODLAZ_E2E_ENABLE_HOST_DISRUPTION}; use true, false, or auto" ;;
  esac

  local mode="${PODLAZ_E2E_HOST_DISRUPTION_MODE}"
  if [[ -z "${mode}" ]]; then if [[ "${PODLAZ_E2E_ENABLE_TUN}" == "true" ]]; then mode="tun"; else mode="proxy-only"; fi; fi
  case "${mode}" in proxy-only) ;; tun) [[ "${PODLAZ_E2E_ENABLE_TUN}" == "true" ]] || fail "PODLAZ_E2E_HOST_DISRUPTION_MODE=tun requires PODLAZ_E2E_ENABLE_TUN=true" ;; *) fail "unsupported PODLAZ_E2E_HOST_DISRUPTION_MODE=${mode}" ;; esac
  local id="$1" available=0 wrapper
  for wrapper in suspend-resume network-reconnect dhcp-renew dns-change polkit-gui-auth polkit-tty-auth; do if [[ -x "$(safe_wrapper_path "${wrapper}")" ]]; then available=$((available + 1)); else printf '%s\n' "missing: $(safe_wrapper_path "${wrapper}")" >>"${E2E_ARTIFACT_DIR}/host-wrapper-availability.txt"; fi; done
  if [[ "${available}" -eq 0 ]]; then
    if [[ "${PODLAZ_E2E_ENABLE_HOST_DISRUPTION}" == "auto" ]]; then
      log "host disruption wrappers are in auto mode, but no supported wrappers exist under ${PODLAZ_E2E_HOST_WRAPPER_DIR}; skipping host disruption probes"
      return 0
    fi
    fail "host disruption enabled, but no supported wrappers exist under ${PODLAZ_E2E_HOST_WRAPPER_DIR}"
  fi
  connect_profile "${mode}" "${id}"
  for wrapper in suspend-resume network-reconnect dhcp-renew dns-change polkit-gui-auth polkit-tty-auth; do if [[ -x "$(safe_wrapper_path "${wrapper}")" ]]; then run_host_wrapper_probe "${wrapper}" "${mode}"; fi; done
  disconnect_active "host-disruption-${mode}"
  capture_secret_command "recover-after-host-disruption" run_podlaz_as_socket_user recover || true
  capture_secret_command "recover-execute-after-host-disruption" run_podlaz_as_socket_user recover --execute --yes || true
}

log "host baseline diagnostics"
collect_host_snapshot baseline

log "import and validate real profile set"
: >"${E2E_ARTIFACT_DIR}/profile-ids.txt"
profile_index=0
while IFS= read -r uri; do profile_index=$((profile_index + 1)); import_profile_uri "profile-${profile_index}" "${uri}"; done < <(profile_uri_candidates)
mapfile -t PROFILE_IDS <"${E2E_ARTIFACT_DIR}/profile-ids.txt"
[[ "${#PROFILE_IDS[@]}" -gt 0 ]] || fail "no real profiles imported"
for id in "${PROFILE_IDS[@]}"; do validate_and_plan_profile "${id}"; done

log "build and install package for server coverage"
# shellcheck disable=SC1091
. packaging/package-toolchain.env
go install github.com/goreleaser/nfpm/v2/cmd/nfpm@"${NFPM_VERSION}"
export PATH="$(go env GOPATH)/bin:${PATH}"
PODLAZ_COMMIT="${GITHUB_SHA:-e2e-server-coverage}" PODLAZ_BUILT="${PODLAZ_E2E_BUILT:-$(date -u '+%b %d %Y')}" PODLAZ_DEB_ARCH="${PODLAZ_DEB_ARCH}" bash scripts/build-deb.sh 2>&1 | tee "${E2E_ARTIFACT_DIR}/server-coverage-build-deb.log"
test -f "${DEV_DEB}" || fail "expected package not found: ${DEV_DEB}"
sudo -n apt install -y "./${DEV_DEB}" 2>&1 | tee "${E2E_ARTIFACT_DIR}/server-coverage-apt-install.log"
PACKAGE_INSTALLED=1
sudo -n systemctl daemon-reload
sudo -n systemctl reset-failed podlazd.service || true
sudo -n systemctl start podlazd.service
SERVICE_TOUCHED=1
sudo -n systemctl is-active --quiet podlazd.service
collect_host_snapshot after-service-start

if [[ -n "${PODLAZ_E2E_SUBSCRIPTION_URL}" ]]; then
  log "real subscription source exercise"
  SUBSCRIPTION_E2E_HOME="$(mktemp -d "${E2E_TMP_ROOT}/server-coverage-subscription.XXXXXX")"
  mkdir -p "${SUBSCRIPTION_E2E_HOME}/config" "${SUBSCRIPTION_E2E_HOME}/state" "${SUBSCRIPTION_E2E_HOME}/cache"
  expect_secret_success subscription-add-secret run_podlaz_with_xdg_root "${SUBSCRIPTION_E2E_HOME}" subscription add --name e2e-real-sub --url "${PODLAZ_E2E_SUBSCRIPTION_URL}"
  SUB_ID="$(awk '/^Subscription added:/ {print $3}' "${LAST_STDOUT}")"
  assert_nonempty "${SUB_ID}" "real subscription id"
  expect_secret_success subscription-update-secret run_podlaz_with_xdg_root "${SUBSCRIPTION_E2E_HOME}" subscription update "${SUB_ID}"
  expect_secret_success subscription-list-secret run_podlaz_with_xdg_root "${SUBSCRIPTION_E2E_HOME}" subscription list
  expect_secret_success subscription-show-secret run_podlaz_with_xdg_root "${SUBSCRIPTION_E2E_HOME}" subscription show "${SUB_ID}"
  expect_secret_success subscription-delete-secret run_podlaz_with_xdg_root "${SUBSCRIPTION_E2E_HOME}" subscription delete "${SUB_ID}" --yes --keep-profiles
fi

log "proxy-only lifecycle across real profile set"
for id in "${PROFILE_IDS[@]}"; do run_lifecycle_for_profile proxy-only "${id}"; done

PRIMARY_PROFILE_ID="${PROFILE_IDS[0]}"
log "concurrent connect/disconnect/status probe"
concurrent_lifecycle_probe proxy-only "${PRIMARY_PROFILE_ID}"

if [[ "${PODLAZ_E2E_ENABLE_CRASH_TESTS}" == "true" ]]; then run_core_crash_probe proxy-only "${PRIMARY_PROFILE_ID}"; run_daemon_crash_probe proxy-only "${PRIMARY_PROFILE_ID}"; else log "crash probes are disabled; set PODLAZ_E2E_ENABLE_CRASH_TESTS=true to test daemon/core crash recovery"; fi

if [[ "${PODLAZ_E2E_ENABLE_TUN}" == "true" ]]; then
  log "TUN lifecycle across real profile set"
  for id in "${PROFILE_IDS[@]}"; do run_lifecycle_for_profile tun "${id}"; done
  log "TUN concurrent connect/disconnect/status probe"
  concurrent_lifecycle_probe tun "${PRIMARY_PROFILE_ID}"
  if [[ "${PODLAZ_E2E_ENABLE_CRASH_TESTS}" == "true" ]]; then run_core_crash_probe tun "${PRIMARY_PROFILE_ID}"; run_daemon_crash_probe tun "${PRIMARY_PROFILE_ID}"; fi
  run_host_disruption_probes "${PRIMARY_PROFILE_ID}"
  run_stability_probe tun "${PRIMARY_PROFILE_ID}" "${PODLAZ_E2E_STABILITY_MINUTES}"
else
  run_host_disruption_probes "${PRIMARY_PROFILE_ID}"
  run_stability_probe proxy-only "${PRIMARY_PROFILE_ID}" "${PODLAZ_E2E_STABILITY_MINUTES}"
fi

collect_host_snapshot final
log "server coverage e2e completed"
