#!/usr/bin/env bash

PACKAGE_RUNTIME_PROVENANCE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=package_provenance.sh
source "${PACKAGE_RUNTIME_PROVENANCE_DIR}/package_provenance.sh"

: "${PODLAZ_V0240_COMMIT:=ab71c876d558a7653d44d5b98c76f3899e569a90}"

# Compatibility adapter for the terminal-recovery harness. Exact package bytes
# and runtime identity are checked by the shared package provenance helpers;
# this adapter additionally binds the tested candidate to the source checkout.
assert_exact_package_runtime_provenance() {
  local deb="$1" phase="$2" version expected_commit repo_root artifact
  version="$(dpkg-deb --field "${deb}" Version 2>/dev/null)" || {
    fail "${phase}: exact package version could not be read"
    return 1
  }

  case "${version%%-*}" in
    0.2.40)
      expected_commit="${PODLAZ_V0240_COMMIT}"
      ;;
    *)
      repo_root="$(git -C "${PACKAGE_RUNTIME_PROVENANCE_DIR}/../.." rev-parse --show-toplevel 2>/dev/null)" || {
        fail "${phase}: source checkout root could not be resolved"
        return 1
      }
      expected_commit="$(git -C "${repo_root}" rev-parse HEAD 2>/dev/null)" || {
        fail "${phase}: source HEAD could not be resolved"
        return 1
      }
      [[ "${expected_commit}" =~ ^[0-9a-f]{40}$ ]] || {
        fail "${phase}: source HEAD is not a full commit identity"
        return 1
      }
      ;;
  esac

  assert_exact_podlaz_package_runtime_provenance "${deb}" "${expected_commit}" || return 1

  artifact="${E2E_ARTIFACT_DIR}/package-provenance-$(safe_name "${phase}").txt"
  {
    printf 'phase=%s\n' "${phase}"
    printf 'package_version=%s\n' "${version}"
    printf 'source_commit=%s\n' "${expected_commit}"
    printf 'runtime_matches_exact_deb=true\n'
    printf 'packaged_xray_matches_installed=true\n'
  } >"${artifact}"
}
