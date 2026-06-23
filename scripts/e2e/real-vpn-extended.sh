#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"

require_cmd bash go python3 grep awk sed mktemp sudo systemctl journalctl apt curl getent ip

: "${PODLAZ_E2E_PROFILE_URI:=}"
: "${PODLAZ_E2E_PROFILE_URI_2:=}"
: "${PODLAZ_E2E_PROFILE_URI_3:=}"
: "${PODLAZ_E2E_PROFILE_URI_4:=}"
: "${PODLAZ_E2E_SUBSCRIPTION_URL:=}"
: "${PODLAZ_E2E_EXPECTED_EGRESS_IP:=}"
: "${PODLAZ_E2E_EXPECTED_EGRESS_IPV6:=}"
: "${PODLAZ_E2E_EXPECT_IPV6:=observe}"
: "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL:=https://api.ipify.org}"
: "${PODLAZ_E2E_PUBLIC_IPV6_CHECK_URL:=https://api6.ipify.org}"
: "${PODLAZ_E2E_DNS_CHECK_HOST:=github.com}"
: "${PODLAZ_E2E_ENABLE_TUN:=false}"
: "${PODLAZ_E2E_ENABLE_CRASH_TESTS:=false}"
: "${PODLAZ_E2E_STABILITY_MINUTES:=0}"
: "${PODLAZ_E2E_STATUS_CONCURRENCY:=6}"

[[ -n "${PODLAZ_E2E_PROFILE_URI}" ]] || fail "PODLAZ_E2E_PROFILE_URI environment secret is required for extended real VPN e2e"

for secret in \
  "${PODLAZ_E2E_PROFILE_URI}" \
  "${PODLAZ_E2E_PROFILE_URI_2}" \
  "${PODLAZ_E2E_PROFILE_URI_3}" \
  "${PODLAZ_E2E_PROFILE_URI_4}" \
  "${PODLAZ_E2E_SUBSCRIPTION_URL}" \
  "${PODLAZ_E2E_EXPECTED_EGRESS_IP}" \
  "${PODLAZ_E2E_EXPECTED_EGRESS_IPV6}"; do
  mask_value "${secret}"
done

build_podlaz_binary
setup_isolated_xdg "real-vpn-extended"
PODLAZ=("${PODLAZ_BIN}")
DEV_DEB="dist/podlaz_0.0.0~dev-1_linux_amd64.deb"
PACKAGE_INSTALLED=0
SERVICE_TOUCHED=0
ACTIVE_CONNECTION=0
ACTIVE_MODE=""

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
  if [[ -s "${LAST_STDOUT}" ]]; then
    sed -e 's/^/stdout: /' "${LAST_STDOUT}"
  fi
  if [[ -s "${LAST_STDERR}" ]]; then
    sed -e 's/^/stderr: /' "${LAST_STDERR}" >&2
  fi
  if [[ "${restore_errexit}" == "1" ]]; then
    set -e
  fi
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
  resolvectl status >"${dir}/resolvectl-status.txt" 2>&1 || true
  resolvectl query "${PODLAZ_E2E_DNS_CHECK_HOST}" >"${dir}/resolvectl-query.txt" 2>&1 || true
  getent hosts "${PODLAZ_E2E_DNS_CHECK_HOST}" >"${dir}/getent-hosts.txt" 2>&1 || true
  ss -ltnup >"${dir}/ss-ltnup.txt" 2>&1 || true
  if command -v nmcli >/dev/null 2>&1; then
    nmcli general status >"${dir}/nmcli-general-status.txt" 2>&1 || true
    nmcli device status >"${dir}/nmcli-device-status.txt" 2>&1 || true
    nmcli connection show --active >"${dir}/nmcli-active-connections.txt" 2>&1 || true
  fi
  sudo -n systemctl status podlazd.service --no-pager >"${dir}/podlazd.service.status" 2>&1 || true
  sudo -n journalctl -u podlazd.service -n 300 --no-pager >"${dir}/podlazd.service.journal" 2>&1 || true
  sudo -n nft list ruleset >"${dir}/nft-ruleset.txt" 2>&1 || true
}

