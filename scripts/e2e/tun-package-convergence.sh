#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"
# shellcheck source=lib/process_lifecycle.sh
source "${SCRIPT_DIR}/lib/process_lifecycle.sh"

require_cmd awk bash cmp curl dpkg dpkg-deb find getent git grep ip journalctl mktemp nft pgrep python3 readlink resolvectl sed sha256sum sleep sudo systemctl systemd-run timeout tr

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

CONNECT_PID=""
CONNECT_START_TIME=""
CONNECT_EXIT_CODE=""
HOST_SENSITIVE_VALUES=""
DEFAULT_ROUTE_BEFORE=""

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

write_evidence() {
  local file="$1" key="$2" value="$3"
  case "${key}${value}" in
    *$'\n'*|*$'\r'*) fail "invalid normalized evidence" ;;
  esac
  printf '%s=%s\n' "${key}" "${value}" >>"${E2E_ARTIFACT_DIR}/${file}"
}

append_sensitive_value() {
  local value="${1:-}"
  [[ -n "${value}" ]] || return 0
  HOST_SENSITIVE_VALUES+="${value}"$'\n'
  mask_multiline_sensitive "${value}"
}

collect_host_sensitive_values() {
  local values
  values="$({
    hostname -f 2>/dev/null || true
    ip -o -4 addr show scope global 2>/dev/null | awk '{split($4, value, "/"); print value[1]; print $2}'
    ip -o -6 addr show scope global 2>/dev/null | awk '{split($4, value, "/"); print value[1]; print $2}'
    ip -4 route show default 2>/dev/null | awk '{for (i=1; i<=NF; i++) {if ($i=="via" || $i=="dev") print $(i+1)}}'
  } | sed '/^[[:space:]]*$/d' | sort -u)"
  append_sensitive_value "${values}"
}

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
  fail "podlazd.service did not create its socket"
}

run_installed_podlaz() {
  sudo -n -u "$(id -un)" -g podlaz env \
    XDG_CONFIG_HOME="${XDG_CONFIG_HOME}" \
    XDG_STATE_HOME="${XDG_STATE_HOME}" \
    XDG_CACHE_HOME="${XDG_CACHE_HOME}" \
    /usr/bin/podlaz "$@"
}

capture_secret_import() {
  local uri="$1" out err
  out="$(mktemp "${E2E_TMP_ROOT}/profile-import.stdout.XXXXXX")"
  err="$(mktemp "${E2E_TMP_ROOT}/profile-import.stderr.XXXXXX")"
  if ! run_installed_podlaz profile import "${uri}" >"${out}" 2>"${err}"; then
    rm -f -- "${out}" "${err}"
    fail "profile import failed"
  fi
  PROFILE_ID="$(awk '/^Imported profile:/ {print $3}' "${out}")"
  rm -f -- "${out}" "${err}"
  assert_nonempty "${PROFILE_ID}" "imported profile id"
  write_evidence acceptance.txt profile_import pass
}

clear_hook() {
  local status=0
  sudo -n rm -f -- "${HOOK_DROPIN}" || status=1
  sudo -n rm -rf -- "${HOOK_DIR}" || status=1
  sudo -n systemctl daemon-reload || status=1
  return "${status}"
}

configure_hook() {
  local phase="$1" tmp
  clear_hook || fail "failed to clear previous E2E hook state"
  sudo -n mkdir -p "${HOOK_DROPIN_DIR}"
  tmp="$(mktemp "${E2E_TMP_ROOT}/podlaz-package-hook.XXXXXX")"
  cat >"${tmp}" <<EOF
[Service]
Environment=PODLAZ_E2E_TUN_HOOKS=true
Environment=PODLAZ_E2E_TUN_HOOK_PHASE=${phase}
Environment=PODLAZ_E2E_TUN_HOOK_DIR=${HOOK_DIR}
Environment=PODLAZ_E2E_TUN_HOOK_TIMEOUT_SECONDS=90
EOF
  sudo -n install -m 0644 "${tmp}" "${HOOK_DROPIN}"
  rm -f -- "${tmp}"
  sudo -n systemctl daemon-reload
  sudo -n systemctl restart podlazd.service
  wait_for_daemon_socket
}

