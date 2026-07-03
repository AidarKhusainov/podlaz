#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"

require_cmd bash go sudo dpkg dpkg-deb file tar systemctl journalctl apt

: "${PODLAZ_DEB_ARCH:=$(dpkg --print-architecture)}"
HOST_DEB_ARCH="$(dpkg --print-architecture)"
if [[ "${PODLAZ_DEB_ARCH}" != "${HOST_DEB_ARCH}" ]]; then
  fail "package/service e2e must install a native package: PODLAZ_DEB_ARCH=${PODLAZ_DEB_ARCH}, host=${HOST_DEB_ARCH}"
fi
DEV_DEB="dist/podlaz_0.0.0~dev-1_linux_${PODLAZ_DEB_ARCH}.deb"
PACKAGE_INSTALLED=0
SERVICE_TOUCHED=0
PACKAGE_SERVICE_DROPIN_TOUCHED=0
PACKAGE_SERVICE_DROPIN_DIR="/etc/systemd/system/podlazd.service.d"
PACKAGE_SERVICE_E2E_DROPIN="${PACKAGE_SERVICE_DROPIN_DIR}/20-podlaz-e2e-package-service.conf"
PACKAGE_SERVICE_PKCHECK_MODE_FILE=""

collect_service_diagnostics() {
  local label="${1:-podlazd}"
  local dir="${E2E_ARTIFACT_DIR}/$(safe_name "${label}")"
  mkdir -p "${dir}"
  if command -v systemctl >/dev/null 2>&1; then
    sudo -n systemctl status podlazd.service --no-pager >"${dir}/podlazd.service.status" 2>&1 || true
    sudo -n systemctl cat podlazd.service >"${dir}/podlazd.service.cat" 2>&1 || true
    sudo -n systemctl is-enabled podlazd.service >"${dir}/podlazd.service.is-enabled" 2>&1 || true
    sudo -n systemctl is-active podlazd.service >"${dir}/podlazd.service.is-active" 2>&1 || true
    sudo -n systemctl list-unit-files 'podlazd.service' >"${dir}/podlazd.service.unit-files" 2>&1 || true
  fi
  if command -v deb-systemd-helper >/dev/null 2>&1; then
    sudo -n deb-systemd-helper was-enabled podlazd.service >"${dir}/podlazd.service.was-enabled" 2>&1 || true
    sudo -n deb-systemd-helper debian-installed podlazd.service >"${dir}/podlazd.service.debian-installed" 2>&1 || true
  fi
  if command -v journalctl >/dev/null 2>&1; then
    sudo -n journalctl -u podlazd.service -n 200 --no-pager >"${dir}/podlazd.service.journal" 2>&1 || true
  fi
}

purge_existing_package_state() {
  log "clean existing podlaz package state"
  sudo -n systemctl stop podlazd.service >"${E2E_ARTIFACT_DIR}/preinstall-systemctl-stop.log" 2>&1 || true
  sudo -n rm -f -- "${PACKAGE_SERVICE_E2E_DROPIN}" >/dev/null 2>&1 || true
  sudo -n systemctl daemon-reload >"${E2E_ARTIFACT_DIR}/preinstall-dropin-daemon-reload.log" 2>&1 || true
  sudo -n apt purge -y podlaz >"${E2E_ARTIFACT_DIR}/preinstall-apt-purge.log" 2>&1 || true
  if command -v deb-systemd-helper >/dev/null 2>&1; then
    sudo -n deb-systemd-helper purge podlazd.service >"${E2E_ARTIFACT_DIR}/preinstall-deb-systemd-helper-purge.log" 2>&1 || true
  fi
  sudo -n systemctl daemon-reload >"${E2E_ARTIFACT_DIR}/preinstall-systemctl-daemon-reload.log" 2>&1 || true
  sudo -n systemctl reset-failed podlazd.service >"${E2E_ARTIFACT_DIR}/preinstall-systemctl-reset-failed.log" 2>&1 || true
}

