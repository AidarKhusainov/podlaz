#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"

require_cmd awk bash curl dpkg find getent go grep ip journalctl mktemp nft pgrep python3 resolvectl sed sleep sudo systemctl systemd-run timeout

: "${PODLAZ_E2E_PROFILE_URI:=}"
: "${PODLAZ_E2E_PROFILE_URI_LIST:=}"
: "${PODLAZ_E2E_DNS_CHECK_HOST:=github.com}"
: "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL:=https://api.ipify.org}"
: "${PODLAZ_DEB_ARCH:=$(dpkg --print-architecture)}"

if [[ -z "${PODLAZ_E2E_PROFILE_URI}" && -z "${PODLAZ_E2E_PROFILE_URI_LIST}" ]]; then
  fail "PODLAZ_E2E_PROFILE_URI or PODLAZ_E2E_PROFILE_URI_LIST is required"
fi
if [[ "${PODLAZ_DEB_ARCH}" != "$(dpkg --print-architecture)" ]]; then
  fail "package convergence E2E requires a native .deb"
fi

DEV_DEB="dist/podlaz_0.0.0~dev-1_linux_${PODLAZ_DEB_ARCH}.deb"
DAEMON_SOCKET="/run/podlaz/podlazd.sock"
HOOK_DIR="/run/podlaz/e2e-tun-hooks"
HOOK_DROPIN_DIR="/run/systemd/system/podlazd.service.d"
HOOK_DROPIN="${HOOK_DROPIN_DIR}/e2e-tun-hooks.conf"
HOOK_READY="${HOOK_DIR}/dns-missing-link.ready"
HOOK_CONTINUE="${HOOK_DIR}/dns-missing-link.continue"
HOOK_EVENTS="${HOOK_DIR}/events.log"
DIAGNOSTIC_REPORT="/run/podlaz/diagnostics/tun-last.json"

FOREIGN_NFT_FAMILY="inet"
FOREIGN_NFT_TABLE="podlaz_e2e_foreign_guard"
FOREIGN_ROUTE_TABLE="42424"
FOREIGN_ROUTE_CIDR="198.51.100.254/32"
FOREIGN_RULE_PRIORITY="42424"
FOREIGN_DNS_LINK="podlaz-e2e-dns0"
FOREIGN_DNS_SERVER="192.0.2.53"
FOREIGN_DNS_DOMAIN="~e2e.invalid"
FOREIGN_SERVICE="podlaz-e2e-foreign.service"

PACKAGE_INSTALLED=0
SERVICE_TOUCHED=0
CONNECT_PID=""

mask_multiline_sensitive() {
  local value="${1:-}"
  [[ -n "${value}" ]] || return 0
  mask_value "${value}"
  while IFS= read -r line; do
    [[ -n "${line}" ]] && mask_value "${line}"
  done <<<"${value}"
}
for sensitive in "${PODLAZ_E2E_PROFILE_URI}" "${PODLAZ_E2E_PROFILE_URI_LIST}"; do
  mask_multiline_sensitive "${sensitive}"
done

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
  fail "podlazd.service did not create ${DAEMON_SOCKET}"
}

run_installed_podlaz() {
  sudo -n -u "$(id -un)" -g podlaz env \
    XDG_CONFIG_HOME="${XDG_CONFIG_HOME}" \
    XDG_STATE_HOME="${XDG_STATE_HOME}" \
    XDG_CACHE_HOME="${XDG_CACHE_HOME}" \
    /usr/bin/podlaz "$@"
}

capture_secret_import() {
  local uri="$1"
  local out="${E2E_ARTIFACT_DIR}/profile-import.stdout"
  local err="${E2E_ARTIFACT_DIR}/profile-import.stderr"
  log "import profile: arguments contain secret material and are not printed"
  if ! "${PODLAZ_BIN}" profile import "${uri}" >"${out}" 2>"${err}"; then
    sed -e 's/^/stderr: /' "${err}" >&2 || true
    fail "profile import failed"
  fi
  PROFILE_ID="$(awk '/^Imported profile:/ {print $3}' "${out}")"
  assert_nonempty "${PROFILE_ID}" "imported profile id"
  assert_not_contains "${out}" "${uri}"
}

