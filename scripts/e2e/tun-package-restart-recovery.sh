#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"
# shellcheck source=lib/profile_input.sh
source "${SCRIPT_DIR}/lib/profile_input.sh"
# shellcheck source=lib/package_runtime_provenance.sh
source "${SCRIPT_DIR}/lib/package_runtime_provenance.sh"
# shellcheck source=lib/tun_foreign_state.sh
source "${SCRIPT_DIR}/lib/tun_foreign_state.sh"

require_cmd \
  apt awk cat curl date dpkg dpkg-deb find getent git grep id ip journalctl mktemp nft \
  python3 readlink resolvectl rm sed seq sha256sum sleep sudo systemctl systemd-run timeout tr

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

HOST_ARCH="$(dpkg --print-architecture)"
CANDIDATE_ARCH="$(dpkg-deb --field "${CANDIDATE}" Architecture)"
PREVIOUS_ARCH="$(dpkg-deb --field "${PREVIOUS}" Architecture)"
CANDIDATE_VERSION="$(dpkg-deb --field "${CANDIDATE}" Version)"
PREVIOUS_VERSION="$(dpkg-deb --field "${PREVIOUS}" Version)"
[[ "$(dpkg-deb --field "${CANDIDATE}" Package)" == podlaz ]] || fail "candidate package is not podlaz"
[[ "$(dpkg-deb --field "${PREVIOUS}" Package)" == podlaz ]] || fail "v0.2.40 package is not podlaz"
[[ "${CANDIDATE_ARCH}" == "${HOST_ARCH}" && "${PREVIOUS_ARCH}" == "${HOST_ARCH}" ]] || \
  fail "candidate and v0.2.40 architectures must match host ${HOST_ARCH}"
[[ "${PREVIOUS_VERSION%%-*}" == "0.2.40" ]] || fail "previous package must be exact v0.2.40"
dpkg --compare-versions "${CANDIDATE_VERSION}" gt "${PREVIOUS_VERSION}" || \
  fail "candidate version ${CANDIDATE_VERSION} must be newer than ${PREVIOUS_VERSION}"

case "${PREVIOUS_ARCH}" in
  amd64) V0240_EXPECTED_SHA256="c9d8f76838292d39355506123e2f03ca1f0a96227fb2c22af8324ac6baf3b278" ;;
  arm64) V0240_EXPECTED_SHA256="8a86c439cc86fb075b66f58ae16eb99baaf35118d9f9b6ddc05a9235b1b57250" ;;
  *) fail "no pinned public v0.2.40 digest for architecture ${PREVIOUS_ARCH}" ;;
esac
V0240_ACTUAL_SHA256="$(sha256sum "${PREVIOUS}" | awk '{print $1}')"
[[ "${V0240_ACTUAL_SHA256}" == "${V0240_EXPECTED_SHA256}" ]] || \
  fail "v0.2.40 package digest does not match the public release asset"

DAEMON_SOCKET="/run/podlaz/podlazd.sock"
SESSION_STATE="/run/podlaz/network-session-continuation.json"
TRANSACTION_DIR="/run/podlaz/transactions"
PRIVATE_SOURCE_SESSION="${E2E_TMP_ROOT}/package-restart-source-session.json"
PRIVATE_SOURCE_TX="${E2E_TMP_ROOT}/package-restart-source-transaction.json"
PRIVATE_SOURCE_JOURNAL="${E2E_TMP_ROOT}/package-restart-source-journal.txt"
PACKAGE_TOUCHED=0
FOREIGN_STATE_CREATED=0
EXPECTED_RUNTIME_DEB=""
EXPECTED_RUNTIME_PHASE=""
PROFILE_URI=""
PROFILE_ID=""
V0240_PRE_DAEMON_PID=""
V0240_PRE_DAEMON_START=""
V0240_PRE_CHILD_PID=""
V0240_PRE_CHILD_START=""
PACKAGE_RESTART_STARTED_AT=""
PACKAGE_RESTART_OUTCOME=""

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

