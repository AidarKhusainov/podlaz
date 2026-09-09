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

require_cmd bash python3 sudo systemctl dpkg dpkg-deb dpkg-query apt ip nft resolvectl getent curl find sha256sum awk sed grep mktemp cat readlink sort seq sleep install rm chmod id env dirname tr mkdir stat

: "${PODLAZ_E2E_PROFILE_URI:=}"
: "${PODLAZ_E2E_PROFILE_URI_LIST:=}"
: "${PODLAZ_E2E_HTTPS_CHECK_URL:=https://example.com/}"

usage() {
  printf 'Usage: %s EXACT-CANDIDATE.deb EXACT-V0.2.40.deb\n' "$0" >&2
}

(($# == 2)) || { usage; exit 2; }
CANDIDATE="$(readlink -f -- "$1")"
PREVIOUS="$(readlink -f -- "$2")"
[[ -f "${CANDIDATE}" ]] || fail "candidate package does not exist"
[[ -f "${PREVIOUS}" ]] || fail "v0.2.40 package does not exist"
[[ -n "${PODLAZ_E2E_PROFILE_URI}" || -n "${PODLAZ_E2E_PROFILE_URI_LIST}" ]] || \
  fail "PODLAZ_E2E_PROFILE_URI or PODLAZ_E2E_PROFILE_URI_LIST is required"

HOST_ARCH="$(dpkg --print-architecture)"
CANDIDATE_ARCH="$(dpkg-deb -f "${CANDIDATE}" Architecture)"
PREVIOUS_ARCH="$(dpkg-deb -f "${PREVIOUS}" Architecture)"
CANDIDATE_VERSION="$(dpkg-deb -f "${CANDIDATE}" Version)"
PREVIOUS_VERSION="$(dpkg-deb -f "${PREVIOUS}" Version)"
[[ "${CANDIDATE_ARCH}" == "${HOST_ARCH}" && "${PREVIOUS_ARCH}" == "${HOST_ARCH}" ]] || \
  fail "candidate and v0.2.40 package architectures must match host ${HOST_ARCH}"
[[ "${PREVIOUS_VERSION%%-*}" == "0.2.40" ]] || \
  fail "previous package must be the exact v0.2.40 release boundary, got ${PREVIOUS_VERSION}"
dpkg --compare-versions "${CANDIDATE_VERSION}" gt "${PREVIOUS_VERSION}" || \
  fail "candidate version ${CANDIDATE_VERSION} must be newer than ${PREVIOUS_VERSION}"

HOOK_DIR="/run/podlaz/e2e-terminal-recovery"
HOOK_DROPIN_DIR="/run/systemd/system/podlazd.service.d"
HOOK_DROPIN="${HOOK_DROPIN_DIR}/99-e2e-terminal-recovery.conf"
HOOK_MARKER="${HOOK_DIR}/terminal-firewall-rollback.injected"
SESSION_STATE="/run/podlaz/network-session-continuation.json"
TRANSACTION_DIR="/run/podlaz/transactions"
FOREIGN_NFT_FAMILY="inet"
FOREIGN_NFT_TABLE="podlaz_e2e_terminal_foreign"
PRIVATE_TX="${E2E_TMP_ROOT}/terminal-recovery-transaction.json"
PRIVATE_SESSION="${E2E_TMP_ROOT}/terminal-recovery-session.json"
PRIVATE_ADDR="${E2E_TMP_ROOT}/terminal-recovery-ip-addr.json"
PRIVATE_ROUTES="${E2E_TMP_ROOT}/terminal-recovery-ip-routes.json"
PRIVATE_RULES="${E2E_TMP_ROOT}/terminal-recovery-ip-rules.json"
PRIVATE_NFT="${E2E_TMP_ROOT}/terminal-recovery-nft-tables.json"
TX_PATH=""
ORIGINAL_CHILD_PID=""
ORIGINAL_CHILD_START=""
PACKAGE_TOUCHED=0
EXPECTED_RUNTIME_DEB=""
EXPECTED_RUNTIME_PHASE=""

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
  log "${name}: private command output is retained outside public artifacts"
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
  [[ "${code}" == 0 ]] || fail "${name} failed with exit code ${code}; inspect private E2E temp output"
}

expect_secret_exit() {
  local want="$1" name="$2"
  shift 2
  set +e
  capture_secret_command "${name}" "$@"
  local code=$?
  set -e
  [[ "${code}" == "${want}" ]] || fail "${name}: expected exit ${want}, got ${code}; inspect private E2E temp output"
}

wait_for_daemon_socket() {
  local attempt
  for attempt in $(seq 1 100); do
    if [[ -S /run/podlaz/podlazd.sock ]]; then
      [[ -n "${EXPECTED_RUNTIME_DEB}" && -n "${EXPECTED_RUNTIME_PHASE}" ]] || \
        fail "package/runtime provenance expectation is not configured"
      assert_exact_package_runtime_provenance "${EXPECTED_RUNTIME_DEB}" "${EXPECTED_RUNTIME_PHASE}"
      return 0
    fi
    sleep 0.1
  done
  fail "podlazd socket did not become ready"
}

remove_test_hook() {
  sudo -n rm -f -- "${HOOK_DROPIN}" >/dev/null 2>&1 || true
  sudo -n rm -rf -- "${HOOK_DIR}" >/dev/null 2>&1 || true
  sudo -n systemctl daemon-reload >/dev/null 2>&1 || true
}

cleanup() {
  local code=$?
  remove_test_hook
  sudo -n nft delete table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}" >/dev/null 2>&1 || true
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

create_foreign_nft_sentinel() {
  sudo -n nft delete table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}" >/dev/null 2>&1 || true
  sudo -n nft add table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}"
}