clear_hook() {
  sudo -n rm -f -- "${HOOK_DROPIN}" >/dev/null 2>&1 || true
  sudo -n rm -rf -- "${HOOK_DIR}" >/dev/null 2>&1 || true
  sudo -n systemctl daemon-reload >/dev/null 2>&1 || true
}

configure_missing_link_hook() {
  clear_hook
  sudo -n mkdir -p "${HOOK_DROPIN_DIR}"
  local tmp
  tmp="$(mktemp "${E2E_TMP_ROOT}/podlaz-package-hook.XXXXXX")"
  cat >"${tmp}" <<EOF
[Service]
Environment=PODLAZ_E2E_TUN_HOOKS=true
Environment=PODLAZ_E2E_TUN_HOOK_PHASE=dns-missing-link-rollback
Environment=PODLAZ_E2E_TUN_HOOK_DIR=${HOOK_DIR}
Environment=PODLAZ_E2E_TUN_HOOK_TIMEOUT_SECONDS=90
EOF
  sudo -n install -m 0644 "${tmp}" "${HOOK_DROPIN}"
  rm -f -- "${tmp}"
  sudo -n systemctl daemon-reload
  sudo -n systemctl restart podlazd.service
  SERVICE_TOUCHED=1
  wait_for_daemon_socket
}

create_foreign_state() {
  sudo -n nft delete table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}" >/dev/null 2>&1 || true
  sudo -n nft add table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}"

  sudo -n ip -4 rule del priority "${FOREIGN_RULE_PRIORITY}" >/dev/null 2>&1 || true
  sudo -n ip -4 route flush table "${FOREIGN_ROUTE_TABLE}" >/dev/null 2>&1 || true
  sudo -n ip -4 route add blackhole "${FOREIGN_ROUTE_CIDR}" table "${FOREIGN_ROUTE_TABLE}"
  sudo -n ip -4 rule add priority "${FOREIGN_RULE_PRIORITY}" to "${FOREIGN_ROUTE_CIDR}" lookup "${FOREIGN_ROUTE_TABLE}"

  sudo -n ip link del dev "${FOREIGN_DNS_LINK}" >/dev/null 2>&1 || true
  sudo -n ip link add "${FOREIGN_DNS_LINK}" type dummy
  sudo -n ip link set dev "${FOREIGN_DNS_LINK}" up
  sudo -n resolvectl dns "${FOREIGN_DNS_LINK}" "${FOREIGN_DNS_SERVER}"
  sudo -n resolvectl domain "${FOREIGN_DNS_LINK}" "${FOREIGN_DNS_DOMAIN}"
  sudo -n resolvectl default-route "${FOREIGN_DNS_LINK}" no

  sudo -n systemctl stop "${FOREIGN_SERVICE}" >/dev/null 2>&1 || true
  sudo -n systemd-run --unit="${FOREIGN_SERVICE%.service}" --property=Type=simple /bin/sh -c 'exec sleep 600' >/dev/null
}

assert_foreign_state() {
  local phase="$1"
  sudo -n nft list table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}" >"${E2E_ARTIFACT_DIR}/${phase}-foreign-nft.txt"
  sudo -n ip -4 route show table "${FOREIGN_ROUTE_TABLE}" >"${E2E_ARTIFACT_DIR}/${phase}-foreign-route.txt"
  grep -F "blackhole ${FOREIGN_ROUTE_CIDR}" "${E2E_ARTIFACT_DIR}/${phase}-foreign-route.txt" >/dev/null || fail "${phase}: unrelated route was changed"
  sudo -n ip -4 rule show >"${E2E_ARTIFACT_DIR}/${phase}-foreign-rules.txt"
  grep -F "${FOREIGN_RULE_PRIORITY}:" "${E2E_ARTIFACT_DIR}/${phase}-foreign-rules.txt" | grep -F "to ${FOREIGN_ROUTE_CIDR}" | grep -E 'lookup (42424|table 42424)' >/dev/null || fail "${phase}: unrelated policy rule was changed"
  sudo -n resolvectl status "${FOREIGN_DNS_LINK}" --no-pager >"${E2E_ARTIFACT_DIR}/${phase}-foreign-resolved.txt"
  grep -F "${FOREIGN_DNS_SERVER}" "${E2E_ARTIFACT_DIR}/${phase}-foreign-resolved.txt" >/dev/null || fail "${phase}: unrelated per-link DNS server was changed"
  grep -F "${FOREIGN_DNS_DOMAIN}" "${E2E_ARTIFACT_DIR}/${phase}-foreign-resolved.txt" >/dev/null || fail "${phase}: unrelated per-link DNS domain was changed"
  sudo -n systemctl is-active --quiet "${FOREIGN_SERVICE}" || fail "${phase}: unrelated service state was changed"
}