setup_isolated_xdg "tun-package-restart-recovery"

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

check_https_and_dns() {
  local phase="$1"
  timeout 15 getent ahostsv4 example.com >/dev/null 2>&1 || \
    fail "${phase}: bounded system IPv4 DNS resolution failed"
  curl -4 -fsS --connect-timeout 10 --max-time 20 -o /dev/null "${PODLAZ_E2E_HTTPS_CHECK_URL}" || \
    fail "${phase}: IPv4 HTTPS/TLS failed"
  printf 'traffic_%s=passed\n' "$(safe_name "${phase}")" >>"${E2E_ARTIFACT_DIR}/package-restart-result.txt"
}

main_pid() {
  sudo -n systemctl show -p MainPID --value podlazd.service | tr -d '[:space:]'
}

process_start_ticks() {
  local pid="$1"
  sudo -n awk '{print $22}' "/proc/${pid}/stat"
}

wait_for_daemon_socket() {
  local attempt
  for attempt in $(seq 1 150); do
    if [[ -S "${DAEMON_SOCKET}" ]] && sudo -n systemctl is-active --quiet podlazd.service; then
      [[ -n "${EXPECTED_RUNTIME_DEB}" && -n "${EXPECTED_RUNTIME_PHASE}" ]] || \
        fail "package/runtime provenance expectation is not configured"
      assert_exact_package_runtime_provenance "${EXPECTED_RUNTIME_DEB}" "${EXPECTED_RUNTIME_PHASE}"
      return 0
    fi
    sleep 0.1
  done
  fail "podlazd socket did not become ready"
}

install_setup_package() {
  local deb="$1" phase="$2" allow_downgrade="${3:-false}"
  EXPECTED_RUNTIME_DEB="${deb}"
  EXPECTED_RUNTIME_PHASE="${phase}"
  if [[ "${allow_downgrade}" == true ]]; then
    sudo -n apt install --allow-downgrades -y "${deb}" >/dev/null
  else
    sudo -n apt install -y "${deb}" >/dev/null
  fi
  PACKAGE_TOUCHED=1
  sudo -n systemctl daemon-reload >/dev/null
  sudo -n systemctl reset-failed podlazd.service >/dev/null 2>&1 || true
  sudo -n systemctl start podlazd.service >/dev/null
  wait_for_daemon_socket
}

wait_for_verified_active() {
  local phase="$1" attempt status
  for attempt in $(seq 1 300); do
    status="$(mktemp "${E2E_TMP_ROOT}/package-restart-active.XXXXXX")"
    if sudo -n curl --fail --silent --show-error --max-time 3 --unix-socket "${DAEMON_SOCKET}" \
      http://localhost/v1/status >"${status}" 2>/dev/null && \
      python3 - "${status}" <<'PY_ACTIVE'
import json,sys
with open(sys.argv[1],encoding='utf-8') as handle:
    status=json.load(handle)
health=status.get('tun_health') or {}
txs=status.get('transactions') or []
committed=[tx for tx in txs if isinstance(tx,dict) and tx.get('state')=='committed' and not tx.get('requires_cleanup')]
if status.get('connection')!='active' or status.get('mode')!='tun' or health.get('state')!='verified' or len(committed)!=1:
    raise SystemExit(1)
PY_ACTIVE
    then
      rm -f -- "${status}"
      printf 'active_%s=verified\n' "$(safe_name "${phase}")" >>"${E2E_ARTIFACT_DIR}/package-restart-result.txt"
      return 0
    fi
    rm -f -- "${status}"
    sleep 0.2
  done
  fail "${phase}: TUN session did not become verified active"
}

