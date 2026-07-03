#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"

require_cmd bash go python3 grep awk sed mktemp sudo systemctl journalctl apt curl getent ip ss timeout dpkg

: "${PODLAZ_E2E_ENABLE_TUN_FAULT_INJECTION:=false}"
: "${PODLAZ_E2E_PROFILE_URI:=}"
: "${PODLAZ_E2E_PROFILE_URI_LIST:=}"
: "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL:=https://api.ipify.org}"
: "${PODLAZ_E2E_DNS_CHECK_HOST:=github.com}"
: "${PODLAZ_DEB_ARCH:=$(dpkg --print-architecture)}"

if [[ "${PODLAZ_E2E_ENABLE_TUN_FAULT_INJECTION}" != "true" ]]; then
  log "TUN fault-injection e2e is disabled; set PODLAZ_E2E_ENABLE_TUN_FAULT_INJECTION=true on a dedicated runner"
  exit 0
fi
if [[ -z "${PODLAZ_E2E_PROFILE_URI}" && -z "${PODLAZ_E2E_PROFILE_URI_LIST}" ]]; then
  fail "PODLAZ_E2E_PROFILE_URI or PODLAZ_E2E_PROFILE_URI_LIST is required for TUN fault-injection e2e"
fi
HOST_DEB_ARCH="$(dpkg --print-architecture)"
if [[ "${PODLAZ_DEB_ARCH}" != "${HOST_DEB_ARCH}" ]]; then
  fail "TUN fault-injection e2e must install a native package: PODLAZ_DEB_ARCH=${PODLAZ_DEB_ARCH}, host=${HOST_DEB_ARCH}"
fi

DEV_DEB="dist/podlaz_0.0.0~dev-1_linux_${PODLAZ_DEB_ARCH}.deb"
DAEMON_SOCKET="/run/podlaz/podlazd.sock"
HOOK_DIR="/run/podlaz/e2e-tun-hooks"
HOOK_DROPIN_DIR="/run/systemd/system/podlazd.service.d"
HOOK_DROPIN="${HOOK_DROPIN_DIR}/e2e-tun-hooks.conf"
PACKAGE_INSTALLED=0
SERVICE_TOUCHED=0
ACTIVE_CONNECT_PID=""

mask_multiline_sensitive() {
  local value="${1:-}"
  [[ -n "${value}" ]] || return 0
  mask_value "${value}"
  while IFS= read -r line; do
    [[ -n "${line}" ]] || continue
    mask_value "${line}"
  done <<<"${value}"
}

for sensitive in "${PODLAZ_E2E_PROFILE_URI}" "${PODLAZ_E2E_PROFILE_URI_LIST}"; do
  mask_multiline_sensitive "${sensitive}"
done

build_podlaz_binary
setup_isolated_xdg "tun-fault-injection"
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

collect_host_snapshot() {
  local name="$1" dir="${E2E_ARTIFACT_DIR}/host-${name}"
  mkdir -p "${dir}"
  date -u '+%Y-%m-%dT%H:%M:%SZ' >"${dir}/timestamp.txt"
  ip addr >"${dir}/ip-addr.txt" 2>&1 || true
  ip route >"${dir}/ip-route.txt" 2>&1 || true
  ip rule >"${dir}/ip-rule.txt" 2>&1 || true
  ss -ltnup >"${dir}/ss-ltnup.txt" 2>&1 || true
  if command -v resolvectl >/dev/null 2>&1; then
    resolvectl status >"${dir}/resolvectl-status.txt" 2>&1 || true
    resolvectl query "${PODLAZ_E2E_DNS_CHECK_HOST}" >"${dir}/resolvectl-query.txt" 2>&1 || true
  fi
  getent hosts "${PODLAZ_E2E_DNS_CHECK_HOST}" >"${dir}/getent-hosts.txt" 2>&1 || true
  sudo -n systemctl status podlazd.service --no-pager >"${dir}/podlazd.service.status" 2>&1 || true
  sudo -n journalctl -u podlazd.service -n 300 --no-pager >"${dir}/podlazd.service.journal" 2>&1 || true
  sudo -n nft list ruleset >"${dir}/nft-ruleset.txt" 2>&1 || true
}

wait_for_daemon_socket() {
  local attempt
  for attempt in $(seq 1 100); do
    [[ -S "${DAEMON_SOCKET}" ]] && return 0
    sleep 0.1
  done
  collect_host_snapshot socket-timeout
  fail "podlazd.service did not create daemon socket within readiness timeout: ${DAEMON_SOCKET}"
}

