#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
# shellcheck source=hosted-synthetic-tun.sh
source "${SCRIPT_DIR}/hosted-synthetic-tun.sh"

REPORT="${E2E_ARTIFACT_DIR}/hosted-v029-package-recovery.txt"
MACHINE="podlaz-v029-recovery"
HOST_VETH="pzv0290"
HOST_ENDPOINT_DEV="pzv029srv"
NFT_TABLE="pzv029_hosted"
FOREIGN_NFT_TABLE="pzv029_foreign"
NETWORK_CIDR="172.31.248.0/30"
HOST_CIDR="172.31.248.1/30"
GUEST_CIDR="172.31.248.2/30"
HOST_IP="172.31.248.1"
ENDPOINT_CIDR="172.31.247.1/32"
ENDPOINT_IP="172.31.247.1"
GUEST_ROOT="${E2E_TMP_ROOT}/system-guest"
PRIVATE_ROOT="${E2E_TMP_ROOT}/private"
XRAY_ROOT="${PRIVATE_ROOT}/synthetic-xray"
GUEST_CANDIDATE="/opt/podlaz-candidate.deb"
GUEST_V029="/run/podlaz-synthetic-xray/podlaz-v0.2.29.deb"
OUTER_V029="${XRAY_ROOT}/podlaz-v0.2.29.deb"
GUEST_XDG="/home/e2e/.local/share/podlaz-v029-recovery"
TUN_RULE="/etc/polkit-1/rules.d/49-podlaz-v029-recovery.rules"
GUEST_PRIVATE="/tmp/podlaz-v029-controller"
GUEST_MANIFEST="${GUEST_PRIVATE}/network-manifest.json"
GUEST_WORK="/tmp/podlaz-v029-work"
GUEST_HISTORY_PRIVATE="/tmp/podlaz-v029-history/private"
GUEST_HISTORY_PUBLIC="/tmp/podlaz-v029-history/public"
GUEST_CANDIDATE_ALIAS="${GUEST_WORK}/dist/podlaz_0.0.0~dev-1_linux_amd64.deb"
GUEST_V029_ALIAS="${GUEST_WORK}/podlaz_0.2.29_linux_amd64.deb"
EXPECTED_COMMIT="${PODLAZ_E2E_CANDIDATE_COMMIT:-${GITHUB_SHA:-}}"
V029_SHA256="${PODLAZ_E2E_V029_SHA256:-91644dee9ca92ddc5c48793b926f20d18da4d4267cbfdd3b41303e1e5c52516e}"
V029_RELEASE_COMMIT="${PODLAZ_E2E_V029_RELEASE_COMMIT:-c846f5465a90a50d72f3fc393d639a402d590798}"
V029_DEB=""

EVIDENCE_KEYS=(
  pinned_v029.source_release
  pinned_v029.source_active
  pinned_v029.candidate_replaced
  pinned_v029.legacy_reconstruction
  pinned_v029.privacy_active
  pinned_v029.post_upgrade_traffic
  pinned_v029.runtime_provenance
  pinned_v029.foreign_state
  pinned_v029.network_restored
  pinned_v029.acceptance_complete
  outer.cleanup
  artifact.privacy
)

assert_public_artifact_privacy() {
  local extra
  extra="$(find "${E2E_ARTIFACT_DIR}" -mindepth 1 -maxdepth 1 ! -name 'hosted-v029-package-recovery.txt' -print -quit)"
  [[ -z "${extra}" ]] || return 1
  [[ -f "${REPORT}" && ! -L "${REPORT}" ]] || return 1
  ! grep -Eq 'vless://|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}|172[.]31[.](247|248)[.]' "${REPORT}"
}

validate_v029() {
  local path="$1" version arch digest
  [[ "${V029_RELEASE_COMMIT}" == "c846f5465a90a50d72f3fc393d639a402d590798" ]] || fail "pinned v0.2.29 release commit changed"
  [[ "${V029_SHA256}" == "91644dee9ca92ddc5c48793b926f20d18da4d4267cbfdd3b41303e1e5c52516e" ]] || fail "pinned v0.2.29 amd64 digest changed"
  [[ -f "${path}" && ! -L "${path}" ]] || fail "v0.2.29 package must be a regular non-symlink file"
  [[ "$(dpkg-deb --field "${path}" Package)" == podlaz ]] || fail "v0.2.29 package is not podlaz"
  version="$(dpkg-deb --field "${path}" Version)"
  arch="$(dpkg-deb --field "${path}" Architecture)"
  [[ "${version%%-*}" == "0.2.29" ]] || fail "historical baseline must be exact v0.2.29"
  [[ "${arch}" == "amd64" && "${arch}" == "$(dpkg --print-architecture)" ]] || fail "hosted v0.2.29 qualification requires native amd64"
  digest="$(sha256sum "${path}" | awk '{print $1}')"
  [[ "${digest}" == "${V029_SHA256}" ]] || fail "v0.2.29 package digest does not match official release asset"
  V029_DEB="$(readlink -f -- "${path}")"
}

stage_v029() {
  install -d -m 0700 "${XRAY_ROOT}"
  install -m 0644 "${V029_DEB}" "${OUTER_V029}"
}