wait_for_installed_service_active() {
  local attempt
  for attempt in $(seq 1 50); do
    if sudo -n systemctl is-active --quiet podlazd.service; then
      return 0
    fi
    sleep 0.2
  done
  collect_service_diagnostics installed-service-active-failure
  fail "installed-service-active failed: podlazd.service did not become active after package install"
}

install_package_service_e2e_authorization_fixture() {
  local fixture_dir="${E2E_TMP_ROOT}/package-service-fixture"
  mkdir -p "${fixture_dir}"
  chmod 755 "${fixture_dir}"
  PACKAGE_SERVICE_PKCHECK_MODE_FILE="${fixture_dir}/pkcheck-mode"
  printf 'unavailable\n' >"${PACKAGE_SERVICE_PKCHECK_MODE_FILE}"
  chmod 644 "${PACKAGE_SERVICE_PKCHECK_MODE_FILE}"

  local fake_pkcheck="${fixture_dir}/pkcheck"
  cat >"${fake_pkcheck}" <<'SH'
#!/usr/bin/env bash
set -Eeuo pipefail
mode_file="${PODLAZ_E2E_PKCHECK_MODE_FILE:?}"
mode="$(tr -d '[:space:]' <"${mode_file}")"
case "${mode}" in
  allow)
    exit 0
    ;;
  deny)
    exit 1
    ;;
  unavailable|*)
    exit 2
    ;;
esac
SH
  chmod 755 "${fake_pkcheck}"

  local fake_xray="${fixture_dir}/xray"
  cat >"${fake_xray}" <<'SH'
#!/usr/bin/env bash
set -Eeuo pipefail
if [[ "${1:-}" != "run" || "${2:-}" != "-config" || -z "${3:-}" ]]; then
  printf 'fake xray expected: run -config <path>\n' >&2
  exit 2
fi
config="${3}"
if [[ ! -r "${config}" ]]; then
  printf 'fake xray cannot read config: %s\n' "${config}" >&2
  exit 3
fi
trap 'exit 0' TERM INT
while :; do
  sleep 1 &
  wait $!
done
SH
  chmod 755 "${fake_xray}"

  sudo -n mkdir -p "${PACKAGE_SERVICE_DROPIN_DIR}"
  cat >"${E2E_ARTIFACT_DIR}/package-service-e2e-dropin.conf" <<EOF
[Service]
Environment=PODLAZ_XRAY_PATH=${fake_xray}
Environment=PATH=${fixture_dir}:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
Environment=PODLAZ_E2E_PKCHECK_MODE_FILE=${PACKAGE_SERVICE_PKCHECK_MODE_FILE}
EOF
  sudo -n install -m 0644 "${E2E_ARTIFACT_DIR}/package-service-e2e-dropin.conf" "${PACKAGE_SERVICE_E2E_DROPIN}"
  PACKAGE_SERVICE_DROPIN_TOUCHED=1
  sudo -n systemctl daemon-reload
}

cleanup_package_service() {
  local code=$?
  if [[ "${SERVICE_TOUCHED}" == "1" ]]; then
    sudo -n systemctl stop podlazd.service >/dev/null 2>&1 || true
    collect_service_diagnostics cleanup
  fi
  if [[ "${PACKAGE_SERVICE_DROPIN_TOUCHED}" == "1" ]]; then
    sudo -n rm -f -- "${PACKAGE_SERVICE_E2E_DROPIN}" >/dev/null 2>&1 || true
    sudo -n systemctl daemon-reload >/dev/null 2>&1 || true
  fi
  if [[ "${PACKAGE_INSTALLED}" == "1" && "${PODLAZ_E2E_KEEP_PACKAGE:-false}" != "true" ]]; then
    sudo -n apt purge -y podlaz >/dev/null 2>&1 || true
    if command -v deb-systemd-helper >/dev/null 2>&1; then
      sudo -n deb-systemd-helper purge podlazd.service >/dev/null 2>&1 || true
    fi
    sudo -n systemctl daemon-reload >/dev/null 2>&1 || true
    sudo -n systemctl reset-failed podlazd.service >/dev/null 2>&1 || true
  fi
  exit "${code}"
}
trap cleanup_package_service EXIT

