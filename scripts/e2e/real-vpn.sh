#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"
# shellcheck source=lib/package_provenance.sh
source "${SCRIPT_DIR}/lib/package_provenance.sh"

require_cmd bash go python3 grep awk sed mktemp

: "${PODLAZ_E2E_PROFILE_URI:=}"
: "${PODLAZ_E2E_ENABLE_LIFECYCLE:=false}"
: "${PODLAZ_E2E_ENABLE_TUN:=false}"
: "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL:=https://api.ipify.org}"
: "${PODLAZ_E2E_DNS_CHECK_HOST:=github.com}"
: "${PODLAZ_E2E_DEB_PATH:=}"
: "${PODLAZ_E2E_EXPECT_COMMIT:=}"

[[ -n "${PODLAZ_E2E_PROFILE_URI}" ]] || fail "PODLAZ_E2E_PROFILE_URI environment secret is required for real VPN e2e"
mask_value "${PODLAZ_E2E_PROFILE_URI}"
mask_value "${PODLAZ_E2E_SUBSCRIPTION_URL:-}"
mask_value "${PODLAZ_E2E_EXPECTED_EGRESS_IP:-}"

build_podlaz_binary
setup_isolated_xdg "real-vpn"
PODLAZ=("${PODLAZ_BIN}")

PROFILE_IMPORT_STDOUT="${E2E_ARTIFACT_DIR}/real-profile-import.stdout"
PROFILE_IMPORT_STDERR="${E2E_ARTIFACT_DIR}/real-profile-import.stderr"
log "import real e2e profile from PODLAZ_E2E_PROFILE_URI"
set +e
"${PODLAZ[@]}" profile import "${PODLAZ_E2E_PROFILE_URI}" >"${PROFILE_IMPORT_STDOUT}" 2>"${PROFILE_IMPORT_STDERR}"
PROFILE_IMPORT_CODE=$?
set -e
[[ "${PROFILE_IMPORT_CODE}" == "0" ]] || fail "real profile import failed with exit code ${PROFILE_IMPORT_CODE}"
PROFILE_ID="$(awk '/^Imported profile:/ {print $3}' "${PROFILE_IMPORT_STDOUT}")"
assert_nonempty "${PROFILE_ID}" "real imported profile id"
mask_value "${PROFILE_ID}"
assert_not_contains "${PROFILE_IMPORT_STDOUT}" "${PODLAZ_E2E_PROFILE_URI}"

expect_success real-profile-show "${PODLAZ[@]}" profile show "${PROFILE_ID}"
expect_success real-profile-show-json "${PODLAZ[@]}" profile show "${PROFILE_ID}" --json
assert_json_file "${LAST_STDOUT}"
expect_success real-profile-validate-proxy-only "${PODLAZ[@]}" profile validate "${PROFILE_ID}" --mode proxy-only
expect_success real-profile-validate-tun "${PODLAZ[@]}" profile validate "${PROFILE_ID}" --mode tun
expect_success real-plan-proxy-only "${PODLAZ[@]}" plan --mode proxy-only "${PROFILE_ID}"
expect_success real-plan-tun-dry-run "${PODLAZ[@]}" plan --mode tun "${PROFILE_ID}"
assert_contains "${LAST_STDOUT}" "No changes were applied."

if [[ "${PODLAZ_E2E_ENABLE_LIFECYCLE}" != "true" ]]; then
  log "real VPN lifecycle is disabled; set PODLAZ_E2E_ENABLE_LIFECYCLE=true to run daemon connect/disconnect checks"
  exit 0
fi

require_cmd sudo systemctl journalctl apt curl dpkg dpkg-deb dpkg-query sha256sum readlink
DEV_DEB="dist/podlaz_0.0.0~dev-1_linux_amd64.deb"
PACKAGE_INSTALLED=0
SERVICE_TOUCHED=0
ACTIVE_CONNECTION=0

run_podlaz_as_socket_user() {
  sudo -n -u "$(id -un)" -g podlaz env \
    XDG_CONFIG_HOME="${XDG_CONFIG_HOME}" \
    XDG_STATE_HOME="${XDG_STATE_HOME}" \
    XDG_CACHE_HOME="${XDG_CACHE_HOME}" \
    /usr/bin/podlaz "$@"
}

