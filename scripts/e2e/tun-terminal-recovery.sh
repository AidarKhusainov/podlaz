#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"
# shellcheck source=lib/host_state.sh
source "${SCRIPT_DIR}/lib/host_state.sh"
# shellcheck source=lib/profile_input.sh
source "${SCRIPT_DIR}/lib/profile_input.sh"
# shellcheck source=lib/package_runtime_provenance.sh
source "${SCRIPT_DIR}/lib/package_runtime_provenance.sh"
# shellcheck source=lib/tun_package_assertions.sh
source "${SCRIPT_DIR}/lib/tun_package_assertions.sh"
# shellcheck source=lib/tun_foreign_state.sh
source "${SCRIPT_DIR}/lib/tun_foreign_state.sh"

require_cmd \
  awk apt bash cat chmod curl dirname dpkg dpkg-deb dpkg-query env find getent git grep id \
  install ip mktemp mkdir nft python3 readlink resolvectl rm sed seq sha256sum sleep sort stat \
  sudo systemctl systemd-run timeout tr

: "${PODLAZ_E2E_PROFILE_URI:=}"
: "${PODLAZ_E2E_PROFILE_URI_LIST:=}"
: "${PODLAZ_E2E_HTTPS_CHECK_URL:=https://example.com/}"
[[ "${PODLAZ_E2E_HTTPS_CHECK_URL}" == https://* ]] || fail "PODLAZ_E2E_HTTPS_CHECK_URL must use HTTPS"

usage() {
  printf 'Usage: %s EXACT-CANDIDATE.deb EXACT-V0.2.40.deb\n' "$0" >&2
}

(($# == 2)) || { usage; exit 2; }
CANDIDATE="$(readlink -f -- "$1")"
PREVIOUS="$(readlink -f -- "$2")"
[[ -f "${CANDIDATE}" && ! -L "${CANDIDATE}" ]] || fail "candidate package must be a regular file"
[[ -f "${PREVIOUS}" && ! -L "${PREVIOUS}" ]] || fail "v0.2.40 package must be a regular file"
[[ -n "${PODLAZ_E2E_PROFILE_URI}" || -n "${PODLAZ_E2E_PROFILE_URI_LIST}" ]] || \
  fail "PODLAZ_E2E_PROFILE_URI or PODLAZ_E2E_PROFILE_URI_LIST is required"

REPO_ROOT="$(git -C "${SCRIPT_DIR}/../.." rev-parse --show-toplevel)" || fail "cannot resolve source checkout"
SOURCE_HEAD="$(git -C "${REPO_ROOT}" rev-parse HEAD)" || fail "cannot resolve source HEAD"
[[ "${SOURCE_HEAD}" =~ ^[0-9a-f]{40}$ ]] || fail "source HEAD is not a full commit identity"

HOST_ARCH="$(dpkg --print-architecture)"
CANDIDATE_ARCH="$(dpkg-deb --field "${CANDIDATE}" Architecture)"
PREVIOUS_ARCH="$(dpkg-deb --field "${PREVIOUS}" Architecture)"
CANDIDATE_VERSION="$(dpkg-deb --field "${CANDIDATE}" Version)"
PREVIOUS_VERSION="$(dpkg-deb --field "${PREVIOUS}" Version)"
[[ "$(dpkg-deb --field "${CANDIDATE}" Package)" == podlaz ]] || fail "candidate package is not podlaz"
[[ "$(dpkg-deb --field "${PREVIOUS}" Package)" == podlaz ]] || fail "v0.2.40 package is not podlaz"
[[ "${CANDIDATE_ARCH}" == "${HOST_ARCH}" && "${PREVIOUS_ARCH}" == "${HOST_ARCH}" ]] || \
  fail "candidate and v0.2.40 package architectures must match host ${HOST_ARCH}"
[[ "${PREVIOUS_VERSION%%-*}" == "0.2.40" ]] || \
  fail "previous package must be exact v0.2.40, got ${PREVIOUS_VERSION}"
dpkg --compare-versions "${CANDIDATE_VERSION}" gt "${PREVIOUS_VERSION}" || \
  fail "candidate version ${CANDIDATE_VERSION} must be newer than ${PREVIOUS_VERSION}"

DAEMON_SOCKET="/run/podlaz/podlazd.sock"
HOOK_DIR="/run/podlaz/e2e-terminal-recovery"
HOOK_DROPIN_DIR="/run/systemd/system/podlazd.service.d"
HOOK_DROPIN="${HOOK_DROPIN_DIR}/99-e2e-terminal-recovery.conf"
HOOK_MARKER="${HOOK_DIR}/terminal-firewall-rollback.injected"
SESSION_STATE="/run/podlaz/network-session-continuation.json"
TRANSACTION_DIR="/run/podlaz/transactions"
FALLBACK_NETWORK_HELPER="${SCRIPT_DIR}/tun-package-fallback-network.py"

PRIVATE_TX="${E2E_TMP_ROOT}/terminal-recovery-transaction.json"
PRIVATE_SESSION="${E2E_TMP_ROOT}/terminal-recovery-session.json"
PRIVATE_ADDR="${E2E_TMP_ROOT}/terminal-recovery-ip-addr.json"
PRIVATE_ROUTES="${E2E_TMP_ROOT}/terminal-recovery-ip-routes.json"
PRIVATE_RULES="${E2E_TMP_ROOT}/terminal-recovery-ip-rules.json"
PRIVATE_NFT="${E2E_TMP_ROOT}/terminal-recovery-nft-tables.json"
PRIVATE_MANIFEST="${E2E_TMP_ROOT}/terminal-recovery-network-manifest.json"

TX_PATH=""
TX_ID=""
CHILD_PID=""
CHILD_START=""
TUN_IFACE=""
TUN_CIDR=""
PACKAGE_TOUCHED=0
EXPECTED_RUNTIME_DEB=""
EXPECTED_RUNTIME_PHASE=""
FOREIGN_STATE_CREATED=0

V0240_PRE_TX_ID=""
V0240_PRE_CHILD_PID=""
V0240_PRE_CHILD_START=""

mask_multiline_sensitive() {
  local value="${1:-}" line
  [[ -n "${value}" ]] || return 0
  mask_value "${value}"
  while IFS= read -r line; do
    [[ -n "${line}" ]] && mask_value "${line}"
  done <<<"${value}"
}
for sensitive in "${PODLAZ_E2E_PROFILE_URI}" "${PODLAZ_E2E_PROFILE_URI_LIST}"; do
  mask_multiline_sensitive "${sensitive}"
done

setup_isolated_xdg "tun-terminal-recovery"

run_client() {
  sudo -n -u "$(id -un)" -g podlaz env \
    XDG_CONFIG_HOME="${XDG_CONFIG_HOME}" \
    XDG_STATE_HOME="${XDG_STATE_HOME}" \
    XDG_CACHE_HOME="${XDG_CACHE_HOME}" \
    /usr/bin/podlaz "$@"
}

capture_secret_command() {
  local name="$1"
  shift
  local safe code restore_errexit=0
  case $- in *e*) restore_errexit=1 ;; esac
  safe="$(safe_name "${name}")"
  E2E_STEP=$((E2E_STEP + 1))
  LAST_STDOUT="${E2E_TMP_ROOT}/$(printf '%03d' "${E2E_STEP}")-${safe}.stdout"
  LAST_STDERR="${E2E_TMP_ROOT}/$(printf '%03d' "${E2E_STEP}")-${safe}.stderr"
  log "${name}: private command output retained outside public artifacts"
  set +e
  "$@" >"${LAST_STDOUT}" 2>"${LAST_STDERR}"
  code=$?
  chmod 0600 "${LAST_STDOUT}" "${LAST_STDERR}"
  ((restore_errexit == 1)) && set -e
  return "${code}"
}

expect_secret_success() {
  local name="$1"
  shift
  set +e
  capture_secret_command "${name}" "$@"
  local code=$?
  set -e
  [[ "${code}" == 0 ]] || fail "${name} failed with exit ${code}; inspect private E2E output"
}

expect_secret_exit() {
  local want="$1" name="$2"
  shift 2
  set +e
  capture_secret_command "${name}" "$@"
  local code=$?
  set -e
  [[ "${code}" == "${want}" ]] || fail "${name}: expected exit ${want}, got ${code}"
}

wait_for_daemon_socket() {
  local attempt
  for attempt in $(seq 1 100); do
    if [[ -S "${DAEMON_SOCKET}" ]]; then
      [[ -n "${EXPECTED_RUNTIME_DEB}" && -n "${EXPECTED_RUNTIME_PHASE}" ]] || \
        fail "package/runtime provenance expectation is not configured"
      assert_exact_package_runtime_provenance "${EXPECTED_RUNTIME_DEB}" "${EXPECTED_RUNTIME_PHASE}"
      return 0
    fi
    sleep 0.1
  done
  fail "podlazd socket did not become ready"
}

install_exact_package() {
  local deb="$1" phase="$2" allow_downgrade="${3:-false}"
  EXPECTED_RUNTIME_DEB="${deb}"
  EXPECTED_RUNTIME_PHASE="${phase}"
  if [[ "${allow_downgrade}" == true ]]; then
    sudo -n apt install --allow-downgrades -y "${deb}"
  else
    sudo -n apt install -y "${deb}"
  fi
  PACKAGE_TOUCHED=1
  sudo -n systemctl daemon-reload
  sudo -n systemctl reset-failed podlazd.service || true
  sudo -n systemctl start podlazd.service
  wait_for_daemon_socket
}

remove_test_hook() {
  sudo -n rm -f -- "${HOOK_DROPIN}" >/dev/null 2>&1 || true
  sudo -n rm -rf -- "${HOOK_DIR}" >/dev/null 2>&1 || true
  sudo -n systemctl daemon-reload >/dev/null 2>&1 || true
}

cleanup() {
  local code=$?
  remove_test_hook
  if [[ "${FOREIGN_STATE_CREATED}" == 1 ]]; then
    cleanup_tun_foreign_state || true
  fi
  if [[ "${PODLAZ_E2E_KEEP_PACKAGE:-false}" != true && "${PACKAGE_TOUCHED}" == 1 ]]; then
    sudo -n systemctl stop podlazd.service >/dev/null 2>&1 || true
    sudo -n apt purge -y podlaz >/dev/null 2>&1 || true
  fi
  exit "${code}"
}
trap cleanup EXIT

install_terminal_hook() {
  sudo -n mkdir -p "${HOOK_DROPIN_DIR}" "${HOOK_DIR}"
  sudo -n rm -f -- "${HOOK_MARKER}"
  local tmp
  tmp="$(mktemp "${E2E_TMP_ROOT}/terminal-hook.XXXXXX")"
  cat >"${tmp}" <<EOF
[Service]
Environment=PODLAZ_E2E_TERMINAL_FIREWALL_ROLLBACK_ONCE=true
Environment=PODLAZ_E2E_TUN_HOOK_DIR=${HOOK_DIR}
EOF
  sudo -n install -m 0644 "${tmp}" "${HOOK_DROPIN}"
  rm -f -- "${tmp}"
  sudo -n systemctl daemon-reload
  sudo -n systemctl restart podlazd.service
  wait_for_daemon_socket
}

check_https_and_dns() {
  local phase="$1"
  timeout 15 getent ahostsv4 example.com >"${E2E_ARTIFACT_DIR}/$(safe_name "${phase}")-dns.txt" 2>&1 || \
    fail "${phase}: bounded system IPv4 DNS resolution failed"
  curl -4 -fsS --connect-timeout 10 --max-time 20 -o /dev/null "${PODLAZ_E2E_HTTPS_CHECK_URL}" || \
    fail "${phase}: IPv4 HTTPS/TLS failed"
}

capture_host_state() {
  sudo -n ip -j -4 addr show >"${PRIVATE_ADDR}"
  sudo -n ip -j -4 route show table all >"${PRIVATE_ROUTES}"
  sudo -n ip -j -4 rule show >"${PRIVATE_RULES}"
  sudo -n nft -j list tables >"${PRIVATE_NFT}"
}

capture_exact_authority() {
  local required_state="$1" tx_files=() values=()
  mapfile -t tx_files < <(sudo -n find "${TRANSACTION_DIR}" -maxdepth 1 -type f -name '*.json' -print 2>/dev/null | sort)
  [[ "${#tx_files[@]}" == 1 ]] || fail "${required_state}: expected exactly one TUN transaction, got ${#tx_files[@]}"
  TX_PATH="${tx_files[0]}"
  sudo -n cat "${TX_PATH}" >"${PRIVATE_TX}"
  sudo -n cat "${SESSION_STATE}" >"${PRIVATE_SESSION}"
  chmod 0600 "${PRIVATE_TX}" "${PRIVATE_SESSION}"

  mapfile -t values < <(python3 - "${required_state}" "${PRIVATE_TX}" "${PRIVATE_SESSION}" <<'PY'
import json,sys
required,tx_path,session_path=sys.argv[1:]
with open(tx_path,encoding='utf-8') as f: tx=json.load(f)
with open(session_path,encoding='utf-8') as f: session=json.load(f)
if tx.get('state') != required:
    raise SystemExit(f"transaction state={tx.get('state')!r}, want {required!r}")
rb=tx.get('rollback') or {}
children=rb.get('child_processes') or []
addresses=rb.get('tun_addresses') or []
nft=rb.get('nftables') or []
configs=rb.get('generated_configs') or []
protection=session.get('protection') or {}
if len(children)!=1 or int(children[0].get('pid',0))<=1:
    raise SystemExit('transaction lacks one exact tracked child')
if len(addresses)!=1 or not addresses[0].get('interface_name') or not addresses[0].get('cidr'):
    raise SystemExit('transaction lacks one exact TUN address authority')
if not (rb.get('routes') or []) or not (rb.get('policy_rules') or []):
    raise SystemExit('transaction lacks exact route/rule authority')
if len(nft)!=1 or not nft[0].get('family') or not nft[0].get('table'):
    raise SystemExit('transaction lacks exact nftables rollback authority')
if not (tx.get('desired_plan') or {}).get('nftables',{}).get('chains'):
    raise SystemExit('transaction lacks exact desired nftables composition')
if not configs or any(not item.get('path') for item in configs):
    raise SystemExit('transaction lacks generated-config authority')
if protection.get('state')!='armed' or not protection.get('family') or not protection.get('table'):
    raise SystemExit('Network Session lacks armed Privacy Envelope authority')
if required=='failed':
    if session.get('intent') not in ('disconnect','terminal'):
        raise SystemExit('failed transaction lacks terminal Network Session intent')
    if 'missing nftables chains' not in (tx.get('failure_reason') or ''):
        raise SystemExit('failed transaction is not historical v0.2.40 nftables failure')
print(tx.get('id') or '')
print(children[0]['pid'])
print(addresses[0]['interface_name'])
print(addresses[0]['cidr'])
PY
)
  [[ "${#values[@]}" == 4 && -n "${values[0]}" ]] || fail "${required_state}: exact authority extraction failed"
  TX_ID="${values[0]}"
  CHILD_PID="${values[1]}"
  TUN_IFACE="${values[2]}"
  TUN_CIDR="${values[3]}"
  if sudo -n test -r "/proc/${CHILD_PID}/stat"; then
    CHILD_START="$(sudo -n awk '{print $22}' "/proc/${CHILD_PID}/stat")"
  else
    CHILD_START=""
  fi
}

assert_child_identity() {
  local pid="$1" start="$2" phase="$3" current_start exe
  [[ -n "${start}" ]] || fail "${phase}: tracked Xray start identity is unavailable"
  sudo -n test -r "/proc/${pid}/stat" || fail "${phase}: tracked Xray process is absent"
  current_start="$(sudo -n awk '{print $22}' "/proc/${pid}/stat")"
  [[ "${current_start}" == "${start}" ]] || fail "${phase}: tracked Xray process identity changed"
  exe="$(sudo -n readlink "/proc/${pid}/exe")" || fail "${phase}: tracked Xray executable cannot be inspected"
  [[ "${exe}" == /usr/lib/podlaz/xray ]] || fail "${phase}: tracked PID is not packaged Xray"
}

assert_original_child_absent() {
  local pid="$1" start="$2" phase="$3" current_start
  [[ -n "${start}" ]] || return 0
  if sudo -n test -r "/proc/${pid}/stat"; then
    current_start="$(sudo -n awk '{print $22}' "/proc/${pid}/stat")"
    [[ "${current_start}" != "${start}" ]] || fail "${phase}: original tracked Xray is still alive"
  fi
}

assert_generated_configs() {
  local mode="$1" phase="$2" config
  while IFS= read -r config; do
    [[ -n "${config}" ]] || continue
    case "${mode}" in
      present) sudo -n test -e "${config}" || fail "${phase}: generated config is absent" ;;
      absent) sudo -n test ! -e "${config}" || fail "${phase}: generated config remains" ;;
      *) fail "invalid generated-config assertion mode ${mode}" ;;
    esac
  done < <(python3 - "${PRIVATE_TX}" <<'PY'
import json,sys
with open(sys.argv[1],encoding='utf-8') as f: tx=json.load(f)
for item in (tx.get('rollback') or {}).get('generated_configs') or []:
    path=item.get('path')
    if path: print(path)
PY
)
}

