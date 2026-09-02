#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"
# shellcheck source=lib/host_state.sh
source "${SCRIPT_DIR}/lib/host_state.sh"
# shellcheck source=lib/profile_input.sh
source "${SCRIPT_DIR}/lib/profile_input.sh"

require_cmd bash go python3 grep awk sed mktemp sudo systemctl journalctl apt curl getent ip ss timeout dpkg nft

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
DIAGNOSTIC_REPORT="/run/podlaz/diagnostics/tun-last.json"
HOOK_DIR="/run/podlaz/e2e-tun-hooks"
HOOK_EVENTS="${HOOK_DIR}/events.log"
HOOK_DROPIN_DIR="/run/systemd/system/podlazd.service.d"
HOOK_DROPIN="${HOOK_DROPIN_DIR}/e2e-tun-hooks.conf"
FOREIGN_NFT_FAMILY="inet"
FOREIGN_NFT_TABLE="podlaz_e2e_foreign_guard"
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

# Deliberately local: this scenario uses sudo's socket-user identity rather than
# the installed-client runuser contract used by package acceptance scenarios.
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

expect_secret_exit_code() {
  local name="$1" expected="$2"
  shift 2
  set +e
  capture_secret_command "${name}" "$@"
  local code=$?
  set -e
  [[ "${code}" == "${expected}" ]] || fail "${name} returned exit code ${code}; expected ${expected}"
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

# Deliberately local: timeout diagnostics must be captured before the failure.
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

create_foreign_nft_sentinel() {
  sudo -n nft delete table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}" >/dev/null 2>&1 || true
  sudo -n nft add table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}"
}

assert_foreign_nft_sentinel() {
  local phase="$1"
  sudo -n nft list table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}" >"${E2E_ARTIFACT_DIR}/foreign-nft-${phase}.txt" 2>&1 || \
    fail "${phase}: unrelated nftables sentinel was removed"
}

cleanup_tun_fault_injection() {
  local code=$?
  if [[ -n "${ACTIVE_CONNECT_PID}" ]]; then
    wait "${ACTIVE_CONNECT_PID}" >/dev/null 2>&1 || true
  fi
  clear_tun_hook
  sudo -n nft delete table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}" >/dev/null 2>&1 || true
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
  assert_foreign_nft_sentinel "${phase}"
}

copy_root_file() {
  local source="$1" target="$2"
  sudo -n test -f "${source}" || fail "required root-owned evidence file is missing: ${source}"
  sudo -n cat "${source}" >"${target}"
}

assert_event_present() {
  local path="$1" event="$2"
  grep -Fx "${event}" "${path}" >/dev/null || fail "event ${event} is missing from ${path}"
}

assert_event_order() {
  local path="$1" first="$2" second="$3"
  python3 - "${path}" "${first}" "${second}" <<'PY'
import sys
path, first, second = sys.argv[1:]
with open(path, encoding="utf-8") as handle:
    events = [line.strip() for line in handle if line.strip()]
try:
    first_index = events.index(first)
    second_index = events.index(second)
except ValueError as exc:
    raise SystemExit(f"missing lifecycle event: {exc}; events={events}")
if first_index >= second_index:
    raise SystemExit(f"invalid lifecycle event order: {first}={first_index}, {second}={second_index}, events={events}")
PY
}

assert_failure_report() {
  local path="$1" phase="$2" classification="$3" rollback="$4" historical="$5"
  python3 - "${path}" "${phase}" "${classification}" "${rollback}" "${historical}" <<'PY'
import json
import sys
path, phase, classification, rollback, historical = sys.argv[1:]
with open(path, encoding="utf-8") as handle:
    report = json.load(handle)
checks = {
    "failure_phase": phase,
    "primary_classification": classification,
    "rollback_status": rollback,
}
for key, expected in checks.items():
    actual = report.get(key)
    if actual != expected:
        raise SystemExit(f"{path}: {key}={actual!r}, expected {expected!r}")
if report.get("primary_classification") in {"healthy", "degraded", "unhealthy", "unavailable"}:
    raise SystemExit(f"{path}: overall status leaked into classification taxonomy")
if historical == "true" and report.get("historical") is not True:
    raise SystemExit(f"{path}: expected historical report after daemon reload")
PY
}