collect_real_diagnostics() {
  sudo -n systemctl status podlazd.service --no-pager >"${E2E_ARTIFACT_DIR}/real-podlazd.service.status" 2>&1 || true
  sudo -n journalctl -u podlazd.service -n 300 --no-pager >"${E2E_ARTIFACT_DIR}/real-podlazd.service.journal" 2>&1 || true
  ip route >"${E2E_ARTIFACT_DIR}/ip-route.txt" 2>&1 || true
  ip rule >"${E2E_ARTIFACT_DIR}/ip-rule.txt" 2>&1 || true
  resolvectl status >"${E2E_ARTIFACT_DIR}/resolvectl-status.txt" 2>&1 || true
  sudo -n nft list ruleset >"${E2E_ARTIFACT_DIR}/nft-ruleset.txt" 2>&1 || true
}

cleanup_real_vpn() {
  local code=$?
  if [[ "${ACTIVE_CONNECTION}" == "1" ]]; then
    run_podlaz_as_socket_user disconnect >"${E2E_ARTIFACT_DIR}/cleanup-disconnect.stdout" 2>"${E2E_ARTIFACT_DIR}/cleanup-disconnect.stderr" || true
  fi
  collect_real_diagnostics
  if [[ "${SERVICE_TOUCHED}" == "1" ]]; then
    sudo -n systemctl stop podlazd.service >/dev/null 2>&1 || true
  fi
  if [[ "${PACKAGE_INSTALLED}" == "1" && "${PODLAZ_E2E_KEEP_PACKAGE:-false}" != "true" ]]; then
    sudo -n apt remove -y podlaz >/dev/null 2>&1 || true
  fi
  exit "${code}"
}
trap cleanup_real_vpn EXIT

if [[ -n "${PODLAZ_E2E_DEB_PATH}" ]]; then
  DEV_DEB="${PODLAZ_E2E_DEB_PATH}"
  [[ -f "${DEV_DEB}" && ! -L "${DEV_DEB}" ]] || fail "prebuilt Podlaz package must be a regular non-symlink file"
  [[ "$(dpkg-deb --field "${DEV_DEB}" Package)" == "podlaz" ]] || fail "prebuilt package is not podlaz"
  [[ -n "${PODLAZ_E2E_EXPECT_COMMIT}" ]] || fail "PODLAZ_E2E_EXPECT_COMMIT is required with PODLAZ_E2E_DEB_PATH"
  log "install prebuilt package for daemon lifecycle"
else
  log "build and install package for daemon lifecycle"
  # shellcheck disable=SC1091
  . packaging/package-toolchain.env
  go install github.com/goreleaser/nfpm/v2/cmd/nfpm@"${NFPM_VERSION}"
  export PATH="$(go env GOPATH)/bin:${PATH}"
  PODLAZ_COMMIT="${GITHUB_SHA:-e2e-real-vpn}" \
  PODLAZ_BUILT="${PODLAZ_E2E_BUILT:-$(date -u '+%b %d %Y')}" \
    bash scripts/build-deb.sh 2>&1 | tee "${E2E_ARTIFACT_DIR}/real-build-deb.log"
fi