capture_v0240_package_restart_authority() {
  local tx_files=() values=()
  mapfile -t tx_files < <(sudo -n find "${TRANSACTION_DIR}" -maxdepth 1 -type f -name '*.json' -print 2>/dev/null | sort)
  [[ "${#tx_files[@]}" == 1 ]] || fail "v0.2.40 package restart requires exactly one committed TUN transaction"
  sudo -n cat "${tx_files[0]}" >"${PRIVATE_SOURCE_TX}"
  sudo -n cat "${SESSION_STATE}" >"${PRIVATE_SOURCE_SESSION}"
  chmod 0600 "${PRIVATE_SOURCE_TX}" "${PRIVATE_SOURCE_SESSION}"
  mapfile -t values < <(python3 - "${PRIVATE_SOURCE_TX}" "${PRIVATE_SOURCE_SESSION}" <<'PY_AUTHORITY'
import json,sys
with open(sys.argv[1],encoding='utf-8') as handle: tx=json.load(handle)
with open(sys.argv[2],encoding='utf-8') as handle: session=json.load(handle)
if tx.get('owner')!='podlaz' or tx.get('state')!='committed':
    raise SystemExit('source transaction is not committed Podlaz authority')
if session.get('owner')!='podlaz' or session.get('intent')!='resume':
    raise SystemExit('source Network Session does not preserve reconnect intent')
protection=session.get('protection') or {}
if protection.get('state')!='armed':
    raise SystemExit('source Network Session Privacy Envelope is not armed')
children=(tx.get('rollback') or {}).get('child_processes') or []
if len(children)!=1 or int(children[0].get('pid') or 0)<=1:
    raise SystemExit('source transaction lacks exact tracked Xray child')
print(children[0]['pid'])
PY_AUTHORITY
  )
  [[ "${#values[@]}" == 1 ]] || fail "v0.2.40 package restart authority extraction failed"
  V0240_PRE_CHILD_PID="${values[0]}"
  V0240_PRE_CHILD_START="$(process_start_ticks "${V0240_PRE_CHILD_PID}")" || fail "source Xray identity is unavailable"
  V0240_PRE_DAEMON_PID="$(main_pid)"
  [[ "${V0240_PRE_DAEMON_PID}" =~ ^[1-9][0-9]*$ ]] || fail "source daemon MainPID is unavailable"
  V0240_PRE_DAEMON_START="$(process_start_ticks "${V0240_PRE_DAEMON_PID}")" || fail "source daemon identity is unavailable"
}

assert_original_process_absent() {
  local pid="$1" start="$2" phase="$3" current
  if sudo -n test -r "/proc/${pid}/stat"; then
    current="$(process_start_ticks "${pid}")" || fail "${phase}: process identity cannot be inspected"
    [[ "${current}" != "${start}" ]] || fail "${phase}: original process identity is still alive"
  fi
}

install_candidate_package_replacement() {
  local deb="$1" candidate_pid
  EXPECTED_RUNTIME_DEB="${deb}"
  EXPECTED_RUNTIME_PHASE="candidate-package-restart"
  PACKAGE_RESTART_STARTED_AT="$(date -u '+%Y-%m-%d %H:%M:%S')"
  sudo -n apt install -y "${deb}" >/dev/null
  PACKAGE_TOUCHED=1
  wait_for_daemon_socket
  candidate_pid="$(main_pid)"
  [[ "${candidate_pid}" =~ ^[1-9][0-9]*$ ]] || fail "candidate daemon MainPID is unavailable"
  [[ "${candidate_pid}" != "${V0240_PRE_DAEMON_PID}" ]] || fail "package replacement did not replace the v0.2.40 daemon"
}