assert_resolved_state() {
  local mode="$1" phase="$2" state
  if inspect_resolved_link_state "${TUN_IFACE}"; then
    state=0
  else
    state=$?
  fi
  case "${mode}:${state}" in
    present:1|absent:0) return 0 ;;
    present:0) fail "${phase}: systemd-resolved TUN state is absent" ;;
    absent:1) fail "${phase}: systemd-resolved TUN state remains" ;;
    *) fail "${phase}: systemd-resolved TUN state is unknown" ;;
  esac
}

assert_exact_live_network() {
  local mode="$1" phase="$2"
  capture_host_state
  python3 "${SCRIPT_DIR}/lib/tun_terminal_stranded.py" "${mode}" \
    "${PRIVATE_TX}" "${PRIVATE_SESSION}" "${PRIVATE_ADDR}" \
    "${PRIVATE_ROUTES}" "${PRIVATE_RULES}" "${PRIVATE_NFT}" || \
    fail "${phase}: exact live network contract failed for mode ${mode}"
}

snapshot_exact_network_manifest() {
  local phase="$1"
  sudo -n rm -f -- "${PRIVATE_MANIFEST}" >/dev/null 2>&1 || fail "${phase}: cannot clear network manifest"
  sudo -n python3 "${FALLBACK_NETWORK_HELPER}" snapshot "${TRANSACTION_DIR}" "${PRIVATE_MANIFEST}" >/dev/null || \
    fail "${phase}: transaction-derived route/rule manifest snapshot failed"
  sudo -n test -f "${PRIVATE_MANIFEST}" || fail "${phase}: route/rule manifest was not persisted"
}