wait_connect_bounded() {
  local pid="$1" attempts="${2:-600}" status
  [[ "${pid}" == "${CONNECT_PID}" && -n "${CONNECT_START_TIME}" ]] || return 2
  if wait_child_bounded "${pid}" "${CONNECT_START_TIME}" "${attempts}"; then
    CONNECT_EXIT_CODE="${WAIT_CHILD_EXIT_CODE}"
    CONNECT_PID=""
    CONNECT_START_TIME=""
    return 0
  fi
  status=$?
  return "${status}"
}

terminate_connect_bounded() {
  local pid="${CONNECT_PID:-}" start="${CONNECT_START_TIME:-}"
  [[ -n "${pid}" && -n "${start}" ]] || return 0
  if ! terminate_child_bounded "${pid}" "${start}" 50; then
    return 1
  fi
  CONNECT_EXIT_CODE="${WAIT_CHILD_EXIT_CODE}"
  CONNECT_PID=""
  CONNECT_START_TIME=""
}

cleanup() {
  local code=$? cleanup_code=0 purge=true
  terminate_connect_bounded || cleanup_code=1
  clear_hook || cleanup_code=1
  if [[ "${PODLAZ_E2E_KEEP_PACKAGE:-false}" == "true" ]]; then
    purge=false
  fi
  PODLAZ_E2E_PURGE_PACKAGE="${purge}" bash "${SCRIPT_DIR}/tun-package-cleanup.sh" || cleanup_code=1
  if [[ "${code}" == "0" && "${cleanup_code}" != "0" ]]; then
    code="${cleanup_code}"
  fi
  exit "${code}"
}
trap cleanup EXIT

sentinel_rule_present() {
  sudo -n ip -4 rule show 2>/dev/null | \
    grep -F "${FOREIGN_RULE_PRIORITY}:" | \
    grep -F "to ${FOREIGN_ROUTE_CIDR%/32}" | \
    grep -E "lookup (${FOREIGN_ROUTE_TABLE})([[:space:]]|$)" >/dev/null
}

sentinel_route_present() {
  sudo -n ip -4 route show table "${FOREIGN_ROUTE_TABLE}" exact "${FOREIGN_ROUTE_CIDR}" 2>/dev/null | \
    grep -F "blackhole ${FOREIGN_ROUTE_CIDR%/32}" >/dev/null
}

assert_sentinel_absent_before_create() {
  if sudo -n nft list table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}" >/dev/null 2>&1 || \
    sentinel_rule_present || sentinel_route_present || \
    sudo -n ip link show dev "${FOREIGN_DNS_LINK}" >/dev/null 2>&1 || \
    systemctl is-active --quiet "${FOREIGN_SERVICE}"; then
    fail "E2E sentinel residue exists before setup"
  fi
}

create_foreign_state() {
  assert_sentinel_absent_before_create
  sudo -n nft add table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}"
  sudo -n ip -4 route add blackhole "${FOREIGN_ROUTE_CIDR}" table "${FOREIGN_ROUTE_TABLE}"
  sudo -n ip -4 rule add priority "${FOREIGN_RULE_PRIORITY}" to "${FOREIGN_ROUTE_CIDR}" lookup "${FOREIGN_ROUTE_TABLE}"
  sudo -n ip link add "${FOREIGN_DNS_LINK}" type dummy
  sudo -n ip link set dev "${FOREIGN_DNS_LINK}" up
  sudo -n resolvectl dns "${FOREIGN_DNS_LINK}" "${FOREIGN_DNS_SERVER}"
  sudo -n resolvectl domain "${FOREIGN_DNS_LINK}" "${FOREIGN_DNS_DOMAIN}"
  sudo -n resolvectl default-route "${FOREIGN_DNS_LINK}" no
  sudo -n systemd-run --unit="${FOREIGN_SERVICE%.service}" --property=Type=simple /bin/sh -c 'exec sleep 600' >/dev/null
}