assert_v0240_package_restart_failure() {
  [[ -n "${PACKAGE_RESTART_STARTED_AT}" ]] || fail "package restart timestamp is unavailable"
  sudo -n journalctl -u podlazd.service "_PID=${V0240_PRE_DAEMON_PID}" \
    --since "${PACKAGE_RESTART_STARTED_AT}" --no-pager -o cat >"${PRIVATE_SOURCE_JOURNAL}" || \
    fail "v0.2.40 package-restart journal cannot be inspected"
  grep -F "missing nftables chains" "${PRIVATE_SOURCE_JOURNAL}" >/dev/null || \
    fail "exact historical v0.2.40 package-restart teardown failure was not exercised"
  python3 - "${PRIVATE_SOURCE_SESSION}" <<'PY_RESUME'
import json,sys
with open(sys.argv[1],encoding='utf-8') as handle: session=json.load(handle)
if session.get('intent')!='resume':
    raise SystemExit('v0.2.40 package restart did not preserve resume intent')
PY_RESUME
  assert_original_process_absent "${V0240_PRE_DAEMON_PID}" "${V0240_PRE_DAEMON_START}" "v0.2.40 package restart daemon"
  assert_original_process_absent "${V0240_PRE_CHILD_PID}" "${V0240_PRE_CHILD_START}" "v0.2.40 package restart Xray"
  printf 'historical_package_restart_failure=missing nftables chains\nintent=resume\n' \
    >>"${E2E_ARTIFACT_DIR}/package-restart-result.txt"
}

classify_package_restart_candidate() {
  local attempt status classification
  for attempt in $(seq 1 300); do
    status="$(mktemp "${E2E_TMP_ROOT}/package-restart-status.XXXXXX")"
    if ! sudo -n curl --fail --silent --show-error --max-time 3 --unix-socket "${DAEMON_SOCKET}" \
      http://localhost/v1/status >"${status}" 2>/dev/null; then
      rm -f -- "${status}"
      sleep 0.2
      continue
    fi
    classification="$(python3 - "${status}" <<'PY_CLASSIFY'
import json,sys
with open(sys.argv[1],encoding='utf-8') as handle: status=json.load(handle)
health=status.get('tun_health') or {}
txs=status.get('transactions') or []
cleanup=any(isinstance(tx,dict) and tx.get('requires_cleanup') for tx in txs)
committed=[tx for tx in txs if isinstance(tx,dict) and tx.get('state')=='committed' and not tx.get('requires_cleanup')]
scan=status.get('startup_scan') or {}
session=scan.get('network_session') or None
if status.get('connection')=='active' and status.get('mode')=='tun' and health.get('state')=='verified' and len(committed)==1 and not cleanup:
    print('resumed')
elif status.get('connection')=='inactive' and not cleanup and not committed and not session:
    print('terminal')
elif isinstance(session,dict) and (session.get('startup_gate')=='blocked' or session.get('next_action') in ('retry-resume','manual-diagnosis')):
    print('blocked')
else:
    print('progress')
PY_CLASSIFY
)"
    rm -f -- "${status}"
    case "${classification}" in
      resumed|terminal)
        PACKAGE_RESTART_OUTCOME="${classification}"
        printf 'candidate_outcome=%s\n' "${classification}" >>"${E2E_ARTIFACT_DIR}/package-restart-result.txt"
        return 0
        ;;
      blocked)
        fail "candidate package-restart recovery remained blocked instead of converging"
        ;;
      progress) ;;
      *) fail "unknown package-restart candidate classification ${classification}" ;;
    esac
    sleep 0.2
  done
  fail "candidate package-restart recovery did not converge"
}

assert_clean_recovery_view() {
  expect_secret_success "recover-clean-json" run_client recover --json
  python3 - "${LAST_STDOUT}" <<'PY_RECOVERY'
import json,sys
with open(sys.argv[1],encoding='utf-8') as handle: payload=json.load(handle)
recovery=payload.get('recovery') or {}
if recovery.get('candidates'):
    raise SystemExit('clean recovery view still contains candidates')
session=recovery.get('network_session')
if session and session.get('next_action') not in (None,'none'):
    raise SystemExit('clean recovery view still contains Network Session work')
PY_RECOVERY
}