assert_native_deb_arch "${DEV_DEB}" "$(dpkg --print-architecture)"
INSTALL_DEB="${DEV_DEB}"
if [[ "${INSTALL_DEB}" != /* ]]; then
  INSTALL_DEB="./${INSTALL_DEB}"
fi
sudo -n apt install -y "${INSTALL_DEB}" 2>&1 | tee "${E2E_ARTIFACT_DIR}/real-apt-install.log"
PACKAGE_INSTALLED=1
sudo -n systemctl daemon-reload
sudo -n systemctl reset-failed podlazd.service || true
sudo -n systemctl start podlazd.service
SERVICE_TOUCHED=1
assert_package_service_active podlazd.service
assert_installed_package_version_matches_deb "${DEV_DEB}" podlaz
if [[ -n "${PODLAZ_E2E_EXPECT_COMMIT}" ]]; then
  assert_installed_podlaz_commit "${PODLAZ_E2E_EXPECT_COMMIT}"
fi
assert_running_podlazd_matches_deb "${DEV_DEB}"
collect_real_diagnostics

log "real VPN proxy-only lifecycle"
BEFORE_IP=""
AFTER_PROXY_IP=""
BEFORE_IP="$(curl -fsS --max-time 15 "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL}" || true)"
mask_value "${BEFORE_IP}"
printf '%s\n' "${BEFORE_IP}" >"${E2E_ARTIFACT_DIR}/public-ip-before.txt"

run_podlaz_as_socket_user profile list >"${E2E_ARTIFACT_DIR}/socket-user-profile-list.stdout"
run_podlaz_as_socket_user connect --mode proxy-only "${PROFILE_ID}" >"${E2E_ARTIFACT_DIR}/connect-proxy-only.stdout" 2>"${E2E_ARTIFACT_DIR}/connect-proxy-only.stderr"
ACTIVE_CONNECTION=1
run_podlaz_as_socket_user status >"${E2E_ARTIFACT_DIR}/status-proxy-only.stdout" 2>"${E2E_ARTIFACT_DIR}/status-proxy-only.stderr" || true
run_podlaz_as_socket_user disconnect >"${E2E_ARTIFACT_DIR}/disconnect-proxy-only.stdout" 2>"${E2E_ARTIFACT_DIR}/disconnect-proxy-only.stderr"
ACTIVE_CONNECTION=0

if [[ "${PODLAZ_E2E_ENABLE_TUN}" != "true" ]]; then
  log "TUN lifecycle is disabled; set PODLAZ_E2E_ENABLE_TUN=true after proxy-only lifecycle is stable"
  exit 0
fi

log "real VPN TUN lifecycle"
run_podlaz_as_socket_user connect --mode tun "${PROFILE_ID}" >"${E2E_ARTIFACT_DIR}/connect-tun.stdout" 2>"${E2E_ARTIFACT_DIR}/connect-tun.stderr"
ACTIVE_CONNECTION=1
run_podlaz_as_socket_user status --json >"${E2E_ARTIFACT_DIR}/status-tun.json" 2>"${E2E_ARTIFACT_DIR}/status-tun.stderr"
python3 - "${E2E_ARTIFACT_DIR}/status-tun.json" <<'PY'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as handle:
    payload = json.load(handle)
status = payload.get("status") or payload
health = status.get("tun_health") or {}
ok = (
    status.get("connection") == "active"
    and status.get("tun") == "active"
    and health.get("state") == "verified"
)
raise SystemExit(0 if ok else "TUN status is not verified active")
PY
getent hosts "${PODLAZ_E2E_DNS_CHECK_HOST}" >"${E2E_ARTIFACT_DIR}/dns-check.txt"
AFTER_TUN_IP="$(curl -fsS --max-time 30 "${PODLAZ_E2E_PUBLIC_IP_CHECK_URL}" || true)"
mask_value "${AFTER_TUN_IP}"
printf '%s\n' "${AFTER_TUN_IP}" >"${E2E_ARTIFACT_DIR}/public-ip-after-tun.txt"
if [[ -n "${PODLAZ_E2E_EXPECTED_EGRESS_IP:-}" && "${AFTER_TUN_IP}" != "${PODLAZ_E2E_EXPECTED_EGRESS_IP}" ]]; then
  fail "unexpected TUN public egress IP"
fi
run_podlaz_as_socket_user disconnect >"${E2E_ARTIFACT_DIR}/disconnect-tun.stdout" 2>"${E2E_ARTIFACT_DIR}/disconnect-tun.stderr"
ACTIVE_CONNECTION=0
run_podlaz_as_socket_user status --json >"${E2E_ARTIFACT_DIR}/status-after-tun-disconnect.json" 2>"${E2E_ARTIFACT_DIR}/status-after-tun-disconnect.stderr"
python3 - "${E2E_ARTIFACT_DIR}/status-after-tun-disconnect.json" <<'PY'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as handle:
    payload = json.load(handle)
status = payload.get("status") or payload
ok = status.get("connection") == "inactive" and status.get("tun") == "disabled"
raise SystemExit(0 if ok else "TUN status did not converge to clean inactive state")
PY
run_podlaz_as_socket_user recover --json >"${E2E_ARTIFACT_DIR}/recover-after-tun.json" 2>"${E2E_ARTIFACT_DIR}/recover-after-tun.stderr"
python3 - "${E2E_ARTIFACT_DIR}/recover-after-tun.json" <<'PY'
import json
import sys
with open(sys.argv[1], encoding="utf-8") as handle:
    payload = json.load(handle)
recovery = payload.get("recovery") or payload
if recovery.get("candidates"):
    raise SystemExit("recovery candidates remain after TUN disconnect")
PY

log "real VPN e2e completed"