assert_network_manifest_absent() {
  local phase="$1"
  verify_tun_package_network_absent "${phase}" "${FALLBACK_NETWORK_HELPER}" "${PRIVATE_MANIFEST}" || \
    fail "${phase}: exact transaction route/rule residue remains or cannot be inspected"
}

assert_active_authority_present() {
  local phase="$1"
  sudo -n test -e "${TX_PATH}" || fail "${phase}: transaction authority disappeared"
  sudo -n test -e "${SESSION_STATE}" || fail "${phase}: Network Session authority disappeared"
  sudo -n ip link show dev "${TUN_IFACE}" >/dev/null 2>&1 || fail "${phase}: TUN link disappeared"
  assert_resolved_state present "${phase}"
  assert_child_identity "${CHILD_PID}" "${CHILD_START}" "${phase}"
  assert_generated_configs present "${phase}"
  assert_exact_live_network active "${phase}"
}

assert_exact_authority_absent() {
  local phase="$1"
  sudo -n test ! -e "${TX_PATH}" || fail "${phase}: exact transaction authority remains"
  sudo -n test ! -e "${SESSION_STATE}" || fail "${phase}: Network Session authority remains"
  if inspect_link_state "${TUN_IFACE}"; then
    :
  else
    case $? in 1) fail "${phase}: exact TUN link remains" ;; *) fail "${phase}: TUN link absence is unknown" ;; esac
  fi
  assert_resolved_state absent "${phase}"
  assert_original_child_absent "${CHILD_PID}" "${CHILD_START}" "${phase}"
  assert_generated_configs absent "${phase}"
  assert_exact_live_network absent "${phase}"
  if sudo -n test -d "${TRANSACTION_DIR}" && \
    sudo -n find "${TRANSACTION_DIR}" -maxdepth 1 -type f -name '*.json' -print -quit | grep -q .; then
    fail "${phase}: transaction cleanup authority remains"
  fi
}