capture_failure_evidence() {
  local phase="$1" classification="$2"
  local events="${E2E_ARTIFACT_DIR}/${phase}-events.log"
  local report="${E2E_ARTIFACT_DIR}/${phase}-report.json"
  copy_root_file "${HOOK_EVENTS}" "${events}"
  copy_root_file "${DIAGNOSTIC_REPORT}" "${report}"
  assert_event_present "${events}" "diagnostics-persisted"
  assert_event_present "${events}" "diagnostics-finalized-completed"
  assert_event_present "${events}" "rollback-completed"
  assert_event_order "${events}" "diagnostics-persisted" "rollback-started"
  assert_event_order "${events}" "rollback-started" "rollback-completed"
  assert_failure_report "${report}" "${phase}" "${classification}" "completed" any
}

assert_tun_owned_runtime_absent() {
  local phase="$1" state routes rules

  if inspect_link_state podlaz0; then
    :
  else
    state=$?
    case "${state}" in
      1) fail "${phase}: podlaz0 still exists after production rollback" ;;
      *) fail "${phase}: podlaz0 absence could not be inspected" ;;
    esac
  fi

  if ! routes="$(sudo -n ip -4 route show table 51820 2>/dev/null)"; then
    fail "${phase}: podlaz routing table absence could not be inspected"
  fi
  [[ -z "${routes}" ]] || fail "${phase}: podlaz routing table still contains routes"

  if ! rules="$(sudo -n ip -4 rule show 2>/dev/null)"; then
    fail "${phase}: podlaz policy-rule absence could not be inspected"
  fi
  if grep -E '^(9999|10000):' <<<"${rules}" >/dev/null; then
    fail "${phase}: podlaz policy rule still exists"
  fi

  if inspect_resolved_link_state podlaz0; then
    :
  else
    state=$?
    case "${state}" in
      1) fail "${phase}: systemd-resolved still has podlaz0 state" ;;
      *) fail "${phase}: systemd-resolved podlaz0 absence could not be inspected" ;;
    esac
  fi

  if inspect_nft_table_state inet podlaz; then
    :
  else
    state=$?
    case "${state}" in
      1) fail "${phase}: podlaz nftables table still exists" ;;
      *) fail "${phase}: podlaz nftables absence could not be inspected" ;;
    esac
  fi
}

run_apply_failure_probe() {
  local hook_phase="$1" id="$2" classification="$3" injected_event="$4"
  log "TUN apply failure probe: ${hook_phase}"
  configure_tun_hook "${hook_phase}"
  collect_host_snapshot "before-${hook_phase}"
  set +e
  capture_secret_command "connect-${hook_phase}" run_podlaz_as_socket_user connect --mode tun "${id}"
  local code=$?
  set -e
  [[ "${code}" != "0" ]] || fail "${hook_phase}: connect unexpectedly succeeded"
  capture_failure_evidence network-apply "${classification}"
  assert_event_present "${E2E_ARTIFACT_DIR}/network-apply-events.log" "${injected_event}"
  assert_foreign_nft_sentinel "after-${hook_phase}-failure"
  if [[ "${hook_phase}" == "tun-address-apply" ]]; then
    assert_tun_owned_runtime_absent "rollback-${hook_phase}"
    assert_no_stale_state "rollback-${hook_phase}"
    expect_secret_success "connect-${hook_phase}-immediate-retry" run_podlaz_as_socket_user connect --mode tun "${id}"
    expect_secret_success "disconnect-${hook_phase}-immediate-retry" run_podlaz_as_socket_user disconnect
    assert_tun_owned_runtime_absent "after-${hook_phase}-retry"
    assert_no_stale_state "after-${hook_phase}-retry"
    check_direct_connectivity "after-${hook_phase}-retry"
    collect_host_snapshot "after-${hook_phase}-retry"
    clear_tun_hook
    return 0
  fi
  clear_tun_hook
  sudo -n systemctl restart podlazd.service
  wait_for_daemon_socket
  expect_secret_success "recover-execute-${hook_phase}" run_podlaz_as_socket_user recover --execute --yes
  check_direct_connectivity "after-${hook_phase}"
  assert_no_stale_state "after-${hook_phase}"
  collect_host_snapshot "after-${hook_phase}"
}