clear_tun_hook() {
  sudo -n rm -f -- "${HOOK_DROPIN}" >/dev/null 2>&1 || true
  sudo -n rm -rf -- "${HOOK_DIR}" >/dev/null 2>&1 || true
  sudo -n systemctl daemon-reload >/dev/null 2>&1 || true
}

configure_tun_hook() {
  local phase="$1"
  clear_tun_hook
  sudo -n mkdir -p "${HOOK_DROPIN_DIR}"
  local tmp
  tmp="$(mktemp "${E2E_TMP_ROOT}/podlaz-e2e-hook.XXXXXX")"
  cat >"${tmp}" <<EOF
[Service]
Environment=PODLAZ_E2E_TUN_HOOKS=true
Environment=PODLAZ_E2E_TUN_HOOK_PHASE=${phase}
Environment=PODLAZ_E2E_TUN_HOOK_DIR=${HOOK_DIR}
Environment=PODLAZ_E2E_TUN_HOOK_TIMEOUT_SECONDS=60
EOF
  sudo -n install -m 0644 "${tmp}" "${HOOK_DROPIN}"
  rm -f -- "${tmp}"
  sudo -n systemctl daemon-reload
  sudo -n systemctl restart podlazd.service
  SERVICE_TOUCHED=1
  wait_for_daemon_socket
}

cleanup_tun_fault_injection() {
  local code=$?
  if [[ -n "${ACTIVE_CONNECT_PID}" ]]; then
    wait "${ACTIVE_CONNECT_PID}" >/dev/null 2>&1 || true
  fi
  clear_tun_hook
  if [[ "${SERVICE_TOUCHED}" == "1" ]]; then
    sudo -n systemctl stop podlazd.service >/dev/null 2>&1 || true
  fi
  if [[ "${PACKAGE_INSTALLED}" == "1" && "${PODLAZ_E2E_KEEP_PACKAGE:-false}" != "true" ]]; then
    sudo -n apt remove -y podlaz >/dev/null 2>&1 || true
  fi
  exit "${code}"
}
trap cleanup_tun_fault_injection EXIT

check_direct_connectivity() {
  local phase="$1" dir="${E2E_ARTIFACT_DIR}/direct-${phase}"
  mkdir -p "${dir}"
  getent hosts "${PODLAZ_E2E_DNS_CHECK_HOST}" >"${dir}/getent-hosts.txt" 2>"${dir}/getent-hosts.stderr" || fail "${phase}: DNS resolution failed for ${PODLAZ_E2E_DNS_CHECK_HOST}"
  local ip4
  ip4="$(curl -4 -fsS --max-time 30 "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL}" 2>"${dir}/public-ipv4.stderr" || true)"
  mask_value "${ip4}"
  printf '%s\n' "${ip4}" >"${dir}/public-ipv4.txt"
  [[ -n "${ip4}" ]] || fail "${phase}: direct IPv4 egress check returned an empty response"
}

assert_recovery_candidates_empty() {
  local phase="$1"
  python3 - "${LAST_STDOUT}" "${phase}" <<'PY'
import json
import sys
path, phase = sys.argv[1], sys.argv[2]
with open(path, encoding="utf-8") as handle:
    payload = json.load(handle)
candidates = payload.get("recovery", {}).get("candidates", [])
if candidates:
    print(f"{phase}: recovery dry-run found podlaz-owned cleanup candidates", file=sys.stderr)
    print(json.dumps(candidates, ensure_ascii=False, indent=2), file=sys.stderr)
    sys.exit(1)
PY
}

assert_no_stale_state() {
  local phase="$1"
  expect_secret_success "status-${phase}" run_podlaz_as_socket_user status
  grep -F "Connection: inactive" "${LAST_STDOUT}" >/dev/null || fail "${phase}: status is not inactive"
  grep -F "Stale state: none" "${LAST_STDOUT}" >/dev/null || fail "${phase}: status reports stale state"
  expect_secret_success "doctor-${phase}" run_podlaz_as_socket_user doctor
  expect_secret_success "recover-${phase}-dry-run-json" run_podlaz_as_socket_user recover --json
  assert_json_file "${LAST_STDOUT}"
  assert_recovery_candidates_empty "${phase}"
}

run_apply_failure_probe() {
  local phase="$1" id="$2"
  log "TUN apply failure probe: ${phase}"
  configure_tun_hook "${phase}"
  collect_host_snapshot "before-${phase}"
  set +e
  capture_secret_command "connect-${phase}" run_podlaz_as_socket_user connect --mode tun "${id}"
  local code=$?
  set -e
  [[ "${code}" != "0" ]] || fail "${phase}: connect unexpectedly succeeded"
  clear_tun_hook
  sudo -n systemctl restart podlazd.service
  wait_for_daemon_socket
  expect_secret_success "recover-execute-${phase}" run_podlaz_as_socket_user recover --execute --yes
  check_direct_connectivity "after-${phase}"
  assert_no_stale_state "after-${phase}"
  collect_host_snapshot "after-${phase}"
}