assert_clean_recovery_view() {
  expect_secret_success "recover-clean-json" run_client recover --json
  python3 - "${LAST_STDOUT}" <<'PY'
import json,sys
with open(sys.argv[1],encoding='utf-8') as f: payload=json.load(f)
recovery=payload.get('recovery') or {}
if recovery.get('candidates'):
    raise SystemExit('clean recovery view still contains candidates')
network_session=recovery.get('network_session')
if network_session and network_session.get('next_action') not in (None,'none'):
    raise SystemExit('clean recovery view still contains Network Session work')
PY
}

assert_networkmanager_tun_absent() {
  local phase="$1" code
  command -v nmcli >/dev/null 2>&1 || return 0
  systemctl is-active --quiet NetworkManager.service || return 0

  set +e
  capture_secret_command "networkmanager-active-${phase}" \
    timeout 10 nmcli --terse --escape no --get-values DEVICE connection show --active
  code=$?
  set -e
  [[ "${code}" == 0 ]] || fail "${phase}: NetworkManager active connections could not be inspected"
  if grep -Fx -- "${TUN_IFACE}" "${LAST_STDOUT}" >/dev/null; then
    fail "${phase}: NetworkManager still publishes the exact TUN interface as an active connection"
  fi
}

assert_post_convergence_diagnostics() {
  local phase="$1" stale_failure="$2"

  expect_secret_success "status-${phase}" run_client status
  assert_contains "${LAST_STDOUT}" "Status: Disconnected"
  assert_not_contains "${LAST_STDOUT}" "${stale_failure}"

  expect_secret_success "doctor-${phase}" run_client doctor
  assert_contains "${LAST_STDOUT}" "Source: daemon"
  assert_not_contains "${LAST_STDOUT}" "${stale_failure}"

  assert_clean_recovery_view
  assert_not_contains "${LAST_STDOUT}" "${stale_failure}"
  assert_networkmanager_tun_absent "${phase}"
}

