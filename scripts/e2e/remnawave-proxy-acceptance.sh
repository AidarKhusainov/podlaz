#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"
# shellcheck source=lib/package_provenance.sh
source "${SCRIPT_DIR}/lib/package_provenance.sh"
# shellcheck source=remnawave-fixture.sh
source "${SCRIPT_DIR}/remnawave-fixture.sh"

require_cmd docker jq sha256sum sudo systemctl

EXPECTED_COMMIT="${PODLAZ_E2E_CANDIDATE_COMMIT:-${GITHUB_SHA:-}}"
CANDIDATE_DEB=""
PACKAGE_INSTALLED=0

validate_candidate() {
  local path="$1" arch
  [[ -f "${path}" && ! -L "${path}" ]] || fail "candidate package must be a regular non-symlink file"
  [[ "$(dpkg-deb --field "${path}" Package)" == podlaz ]] || fail "candidate package is not podlaz"
  arch="$(dpkg-deb --field "${path}" Architecture)"
  [[ "${arch}" == "$(dpkg --print-architecture)" ]] || fail "candidate package architecture does not match runner"
  [[ "${EXPECTED_COMMIT}" =~ ^[0-9a-fA-F]{40}$ ]] || fail "candidate commit provenance is required"
  CANDIDATE_DEB="$(readlink -f -- "${path}")"
}

node_access_count() {
  local value
  value="$(docker compose -f "${COMPOSE_FILE}" exec -T remnanode sh -c \
    'if [ -f /tmp/podlaz-remnawave-access.log ]; then wc -l </tmp/podlaz-remnawave-access.log; else printf 0; fi' \
    2>/dev/null | tr -d '[:space:]')"
  [[ "${value}" =~ ^[0-9]+$ ]] || fail "could not read Remnawave Node access counter"
  printf '%s' "${value}"
}

get_profile_uri() {
  local response="${STATE_DIR}/connection-keys.json" uri
  api GET "/api/subscriptions/connection-keys/${USER_ID}" "" "${response}"
  uri="$(jq -er '.response.enabledKeys | select(length == 1) | .[0]' "${response}")"
  [[ "${uri}" == vless://* ]] || fail "Remnawave did not generate the expected VLESS connection key"
  printf '%s' "${uri}"
}

cleanup_proxy_acceptance() {
  local code=$?
  set +e
  if dpkg-query -W -f='${db:Status-Status}' podlaz 2>/dev/null | grep -Fx installed >/dev/null 2>&1; then
    sudo -n systemctl stop podlazd.service >/dev/null 2>&1 || true
    sudo -n apt purge -y podlaz >/dev/null 2>&1 || true
    sudo -n systemctl daemon-reload >/dev/null 2>&1 || true
    sudo -n systemctl reset-failed podlazd.service >/dev/null 2>&1 || true
  fi
  cleanup_fixture "${code}"
}
trap cleanup_proxy_acceptance EXIT INT TERM

main() {
  (($# == 1)) || fail "usage: $0 CANDIDATE.deb"
  validate_candidate "$1"
  install -d -m 0700 "${E2E_TMP_ROOT}" "${E2E_ARTIFACT_DIR}"

  remnawave_fixture_start

  local profile_uri before after delta
  profile_uri="$(get_profile_uri)"
  mask_value "${profile_uri}"
  before="$(node_access_count)"

  PODLAZ_E2E_PROFILE_URI="${profile_uri}" \
  PODLAZ_E2E_PROFILE_URI_LIST="" \
  PODLAZ_E2E_EXPECTED_EGRESS_IP="" \
  PODLAZ_E2E_RELIABILITY_CYCLES="${PODLAZ_E2E_RELIABILITY_CYCLES:-3}" \
  PODLAZ_E2E_PACKAGE_PATH="${CANDIDATE_DEB}" \
  PODLAZ_E2E_KEEP_PACKAGE=true \
  E2E_TMP_ROOT="${E2E_TMP_ROOT}/proxy-client" \
  E2E_ARTIFACT_DIR="${E2E_TMP_ROOT}/proxy-private-artifacts" \
    bash "${SCRIPT_DIR}/data-plane.sh"

  PACKAGE_INSTALLED=1
  assert_installed_package_version_matches_deb "${CANDIDATE_DEB}" podlaz
  assert_installed_podlaz_files_match_deb "${CANDIDATE_DEB}"
  assert_installed_podlaz_commit "${EXPECTED_COMMIT}"

  after="$(node_access_count)"
  delta=$((after - before))
  # One explicit phase plus every reliability cycle performs one SOCKS and one
  # HTTP external request. Count-only attribution avoids publishing Node logs.
  expected_min=$((2 * (1 + PODLAZ_E2E_RELIABILITY_CYCLES)))
  (( delta >= expected_min )) || fail "Remnawave Node did not observe all proxy data-plane requests"

  printf 'candidate.commit=%s\n' "${EXPECTED_COMMIT,,}"
  printf 'candidate.package_sha256=%s\n' "$(sha256sum "${CANDIDATE_DEB}" | awk '{print $1}')"
  printf 'remnawave.proxy_data_plane=pass\n'
  printf 'remnawave.proxy_path_attribution=pass\n'
  printf 'remnawave.proxy_cleanup=pass\n'
}

main "$@"