cleanup_extended() {
  local code=$?
  if [[ "${ACTIVE_CONNECTION}" == "1" ]]; then
    run_podlaz_as_socket_user disconnect >"${E2E_ARTIFACT_DIR}/cleanup-disconnect.stdout" 2>"${E2E_ARTIFACT_DIR}/cleanup-disconnect.stderr" || true
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
trap cleanup_extended EXIT

import_profile_uri() {
  local label="$1"
  local uri="$2"
  local out err id
  [[ -n "${uri}" ]] || return 0
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

check_public_egress() {
  local phase="$1"
  local dir="${E2E_ARTIFACT_DIR}/egress-${phase}"
  mkdir -p "${dir}"
  local ip4="" ip6=""
  ip4="$(curl -4 -fsS --max-time 30 "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL}" 2>"${dir}/public-ipv4.stderr" || true)"
  mask_value "${ip4}"
  printf '%s\n' "${ip4}" >"${dir}/public-ipv4.txt"
  if [[ -n "${PODLAZ_E2E_EXPECTED_EGRESS_IP}" && "${ip4}" != "${PODLAZ_E2E_EXPECTED_EGRESS_IP}" ]]; then
    fail "${phase}: unexpected IPv4 egress IP"
  fi

  set +e
  ip6="$(curl -6 -fsS --max-time 30 "${PODLAZ_E2E_PUBLIC_IPV6_CHECK_URL}" 2>"${dir}/public-ipv6.stderr")"
  local ipv6_code=$?
  set -e
  mask_value "${ip6}"
  printf '%s\n' "${ip6}" >"${dir}/public-ipv6.txt"
  printf '%s\n' "${ipv6_code}" >"${dir}/public-ipv6.exit"

  case "${PODLAZ_E2E_EXPECT_IPV6}" in
    observe|"") ;;
    blocked)
      [[ "${ipv6_code}" != "0" ]] || fail "${phase}: IPv6 egress succeeded but PODLAZ_E2E_EXPECT_IPV6=blocked"
      ;;
    egress)
      [[ "${ipv6_code}" == "0" ]] || fail "${phase}: IPv6 egress failed but PODLAZ_E2E_EXPECT_IPV6=egress"
      if [[ -n "${PODLAZ_E2E_EXPECTED_EGRESS_IPV6}" && "${ip6}" != "${PODLAZ_E2E_EXPECTED_EGRESS_IPV6}" ]]; then
        fail "${phase}: unexpected IPv6 egress IP"
      fi
      ;;
    *) fail "unsupported PODLAZ_E2E_EXPECT_IPV6=${PODLAZ_E2E_EXPECT_IPV6}" ;;
  esac
}

status_concurrency_probe() {
  local label="$1"
  local workers="${PODLAZ_E2E_STATUS_CONCURRENCY}"
  local i
  mkdir -p "${E2E_ARTIFACT_DIR}/concurrency-${label}"
  for i in $(seq 1 "${workers}"); do
    (
      set +e
      for _ in $(seq 1 10); do
        run_podlaz_as_socket_user status >>"${E2E_ARTIFACT_DIR}/concurrency-${label}/status-${i}.log" 2>&1
        sleep 0.2
      done
    ) &
  done
  wait
}

connect_profile() {
  local mode="$1"
  local id="$2"
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
  local mode="$1"
  local id="$2"
  set +e
  capture_secret_command "second-connect-rejected-${mode}-${id}" run_podlaz_as_socket_user connect --mode "${mode}" "${id}"
  local code=$?
  set -e
  [[ "${code}" != "0" ]] || fail "second connect unexpectedly succeeded while another connection was active"
}

find_xray_pids() {
  pgrep -u podlaz -f 'xray.*run.*-config' || true
}

kill_supervised_core() {
  local pids
  pids="$(find_xray_pids)"
  [[ -n "${pids}" ]] || fail "no supervised xray process found to kill"
  printf '%s\n' "${pids}" >"${E2E_ARTIFACT_DIR}/killed-xray-pids.txt"
  # shellcheck disable=SC2086
  sudo -n kill -KILL ${pids}
  sleep 3
}

