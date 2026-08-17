#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"
# shellcheck source=lib/tun_package_assertions.sh
source "${SCRIPT_DIR}/lib/tun_package_assertions.sh"

require_cmd apt awk bash curl dpkg dpkg-deb find getent git grep ip mktemp nft python3 readlink resolvectl runuser sed sha256sum sudo systemctl timeout tr

: "${PODLAZ_E2E_PROFILE_URI:=}"
: "${PODLAZ_E2E_PROFILE_URI_LIST:=}"
: "${PODLAZ_E2E_DNS_CHECK_HOST:=github.com}"
: "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL:=https://api.ipify.org}"
: "${PODLAZ_DEB_ARCH:=$(dpkg --print-architecture)}"

if [[ -z "${PODLAZ_E2E_PROFILE_URI}" && -z "${PODLAZ_E2E_PROFILE_URI_LIST}" ]]; then
  fail "PODLAZ_E2E_PROFILE_URI or PODLAZ_E2E_PROFILE_URI_LIST is required"
fi
if [[ "${PODLAZ_DEB_ARCH}" != "$(dpkg --print-architecture)" ]]; then
  fail "issue 256 package acceptance requires a native .deb"
fi

DEV_DEB="dist/podlaz_0.0.0~dev-1_linux_${PODLAZ_DEB_ARCH}.deb"
DAEMON_SOCKET="/run/podlaz/podlazd.sock"
TRANSACTION_DIR="/run/podlaz/transactions"
FALLBACK_NETWORK_HELPER="${SCRIPT_DIR}/tun-package-fallback-network.py"
ORPHAN_SERVER_PRIORITY="9999"
ORPHAN_TUN_PRIORITY="10000"
ORPHAN_SERVER_TARGET="203.0.113.10"
ORPHAN_ROUTE_TABLE="51820"
BASELINE_VERSION="0.0.0~0issue256base"
BASELINE_DEB_VERSION="${BASELINE_VERSION}-1"

ACTIVE_CONNECTION=0
ORPHAN_STATE=0
BASE_WORKTREE=""
BASE_DEB=""
BASE_COMMIT=""
BUILD_COMMIT="${GITHUB_SHA:-$(git rev-parse HEAD)}"
HOST_SENSITIVE_VALUES=""

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
  local key="$1" value="$2"
  case "${key}${value}" in
    *$'\n'*|*$'\r'*) fail "invalid normalized issue 256 evidence" ;;
  esac
  printf '%s=%s\n' "${key}" "${value}" >>"${E2E_ARTIFACT_DIR}/issue256-acceptance.txt"
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
    if [[ -S "${DAEMON_SOCKET}" ]] && sudo -n systemctl is-active --quiet podlazd.service; then
      return 0
    fi
    sleep 0.1
  done
  fail "podlazd.service did not become ready"
}

run_installed_podlaz() {
  sudo -n runuser -u "$(id -un)" -g podlaz -- env \
    XDG_CONFIG_HOME="${XDG_CONFIG_HOME}" \
    XDG_STATE_HOME="${XDG_STATE_HOME}" \
    XDG_CACHE_HOME="${XDG_CACHE_HOME}" \
    /usr/bin/podlaz "$@"
}

remove_orphan_routing_state() {
  sudo -n ip -4 rule del priority "${ORPHAN_SERVER_PRIORITY}" to "${ORPHAN_SERVER_TARGET}/32" lookup main >/dev/null 2>&1 || true
  sudo -n ip -4 rule del priority "${ORPHAN_TUN_PRIORITY}" lookup "${ORPHAN_ROUTE_TABLE}" >/dev/null 2>&1 || true
  ORPHAN_STATE=0
}

restore_current_package() {
  if [[ ! -f "${DEV_DEB}" ]]; then
    return 1
  fi
  if /usr/bin/podlaz version 2>/dev/null | grep -F "${BUILD_COMMIT}" >/dev/null; then
    return 0
  fi
  sudo -n apt install --allow-downgrades -y "./${DEV_DEB}" >/dev/null 2>&1 || return 1
  sudo -n systemctl daemon-reload >/dev/null 2>&1 || true
  sudo -n systemctl restart podlazd.service >/dev/null 2>&1 || true
  wait_for_daemon_socket
}