assert_podlaz_resources_absent() {
  local phase="$1"
  if sudo -n ip link show dev podlaz0 >"${E2E_ARTIFACT_DIR}/${phase}-podlaz0.txt" 2>&1; then
    fail "${phase}: podlaz0 still exists"
  fi

  sudo -n ip -4 route show table 51820 >"${E2E_ARTIFACT_DIR}/${phase}-podlaz-routes-51820.txt" 2>&1 || true
  [[ ! -s "${E2E_ARTIFACT_DIR}/${phase}-podlaz-routes-51820.txt" ]] || fail "${phase}: podlaz route table 51820 is not empty"
  sudo -n ip -4 route show table podlaz >"${E2E_ARTIFACT_DIR}/${phase}-podlaz-routes-named.txt" 2>&1 || true
  if grep -v -E '^(Error: ipv4: FIB table does not exist|Dump terminated)$' "${E2E_ARTIFACT_DIR}/${phase}-podlaz-routes-named.txt" | grep -q '[^[:space:]]'; then
    fail "${phase}: named podlaz route table is not empty"
  fi

  sudo -n ip -4 rule show >"${E2E_ARTIFACT_DIR}/${phase}-rules.txt"
  if grep -E 'lookup (podlaz|51820)([[:space:]]|$)' "${E2E_ARTIFACT_DIR}/${phase}-rules.txt" >/dev/null; then
    fail "${phase}: podlaz policy rule remains"
  fi

  sudo -n resolvectl status --no-pager >"${E2E_ARTIFACT_DIR}/${phase}-resolved.txt"
  if grep -E '^Link [0-9]+ \(podlaz0\)$' "${E2E_ARTIFACT_DIR}/${phase}-resolved.txt" >/dev/null; then
    fail "${phase}: systemd-resolved still has a podlaz0 link record"
  fi

  if sudo -n nft list table inet podlaz >"${E2E_ARTIFACT_DIR}/${phase}-podlaz-nft.txt" 2>&1; then
    fail "${phase}: inet podlaz still exists"
  fi
  if pgrep -x xray >"${E2E_ARTIFACT_DIR}/${phase}-xray-pids.txt" 2>&1; then
    fail "${phase}: Xray process still exists"
  fi
  if sudo -n test -d /run/podlaz/generated && sudo -n find /run/podlaz/generated -mindepth 1 -print -quit | grep -q .; then
    sudo -n find /run/podlaz/generated -maxdepth 2 -printf '%y %p\n' >"${E2E_ARTIFACT_DIR}/${phase}-generated.txt"
    fail "${phase}: generated runtime config remains"
  fi
}

assert_no_recovery_candidates() {
  expect_success "recover-${1}-json" run_installed_podlaz recover --json
  python3 - "${LAST_STDOUT}" <<'PY'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as handle:
    payload = json.load(handle)
if payload.get("recovery", {}).get("candidates"):
    raise SystemExit("recovery candidates remain after rollback")
PY
}

assert_event_order() {
  local first="$1" second="$2"
  python3 - "${E2E_ARTIFACT_DIR}/missing-link-events.log" "${first}" "${second}" <<'PY'
import sys
path, first, second = sys.argv[1:]
with open(path, encoding="utf-8") as handle:
    events = [line.strip() for line in handle if line.strip()]
if first not in events or second not in events:
    raise SystemExit(f"missing event: {first!r} or {second!r}; events={events}")
if events.index(first) >= events.index(second):
    raise SystemExit(f"invalid event order: {first!r} >= {second!r}; events={events}")
PY
}

