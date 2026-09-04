#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BASE_DEB="${PODLAZ_E2E_BASE_DEB:-}"
BASE_VERSION="${PODLAZ_E2E_BASE_VERSION:-v0.2.29}"
V029_RELEASE_COMMIT="c846f5465a90a50d72f3fc393d639a402d590798"

fail() {
  printf 'podlaz e2e: %s\n' "$*" >&2
  exit 1
}

[[ -n "${BASE_DEB}" ]] || fail "PODLAZ_E2E_BASE_DEB is required"
[[ -f "${BASE_DEB}" ]] || fail "released baseline package is missing"
[[ "${BASE_VERSION}" == "v0.2.29" ]] || fail "network-recovery baseline must be exactly v0.2.29"
command -v dpkg-deb >/dev/null 2>&1 || fail "dpkg-deb is required"
command -v sha256sum >/dev/null 2>&1 || fail "sha256sum is required"

base_arch="$(dpkg-deb --field "${BASE_DEB}" Architecture 2>/dev/null || true)"
case "${base_arch}" in
  amd64) expected_sha256="91644dee9ca92ddc5c48793b926f20d18da4d4267cbfdd3b41303e1e5c52516e" ;;
  arm64) expected_sha256="74a4fe360fc0b05ec419440ae6f54ec3b76f9679a525671d1a905142920fa673" ;;
  *) fail "no pinned v0.2.29 baseline package digest for architecture ${base_arch:-unknown}" ;;
esac
actual_sha256="$(sha256sum "${BASE_DEB}" | awk '{print $1}')"
[[ "${actual_sha256}" == "${expected_sha256}" ]] || fail "baseline package digest does not match official v0.2.29 release asset"

export PODLAZ_E2E_BASE_VERSION="${BASE_VERSION}"
export PODLAZ_E2E_V029_RELEASE_COMMIT="${V029_RELEASE_COMMIT}"
exec bash "${SCRIPT_DIR}/network-recovery-package-scenario.sh" "$@"