cleanup() {
  local code=$? cleanup_code=0
  if [[ "${ACTIVE_CONNECTION}" == "1" ]]; then
    run_installed_podlaz disconnect >/dev/null 2>&1 || cleanup_code=1
    ACTIVE_CONNECTION=0
  fi
  if [[ "${ORPHAN_STATE}" == "1" ]]; then
    remove_orphan_routing_state
  fi
  if [[ -n "${BASE_WORKTREE}" && -d "${BASE_WORKTREE}" ]]; then
    git worktree remove --force "${BASE_WORKTREE}" >/dev/null 2>&1 || cleanup_code=1
    BASE_WORKTREE=""
  fi
  restore_current_package || cleanup_code=1
  if [[ "${code}" == "0" && "${cleanup_code}" != "0" ]]; then
    code="${cleanup_code}"
  fi
  exit "${code}"
}
trap cleanup EXIT

capture_profile_import() {
  local uri="$1" out err
  out="$(mktemp "${E2E_TMP_ROOT}/issue256-profile-import.stdout.XXXXXX")"
  err="$(mktemp "${E2E_TMP_ROOT}/issue256-profile-import.stderr.XXXXXX")"
  if ! run_installed_podlaz profile import "${uri}" >"${out}" 2>"${err}"; then
    fail "issue 256 profile import failed"
  fi
  PROFILE_ID="$(awk '/^Imported profile:/ {print $3}' "${out}")"
  assert_nonempty "${PROFILE_ID}" "issue 256 imported profile id"
  rm -f -- "${out}" "${err}"
}

assert_recovery_plan_empty() {
  local phase="$1" output
  output="$(mktemp "${E2E_TMP_ROOT}/issue256-recover-${phase}.XXXXXX")"
  run_installed_podlaz recover --json >"${output}" 2>/dev/null || fail "${phase}: recovery inspection failed"
  python3 - "${output}" "${phase}" <<'PY'
import json
import sys
path, phase = sys.argv[1:]
with open(path, encoding="utf-8") as handle:
    payload = json.load(handle)
recovery = payload.get("recovery", {})
candidates = recovery.get("candidates", [])
warnings = recovery.get("warnings", [])
if candidates or warnings:
    raise SystemExit(f"{phase}: recovery plan is not empty: candidates={candidates!r} warnings={warnings!r}")
PY
  rm -f -- "${output}"
  write_evidence "recovery_clean_${phase}" pass
}

snapshot_tun_network_manifest() {
  local phase="$1" manifest="$2"
  sudo -n rm -f -- "${manifest}" >/dev/null 2>&1 || fail "${phase}: failed to clear network manifest"
  sudo -n python3 "${FALLBACK_NETWORK_HELPER}" snapshot "${TRANSACTION_DIR}" "${manifest}" >/dev/null || \
    fail "${phase}: exact route/rule ownership snapshot failed"
  sudo -n test -f "${manifest}" || fail "${phase}: exact route/rule manifest was not persisted"
}

assert_clean_after_owned_lifecycle() {
  local phase="$1" manifest="$2"
  verify_tun_package_resources_absent "${phase}" "${FALLBACK_NETWORK_HELPER}" "${manifest}" || \
    fail "${phase}: podlaz-owned resources remain"
  assert_recovery_plan_empty "${phase}"
}

check_direct_connectivity() {
  local phase="$1" public_ip
  getent hosts "${PODLAZ_E2E_DNS_CHECK_HOST}" >/dev/null 2>&1 || fail "${phase}: direct DNS resolution failed"
  public_ip="$(curl -4 -fsS --max-time 30 "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL}")"
  [[ -n "${public_ip}" ]] || fail "${phase}: direct IPv4 egress returned an empty response"
  append_sensitive_value "${public_ip}"
  write_evidence "direct_connectivity_${phase}" pass
}

assert_reserved_priorities_absent() {
  local rules
  rules="$(mktemp "${E2E_TMP_ROOT}/issue256-rules-clean.XXXXXX")"
  sudo -n ip -4 rule show >"${rules}"
  if grep -E "^(${ORPHAN_SERVER_PRIORITY}|${ORPHAN_TUN_PRIORITY}):" "${rules}" >/dev/null; then
    fail "reserved Podlaz rule priorities are already occupied before orphan fixture"
  fi
  rm -f -- "${rules}"
}

orphan_server_rule_present() {
  sudo -n ip -4 rule show | \
    grep -F "${ORPHAN_SERVER_PRIORITY}:" | \
    grep -F "to ${ORPHAN_SERVER_TARGET}" | \
    grep -E 'lookup main([[:space:]]|$)' >/dev/null
}

orphan_tun_rule_present() {
  sudo -n ip -4 rule show | \
    grep -F "${ORPHAN_TUN_PRIORITY}:" | \
    grep -E "lookup (${ORPHAN_ROUTE_TABLE}|podlaz)([[:space:]]|$)" >/dev/null
}