assert_v0240_stranded_shape() {
  local current_start
  [[ "${TX_ID}" == "${V0240_PRE_TX_ID}" ]] || fail "v0.2.40 failed transaction identity changed across disconnect"
  [[ "${CHILD_PID}" == "${V0240_PRE_CHILD_PID}" ]] || fail "v0.2.40 rollback child PID changed across disconnect"
  assert_child_identity "${V0240_PRE_CHILD_PID}" "${V0240_PRE_CHILD_START}" "v0.2.40 stranded"
  sudo -n ip link show dev "${TUN_IFACE}" >/dev/null 2>&1 || fail "v0.2.40 stranded boundary lost TUN link"
  assert_generated_configs present "v0.2.40 stranded"
  capture_host_state
  python3 "${SCRIPT_DIR}/lib/tun_terminal_stranded.py" v0240-stranded \
    "${PRIVATE_TX}" "${PRIVATE_SESSION}" "${PRIVATE_ADDR}" \
    "${PRIVATE_ROUTES}" "${PRIVATE_RULES}" "${PRIVATE_NFT}" || \
    fail "v0.2.40 captured stranded live shape was not reproduced"
  current_start="$(sudo -n awk '{print $22}' "/proc/${V0240_PRE_CHILD_PID}/stat")"
  [[ "${current_start}" == "${V0240_PRE_CHILD_START}" ]] || fail "v0.2.40 Xray identity changed during stranded assertion"
  printf 'v0.2.40_stranded_shape=confirmed\npre_disconnect_child_identity=preserved\n' \
    >"${E2E_ARTIFACT_DIR}/v0.2.40-stranded-shape.txt"
}