restart_daemon_after_crash() {
  sudo -n systemctl reset-failed podlazd.service || true
  sudo -n systemctl start podlazd.service || sudo -n systemctl restart podlazd.service
  for _ in $(seq 1 20); do
    if sudo -n systemctl is-active --quiet podlazd.service; then
      return 0
    fi
    sleep 1
  done
  sudo -n systemctl status podlazd.service --no-pager || true
  fail "podlazd.service did not become active after crash/restart"
}

run_proxy_lifecycle_for_profile() {
  local id="$1"
  connect_profile proxy-only "${id}"
  status_concurrency_probe "proxy-${id}"
  expect_second_connect_rejected proxy-only "${id}"
  capture_secret_command "status-proxy-${id}" run_podlaz_as_socket_user status || true
  disconnect_active "proxy-${id}"
  capture_secret_command "disconnect-idempotent-proxy-${id}" run_podlaz_as_socket_user disconnect || true
}

run_core_crash_probe() {
  local mode="$1"
  local id="$2"
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
  local mode="$1"
  local id="$2"
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

run_tun_lifecycle_for_profile() {
  local id="$1"
  connect_profile tun "${id}"
  collect_host_snapshot "tun-active-${id}"
  status_concurrency_probe "tun-${id}"
  expect_second_connect_rejected tun "${id}"
  capture_secret_command "status-tun-${id}" run_podlaz_as_socket_user status || true
  resolvectl query "${PODLAZ_E2E_DNS_CHECK_HOST}" >"${E2E_ARTIFACT_DIR}/tun-resolvectl-query-${id}.txt" 2>&1 || true
  getent hosts "${PODLAZ_E2E_DNS_CHECK_HOST}" >"${E2E_ARTIFACT_DIR}/tun-getent-${id}.txt" 2>&1 || true
  check_public_egress "tun-${id}"
  disconnect_active "tun-${id}"
  capture_secret_command "recover-after-tun-${id}" run_podlaz_as_socket_user recover || true
  capture_secret_command "recover-execute-after-tun-${id}" run_podlaz_as_socket_user recover --execute --yes || true
  collect_host_snapshot "tun-after-disconnect-${id}"
}

run_stability_probe() {
  local mode="$1"
  local id="$2"
  local minutes="$3"
  [[ "${minutes}" =~ ^[0-9]+$ ]] || fail "PODLAZ_E2E_STABILITY_MINUTES must be an integer"
  [[ "${minutes}" -gt 0 ]] || return 0
  local end now iter=0
  end=$(( $(date +%s) + minutes * 60 ))
  log "long-running stability probe (${mode}, ${minutes} min)"
  connect_profile "${mode}" "${id}"
  while true; do
    now=$(date +%s)
    [[ "${now}" -lt "${end}" ]] || break
    iter=$((iter + 1))
    capture_secret_command "stability-status-${mode}-${iter}" run_podlaz_as_socket_user status || true
    getent hosts "${PODLAZ_E2E_DNS_CHECK_HOST}" >"${E2E_ARTIFACT_DIR}/stability-getent-${mode}-${iter}.txt" 2>&1 || true
    check_public_egress "stability-${mode}-${iter}"
    sleep 30
  done
  disconnect_active "stability-${mode}"
}

log "host baseline diagnostics"
collect_host_snapshot baseline

log "import and validate real profile set"
: >"${E2E_ARTIFACT_DIR}/profile-ids.txt"
import_profile_uri primary "${PODLAZ_E2E_PROFILE_URI}"
import_profile_uri secondary "${PODLAZ_E2E_PROFILE_URI_2}"
import_profile_uri tertiary "${PODLAZ_E2E_PROFILE_URI_3}"
import_profile_uri quaternary "${PODLAZ_E2E_PROFILE_URI_4}"
mapfile -t PROFILE_IDS <"${E2E_ARTIFACT_DIR}/profile-ids.txt"
[[ "${#PROFILE_IDS[@]}" -gt 0 ]] || fail "no real profiles imported"
for id in "${PROFILE_IDS[@]}"; do
  validate_and_plan_profile "${id}"
done

log "build and install package for extended lifecycle"
# shellcheck disable=SC1091
. packaging/package-toolchain.env
go install github.com/goreleaser/nfpm/v2/cmd/nfpm@"${NFPM_VERSION}"
export PATH="$(go env GOPATH)/bin:${PATH}"
PODLAZ_COMMIT="${GITHUB_SHA:-e2e-real-vpn-extended}" \
PODLAZ_BUILT="${PODLAZ_E2E_BUILT:-$(date -u '+%b %d %Y')}" \
  bash scripts/build-deb.sh 2>&1 | tee "${E2E_ARTIFACT_DIR}/extended-build-deb.log"
sudo -n apt install -y "./${DEV_DEB}" 2>&1 | tee "${E2E_ARTIFACT_DIR}/extended-apt-install.log"
PACKAGE_INSTALLED=1
sudo -n systemctl daemon-reload
sudo -n systemctl reset-failed podlazd.service || true
sudo -n systemctl start podlazd.service
SERVICE_TOUCHED=1
sudo -n systemctl is-active --quiet podlazd.service
collect_host_snapshot after-service-start

if [[ -n "${PODLAZ_E2E_SUBSCRIPTION_URL}" ]]; then
  log "real subscription source exercise"
  expect_secret_success subscription-add-secret run_podlaz_as_socket_user subscription add --name e2e-real-sub --url "${PODLAZ_E2E_SUBSCRIPTION_URL}"
  SUB_ID="$(awk '/^Subscription added:/ {print $3}' "${LAST_STDOUT}")"
  assert_nonempty "${SUB_ID}" "real subscription id"
  expect_secret_success subscription-update-secret run_podlaz_as_socket_user subscription update "${SUB_ID}"
  expect_secret_success subscription-list-secret run_podlaz_as_socket_user subscription list
  expect_secret_success subscription-show-secret run_podlaz_as_socket_user subscription show "${SUB_ID}"
  expect_secret_success subscription-delete-secret run_podlaz_as_socket_user subscription delete "${SUB_ID}" --yes --keep-profiles
fi

log "proxy-only lifecycle across real profile set"
for id in "${PROFILE_IDS[@]}"; do
  run_proxy_lifecycle_for_profile "${id}"
done

PRIMARY_PROFILE_ID="${PROFILE_IDS[0]}"

if [[ "${PODLAZ_E2E_ENABLE_CRASH_TESTS}" == "true" ]]; then
  run_core_crash_probe proxy-only "${PRIMARY_PROFILE_ID}"
  run_daemon_crash_probe proxy-only "${PRIMARY_PROFILE_ID}"
else
  log "crash probes are disabled; set PODLAZ_E2E_ENABLE_CRASH_TESTS=true to test daemon/core crash recovery"
fi

if [[ "${PODLAZ_E2E_ENABLE_TUN}" == "true" ]]; then
  log "TUN lifecycle across real profile set"
  for id in "${PROFILE_IDS[@]}"; do
    run_tun_lifecycle_for_profile "${id}"
  done
  if [[ "${PODLAZ_E2E_ENABLE_CRASH_TESTS}" == "true" ]]; then
    run_core_crash_probe tun "${PRIMARY_PROFILE_ID}"
    run_daemon_crash_probe tun "${PRIMARY_PROFILE_ID}"
  fi
  run_stability_probe tun "${PRIMARY_PROFILE_ID}" "${PODLAZ_E2E_STABILITY_MINUTES}"
else
  run_stability_probe proxy-only "${PRIMARY_PROFILE_ID}" "${PODLAZ_E2E_STABILITY_MINUTES}"
fi

log "NetworkManager, DHCP, suspend/resume capability record"
{
  echo "NetworkManager destructive reconnect, DHCP renew, and suspend/resume are not executed by this script."
  echo "They are host/distribution/provider dependent and can strand a remote CI runner."
  echo "This run records nmcli/systemd/networking diagnostics instead."
} >"${E2E_ARTIFACT_DIR}/host-disruption-non-goals.txt"

collect_host_snapshot final
log "extended real VPN e2e completed"