assert_no_mutation_beyond_orphan_rules() {
  local phase="$1" routes route_err
  orphan_server_rule_present || fail "${phase}: orphan server-bypass rule was mutated"
  orphan_tun_rule_present || fail "${phase}: orphan TUN lookup rule was mutated"
  sudo -n ip link show dev podlaz0 >/dev/null 2>&1 && fail "${phase}: podlaz0 was created despite preflight block"
  sudo -n nft list table inet podlaz >/dev/null 2>&1 && fail "${phase}: podlaz nftables table was created despite preflight block"
  if sudo -n test -d "${TRANSACTION_DIR}" && sudo -n find "${TRANSACTION_DIR}" -type f -name '*.json' -print -quit | grep -q .; then
    fail "${phase}: transaction state was created despite preflight block"
  fi
  routes="$(mktemp "${E2E_TMP_ROOT}/issue256-orphan-routes.XXXXXX")"
  route_err="$(mktemp "${E2E_TMP_ROOT}/issue256-orphan-routes.stderr.XXXXXX")"
  set +e
  sudo -n ip -4 route show table "${ORPHAN_ROUTE_TABLE}" >"${routes}" 2>"${route_err}"
  local route_code=$?
  set -e
  if [[ "${route_code}" == "0" ]]; then
    [[ ! -s "${routes}" ]] || fail "${phase}: route table ${ORPHAN_ROUTE_TABLE} was mutated"
  else
    grep -F "FIB table does not exist" "${route_err}" >/dev/null || fail "${phase}: route table inspection failed unexpectedly"
  fi
  rm -f -- "${routes}" "${route_err}"
}

run_orphan_routing_convergence_probe() {
  local output code
  assert_recovery_plan_empty orphan-precondition
  assert_reserved_priorities_absent
  sudo -n ip -4 rule add priority "${ORPHAN_SERVER_PRIORITY}" to "${ORPHAN_SERVER_TARGET}/32" lookup main
  sudo -n ip -4 rule add priority "${ORPHAN_TUN_PRIORITY}" lookup "${ORPHAN_ROUTE_TABLE}"
  ORPHAN_STATE=1
  assert_no_mutation_beyond_orphan_rules orphan-fixture

  output="$(mktemp "${E2E_TMP_ROOT}/issue256-orphan-connect.XXXXXX")"
  set +e
  run_installed_podlaz connect --mode tun "${PROFILE_ID}" >"${output}" 2>&1
  code=$?
  set -e
  [[ "${code}" != "0" ]] || fail "orphan-routing: TUN connect unexpectedly succeeded"
  grep -F "ambiguous stale routing state blocks TUN connect before network mutation" "${output}" >/dev/null || \
    fail "orphan-routing: connect did not publish the ambiguous/unrecoverable classification"
  grep -F "ownership evidence is unavailable" "${output}" >/dev/null || \
    fail "orphan-routing: connect did not explain missing ownership evidence"
  if grep -F "recover --execute" "${output}" >/dev/null; then
    fail "orphan-routing: connect still recommends unauthoritative recovery"
  fi
  assert_no_mutation_beyond_orphan_rules orphan-after-connect
  assert_recovery_plan_empty orphan-same-host-state
  assert_no_mutation_beyond_orphan_rules orphan-after-recovery-inspection

  remove_orphan_routing_state
  assert_recovery_plan_empty orphan-after-manual-owned-cleanup
  write_evidence orphan_routing_preflight_recovery_convergence pass
  rm -f -- "${output}"
}

verify_systemd_255() {
  local version
  version="$(systemctl --version | awk 'NR == 1 {print $2}')"
  [[ "${version}" == "255" ]] || fail "issue 256 resolved acceptance requires systemd 255, got ${version:-unknown}"
  write_evidence systemd_version_255 pass
}

assert_supported_exit_zero_resolved_missing_link() {
  local phase="$1" stdout stderr code
  stdout="$(mktemp "${E2E_TMP_ROOT}/issue256-resolved-${phase}.stdout.XXXXXX")"
  stderr="$(mktemp "${E2E_TMP_ROOT}/issue256-resolved-${phase}.stderr.XXXXXX")"
  set +e
  sudo -n resolvectl status podlaz0 --no-pager >"${stdout}" 2>"${stderr}"
  code=$?
  set -e
  [[ "${code}" == "0" ]] || fail "${phase}: resolvectl status podlaz0 did not return supported exit 0"
  [[ ! -s "${stdout}" ]] || fail "${phase}: supported exit-0 missing-link stdout is not empty"
  python3 - "${stderr}" <<'PY'
import sys
from pathlib import Path
raw = Path(sys.argv[1]).read_bytes()
marker = b'Failed to resolve interface "podlaz0", ignoring: No such device'
if raw not in (marker + b"\n", marker + b"\r\n"):
    raise SystemExit(f"unexpected exit-0 missing-link stderr: {raw!r}")
PY
  rm -f -- "${stdout}" "${stderr}"
}