log "record exact package identities"
printf '%s\n' "$(sha256sum "${CANDIDATE}" | awk '{print $1}')" >"${E2E_ARTIFACT_DIR}/candidate.sha256"
printf '%s\n' "$(sha256sum "${PREVIOUS}" | awk '{print $1}')" >"${E2E_ARTIFACT_DIR}/v0.2.40.sha256"
printf '%s\n' "${SOURCE_HEAD}" >"${E2E_ARTIFACT_DIR}/source-head.txt"

log "install exact candidate package"
install_exact_package "${CANDIDATE}" candidate-initial

log "import private TUN profile"
PROFILE_URI="$(first_configured_profile_uri)"
assert_nonempty "${PROFILE_URI}" "private profile URI"
mask_multiline_sensitive "${PROFILE_URI}"
expect_secret_success "import-profile" run_client profile import "${PROFILE_URI}"
PROFILE_ID="$(awk '/^Imported profile:/ {print $3}' "${LAST_STDOUT}")"
assert_nonempty "${PROFILE_ID}" "imported profile id"
mask_multiline_sensitive "${PROFILE_ID}"
assert_not_contains "${LAST_STDOUT}" "${PROFILE_URI}"
expect_secret_success "validate-profile-tun" run_client profile validate "${PROFILE_ID}" --mode tun