assert_foreign_state() {
  local phase="$1" tmp
  sudo -n nft list table "${FOREIGN_NFT_FAMILY}" "${FOREIGN_NFT_TABLE}" >/dev/null 2>&1 || fail "${phase}: unrelated nftables state changed"
  sentinel_route_present || fail "${phase}: unrelated route changed"
  sentinel_rule_present || fail "${phase}: unrelated policy rule changed"
  tmp="$(mktemp "${E2E_TMP_ROOT}/foreign-resolved.XXXXXX")"
  sudo -n resolvectl status "${FOREIGN_DNS_LINK}" --no-pager >"${tmp}"
  grep -F "${FOREIGN_DNS_SERVER}" "${tmp}" >/dev/null || fail "${phase}: unrelated DNS server changed"
  grep -F "${FOREIGN_DNS_DOMAIN}" "${tmp}" >/dev/null || fail "${phase}: unrelated DNS domain changed"
  rm -f -- "${tmp}"
  sudo -n systemctl is-active --quiet "${FOREIGN_SERVICE}" || fail "${phase}: unrelated service changed"
  write_evidence acceptance.txt "foreign_state_${phase}" pass
}

assert_podlaz_resources_absent() {
  local phase="$1"
  if sudo -n ip link show dev podlaz0 >/dev/null 2>&1; then
    fail "${phase}: podlaz0 still exists"
  fi
  if sudo -n resolvectl status --no-pager 2>/dev/null | grep -E '^Link [0-9]+ \(podlaz0\)$' >/dev/null; then
    fail "${phase}: resolved still has podlaz0"
  fi
  if sudo -n nft list table inet podlaz >/dev/null 2>&1; then
    fail "${phase}: inet podlaz remains"
  fi
  if pgrep -f '/usr/lib/podlaz/xray.*\/run\/podlaz\/generated\/' >/dev/null 2>&1; then
    fail "${phase}: transaction-owned Xray remains"
  fi
  if sudo -n test -d /run/podlaz/generated && sudo -n find /run/podlaz/generated -mindepth 1 -print -quit | grep -q .; then
    fail "${phase}: generated config remains"
  fi
  if sudo -n test -d /run/podlaz/transactions && sudo -n find /run/podlaz/transactions -mindepth 1 -print -quit | grep -q .; then
    fail "${phase}: transaction metadata remains"
  fi
  write_evidence acceptance.txt "resources_absent_${phase}" pass
}

assert_no_recovery_candidates() {
  local phase="$1" output
  output="$(mktemp "${E2E_TMP_ROOT}/recover.XXXXXX")"
  run_installed_podlaz recover --json >"${output}" 2>/dev/null || fail "${phase}: recovery inspection failed"
  python3 - "${output}" <<'PY'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as handle:
    payload = json.load(handle)
if payload.get("recovery", {}).get("candidates"):
    raise SystemExit("recovery candidates remain")
PY
  rm -f -- "${output}"
  write_evidence acceptance.txt "recovery_clean_${phase}" pass
}

assert_event_order() {
  local path="$1" first="$2" second="$3"
  python3 - "${path}" "${first}" "${second}" <<'PY'
import sys
path, first, second = sys.argv[1:]
with open(path, encoding="utf-8") as handle:
    events = [line.strip() for line in handle if line.strip()]
if first not in events or second not in events:
    raise SystemExit(f"missing lifecycle event: {first!r} or {second!r}")
if events.index(first) >= events.index(second):
    raise SystemExit(f"invalid lifecycle event order: {first!r} >= {second!r}")
PY
}

normalize_failure_report() {
  local source="$1" target="$2" historical="$3"
  python3 - "${source}" "${target}" "${historical}" <<'PY'
import json
import sys
source, target, expected_historical = sys.argv[1:]
with open(source, encoding="utf-8") as handle:
    report = json.load(handle)
checks = {
    "failure_phase": "network-verify",
    "primary_classification": "network_verify_failure",
    "rollback_status": "completed",
}
for key, expected in checks.items():
    if report.get(key) != expected:
        raise SystemExit(f"{key}={report.get(key)!r}, expected {expected!r}")
historical = report.get("historical") is True
if historical != (expected_historical == "true"):
    raise SystemExit(f"historical={historical!r}, expected {expected_historical!r}")
safe = {
    "schema_version": report.get("schema_version"),
    **checks,
    "historical": historical,
    "persistence": "verified",
}
with open(target, "w", encoding="utf-8") as handle:
    json.dump(safe, handle, sort_keys=True)
    handle.write("\n")
PY
}