check_direct_connectivity() {
  getent hosts "${PODLAZ_E2E_DNS_CHECK_HOST}" >"${E2E_ARTIFACT_DIR}/direct-getent.txt"
  local ip4
  ip4="$(curl -4 -fsS --max-time 30 "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL}")"
  mask_value "${ip4}"
  [[ -n "${ip4}" ]] || fail "direct IPv4 egress returned an empty response"
  printf '%s\n' "${ip4}" >"${E2E_ARTIFACT_DIR}/direct-public-ipv4.txt"
}

cleanup() {
  local code=$?
  if [[ -n "${CONNECT_PID}" ]]; then
    wait "${CONNECT_PID}" >/dev/null 2>&1 || true
  fi
  clear_hook
  sudo -n systemctl stop "${FOREIGN_SERVICE}" >/dev/null 2>&1 || true
  sudo -n systemctl reset-failed "${FOREIGN_SERVICE}" >/dev/null 2>&1 || true
  sudo -n resolvectl revert "${FOREIGN_DNS_LINK}" >/dev/null 2>&1 || true
  sudo -n ip link del dev "${FOREIGN_DNS_LINK}" >/dev/null 2>&1 || true
  sudo -n ip -4 rule del priority "${FOREIGN_RULE_PRIORITY}" >/dev/null 2>&1 || true
  sudo -n ip -4 route flush table "${FOREIGN_ROUTE_TABLE}" >/dev/null 2>&1 || true
  sudo -n nft delete table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}" >/dev/null 2>&1 || true
  if [[ "${SERVICE_TOUCHED}" == "1" ]]; then
    sudo -n systemctl stop podlazd.service >/dev/null 2>&1 || true
  fi
  if [[ "${PACKAGE_INSTALLED}" == "1" && "${PODLAZ_E2E_KEEP_PACKAGE:-false}" != "true" ]]; then
    sudo -n apt purge -y podlaz >/dev/null 2>&1 || true
  fi
  sudo -n systemctl daemon-reload >/dev/null 2>&1 || true
  exit "${code}"
}
trap cleanup EXIT

build_podlaz_binary
setup_isolated_xdg "tun-package-convergence"
PRIMARY_URI="$(first_profile_uri)"
assert_nonempty "${PRIMARY_URI}" "primary profile URI"
capture_secret_import "${PRIMARY_URI}"

log "build and install release-like package"
# shellcheck disable=SC1091
. packaging/package-toolchain.env
go install github.com/goreleaser/nfpm/v2/cmd/nfpm@"${NFPM_VERSION}"
export PATH="$(go env GOPATH)/bin:${PATH}"
PODLAZ_COMMIT="${GITHUB_SHA:-e2e-package-convergence}" PODLAZ_BUILT="${PODLAZ_E2E_BUILT:-$(date -u '+%b %d %Y')}" PODLAZ_DEB_ARCH="${PODLAZ_DEB_ARCH}" bash scripts/build-deb.sh >"${E2E_ARTIFACT_DIR}/build-deb.log" 2>&1
test -f "${DEV_DEB}" || fail "expected package not found: ${DEV_DEB}"
sudo -n apt install -y "./${DEV_DEB}" >"${E2E_ARTIFACT_DIR}/apt-install.log" 2>&1
PACKAGE_INSTALLED=1
sudo -n systemctl daemon-reload
sudo -n systemctl restart podlazd.service
SERVICE_TOUCHED=1
wait_for_daemon_socket

[[ ! -e /usr/bin/podlazd ]] || true
/usr/bin/podlaz version >"${E2E_ARTIFACT_DIR}/installed-version.txt"
sudo -n systemctl show -p ExecStart podlazd.service >"${E2E_ARTIFACT_DIR}/installed-service-exec.txt"
grep -F "/usr/bin/podlazd" "${E2E_ARTIFACT_DIR}/installed-service-exec.txt" >/dev/null || fail "installed service does not execute /usr/bin/podlazd"

create_foreign_state
assert_foreign_state before
sudo -n systemctl is-active systemd-resolved >"${E2E_ARTIFACT_DIR}/resolved-service-before.txt"
ip -4 route show default >"${E2E_ARTIFACT_DIR}/default-route-before.txt"

configure_missing_link_hook
log "start installed-package TUN connect and wait after DNS apply"
run_installed_podlaz connect --mode tun "${PROFILE_ID}" >"${E2E_ARTIFACT_DIR}/connect.stdout" 2>"${E2E_ARTIFACT_DIR}/connect.stderr" &
CONNECT_PID=$!
for _ in $(seq 1 900); do
  sudo -n test -f "${HOOK_READY}" && break
  sleep 0.1