assert_foreign_nft_sentinel() {
  sudo -n nft list table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}" >/dev/null 2>&1 || \
    fail "unrelated nftables sentinel was removed"
}

check_https_and_dns() {
  local phase="$1"
  getent hosts example.com >"${E2E_ARTIFACT_DIR}/$(safe_name "${phase}")-dns.txt" 2>&1 || \
    fail "${phase}: system DNS resolution failed"
  curl -4 -fsS --max-time 20 -o /dev/null "${PODLAZ_E2E_HTTPS_CHECK_URL}" || \
    fail "${phase}: IPv4 HTTPS failed"
}

assert_tun_path_usable() {
  local phase="$1" route
  sudo -n ip link show dev podlaz0 >/dev/null 2>&1 || fail "${phase}: podlaz0 is absent"
  sudo -n ip -4 addr show dev podlaz0 | grep -F 'inet ' >/dev/null || fail "${phase}: podlaz0 has no IPv4 address"
  route="$(sudo -n ip -4 route get 198.51.100.1)" || fail "${phase}: TUN route lookup failed"
  grep -F 'dev podlaz0' <<<"${route}" >/dev/null || fail "${phase}: test route no longer uses podlaz0"
  check_https_and_dns "${phase}"
}

capture_exact_authority_common() {
  local required_state="$1" tx_files=()
  mapfile -t tx_files < <(sudo -n find "${TRANSACTION_DIR}" -maxdepth 1 -type f -name '*.json' -print 2>/dev/null | sort)
  [[ "${#tx_files[@]}" == 1 ]] || fail "TUN state must expose exactly one transaction, got ${#tx_files[@]}"
  TX_PATH="${tx_files[0]}"
  sudo -n cat "${TX_PATH}" >"${PRIVATE_TX}"
  sudo -n cat "${SESSION_STATE}" >"${PRIVATE_SESSION}"
  chmod 0600 "${PRIVATE_TX}" "${PRIVATE_SESSION}"
  python3 - "${required_state}" "${PRIVATE_TX}" "${PRIVATE_SESSION}" <<'PY'
import json, sys
required=sys.argv[1]
with open(sys.argv[2], encoding='utf-8') as f: tx=json.load(f)
with open(sys.argv[3], encoding='utf-8') as f: session=json.load(f)
if tx.get('state') != required:
    raise SystemExit(f"transaction state={tx.get('state')!r}, expected {required!r}")
rb=tx.get('rollback',{})
nft=rb.get('nftables') or []
if len(nft) != 1 or not tx.get('desired_plan',{}).get('nftables',{}).get('chains'):
    raise SystemExit('transaction lacks exact nftables rollback composition')
children=rb.get('child_processes') or []
if len(children) != 1 or int(children[0].get('pid',0)) <= 1:
    raise SystemExit('transaction lacks one exact tracked child process')
configs=rb.get('generated_configs') or []
if not configs or any(not item.get('path') for item in configs):
    raise SystemExit('transaction lacks exact generated-config authority')
protection=session.get('protection') or {}
if protection.get('state') != 'armed' or not protection.get('family') or not protection.get('table'):
    raise SystemExit('Network Session lacks armed Privacy Envelope authority')
if required == 'failed':
    if session.get('intent') not in ('disconnect','terminal'):
        raise SystemExit(f"stranded Network Session intent={session.get('intent')!r}")
    if 'missing nftables chains' not in (tx.get('failure_reason') or ''):
        raise SystemExit(f"v0.2.40 stranded transaction has unexpected failure: {tx.get('failure_reason')!r}")
PY
  ORIGINAL_CHILD_PID="$(python3 - "${PRIVATE_TX}" <<'PY'
import json,sys
with open(sys.argv[1],encoding='utf-8') as f: tx=json.load(f)
print((tx.get('rollback',{}).get('child_processes') or [{}])[0].get('pid',0))
PY
)"
  if sudo -n test -r "/proc/${ORIGINAL_CHILD_PID}/stat"; then
    ORIGINAL_CHILD_START="$(sudo -n awk '{print $22}' "/proc/${ORIGINAL_CHILD_PID}/stat")"
  else
    ORIGINAL_CHILD_START=""
  fi
}