check_direct_connectivity() {
  local public_ip
  getent hosts "${PODLAZ_E2E_DNS_CHECK_HOST}" >/dev/null 2>&1 || fail "direct DNS resolution failed"
  public_ip="$(curl -4 -fsS --max-time 30 "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL}")"
  [[ -n "${public_ip}" ]] || fail "direct IPv4 egress returned an empty response"
  append_sensitive_value "${public_ip}"
  write_evidence acceptance.txt direct_connectivity pass
}

verify_package_provenance() {
  local extract_dir expected_cli expected_daemon installed_cli installed_daemon main_pid running_exe running_hash version_output
  extract_dir="$(mktemp -d "${E2E_TMP_ROOT}/package-extract.XXXXXX")"
  dpkg-deb -x "${DEV_DEB}" "${extract_dir}"
  expected_cli="$(sha256sum "${extract_dir}/usr/bin/podlaz" | awk '{print $1}')"
  expected_daemon="$(sha256sum "${extract_dir}/usr/bin/podlazd" | awk '{print $1}')"
  installed_cli="$(sha256sum /usr/bin/podlaz | awk '{print $1}')"
  installed_daemon="$(sha256sum /usr/bin/podlazd | awk '{print $1}')"
  [[ "${installed_cli}" == "${expected_cli}" ]] || fail "installed podlaz hash does not match built package"
  [[ "${installed_daemon}" == "${expected_daemon}" ]] || fail "installed podlazd hash does not match built package"
  main_pid="$(systemctl show -p MainPID --value podlazd.service)"
  [[ "${main_pid}" =~ ^[1-9][0-9]*$ ]] || fail "podlazd.service has no running MainPID"
  running_exe="$(sudo -n readlink -f "/proc/${main_pid}/exe")"
  [[ "${running_exe}" == "/usr/bin/podlazd" ]] || fail "running daemon executable is not /usr/bin/podlazd"
  running_hash="$(sudo -n sha256sum "/proc/${main_pid}/exe" | awk '{print $1}')"
  [[ "${running_hash}" == "${expected_daemon}" ]] || fail "running daemon hash does not match built package"
  version_output="$(mktemp "${E2E_TMP_ROOT}/installed-version.XXXXXX")"
  /usr/bin/podlaz version >"${version_output}"
  grep -F "${BUILD_COMMIT}" "${version_output}" >/dev/null || fail "installed CLI version does not identify the tested commit"
  {
    printf 'package_sha256=%s\n' "$(sha256sum "${DEV_DEB}" | awk '{print $1}')"
    printf 'podlaz_sha256=%s\n' "${expected_cli}"
    printf 'podlazd_sha256=%s\n' "${expected_daemon}"
    printf 'commit=%s\n' "${BUILD_COMMIT}"
    printf 'running_daemon_match=pass\n'
  } >"${E2E_ARTIFACT_DIR}/package-provenance.txt"
  rm -rf -- "${extract_dir}" "${version_output}"
}

run_inactive_scope_probe() {
  local events tmp
  configure_hook dns-inactive-scope
  tmp="$(mktemp "${E2E_TMP_ROOT}/inactive-scope-connect.XXXXXX")"
  run_installed_podlaz connect --mode tun "${PROFILE_ID}" >"${tmp}" 2>&1 || fail "Current Scopes: none package connect failed"
  events="${E2E_ARTIFACT_DIR}/inactive-scope-events.log"
  sudo -n cat "${HOOK_EVENTS}" >"${events}"
  grep -Fx resolved-current-scopes-none "${events}" >/dev/null || fail "inactive-scope production fixture did not run"
  sudo -n resolvectl status podlaz0 --no-pager >"${tmp}"
  grep -F "DNS Servers:" "${tmp}" >/dev/null || fail "inactive-scope DNS servers missing"
  grep -F "DNS Domain: ~." "${tmp}" >/dev/null || fail "inactive-scope route-only domain missing"
  grep -F "+DefaultRoute" "${tmp}" >/dev/null || fail "inactive-scope default route missing"
  run_installed_podlaz disconnect >"${tmp}" 2>&1 || fail "inactive-scope disconnect failed"
  rm -f -- "${tmp}"
  clear_hook
  sudo -n systemctl restart podlazd.service
  wait_for_daemon_socket
  assert_no_recovery_candidates inactive-scope
  assert_podlaz_resources_absent inactive-scope
  assert_foreign_state inactive-scope
  write_evidence acceptance.txt current_scopes_none pass
}