assert_doctor_resolved_clean() {
  local phase="$1" output code
  output="$(mktemp "${E2E_TMP_ROOT}/issue256-doctor-${phase}.XXXXXX")"
  set +e
  run_installed_podlaz doctor >"${output}" 2>&1
  code=$?
  set -e
  [[ "${code}" == "0" ]] || fail "${phase}: base doctor returned exit ${code} after clean disconnect"
  grep -F "resolved:" "${output}" | grep -F "no podlaz-owned DNS state found for podlaz0" >/dev/null || \
    fail "${phase}: doctor did not classify supported missing-link state as clean"
  if grep -F "run plz recover --execute --yes" "${output}" >/dev/null; then
    fail "${phase}: clean doctor output recommends impossible recovery"
  fi
  rm -f -- "${output}"
}

run_clean_disconnect_resolved_probe() {
  local manifest
  manifest="${E2E_TMP_ROOT}/issue256-clean-disconnect-network.json"
  verify_systemd_255
  run_installed_podlaz connect --mode tun "${PROFILE_ID}" >/dev/null 2>&1 || fail "clean-disconnect: TUN connect failed"
  ACTIVE_CONNECTION=1
  assert_tun_package_address_present clean-disconnect
  snapshot_tun_network_manifest clean-disconnect "${manifest}"
  run_installed_podlaz disconnect >/dev/null 2>&1 || fail "clean-disconnect: disconnect failed"
  ACTIVE_CONNECTION=0
  assert_clean_after_owned_lifecycle clean-disconnect "${manifest}"
  assert_supported_exit_zero_resolved_missing_link clean-disconnect
  assert_doctor_resolved_clean clean-disconnect
  write_evidence clean_disconnect_resolved_doctor pass
}

run_service_restart_probe() {
  local manifest retry_manifest
  manifest="${E2E_TMP_ROOT}/issue256-service-restart-network.json"
  retry_manifest="${E2E_TMP_ROOT}/issue256-service-restart-retry-network.json"
  run_installed_podlaz connect --mode tun "${PROFILE_ID}" >/dev/null 2>&1 || fail "service-restart: TUN connect failed"
  ACTIVE_CONNECTION=1
  assert_tun_package_address_present service-restart
  snapshot_tun_network_manifest service-restart "${manifest}"
  sudo -n systemctl restart podlazd.service
  ACTIVE_CONNECTION=0
  wait_for_daemon_socket
  assert_clean_after_owned_lifecycle service-restart "${manifest}"
  check_direct_connectivity service-restart

  run_installed_podlaz connect --mode tun "${PROFILE_ID}" >/dev/null 2>&1 || fail "service-restart: fresh TUN reconnect failed"
  ACTIVE_CONNECTION=1
  assert_tun_package_address_present service-restart-retry
  snapshot_tun_network_manifest service-restart-retry "${retry_manifest}"
  run_installed_podlaz disconnect >/dev/null 2>&1 || fail "service-restart: retry disconnect failed"
  ACTIVE_CONNECTION=0
  assert_clean_after_owned_lifecycle service-restart-retry "${retry_manifest}"
  write_evidence active_tun_service_restart pass
}

build_baseline_package() {
  local base_dist current_version base_version
  BASE_COMMIT="${PODLAZ_E2E_BASE_SHA:-$(git merge-base HEAD origin/master)}"
  [[ -n "${BASE_COMMIT}" ]] || fail "could not resolve issue 256 baseline commit"
  [[ "${BASE_COMMIT}" != "${BUILD_COMMIT}" ]] || fail "baseline commit equals current head; package upgrade regression would be meaningless"
  BASE_WORKTREE="${E2E_TMP_ROOT}/issue256-baseline-worktree"
  base_dist="${E2E_TMP_ROOT}/issue256-baseline-dist"
  rm -rf -- "${BASE_WORKTREE}" "${base_dist}"
  git worktree add --detach "${BASE_WORKTREE}" "${BASE_COMMIT}" >/dev/null
  (
    cd "${BASE_WORKTREE}"
    PODLAZ_VERSION="${BASELINE_VERSION}" \
      PODLAZ_DEB_VERSION="${BASELINE_DEB_VERSION}" \
      PODLAZ_DIST_DIR="${base_dist}" \
      PODLAZ_DEB_ARCH="${PODLAZ_DEB_ARCH}" \
      PODLAZ_COMMIT="${BASE_COMMIT}" \
      PODLAZ_BUILT="issue256-baseline" \
      bash scripts/build-deb.sh >/dev/null
  )
  git worktree remove --force "${BASE_WORKTREE}" >/dev/null
  BASE_WORKTREE=""
  BASE_DEB="${base_dist}/podlaz_${BASELINE_DEB_VERSION}_linux_${PODLAZ_DEB_ARCH}.deb"
  [[ -f "${BASE_DEB}" ]] || fail "baseline package was not built"
  current_version="$(dpkg-deb --field "${DEV_DEB}" Version)"
  base_version="$(dpkg-deb --field "${BASE_DEB}" Version)"
  dpkg --compare-versions "${base_version}" lt "${current_version}" || \
    fail "baseline package version ${base_version} is not older than current ${current_version}"
  write_evidence package_upgrade_baseline_built pass
}