capture_committed_authority() {
  capture_exact_authority_common committed
  [[ -n "${ORIGINAL_CHILD_START}" ]] || fail "tracked Xray process is not inspectable"
}

capture_v0240_stranded_authority() {
  capture_exact_authority_common failed
}

capture_host_state() {
  sudo -n ip -j -4 addr show >"${PRIVATE_ADDR}"
  sudo -n ip -j -4 route show table all >"${PRIVATE_ROUTES}"
  sudo -n ip -j -4 rule show >"${PRIVATE_RULES}"
  sudo -n nft -j list tables >"${PRIVATE_NFT}"
}

assert_v0240_stranded_shape() {
  local current_start config
  [[ -n "${ORIGINAL_CHILD_START}" ]] || fail "v0.2.40 tracked Xray process is not inspectable"
  sudo -n ip link show dev podlaz0 >/dev/null 2>&1 || fail "v0.2.40 stranded boundary lost podlaz0"
  sudo -n test -r "/proc/${ORIGINAL_CHILD_PID}/stat" || fail "v0.2.40 stranded boundary lost tracked Xray child"
  current_start="$(sudo -n awk '{print $22}' "/proc/${ORIGINAL_CHILD_PID}/stat")"
  [[ "${current_start}" == "${ORIGINAL_CHILD_START}" ]] || fail "v0.2.40 stranded tracked Xray identity changed"
  while IFS= read -r config; do
    [[ -n "${config}" ]] || continue
    sudo -n test -e "${config}" || fail "v0.2.40 stranded generated config is absent"
  done < <(python3 - "${PRIVATE_TX}" <<'PY'
import json,sys
with open(sys.argv[1],encoding='utf-8') as f: tx=json.load(f)
for item in tx.get('rollback',{}).get('generated_configs') or []:
    path=item.get('path')
    if path: print(path)
PY
)
  capture_host_state
  python3 "${SCRIPT_DIR}/lib/tun_terminal_stranded.py" \
    "${PRIVATE_TX}" "${PRIVATE_SESSION}" "${PRIVATE_ADDR}" \
    "${PRIVATE_ROUTES}" "${PRIVATE_RULES}" "${PRIVATE_NFT}"
  printf 'v0.2.40_stranded_shape=confirmed\n' >"${E2E_ARTIFACT_DIR}/v0.2.40-stranded-shape.txt"
}