run_network_verify_probe() {
  local id="$1" phase="network-verify"
  log "TUN network verification failure and diagnostic persistence probe"
  configure_tun_hook "${phase}"
  collect_host_snapshot "before-${phase}"
  set +e
  capture_secret_command "connect-${phase}" run_podlaz_as_socket_user connect --mode tun "${id}"
  local code=$?
  set -e
  [[ "${code}" != "0" ]] || fail "${phase}: connect unexpectedly succeeded"
  capture_failure_evidence "${phase}" network_verify_failure
  assert_event_present "${E2E_ARTIFACT_DIR}/${phase}-events.log" network-verify-injected
  assert_foreign_nft_sentinel "after-${phase}-failure"

  clear_tun_hook
  sudo -n systemctl restart podlazd.service
  wait_for_daemon_socket
  expect_secret_exit_code "doctor-${phase}-historical-json" 3 run_podlaz_as_socket_user doctor --tun --json
  assert_json_file "${LAST_STDOUT}"
  assert_failure_report "${LAST_STDOUT}" "${phase}" network_verify_failure completed true

  expect_secret_success "connect-${phase}-immediate-retry" run_podlaz_as_socket_user connect --mode tun "${id}"
  expect_secret_success "disconnect-${phase}-immediate-retry" run_podlaz_as_socket_user disconnect
  check_direct_connectivity "after-${phase}-retry"
  assert_no_stale_state "after-${phase}-retry"
  collect_host_snapshot "after-${phase}-retry"
}

run_inactive_scope_probe() {
  local id="$1" phase="dns-inactive-scope"
  log "Packaged resolved verification with synthetic Current Scopes: none"
  configure_tun_hook "${phase}"
  collect_host_snapshot "before-${phase}"
  expect_secret_success "connect-${phase}" run_podlaz_as_socket_user connect --mode tun "${id}"

  local events="${E2E_ARTIFACT_DIR}/${phase}-events.log"
  local status="${E2E_ARTIFACT_DIR}/${phase}-resolvectl-status.txt"
  copy_root_file "${HOOK_EVENTS}" "${events}"
  assert_event_present "${events}" resolved-current-scopes-none
  sudo -n resolvectl status podlaz0 --no-pager >"${status}" 2>&1
  grep -F "DNS Servers:" "${status}" >/dev/null || fail "${phase}: resolved status has no configured DNS servers"
  grep -F "DNS Domain: ~." "${status}" >/dev/null || fail "${phase}: resolved status has no route-only domain"
  grep -F "+DefaultRoute" "${status}" >/dev/null || fail "${phase}: resolved status has no DNS default route"
  assert_foreign_nft_sentinel "during-${phase}"

  expect_secret_success "disconnect-${phase}" run_podlaz_as_socket_user disconnect
  clear_tun_hook
  sudo -n systemctl restart podlazd.service
  wait_for_daemon_socket
  check_direct_connectivity "after-${phase}"
  assert_no_stale_state "after-${phase}"
  collect_host_snapshot "after-${phase}"
}

run_resolved_subprocess_matrix() {
  log "Real subprocess matrix for strict resolved missing-device semantics"
  go test ./internal/recovery \
    -run 'Test(ResolvedMissingDeviceRecoveryValidatesRealProcessOutcome|TransactionDNSRollbackValidatesRealProcessOutcome)$' \
    -count=1 -v 2>&1 | tee "${E2E_ARTIFACT_DIR}/resolved-subprocess-matrix.log"
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
PRIMARY_URI="$(first_configured_profile_uri)"
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
create_foreign_nft_sentinel
assert_foreign_nft_sentinel initial

run_resolved_subprocess_matrix
run_apply_failure_probe tun-address-apply "${PROFILE_ID}" tun_address_apply_failure tun-address-apply-injected
run_apply_failure_probe dns-apply "${PROFILE_ID}" network_apply_failure dns-apply-injected
run_apply_failure_probe route-apply "${PROFILE_ID}" network_apply_failure route-apply-injected
run_network_verify_probe "${PROFILE_ID}"
run_inactive_scope_probe "${PROFILE_ID}"
run_before_commit_probe "${PROFILE_ID}"
assert_foreign_nft_sentinel final
assert_artifacts_do_not_contain_sensitive_values "tun-fault-injection" "${PODLAZ_E2E_PROFILE_URI}" "${PODLAZ_E2E_PROFILE_URI_LIST}"

log "TUN fault-injection e2e completed"