prepare_v029_workdir() {
  guest_exec install -d -o e2e -g e2e -m 0700 \
    "${GUEST_WORK}" "${GUEST_WORK}/dist" \
    "${GUEST_HISTORY_PRIVATE}" "${GUEST_HISTORY_PUBLIC}" \
    "${GUEST_PRIVATE}"
  guest_exec install -o e2e -g e2e -m 0600 "${GUEST_CANDIDATE}" "${GUEST_CANDIDATE_ALIAS}"
  guest_exec install -o e2e -g e2e -m 0600 "${GUEST_V029}" "${GUEST_V029_ALIAS}"
  guest_exec runuser -u e2e -- test -r "${GUEST_CANDIDATE_ALIAS}"
  guest_exec runuser -u e2e -- test -r "${GUEST_V029_ALIAS}"
}

run_pinned_v029_acceptance() {
  guest_exec /bin/bash -lc "uri=\$(cat /run/podlaz-synthetic-xray/client-uri); cd '${GUEST_WORK}'; runuser -u e2e -- env E2E_TMP_ROOT='${GUEST_HISTORY_PRIVATE}' E2E_ARTIFACT_DIR='${GUEST_HISTORY_PUBLIC}' PODLAZ_E2E_BASE_DEB='${GUEST_V029_ALIAS}' PODLAZ_E2E_BASE_VERSION=v0.2.29 PODLAZ_E2E_PROFILE_URI=\"\${uri}\" PODLAZ_E2E_DNS_CHECK_HOST=example.com PODLAZ_E2E_PUBLIC_IP_CHECK_URL=https://example.com/ bash /workspace/scripts/e2e/network-recovery-package-acceptance.sh"
}

require_legacy_evidence() {
  local key="$1"
  guest_exec grep -Fx "${key}=pass" "${GUEST_HISTORY_PUBLIC}/network-recovery-acceptance.txt" >/dev/null
}

assert_guest_package_provenance_after_history() {
  assert_guest_package_provenance
  guest_exec /bin/bash -lc "cd /workspace && source scripts/e2e/lib/e2e.sh && source scripts/e2e/lib/package_provenance.sh && assert_exact_podlaz_package_runtime_provenance '${GUEST_CANDIDATE}' '${EXPECTED_COMMIT}'"
}

run_scenario() {
  mark_failure infrastructure outer.baseline
  capture_outer_baseline
  mark_failure infrastructure guest.prepare
  prepare_system_guest
  start_system_guest

  mark_failure fixture historical.release
  start_synthetic_xray_endpoint
  install_tun_authorization
  prepare_v029_workdir
  create_foreign_sentinel
  capture_guest_network_baseline
  record_evidence pinned_v029.source_release pass

  mark_failure product pinned_v029.runtime
  run_pinned_v029_acceptance

  require_legacy_evidence released_package_connected
  record_evidence pinned_v029.source_active pass
  require_legacy_evidence candidate_package_replaced_daemon
  require_legacy_evidence candidate_package_transition_result_success
  record_evidence pinned_v029.candidate_replaced pass
  require_legacy_evidence legacy_upgrade_reconstructed_current_boot_session
  record_evidence pinned_v029.legacy_reconstruction pass
  require_legacy_evidence privacy_envelope_active
  record_evidence pinned_v029.privacy_active pass
  require_legacy_evidence active_dns_https
  record_evidence pinned_v029.post_upgrade_traffic pass
  require_legacy_evidence historical_upgrade_terminal_cleanup
  require_legacy_evidence historical_upgrade_recovery_clean
  require_legacy_evidence network_recovery_acceptance_complete
  record_evidence pinned_v029.acceptance_complete pass

  assert_guest_package_provenance_after_history
  record_evidence pinned_v029.runtime_provenance pass
  assert_foreign_sentinel
  record_evidence pinned_v029.foreign_state pass
  assert_guest_network_baseline_restored
  guest_exec resolvectl flush-caches
  guest_exec timeout 20 getent ahostsv4 example.com >/dev/null
  guest_exec timeout 30 curl -4 -fsS -o /dev/null https://example.com/
  record_evidence pinned_v029.network_restored pass

  FAILURE_CLASS=none
  FAILURE_STEP=none
}

main() {
  (($# == 2)) || fail "usage: $0 CANDIDATE.deb EXACT-V0.2.29.deb"
  require_cmd awk bash cmp curl debootstrap dpkg dpkg-deb find grep install ip iptables jq mktemp nft python3 readlink rm seq sha256sum sleep ss sudo systemd-nspawn systemd-run timeout
  install -d -m 0700 "${PRIVATE_ROOT}" "${E2E_ARTIFACT_DIR}" "${XRAY_ROOT}"
  : >"${REPORT}"
  chmod 0600 "${REPORT}"
  validate_candidate "$1"
  [[ "${EXPECTED_COMMIT}" =~ ^[0-9a-f]{40}$ ]] || fail "candidate commit identity is unavailable"
  validate_v029 "$2"
  dpkg --compare-versions "$(dpkg-deb --field "${CANDIDATE_DEB}" Version)" gt "$(dpkg-deb --field "${V029_DEB}" Version)" || \
    fail "candidate Debian version must be newer than v0.2.29"
  stage_v029
  trap teardown_all EXIT
  run_scenario
}

if [[ "${1:-}" == validate-report ]]; then
  validate_report
  exit 0
fi

main "$@"
