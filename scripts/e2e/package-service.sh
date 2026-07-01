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

cleanup_package_service() {
  local code=$?
  if [[ "${SERVICE_TOUCHED}" == "1" ]]; then
    sudo -n systemctl stop podlazd.service >/dev/null 2>&1 || true
    collect_service_diagnostics cleanup
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

log "package first-run service availability"
sudo -n systemctl daemon-reload
if ! sudo -n systemctl is-enabled --quiet podlazd.service; then
  collect_service_diagnostics installed-service-enabled-failure
  fail "installed-service-enabled failed: podlazd.service is not enabled after clean package install"
fi
SERVICE_TOUCHED=1
wait_for_installed_service_active
collect_service_diagnostics installed-service-active
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