run_missing_link_probe() {
  local attempt transaction_count revert_out revert_err wait_status connect_code events report doctor_output
  configure_hook dns-missing-link-rollback
  run_installed_podlaz connect --mode tun "${PROFILE_ID}" >"${E2E_TMP_ROOT}/missing-link-connect.stdout" 2>"${E2E_TMP_ROOT}/missing-link-connect.stderr" &
  CONNECT_PID=$!
  CONNECT_START_TIME="$(process_start_time "${CONNECT_PID}")"
  [[ -n "${CONNECT_START_TIME}" ]] || fail "failed to record connect process identity"
  for attempt in $(seq 1 900); do
    sudo -n test -f "${HOOK_READY}" && break
    sleep 0.1
  done
  sudo -n test -f "${HOOK_READY}" || fail "daemon did not reach DNS missing-link pause"
  sudo -n ip link show dev podlaz0 >/dev/null 2>&1 || fail "podlaz0 was not present before fault injection"
  sudo -n resolvectl status podlaz0 --no-pager >/dev/null 2>&1 || fail "resolved state was not present before fault injection"
  transaction_count="$(sudo -n find /run/podlaz/transactions -type f -name '*.json' | wc -l | tr -d '[:space:]')"
  [[ "${transaction_count}" -ge 1 ]] || fail "daemon-owned transaction was not persisted before fault injection"
  write_evidence acceptance.txt transaction_persisted_before_fault pass

  sudo -n ip link del dev podlaz0
  revert_out="$(mktemp "${E2E_TMP_ROOT}/resolvectl-revert.stdout.XXXXXX")"
  revert_err="$(mktemp "${E2E_TMP_ROOT}/resolvectl-revert.stderr.XXXXXX")"
  set +e
  sudo -n resolvectl revert podlaz0 >"${revert_out}" 2>"${revert_err}"
  REAL_REVERT_CODE=$?
  set -e
  [[ "${REAL_REVERT_CODE}" == "1" ]] || fail "real resolvectl revert did not return exit 1"
  [[ ! -s "${revert_out}" ]] || fail "real resolvectl missing-link stdout is not empty"
  [[ "$(tr -d '\r\n' <"${revert_err}")" == 'Failed to resolve interface "podlaz0": No such device' ]] || fail "real resolvectl missing-link stderr mismatch"
  rm -f -- "${revert_out}" "${revert_err}"
  write_evidence acceptance.txt real_resolvectl_missing_link pass

  sudo -n touch "${HOOK_CONTINUE}"
  set +e
  wait_connect_bounded "${CONNECT_PID}" 600
  wait_status=$?
  set -e
  if [[ "${wait_status}" != "0" ]]; then
    [[ "${wait_status}" != "124" ]] || fail "missing-link connect timed out"
    fail "missing-link connect wait failed"
  fi
  connect_code="${CONNECT_EXIT_CODE}"
  [[ "${connect_code}" != "0" ]] || fail "missing-link connect unexpectedly succeeded"

  events="${E2E_ARTIFACT_DIR}/missing-link-events.log"
  sudo -n cat "${HOOK_EVENTS}" >"${events}"
  for event in dns-missing-link-ready dns-missing-link-released diagnostics-persisted rollback-started dns-rollback-started rollback-completed; do
    grep -Fx "${event}" "${events}" >/dev/null || fail "missing lifecycle event: ${event}"
  done
  assert_event_order "${events}" diagnostics-persisted rollback-started
  assert_event_order "${events}" rollback-started dns-rollback-started
  assert_event_order "${events}" diagnostics-persisted dns-rollback-started

  report="$(mktemp "${E2E_TMP_ROOT}/missing-link-report.XXXXXX")"
  sudo -n cat "${DIAGNOSTIC_REPORT}" >"${report}"
  normalize_failure_report "${report}" "${E2E_ARTIFACT_DIR}/missing-link-report-summary.json" false
  rm -f -- "${report}"

  clear_hook
  sudo -n systemctl restart podlazd.service
  wait_for_daemon_socket
  doctor_output="$(mktemp "${E2E_TMP_ROOT}/historical-doctor.XXXXXX")"
  set +e
  run_installed_podlaz doctor --tun --json >"${doctor_output}" 2>/dev/null
  DOCTOR_CODE=$?
  set -e
  [[ "${DOCTOR_CODE}" == "3" ]] || fail "historical doctor returned unexpected exit code"
  normalize_failure_report "${doctor_output}" "${E2E_ARTIFACT_DIR}/historical-report-summary.json" true
  rm -f -- "${doctor_output}"

  assert_no_recovery_candidates missing-link
  assert_podlaz_resources_absent missing-link
  assert_foreign_state missing-link

  run_installed_podlaz connect --mode tun "${PROFILE_ID}" >/dev/null 2>&1 || fail "immediate retry connect failed"
  run_installed_podlaz disconnect >/dev/null 2>&1 || fail "immediate retry disconnect failed"
  assert_no_recovery_candidates retry
  assert_podlaz_resources_absent retry
  assert_foreign_state retry
  write_evidence acceptance.txt immediate_retry pass
}