assert_installed_commit() {
  local phase="$1" expected="$2" output
  output="$(mktemp "${E2E_TMP_ROOT}/issue256-version-${phase}.XXXXXX")"
  /usr/bin/podlaz version >"${output}"
  grep -F "${expected}" "${output}" >/dev/null || fail "${phase}: installed CLI does not identify expected commit"
  rm -f -- "${output}"
}

run_package_upgrade_restart_probe() {
  local manifest retry_manifest
  manifest="${E2E_TMP_ROOT}/issue256-package-upgrade-network.json"
  retry_manifest="${E2E_TMP_ROOT}/issue256-package-upgrade-retry-network.json"

  sudo -n apt install --allow-downgrades -y "${BASE_DEB}" >/dev/null
  sudo -n systemctl daemon-reload
  wait_for_daemon_socket
  assert_installed_commit package-baseline "${BASE_COMMIT}"
  assert_recovery_plan_empty package-baseline-precondition

  run_installed_podlaz connect --mode tun "${PROFILE_ID}" >/dev/null 2>&1 || fail "package-upgrade: baseline TUN connect failed"
  ACTIVE_CONNECTION=1
  assert_tun_package_address_present package-upgrade-baseline
  snapshot_tun_network_manifest package-upgrade-baseline "${manifest}"

  sudo -n apt install -y "./${DEV_DEB}" >/dev/null
  ACTIVE_CONNECTION=0
  sudo -n systemctl daemon-reload
  wait_for_daemon_socket
  assert_installed_commit package-upgraded "${BUILD_COMMIT}"
  assert_clean_after_owned_lifecycle package-upgrade "${manifest}"
  check_direct_connectivity package-upgrade

  run_installed_podlaz connect --mode tun "${PROFILE_ID}" >/dev/null 2>&1 || fail "package-upgrade: fresh TUN reconnect failed"
  ACTIVE_CONNECTION=1
  assert_tun_package_address_present package-upgrade-retry
  snapshot_tun_network_manifest package-upgrade-retry "${retry_manifest}"
  run_installed_podlaz disconnect >/dev/null 2>&1 || fail "package-upgrade: retry disconnect failed"
  ACTIVE_CONNECTION=0
  assert_clean_after_owned_lifecycle package-upgrade-retry "${retry_manifest}"
  write_evidence active_tun_package_upgrade_restart pass
}

setup_isolated_xdg "issue256-package-acceptance"
collect_host_sensitive_values
: >"${E2E_ARTIFACT_DIR}/issue256-acceptance.txt"

[[ -f "${DEV_DEB}" ]] || fail "current package artifact is missing: ${DEV_DEB}"
wait_for_daemon_socket
assert_installed_commit current-precondition "${BUILD_COMMIT}"

# shellcheck disable=SC1091
. packaging/package-toolchain.env
go install github.com/goreleaser/nfpm/v2/cmd/nfpm@"${NFPM_VERSION}"
export PATH="$(go env GOPATH)/bin:${PATH}"
build_baseline_package

PRIMARY_URI="$(first_profile_uri)"
assert_nonempty "${PRIMARY_URI}" "issue 256 primary profile URI"
capture_profile_import "${PRIMARY_URI}"

run_orphan_routing_convergence_probe
run_clean_disconnect_resolved_probe
run_service_restart_probe
run_package_upgrade_restart_probe
check_direct_connectivity final

assert_artifacts_do_not_contain_sensitive_values \
  "issue256-package-acceptance" \
  "${PODLAZ_E2E_PROFILE_URI}" \
  "${PODLAZ_E2E_PROFILE_URI_LIST}" \
  "${HOST_SENSITIVE_VALUES}"

write_evidence issue256_package_acceptance pass
log "issue 256 installed-package acceptance completed"