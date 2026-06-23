#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"

require_cmd bash go sudo dpkg-deb file tar systemctl journalctl apt

DEV_DEB="dist/podlaz_0.0.0~dev-1_linux_amd64.deb"
PACKAGE_INSTALLED=0
SERVICE_TOUCHED=0

collect_service_diagnostics() {
  if command -v systemctl >/dev/null 2>&1; then
    sudo -n systemctl status podlazd.service --no-pager >"${E2E_ARTIFACT_DIR}/podlazd.service.status" 2>&1 || true
  fi
  if command -v journalctl >/dev/null 2>&1; then
    sudo -n journalctl -u podlazd.service -n 200 --no-pager >"${E2E_ARTIFACT_DIR}/podlazd.service.journal" 2>&1 || true
  fi
}

cleanup_package_service() {
  local code=$?
  if [[ "${SERVICE_TOUCHED}" == "1" ]]; then
    sudo -n systemctl stop podlazd.service >/dev/null 2>&1 || true
    collect_service_diagnostics
  fi
  if [[ "${PACKAGE_INSTALLED}" == "1" && "${PODLAZ_E2E_KEEP_PACKAGE:-false}" != "true" ]]; then
    sudo -n apt remove -y podlaz >/dev/null 2>&1 || true
  fi
  exit "${code}"
}
trap cleanup_package_service EXIT

log "package/service sudo preflight"
expect_success sudo-true sudo -n true
expect_success sudo-systemctl-version sudo -n systemctl --version
expect_success sudo-apt-version sudo -n apt --version

log "install pinned packaging tools"
# shellcheck disable=SC1091
. packaging/package-toolchain.env
go install github.com/goreleaser/nfpm/v2/cmd/nfpm@"${NFPM_VERSION}"
export PATH="$(go env GOPATH)/bin:${PATH}"
require_cmd nfpm

log "build Debian package"
PODLAZ_COMMIT="${GITHUB_SHA:-e2e-package-service}" \
PODLAZ_BUILT="${PODLAZ_E2E_BUILT:-$(date -u '+%b %d %Y')}" \
  bash scripts/build-deb.sh 2>&1 | tee "${E2E_ARTIFACT_DIR}/build-deb.log"

test -f "${DEV_DEB}" || fail "expected package not found: ${DEV_DEB}"

dpkg-deb --info "${DEV_DEB}" | tee "${E2E_ARTIFACT_DIR}/package.info"
dpkg-deb --contents "${DEV_DEB}" | tee "${E2E_ARTIFACT_DIR}/package.contents"
assert_contains "${E2E_ARTIFACT_DIR}/package.contents" "./usr/bin/podlaz"
assert_contains "${E2E_ARTIFACT_DIR}/package.contents" "./usr/bin/podlazd"
assert_contains "${E2E_ARTIFACT_DIR}/package.contents" "./usr/lib/systemd/system/podlazd.service"
assert_contains "${E2E_ARTIFACT_DIR}/package.contents" "./usr/lib/sysusers.d/podlaz.conf"
assert_contains "${E2E_ARTIFACT_DIR}/package.contents" "./usr/share/bash-completion/completions/podlaz"
assert_contains "${E2E_ARTIFACT_DIR}/package.contents" "./usr/share/zsh/vendor-completions/_podlaz"
assert_contains "${E2E_ARTIFACT_DIR}/package.contents" "./usr/share/fish/vendor_completions.d/podlaz.fish"
assert_not_contains "${E2E_ARTIFACT_DIR}/package.contents" "./usr/local/"
assert_not_contains "${E2E_ARTIFACT_DIR}/package.contents" "./run/"
assert_not_contains "${E2E_ARTIFACT_DIR}/package.contents" "./var/run/"
assert_not_contains "${E2E_ARTIFACT_DIR}/package.contents" "./home/"

log "install package locally"
sudo -n apt install -y "./${DEV_DEB}" 2>&1 | tee "${E2E_ARTIFACT_DIR}/apt-install.log"
PACKAGE_INSTALLED=1

expect_success installed-version podlaz version
expect_success installed-completion-bash podlaz completion bash
expect_success installed-completion-zsh podlaz completion zsh
expect_success installed-completion-fish podlaz completion fish

test -x /usr/bin/podlaz || fail "missing /usr/bin/podlaz"
test -x /usr/bin/podlazd || fail "missing /usr/bin/podlazd"
test -f /usr/lib/systemd/system/podlazd.service || fail "missing podlazd.service"
test -f /usr/lib/sysusers.d/podlaz.conf || fail "missing sysusers contract"

log "systemd daemon lifecycle"
sudo -n systemctl daemon-reload
sudo -n systemctl reset-failed podlazd.service || true
sudo -n systemctl start podlazd.service
SERVICE_TOUCHED=1
sudo -n systemctl is-active --quiet podlazd.service
collect_service_diagnostics
sudo -n systemctl stop podlazd.service
SERVICE_TOUCHED=0

log "same-version reinstall and remove"
sudo -n apt install -y --reinstall "./${DEV_DEB}" 2>&1 | tee "${E2E_ARTIFACT_DIR}/apt-reinstall.log"
expect_success reinstalled-version podlaz version
if [[ "${PODLAZ_E2E_KEEP_PACKAGE:-false}" != "true" ]]; then
  sudo -n apt remove -y podlaz 2>&1 | tee "${E2E_ARTIFACT_DIR}/apt-remove.log"
  PACKAGE_INSTALLED=0
fi

log "package/service e2e completed"