setup_isolated_xdg "tun-package-convergence"
collect_host_sensitive_values
DEFAULT_ROUTE_BEFORE="$(mktemp "${E2E_TMP_ROOT}/default-route-before.XXXXXX")"
ip -4 route show default >"${DEFAULT_ROUTE_BEFORE}"

PODLAZ_E2E_PURGE_PACKAGE=true bash "${SCRIPT_DIR}/tun-package-cleanup.sh"
: >"${E2E_ARTIFACT_DIR}/acceptance.txt"

log "build release-like package"
# shellcheck disable=SC1091
. packaging/package-toolchain.env
go install github.com/goreleaser/nfpm/v2/cmd/nfpm@"${NFPM_VERSION}"
export PATH="$(go env GOPATH)/bin:${PATH}"
BUILD_COMMIT="${GITHUB_SHA:-$(git rev-parse HEAD)}"
PODLAZ_COMMIT="${BUILD_COMMIT}" PODLAZ_BUILT="${PODLAZ_E2E_BUILT:-$(date -u '+%b %d %Y')}" PODLAZ_DEB_ARCH="${PODLAZ_DEB_ARCH}" bash scripts/build-deb.sh >"${E2E_ARTIFACT_DIR}/build-deb.log" 2>&1
test -f "${DEV_DEB}" || fail "expected package was not built"

sudo -n apt install -y "./${DEV_DEB}" >"${E2E_ARTIFACT_DIR}/apt-install.log" 2>&1
sudo -n apt install --reinstall -y "./${DEV_DEB}" >"${E2E_ARTIFACT_DIR}/apt-reinstall.log" 2>&1
sudo -n systemctl daemon-reload
sudo -n systemctl restart podlazd.service
wait_for_daemon_socket
verify_package_provenance

PRIMARY_URI="$(first_profile_uri)"
assert_nonempty "${PRIMARY_URI}" "primary profile URI"
capture_secret_import "${PRIMARY_URI}"

create_foreign_state
assert_foreign_state before
RESOLVED_STATE_BEFORE="$(sudo -n systemctl is-active systemd-resolved)"
[[ "${RESOLVED_STATE_BEFORE}" == "active" ]] || fail "systemd-resolved is not active"

run_inactive_scope_probe
run_missing_link_probe
check_direct_connectivity

DEFAULT_ROUTE_AFTER="$(mktemp "${E2E_TMP_ROOT}/default-route-after.XXXXXX")"
ip -4 route show default >"${DEFAULT_ROUTE_AFTER}"
cmp -s "${DEFAULT_ROUTE_BEFORE}" "${DEFAULT_ROUTE_AFTER}" || fail "unrelated default route changed"
[[ "$(sudo -n systemctl is-active systemd-resolved)" == "${RESOLVED_STATE_BEFORE}" ]] || fail "systemd-resolved service state changed"
write_evidence acceptance.txt default_route_preserved pass
write_evidence acceptance.txt resolved_service_preserved pass

assert_artifacts_do_not_contain_sensitive_values \
  "tun-package-convergence" \
  "${PODLAZ_E2E_PROFILE_URI}" \
  "${PODLAZ_E2E_PROFILE_URI_LIST}" \
  "${HOST_SENSITIVE_VALUES}"

log "installed-package TUN convergence E2E completed"