log "package/service sudo preflight"
expect_success sudo-true sudo -n true
expect_success sudo-systemctl-version sudo -n systemctl --version
expect_success sudo-apt-version sudo -n apt --version
purge_existing_package_state

log "install pinned packaging tools"
# shellcheck disable=SC1091
. packaging/package-toolchain.env
go install github.com/goreleaser/nfpm/v2/cmd/nfpm@"${NFPM_VERSION}"
export PATH="$(go env GOPATH)/bin:${PATH}"
require_cmd nfpm

log "build Debian package"
PODLAZ_COMMIT="${GITHUB_SHA:-e2e-package-service}" \
PODLAZ_BUILT="${PODLAZ_E2E_BUILT:-$(date -u '+%b %d %Y')}" \
PODLAZ_DEB_ARCH="${PODLAZ_DEB_ARCH}" \
  bash scripts/build-deb.sh 2>&1 | tee "${E2E_ARTIFACT_DIR}/build-deb.log"

test -f "${DEV_DEB}" || fail "expected package not found: ${DEV_DEB}"

dpkg-deb --info "${DEV_DEB}" | tee "${E2E_ARTIFACT_DIR}/package.info"
dpkg-deb --contents "${DEV_DEB}" | tee "${E2E_ARTIFACT_DIR}/package.contents"
assert_contains "${E2E_ARTIFACT_DIR}/package.contents" "./usr/bin/podlaz"
assert_contains "${E2E_ARTIFACT_DIR}/package.contents" "./usr/bin/plz"
assert_contains "${E2E_ARTIFACT_DIR}/package.contents" "./usr/bin/podlazd"
assert_contains "${E2E_ARTIFACT_DIR}/package.contents" "./usr/lib/systemd/system/podlazd.service"
assert_contains "${E2E_ARTIFACT_DIR}/package.contents" "./usr/lib/sysusers.d/podlaz.conf"
assert_contains "${E2E_ARTIFACT_DIR}/package.contents" "./usr/share/bash-completion/completions/podlaz"
assert_contains "${E2E_ARTIFACT_DIR}/package.contents" "./usr/share/bash-completion/completions/plz"
assert_contains "${E2E_ARTIFACT_DIR}/package.contents" "./usr/share/zsh/vendor-completions/_podlaz"
assert_contains "${E2E_ARTIFACT_DIR}/package.contents" "./usr/share/zsh/vendor-completions/_plz"
assert_contains "${E2E_ARTIFACT_DIR}/package.contents" "./usr/share/fish/vendor_completions.d/podlaz.fish"
assert_contains "${E2E_ARTIFACT_DIR}/package.contents" "./usr/share/fish/vendor_completions.d/plz.fish"
assert_not_contains "${E2E_ARTIFACT_DIR}/package.contents" "./usr/local/"
assert_not_contains "${E2E_ARTIFACT_DIR}/package.contents" "./run/"
assert_not_contains "${E2E_ARTIFACT_DIR}/package.contents" "./var/run/"
assert_not_contains "${E2E_ARTIFACT_DIR}/package.contents" "./home/"

log "install package locally"
sudo -n apt install -y "./${DEV_DEB}" 2>&1 | tee "${E2E_ARTIFACT_DIR}/apt-install.log"
PACKAGE_INSTALLED=1

expect_success installed-version podlaz version
expect_success installed-alias-version plz version
expect_success installed-completion-bash podlaz completion bash
expect_success installed-alias-completion-bash plz completion bash
expect_success installed-completion-zsh podlaz completion zsh
expect_success installed-completion-fish podlaz completion fish