assert_exact_network_tuples() {
  local mode="$1"
  capture_host_state
  python3 - "${mode}" "${PRIVATE_TX}" "${PRIVATE_SESSION}" "${PRIVATE_ADDR}" "${PRIVATE_ROUTES}" "${PRIVATE_RULES}" "${PRIVATE_NFT}" <<'PY'
import ipaddress, json, sys
mode=sys.argv[1]
if mode not in {'present','absent'}:
    raise SystemExit('invalid exact tuple assertion mode')
with open(sys.argv[2],encoding='utf-8') as f: tx=json.load(f)
with open(sys.argv[3],encoding='utf-8') as f: session=json.load(f)
with open(sys.argv[4],encoding='utf-8') as f: addrs=json.load(f)
with open(sys.argv[5],encoding='utf-8') as f: routes=json.load(f)
with open(sys.argv[6],encoding='utf-8') as f: rules=json.load(f)
with open(sys.argv[7],encoding='utf-8') as f: nft=json.load(f)
rb=tx.get('rollback',{})

def require(found, description):
    if found != (mode == 'present'):
        state='present' if found else 'absent'
        raise SystemExit(f'{description} is {state}, expected {mode}')

def norm_table(value):
    text=str(value if value is not None else 'main')
    return 'main' if text in {'254','main'} else text

def norm_dst(value):
    return '0.0.0.0/0' if value in (None,'default') else value

for expected in rb.get('tun_addresses') or []:
    iface=expected.get('interface_name')
    net=ipaddress.ip_interface(expected.get('cidr'))
    found=False
    for link in addrs:
        if link.get('ifname') != iface: continue
        for info in link.get('addr_info') or []:
            if info.get('family')=='inet' and info.get('local')==str(net.ip) and info.get('prefixlen')==net.network.prefixlen:
                found=True
    require(found, f'exact TUN address on {iface}')

for expected in rb.get('routes') or []:
    found=False
    for route in routes:
        if norm_table(route.get('table')) != norm_table(expected.get('table')): continue
        if norm_dst(route.get('dst')) != norm_dst(expected.get('cidr')): continue
        if (route.get('dev') or '') != (expected.get('dev') or ''): continue
        if (route.get('gateway') or '') != (expected.get('via') or ''): continue
        found=True
    require(found, f'exact route {expected}')

for expected in rb.get('policy_rules') or []:
    found=False
    for rule in rules:
        if int(rule.get('priority',-1)) != int(expected.get('priority',-2)): continue
        if norm_table(rule.get('table','')) != norm_table(expected.get('table','')): continue
        if expected.get('from') and rule.get('from','all') != expected.get('from'): continue
        if expected.get('to') and rule.get('to') != expected.get('to'): continue
        if expected.get('mark') and str(rule.get('fwmark','')) != str(expected.get('mark')): continue
        found=True
    require(found, f'exact policy rule {expected}')

tables=[]
for item in nft.get('nftables') or []:
    table=item.get('table') if isinstance(item,dict) else None
    if table: tables.append((table.get('family'),table.get('name')))
for expected in rb.get('nftables') or []:
    require((expected.get('family'),expected.get('table')) in tables, 'exact transaction nftables table')
protection=session.get('protection') or {}
require((protection.get('family'),protection.get('table')) in tables, 'exact Privacy Envelope table')
PY
}

assert_exact_authority_absent() {
  local state current_start config
  sudo -n test ! -e "${TX_PATH}" || fail "exact transaction authority still exists"
  sudo -n test ! -e "${SESSION_STATE}" || fail "Network Session authority still exists"
  if inspect_link_state podlaz0; then :; else
    state=$?
    case "${state}" in 1) fail "podlaz0 still exists" ;; *) fail "podlaz0 absence is unknown" ;; esac
  fi
  if inspect_resolved_link_state podlaz0; then :; else
    state=$?
    case "${state}" in 1) fail "systemd-resolved still has podlaz0 state" ;; *) fail "resolved absence is unknown" ;; esac
  fi
  if [[ -n "${ORIGINAL_CHILD_START}" ]] && sudo -n test -r "/proc/${ORIGINAL_CHILD_PID}/stat"; then
    current_start="$(sudo -n awk '{print $22}' "/proc/${ORIGINAL_CHILD_PID}/stat")"
    [[ "${current_start}" != "${ORIGINAL_CHILD_START}" ]] || fail "original tracked Xray process is still alive"
  fi
  while IFS= read -r config; do
    [[ -n "${config}" ]] || continue
    sudo -n test ! -e "${config}" || fail "transaction-owned generated config still exists"
  done < <(python3 - "${PRIVATE_TX}" <<'PY'
import json,sys
with open(sys.argv[1],encoding='utf-8') as f: tx=json.load(f)
for item in tx.get('rollback',{}).get('generated_configs') or []:
    path=item.get('path')
    if path: print(path)
PY
)
  assert_exact_network_tuples absent
}

