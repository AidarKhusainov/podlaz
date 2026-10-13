#!/usr/bin/env bash
# Rebuild the installed distro NM source with logging-only selector insertions.
# Called only before the disposable guest starts and while policy-rc.d denies
# service startup. Never run this against a physical host or publish its logs.
set -Eeuo pipefail

ROOT="${1:?disposable guest root required}"
PRIVATE="${2:?private build log directory required}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
[[ "${GITHUB_ACTIONS:-}" == true && "${PODLAZ_E2E_NM_REGISTRATION_RESEARCH:-}" == true ]]
[[ -n "${E2E_TMP_ROOT:-}" && "${E2E_TMP_ROOT}" == /* && "${E2E_TMP_ROOT}" != / ]]
[[ "${ROOT}" == "${E2E_TMP_ROOT}/system-guest" && "${PRIVATE}" == "${E2E_TMP_ROOT}/private" ]]
[[ ! -L "${ROOT}" && -d "${ROOT}" && ! -L "${PRIVATE}" && -d "${PRIVATE}" ]]
sudo -n test -x "${ROOT}/usr/sbin/policy-rc.d"
sudo -n grep -qx 'exit 101' "${ROOT}/usr/sbin/policy-rc.d"

stage=prepare
build_log="${PRIVATE}/nm-selector-build.log"
report_progress() {
  grep -E '^nm-selector-build-progress=(version|sources|apt_update|dpkg_dev|build_dependencies|source_download|source_unpack|instrument|compile|install|verify|cleanup)$|^nm-selector-build-exit=[0-9]{1,3}$' "${build_log}" || true
}
report_failure() {
  local code=$?
  if (( code != 0 )); then
    printf 'nm-selector-build=failed stage=%s\n' "${stage}"
    # No raw apt/source/build output escapes the private directory.
    if [[ -f "${build_log}" ]]; then
      report_progress
      if grep -q "You must put some 'deb-src'" "${build_log}"; then
        printf 'nm-selector-build-class=source_configuration\n'
      elif grep -q 'Unable to find a source package\|Unable to locate package' "${build_log}"; then
        printf 'nm-selector-build-class=package_missing\n'
      elif grep -q 'unmet dependencies' "${build_log}"; then
        printf 'nm-selector-build-class=dependency_resolution\n'
      elif grep -q 'nm-selector-probe=failed' "${build_log}"; then
        printf 'nm-selector-build-class=source_contract\n'
      elif grep -q 'error:' "${build_log}"; then
        printf 'nm-selector-build-class=compiler_or_package\n'
      else
        printf 'nm-selector-build-class=source_or_dependency\n'
      fi
    fi
  fi
}
trap report_failure EXIT

sudo -n install -D -m 0644 "${SCRIPT_DIR}/nm_route_selection_probe.py" "${ROOT}/tmp/nm-selector-probe.py"
stage=distro_source_build
# This is a diagnostic NM binary, not a promoted or release package. The normal
# research matrix continues to run the unmodified distribution NetworkManager.
if ! sudo -n chroot "${ROOT}" /bin/bash -s >"${build_log}" 2>&1 <<'GUEST'
set -Eeuo pipefail
export DEBIAN_FRONTEND=noninteractive
export DEB_BUILD_OPTIONS='nocheck parallel=2'
progress() {
  case "$1" in
    version|sources|apt_update|dpkg_dev|build_dependencies|source_download|source_unpack|instrument|compile|install|verify|cleanup) ;;
    *) exit 1 ;;
  esac
  printf 'nm-selector-build-progress=%s\n' "$1"
}
trap 'code=$?; printf "nm-selector-build-exit=%s\n" "$code"' EXIT
progress version
version="$(dpkg-query -W -f='${source:Version}' network-manager)"
[[ "${version}" =~ ^[0-9][0-9A-Za-z.+:~_-]*$ ]]
upstream="$(nmcli --version)"
case "${upstream}" in *' 1.46.0'|*' 1.54.3') ;; *) exit 1 ;; esac
progress sources
# Use the very same signed archive/source suite used by debootstrap, not a PPA.
sed -n 's/^deb /deb-src /p' /etc/apt/sources.list > /etc/apt/sources.list.d/nm-selector-source.list
test -s /etc/apt/sources.list.d/nm-selector-source.list
progress apt_update
apt-get update
progress dpkg_dev
apt-get install -y --no-install-recommends dpkg-dev
progress build_dependencies
apt-get build-dep -y --no-install-recommends "network-manager=${version}"
install -d -m 0700 /tmp/nm-selector-source
cd /tmp/nm-selector-source
progress source_download
apt-get source --download-only "network-manager=${version}"
shopt -s nullglob
dsc=(./*.dsc)
(( ${#dsc[@]} == 1 ))
progress source_unpack
dpkg-source -x "${dsc[0]}" source
sha256sum "${dsc[0]}" | cut -d ' ' -f 1 > /tmp/nm-selector-dsc.sha256
sha256sum source/src/core/nm-manager.c | cut -d ' ' -f 1 > /tmp/nm-selector-original.sha256
progress instrument
python3 /tmp/nm-selector-probe.py instrument source/src/core/nm-manager.c
cd source
# Upstream's large build-time test suite is not rerun here. This does not skip
# any Podlaz test, installed-package acceptance or ordinary CI workflow.
progress compile
dpkg-buildpackage -b -us -uc -j2
cd ..
packages=(./network-manager_*.deb)
(( ${#packages[@]} == 1 ))
progress install
dpkg -i "${packages[0]}"
progress verify
test "$(dpkg-query -W -f='${source:Version}' network-manager)" = "${version}"
sha256sum /usr/sbin/NetworkManager | cut -d ' ' -f 1 > /tmp/nm-selector-binary.sha256
printf '%s\n' "${version}" > /tmp/nm-selector-version
progress cleanup
rm -rf /tmp/nm-selector-source /tmp/nm-selector-probe.py
rm -f /etc/apt/sources.list.d/nm-selector-source.list
apt-get clean
GUEST
then
  exit 1
fi
report_progress
stage=provenance
for kind in dsc original binary; do
  value="$(sudo -n cat "${ROOT}/tmp/nm-selector-${kind}.sha256")"
  [[ "${value}" =~ ^[0-9a-f]{64}$ ]]
  printf 'nm-selector-build-%s-sha256=%s\n' "${kind}" "${value}"
done
version="$(sudo -n cat "${ROOT}/tmp/nm-selector-version")"
[[ "${version}" =~ ^[0-9][0-9A-Za-z.+:~_-]*$ ]]
printf 'nm-selector-build=pass source-version=%s kind=instrumented-distro\n' "${version}"