check_https_and_dns baseline
create_tun_foreign_state
FOREIGN_STATE_CREATED=1
assert_tun_foreign_state baseline
install_terminal_hook

log "connect exact candidate TUN"
expect_secret_success "connect-tun" run_client connect --mode tun "${PROFILE_ID}"
expect_secret_success "status-active" run_client status
assert_contains "${LAST_STDOUT}" "Status: Connected"
capture_exact_authority committed
assert_active_authority_present candidate-active
assert_tun_foreign_state candidate-active
check_https_and_dns candidate-active-vpn

log "inject terminal firewall rollback blocker"
set +e
capture_secret_command "disconnect-injected" run_client disconnect
DISCONNECT_RC=$?
set -e
[[ "${DISCONNECT_RC}" != 0 ]] || fail "injected terminal disconnect unexpectedly succeeded"
sudo -n test -f "${HOOK_MARKER}" || fail "terminal firewall rollback hook did not fire"
assert_active_authority_present after-failed-disconnect
assert_tun_foreign_state after-failed-disconnect
expect_secret_exit 3 "status-terminal-incomplete" run_client status
assert_contains "${LAST_STDOUT}" "Status: Unknown"

log "recover terminal state in same daemon without reboot/service restart"
expect_secret_success "recover-terminal-execute" run_client recover --execute --yes
check_https_and_dns after-recover
assert_exact_authority_absent after-recover
assert_tun_foreign_state after-recover
assert_post_convergence_diagnostics after-recover "terminal firewall rollback blocked before nftables mutation"