assert_committed_authority_present() {
  local state current_start config
  sudo -n test -e "${TX_PATH}" || fail "exact transaction authority disappeared"
  sudo -n test -e "${SESSION_STATE}" || fail "Network Session authority disappeared"
  sudo -n ip link show dev podlaz0 >/dev/null 2>&1 || fail "podlaz0 disappeared"
  if inspect_resolved_link_state podlaz0; then
    fail "systemd-resolved podlaz0 state disappeared"
  else
    state=$?
    case "${state}" in 1) ;; *) fail "systemd-resolved podlaz0 observation is unknown" ;; esac
  fi
  sudo -n test -r "/proc/${ORIGINAL_CHILD_PID}/stat" || fail "tracked Xray process disappeared"
  current_start="$(sudo -n awk '{print $22}' "/proc/${ORIGINAL_CHILD_PID}/stat")"
  [[ "${current_start}" == "${ORIGINAL_CHILD_START}" ]] || fail "tracked Xray process identity changed"
  while IFS= read -r config; do
    [[ -n "${config}" ]] || continue
    sudo -n test -e "${config}" || fail "transaction-owned generated config disappeared"
  done < <(python3 - "${PRIVATE_TX}" <<'PY'
import json,sys
with open(sys.argv[1],encoding='utf-8') as f: tx=json.load(f)
for item in tx.get('rollback',{}).get('generated_configs') or []:
    path=item.get('path')
    if path: print(path)
PY
)
  assert_exact_network_tuples present
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
    raise SystemExit(f'clean recovery view still contains Network Session work: {network_session}')
PY
}

assert_no_podlaz_runtime_residue() {
  local state tables
  if inspect_link_state podlaz0; then :; else
    state=$?
    case "${state}" in 1) fail "podlaz0 still exists" ;; *) fail "podlaz0 absence is unknown" ;; esac
  fi
  if inspect_resolved_link_state podlaz0; then :; else
    state=$?
    case "${state}" in 1) fail "systemd-resolved still has podlaz0 state" ;; *) fail "resolved absence is unknown" ;; esac
  fi
  tables="$(sudo -n nft list tables)" || fail "nftables table inspection failed"
  if grep -Eq '^table inet podlaz$|^table inet podlaz_pe_' <<<"${tables}"; then
    fail "Podlaz nftables residue remains"
  fi
  if sudo -n test -d "${TRANSACTION_DIR}"; then
    if sudo -n find "${TRANSACTION_DIR}" -maxdepth 1 -type f -name '*.json' -print -quit | grep -q .; then
      fail "Podlaz transaction residue remains"
    fi
  fi
  sudo -n test ! -e "${SESSION_STATE}" || fail "Network Session authority remains"
}

log "record exact package identities"
sha256sum "${CANDIDATE}" | awk '{print $1}' >"${E2E_ARTIFACT_DIR}/candidate.sha256"
sha256sum "${PREVIOUS}" | awk '{print $1}' >"${E2E_ARTIFACT_DIR}/v0.2.40.sha256"

log "install exact candidate package"
EXPECTED_RUNTIME_DEB="${CANDIDATE}"
EXPECTED_RUNTIME_PHASE="candidate-initial"
sudo -n apt install -y "${CANDIDATE}"
PACKAGE_TOUCHED=1
sudo -n systemctl daemon-reload
sudo -n systemctl reset-failed podlazd.service || true
sudo -n systemctl start podlazd.service
wait_for_daemon_socket

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
create_foreign_nft_sentinel
assert_foreign_nft_sentinel
install_terminal_hook

log "connect exact candidate TUN"
expect_secret_success "connect-tun" run_client connect --mode tun "${PROFILE_ID}"
expect_secret_success "status-active" run_client status
assert_contains "${LAST_STDOUT}" "Status: Connected"
assert_tun_path_usable active
capture_committed_authority
assert_committed_authority_present

log "inject first terminal firewall rollback blocker"
set +e
capture_secret_command "disconnect-injected" run_client disconnect
DISCONNECT_RC=$?
set -e
[[ "${DISCONNECT_RC}" != 0 ]] || fail "injected terminal disconnect unexpectedly succeeded"
sudo -n test -f "${HOOK_MARKER}" || fail "terminal firewall rollback hook did not fire"
assert_foreign_nft_sentinel