assert_terminal_clean() {
  local phase="$1"
  sudo -n test ! -e "${SESSION_STATE}" || fail "${phase}: Network Session authority remains"
  if sudo -n test -d "${TRANSACTION_DIR}" && \
    sudo -n find "${TRANSACTION_DIR}" -maxdepth 1 -type f -name '*.json' -print -quit | grep -q .; then
    fail "${phase}: transaction cleanup authority remains"
  fi
  if sudo -n ip link show dev podlaz0 >/dev/null 2>&1; then
    fail "${phase}: podlaz0 remains after terminal convergence"
  fi
  assert_clean_recovery_view
}

cleanup() {
  local code=$?
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

: >"${E2E_ARTIFACT_DIR}/package-restart-result.txt"
printf '%s\n' "${V0240_ACTUAL_SHA256}" >"${E2E_ARTIFACT_DIR}/v0.2.40-package.sha256"
printf '%s\n' "$(sha256sum "${CANDIDATE}" | awk '{print $1}')" >"${E2E_ARTIFACT_DIR}/candidate-package.sha256"

log "prove ordinary networking before package boundary"
check_https_and_dns baseline-ordinary

log "reproduce exact v0.2.40 package-restart resume boundary"
install_setup_package "${PREVIOUS}" v0.2.40-package-restart true
PROFILE_URI="$(first_configured_profile_uri)"
assert_nonempty "${PROFILE_URI}" "private profile URI"
mask_multiline_sensitive "${PROFILE_URI}"
expect_secret_success "import-profile" run_client profile import "${PROFILE_URI}"
PROFILE_ID="$(awk '/^Imported profile:/ {print $3}' "${LAST_STDOUT}")"
assert_nonempty "${PROFILE_ID}" "imported profile id"
mask_multiline_sensitive "${PROFILE_ID}"
expect_secret_success "validate-v0240-profile" run_client profile validate "${PROFILE_ID}" --mode tun
create_tun_foreign_state
FOREIGN_STATE_CREATED=1
assert_tun_foreign_state v0.2.40-package-restart-baseline
expect_secret_success "connect-v0240" run_client connect --mode tun "${PROFILE_ID}"
wait_for_verified_active v0.2.40-package-restart-active
capture_v0240_package_restart_authority
assert_tun_foreign_state v0.2.40-package-restart-active
check_https_and_dns v0.2.40-package-restart-vpn

install_candidate_package_replacement "${CANDIDATE}"
assert_v0240_package_restart_failure
assert_tun_foreign_state package-restart-after-candidate
classify_package_restart_candidate

case "${PACKAGE_RESTART_OUTCOME}" in
  resumed)
    check_https_and_dns package-restart-resumed-vpn
    expect_secret_success "disconnect-resumed-candidate" run_client disconnect
    check_https_and_dns package-restart-terminal-ordinary
    ;;
  terminal)
    check_https_and_dns package-restart-terminal-ordinary
    ;;
  *) fail "package-restart candidate outcome is unavailable" ;;
esac

assert_terminal_clean package-restart-terminal
assert_tun_foreign_state package-restart-terminal

log "prove package-restart recovery is clean and idempotent"
expect_secret_success "package-restart-first-recovery" run_client recover --execute --yes
assert_terminal_clean package-restart-first-recovery
assert_tun_foreign_state package-restart-first-recovery
expect_secret_success "package-restart-second-recovery" run_client recover --execute --yes
assert_terminal_clean package-restart-second-recovery
assert_tun_foreign_state package-restart-second-recovery
printf 'package_restart_second_recovery_clean=true\n' >>"${E2E_ARTIFACT_DIR}/package-restart-result.txt"

assert_artifacts_do_not_contain_sensitive_values \
  "tun-package-restart-recovery" \
  "${PODLAZ_E2E_PROFILE_URI}" "${PODLAZ_E2E_PROFILE_URI_LIST}" "${PROFILE_URI}" "${PROFILE_ID}"

log "TUN package-restart recovery acceptance completed"