done
sudo -n test -f "${HOOK_READY}" || fail "daemon did not reach DNS missing-link rollback pause"
sudo -n ip link show dev podlaz0 >"${E2E_ARTIFACT_DIR}/podlaz0-before-delete.txt"
sudo -n resolvectl status podlaz0 --no-pager >"${E2E_ARTIFACT_DIR}/podlaz0-resolved-before-delete.txt"

log "delete real podlaz0 and capture real resolvectl missing-link outcome"
sudo -n ip link del dev podlaz0
set +e
sudo -n resolvectl revert podlaz0 >"${E2E_ARTIFACT_DIR}/real-resolvectl-revert.stdout" 2>"${E2E_ARTIFACT_DIR}/real-resolvectl-revert.stderr"
REAL_REVERT_CODE=$?
set -e
[[ "${REAL_REVERT_CODE}" == "1" ]] || fail "real resolvectl revert exit=${REAL_REVERT_CODE}, expected 1"
[[ ! -s "${E2E_ARTIFACT_DIR}/real-resolvectl-revert.stdout" ]] || fail "real resolvectl missing-link stdout is not empty"
EXPECTED_RESOLVED_ERROR='Failed to resolve interface "podlaz0": No such device'
[[ "$(tr -d '\r\n' <"${E2E_ARTIFACT_DIR}/real-resolvectl-revert.stderr")" == "${EXPECTED_RESOLVED_ERROR}" ]] || fail "real resolvectl missing-link stderr did not match exact supported result"

sudo -n touch "${HOOK_CONTINUE}"
set +e
wait "${CONNECT_PID}"
CONNECT_CODE=$?
set -e
CONNECT_PID=""
[[ "${CONNECT_CODE}" != "0" ]] || fail "missing-link fault connect unexpectedly succeeded"
sudo -n cat "${HOOK_EVENTS}" >"${E2E_ARTIFACT_DIR}/missing-link-events.log"
for event in dns-missing-link-ready dns-missing-link-released diagnostics-persisted rollback-started rollback-completed; do
  grep -Fx "${event}" "${E2E_ARTIFACT_DIR}/missing-link-events.log" >/dev/null || fail "missing lifecycle event: ${event}"
done
assert_event_order dns-missing-link-ready dns-missing-link-released
assert_event_order diagnostics-persisted rollback-started
sudo -n cat "${DIAGNOSTIC_REPORT}" >"${E2E_ARTIFACT_DIR}/missing-link-report.json"
assert_json_file "${E2E_ARTIFACT_DIR}/missing-link-report.json"

clear_hook
sudo -n systemctl restart podlazd.service
wait_for_daemon_socket
expect_exit 3 historical-doctor run_installed_podlaz doctor --tun --json
assert_json_file "${LAST_STDOUT}"
assert_no_recovery_candidates after-missing-link
assert_podlaz_resources_absent after-missing-link
assert_foreign_state after-missing-link

log "immediate installed-package retry"
expect_success retry-connect run_installed_podlaz connect --mode tun "${PROFILE_ID}"
expect_success retry-disconnect run_installed_podlaz disconnect
assert_no_recovery_candidates after-retry
assert_podlaz_resources_absent after-retry
assert_foreign_state after-retry
check_direct_connectivity

sudo -n systemctl is-active systemd-resolved >"${E2E_ARTIFACT_DIR}/resolved-service-after.txt"
cmp -s "${E2E_ARTIFACT_DIR}/resolved-service-before.txt" "${E2E_ARTIFACT_DIR}/resolved-service-after.txt" || fail "systemd-resolved service state changed"
ip -4 route show default >"${E2E_ARTIFACT_DIR}/default-route-after.txt"
cmp -s "${E2E_ARTIFACT_DIR}/default-route-before.txt" "${E2E_ARTIFACT_DIR}/default-route-after.txt" || fail "unrelated default route changed"

assert_artifacts_do_not_contain_sensitive_values "tun-package-convergence" "${PODLAZ_E2E_PROFILE_URI}" "${PODLAZ_E2E_PROFILE_URI_LIST}"
log "installed-package TUN convergence E2E completed"