log "prove early blocker did not amplify into dependent teardown"
assert_tun_path_usable after-failed-disconnect
assert_committed_authority_present
expect_secret_exit 3 "status-terminal-incomplete" run_client status
assert_contains "${LAST_STDOUT}" "Status: Unknown"

log "recover terminal state in the same daemon without reboot or service restart"
expect_secret_success "recover-terminal-execute" run_client recover --execute --yes
check_https_and_dns after-recover
assert_exact_authority_absent
assert_foreign_nft_sentinel
expect_secret_success "status-disconnected" run_client status
assert_contains "${LAST_STDOUT}" "Status: Disconnected"

log "prove recovery is idempotent without depending on unrelated host churn"
assert_exact_authority_absent
expect_secret_success "recover-terminal-second" run_client recover --execute --yes
assert_exact_authority_absent
assert_clean_recovery_view
assert_foreign_nft_sentinel

log "prove a subsequent normal lifecycle still works"
expect_secret_success "connect-after-recovery" run_client connect --mode tun "${PROFILE_ID}"
assert_tun_path_usable reconnect
expect_secret_success "disconnect-after-recovery" run_client disconnect
check_https_and_dns final-candidate
assert_no_podlaz_runtime_residue
assert_clean_recovery_view
assert_foreign_nft_sentinel

log "reproduce the exact v0.2.40 stranded upgrade boundary"
remove_test_hook
EXPECTED_RUNTIME_DEB="${PREVIOUS}"
EXPECTED_RUNTIME_PHASE="v0.2.40-downgrade"
sudo -n apt install --allow-downgrades -y "${PREVIOUS}"
sudo -n systemctl daemon-reload
sudo -n systemctl reset-failed podlazd.service || true
sudo -n systemctl start podlazd.service
wait_for_daemon_socket
expect_secret_success "v0240-validate-profile-tun" run_client profile validate "${PROFILE_ID}" --mode tun
expect_secret_success "v0240-connect-tun" run_client connect --mode tun "${PROFILE_ID}"
assert_tun_path_usable v0240-active
set +e
capture_secret_command "v0240-disconnect-stranded" run_client disconnect
V0240_DISCONNECT_RC=$?
set -e
[[ "${V0240_DISCONNECT_RC}" != 0 ]] || fail "v0.2.40 disconnect unexpectedly converged; stranded upgrade boundary was not reproduced"
capture_v0240_stranded_authority
assert_v0240_stranded_shape
assert_foreign_nft_sentinel

log "install exact fixed candidate over v0.2.40 stranded state without reboot"
EXPECTED_RUNTIME_DEB="${CANDIDATE}"
EXPECTED_RUNTIME_PHASE="candidate-upgrade-over-stranded"
sudo -n apt install -y "${CANDIDATE}"
sudo -n systemctl daemon-reload
sudo -n systemctl reset-failed podlazd.service || true
sudo -n systemctl start podlazd.service
wait_for_daemon_socket
expect_secret_success "upgrade-recover-terminal" run_client recover --execute --yes
check_https_and_dns after-v0240-upgrade-recover
assert_exact_authority_absent
assert_foreign_nft_sentinel
expect_secret_success "upgrade-status-disconnected" run_client status
assert_contains "${LAST_STDOUT}" "Status: Disconnected"
assert_clean_recovery_view

log "prove post-upgrade lifecycle remains reusable"
expect_secret_success "upgrade-connect-after-recovery" run_client connect --mode tun "${PROFILE_ID}"
assert_tun_path_usable upgrade-reconnect
expect_secret_success "upgrade-disconnect-after-recovery" run_client disconnect
check_https_and_dns final-upgrade
assert_no_podlaz_runtime_residue
assert_clean_recovery_view
assert_foreign_nft_sentinel

assert_artifacts_do_not_contain_sensitive_values \
  "tun-terminal-recovery" "${PODLAZ_E2E_PROFILE_URI}" "${PODLAZ_E2E_PROFILE_URI_LIST}" "${PROFILE_URI}" "${PROFILE_ID}"

log "TUN terminal recovery acceptance completed"