run_before_commit_probe() {
  local id="$1" phase="before-commit-pause"
  log "TUN pre-commit interruption probe"
  configure_tun_hook "${phase}"
  collect_host_snapshot "before-${phase}"
  local safe="connect-${phase}"
  local out="${E2E_ARTIFACT_DIR}/$(safe_name "${safe}").stdout"
  local err="${E2E_ARTIFACT_DIR}/$(safe_name "${safe}").stderr"
  set +e
  run_podlaz_as_socket_user connect --mode tun "${id}" >"${out}" 2>"${err}" &
  ACTIVE_CONNECT_PID=$!
  set -e
  local attempt
  for attempt in $(seq 1 300); do
    if sudo -n test -f "${HOOK_DIR}/before-commit-pause.ready"; then
      sudo -n cat "${HOOK_DIR}/before-commit-pause.ready" >"${E2E_ARTIFACT_DIR}/before-commit-pause.marker" 2>&1 || true
      break
    fi
    sleep 0.1
  done
  sudo -n test -f "${HOOK_DIR}/before-commit-pause.ready" || fail "${phase}: marker was not created"
  sudo -n systemctl kill --kill-whom=main --signal=SIGKILL podlazd.service
  set +e
  wait "${ACTIVE_CONNECT_PID}"
  local code=$?
  set -e
  ACTIVE_CONNECT_PID=""
  [[ "${code}" != "0" ]] || fail "${phase}: connect unexpectedly succeeded"
  clear_tun_hook
  sudo -n systemctl reset-failed podlazd.service || true
  sudo -n systemctl start podlazd.service || sudo -n systemctl restart podlazd.service
  wait_for_daemon_socket
  expect_secret_success "recover-before-execute-${phase}" run_podlaz_as_socket_user recover
  grep -F "Transaction:" "${LAST_STDOUT}" >/dev/null || fail "${phase}: recover did not report pending transaction evidence"
  expect_secret_success "recover-execute-${phase}" run_podlaz_as_socket_user recover --execute --yes
  check_direct_connectivity "after-${phase}"
  assert_no_stale_state "after-${phase}"
  collect_host_snapshot "after-${phase}"
}

log "import primary profile for TUN fault-injection checks"
PRIMARY_URI="$(first_profile_uri)"
assert_nonempty "${PRIMARY_URI}" "primary real profile URI"
expect_secret_success import-primary-profile "${PODLAZ[@]}" profile import "${PRIMARY_URI}"
PROFILE_ID="$(awk '/^Imported profile:/ {print $3}' "${LAST_STDOUT}")"
assert_nonempty "${PROFILE_ID}" "primary profile id"
assert_not_contains "${LAST_STDOUT}" "${PRIMARY_URI}"
expect_success validate-primary-tun "${PODLAZ[@]}" profile validate "${PROFILE_ID}" --mode tun

log "build and install package for TUN fault-injection checks"
# shellcheck disable=SC1091
. packaging/package-toolchain.env
go install github.com/goreleaser/nfpm/v2/cmd/nfpm@"${NFPM_VERSION}"
export PATH="$(go env GOPATH)/bin:${PATH}"
PODLAZ_COMMIT="${GITHUB_SHA:-e2e-tun-fault-injection}" PODLAZ_BUILT="${PODLAZ_E2E_BUILT:-$(date -u '+%b %d %Y')}" PODLAZ_DEB_ARCH="${PODLAZ_DEB_ARCH}" bash scripts/build-deb.sh 2>&1 | tee "${E2E_ARTIFACT_DIR}/tun-fault-build-deb.log"
test -f "${DEV_DEB}" || fail "expected package not found: ${DEV_DEB}"
sudo -n apt install -y "./${DEV_DEB}" 2>&1 | tee "${E2E_ARTIFACT_DIR}/tun-fault-apt-install.log"
PACKAGE_INSTALLED=1
sudo -n systemctl daemon-reload
sudo -n systemctl reset-failed podlazd.service || true
sudo -n systemctl start podlazd.service
SERVICE_TOUCHED=1
wait_for_daemon_socket

run_apply_failure_probe dns-apply "${PROFILE_ID}"
run_apply_failure_probe route-apply "${PROFILE_ID}"
run_before_commit_probe "${PROFILE_ID}"
assert_artifacts_do_not_contain_sensitive_values "tun-fault-injection" "${PODLAZ_E2E_PROFILE_URI}" "${PODLAZ_E2E_PROFILE_URI_LIST}"

log "TUN fault-injection e2e completed"