test -x /usr/bin/podlaz || fail "missing /usr/bin/podlaz"
test -x /usr/bin/plz || fail "missing /usr/bin/plz"
test -x /usr/bin/podlazd || fail "missing /usr/bin/podlazd"
test -f /usr/lib/systemd/system/podlazd.service || fail "missing podlazd.service"
test -f /usr/lib/sysusers.d/podlaz.conf || fail "missing sysusers contract"
assert_contains /usr/lib/systemd/system/podlazd.service "Environment=PODLAZ_POLKIT_AUTHORIZATION=required"
install_package_service_e2e_authorization_fixture

log "package first-run service availability"
sudo -n systemctl daemon-reload
if ! sudo -n systemctl is-enabled --quiet podlazd.service; then
  collect_service_diagnostics installed-service-enabled-failure
  fail "installed-service-enabled failed: podlazd.service is not enabled after clean package install"
fi
SERVICE_TOUCHED=1
wait_for_installed_service_active
collect_service_diagnostics installed-service-active
expect_success installed-status-access podlaz status

setup_isolated_xdg "package-service-normal-user"
PACKAGE_PROFILE_URI='vless://00000000-0000-0000-0000-000000000042@package-service.example.net:443?type=tcp&security=tls&encryption=none#package-service-connect'
expect_success packaged-profile-import plz profile import "${PACKAGE_PROFILE_URI}"
PACKAGE_PROFILE_ID="$(awk '/^Imported profile:/ {print $3}' "${LAST_STDOUT}")"
assert_nonempty "${PACKAGE_PROFILE_ID}" "packaged imported profile id"

printf 'unavailable\n' >"${PACKAGE_SERVICE_PKCHECK_MODE_FILE}"
expect_exit 1 packaged-connect-authorization-unavailable plz connect "${PACKAGE_PROFILE_ID}"
assert_contains "${LAST_STDERR}" "authorization unavailable"
assert_contains "${LAST_STDERR}" "polkit could not authorize"
assert_not_contains "${LAST_STDERR}" "add the user to the podlaz group"
assert_not_contains "${LAST_STDERR}" "start a new login session"

printf 'allow\n' >"${PACKAGE_SERVICE_PKCHECK_MODE_FILE}"
expect_success packaged-connect-authorized plz connect "${PACKAGE_PROFILE_ID}"
assert_contains "${LAST_STDOUT}" "podlaz connection started"
assert_contains "${LAST_STDOUT}" "Connection: active"
assert_contains "${LAST_STDOUT}" "Mode: proxy-only"
expect_success packaged-status-active plz status
expect_success packaged-disconnect-authorized plz disconnect
assert_contains "${LAST_STDOUT}" "podlaz disconnected"
assert_contains "${LAST_STDOUT}" "Connection: inactive"
expect_success packaged-status-inactive plz status

sudo -n systemctl stop podlazd.service
SERVICE_TOUCHED=0

log "same-version reinstall and purge"
sudo -n apt install -y --reinstall "./${DEV_DEB}" 2>&1 | tee "${E2E_ARTIFACT_DIR}/apt-reinstall.log"
expect_success reinstalled-version podlaz version
expect_success reinstalled-alias-version plz version
if [[ "${PODLAZ_E2E_KEEP_PACKAGE:-false}" != "true" ]]; then
  sudo -n apt purge -y podlaz 2>&1 | tee "${E2E_ARTIFACT_DIR}/apt-purge.log"
  PACKAGE_INSTALLED=0
  if command -v deb-systemd-helper >/dev/null 2>&1; then
    sudo -n deb-systemd-helper purge podlazd.service >"${E2E_ARTIFACT_DIR}/posttest-deb-systemd-helper-purge.log" 2>&1 || true
  fi
  sudo -n systemctl daemon-reload >"${E2E_ARTIFACT_DIR}/posttest-systemctl-daemon-reload.log" 2>&1 || true
  sudo -n systemctl reset-failed podlazd.service >"${E2E_ARTIFACT_DIR}/posttest-systemctl-reset-failed.log" 2>&1 || true
fi

log "package/service e2e completed"
