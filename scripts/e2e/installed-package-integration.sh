#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"
# shellcheck source=lib/exit_trap.sh
source "${SCRIPT_DIR}/lib/exit_trap.sh"

require_cmd apt dpkg go sudo systemctl

: "${PODLAZ_DEB_ARCH:=$(dpkg --print-architecture)}"
HOST_DEB_ARCH="$(dpkg --print-architecture)"
if [[ "${PODLAZ_DEB_ARCH}" != "${HOST_DEB_ARCH}" ]]; then
  fail "installed-package integration requires a native package: requested=${PODLAZ_DEB_ARCH}, host=${HOST_DEB_ARCH}"
fi

DEV_DEB="dist/podlaz_0.0.0~dev-1_linux_${PODLAZ_DEB_ARCH}.deb"
PACKAGE_INSTALLED=false

cleanup() {
  local saved=$? cleanup_failed=0
  set +e
  sudo -n systemctl stop podlazd.service >/dev/null 2>&1 || true
  if [[ "${PACKAGE_INSTALLED}" == "true" ]]; then
    sudo -n apt purge -y podlaz >/dev/null 2>&1 || cleanup_failed=1
    if command -v deb-systemd-helper >/dev/null 2>&1; then
      sudo -n deb-systemd-helper purge podlazd.service >/dev/null 2>&1 || true
    fi
  fi
  sudo -n systemctl daemon-reload >/dev/null 2>&1 || cleanup_failed=1
  sudo -n systemctl reset-failed podlazd.service >/dev/null 2>&1 || true
  finish_exit_trap "${saved}" "${cleanup_failed}"
}
trap cleanup EXIT

log "clean package baseline"
sudo -n systemctl stop podlazd.service >/dev/null 2>&1 || true
sudo -n apt purge -y podlaz >/dev/null 2>&1 || true
if command -v deb-systemd-helper >/dev/null 2>&1; then
  sudo -n deb-systemd-helper purge podlazd.service >/dev/null 2>&1 || true
fi
sudo -n systemctl daemon-reload
sudo -n systemctl reset-failed podlazd.service >/dev/null 2>&1 || true

log "build native package"
# shellcheck disable=SC1091
. packaging/package-toolchain.env
go install github.com/goreleaser/nfpm/v2/cmd/nfpm@"${NFPM_VERSION}"
export PATH="$(go env GOPATH)/bin:${PATH}"
PODLAZ_COMMIT="${GITHUB_SHA:-hosted-integration}" \
PODLAZ_BUILT="${PODLAZ_E2E_BUILT:-$(date -u '+%b %d %Y')}" \
PODLAZ_DEB_ARCH="${PODLAZ_DEB_ARCH}" \
  bash scripts/build-deb.sh 2>&1 | tee "${E2E_ARTIFACT_DIR}/installed-package-build.log"
[[ -f "${DEV_DEB}" ]] || fail "expected package not found: ${DEV_DEB}"

log "install package and wait for daemon"
sudo -n apt install -y "./${DEV_DEB}" 2>&1 | tee "${E2E_ARTIFACT_DIR}/installed-package-apt-install.log"
PACKAGE_INSTALLED=true
sudo -n systemctl daemon-reload
sudo -n systemctl reset-failed podlazd.service >/dev/null 2>&1 || true
sudo -n systemctl start podlazd.service

ready=false
for _ in $(seq 1 50); do
  if sudo -n systemctl is-active --quiet podlazd.service && podlaz status >/dev/null 2>&1; then
    ready=true
    break
  fi
  sleep 0.2
done
if [[ "${ready}" != "true" ]]; then
  sudo -n systemctl status podlazd.service --no-pager >"${E2E_ARTIFACT_DIR}/installed-package-service.status" 2>&1 || true
  sudo -n journalctl -u podlazd.service -n 200 --no-pager >"${E2E_ARTIFACT_DIR}/installed-package-service.journal" 2>&1 || true
  fail "installed podlazd.service did not become ready"
fi

log "verify installed runtime contracts"
bash "${SCRIPT_DIR}/log-window-acceptance.sh"
bash "${SCRIPT_DIR}/remote-client-acceptance.sh"

log "installed-package integration completed"