log "prove recovery is idempotent"
expect_secret_success "recover-terminal-second" run_client recover --execute --yes
assert_exact_authority_absent after-second-recover
assert_clean_recovery_view
assert_tun_foreign_state after-second-recover

log "prove normal candidate disconnect has no exact route/rule residue"
expect_secret_success "connect-after-recovery" run_client connect --mode tun "${PROFILE_ID}"
capture_exact_authority committed
assert_active_authority_present candidate-reconnect
check_https_and_dns candidate-reconnect-vpn
snapshot_exact_network_manifest candidate-reconnect
expect_secret_success "disconnect-after-recovery" run_client disconnect
check_https_and_dns final-candidate
assert_exact_authority_absent final-candidate
assert_network_manifest_absent final-candidate
assert_clean_recovery_view
assert_tun_foreign_state final-candidate

log "reproduce exact v0.2.40 stranded upgrade boundary"
remove_test_hook
install_exact_package "${PREVIOUS}" v0.2.40-downgrade true
expect_secret_success "v0240-validate-profile-tun" run_client profile validate "${PROFILE_ID}" --mode tun
expect_secret_success "v0240-connect-tun" run_client connect --mode tun "${PROFILE_ID}"
capture_exact_authority committed
assert_active_authority_present v0.2.40-active
V0240_PRE_TX_ID="${TX_ID}"
V0240_PRE_CHILD_PID="${CHILD_PID}"
V0240_PRE_CHILD_START="${CHILD_START}"
assert_child_identity "${V0240_PRE_CHILD_PID}" "${V0240_PRE_CHILD_START}" "v0.2.40 pre-disconnect"
assert_tun_foreign_state v0.2.40-active

set +e
capture_secret_command "v0240-disconnect-stranded" run_client disconnect
V0240_DISCONNECT_RC=$?
set -e
[[ "${V0240_DISCONNECT_RC}" != 0 ]] || fail "v0.2.40 disconnect unexpectedly converged"
capture_exact_authority failed
assert_v0240_stranded_shape
assert_tun_foreign_state v0.2.40-stranded

log "install exact fixed candidate over v0.2.40 stranded state without reboot"
install_exact_package "${CANDIDATE}" candidate-upgrade-over-stranded
expect_secret_success "upgrade-recover-terminal" run_client recover --execute --yes
check_https_and_dns after-v0240-upgrade-recover
assert_exact_authority_absent after-v0240-upgrade-recover
assert_tun_foreign_state after-v0240-upgrade-recover
assert_post_convergence_diagnostics after-v0240-upgrade-recover "missing nftables chains"

log "prove post-upgrade normal lifecycle and route/rule cleanup"
expect_secret_success "upgrade-connect-after-recovery" run_client connect --mode tun "${PROFILE_ID}"
capture_exact_authority committed
assert_active_authority_present upgrade-reconnect
check_https_and_dns upgrade-reconnect-vpn
snapshot_exact_network_manifest upgrade-reconnect
expect_secret_success "upgrade-disconnect-after-recovery" run_client disconnect
check_https_and_dns final-upgrade
assert_exact_authority_absent final-upgrade
assert_network_manifest_absent final-upgrade
assert_clean_recovery_view
assert_tun_foreign_state final-upgrade

assert_artifacts_do_not_contain_sensitive_values \
  "tun-terminal-recovery" "${PODLAZ_E2E_PROFILE_URI}" "${PODLAZ_E2E_PROFILE_URI_LIST}" "${PROFILE_URI}" "${PROFILE_ID}"

log "TUN terminal recovery acceptance completed"
