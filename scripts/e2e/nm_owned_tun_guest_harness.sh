#!/usr/bin/env bash
# Separate, disposable NM-owned TUN/Xray lifetime feasibility gate.
# The nspawn child alone may mutate network state; never modify the outer host
# except through the existing exact-owned synthetic fixture.
set -Eeuo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=hosted-synthetic-tun.sh
source "${SCRIPT_DIR}/hosted-synthetic-tun.sh"

PODLAZ_E2E_SYNTHETIC_PROTOCOL=vmess
PODLAZ_E2E_NM_L3_GUEST=true
case "${PODLAZ_E2E_GUEST_RELEASE:-noble}" in
  noble|resolute) ;;
  *) fail "unsupported disposable NM guest" ;;
esac

EVIDENCE_KEYS=(
  candidate.provenance
  ordinary_user.boundary
  proxy_only.connect
  proxy_only.data_plane
  proxy_only.clean_disconnect
  tun.verified_active
  tun.system_dns
  tun.https_tls
  tun.nm_owned_link
  tun.xray_attached
  tun.protected_traffic
  tun.natural_full
  tun.nm_post_xray_active
  tun.terminal_cleanup
  tun.recovery_clean
  guest.baseline_restored
  outer.cleanup
  artifact.privacy
)

run_scenario() {
  mark_failure infrastructure outer.baseline
  capture_outer_baseline
  mark_failure infrastructure guest.prepare
  prepare_system_guest
  start_system_guest

  guest_exec cat /etc/os-release | grep -Fx "VERSION_CODENAME=${GUEST_RELEASE}" >/dev/null
  local series
  case "${GUEST_RELEASE}" in
    noble) series=1.46 ;;
    resolute) series=1.54 ;;
  esac
  guest_exec nmcli --version | grep -Ex "nmcli tool, version ${series}\\.[0-9]+" >/dev/null

  mark_failure product candidate.provenance
  install_candidate_in_guest
  assert_guest_package_provenance
  record_evidence candidate.provenance pass

  guest_exec install -d -o e2e -g e2e -m 0700 \
    "${GUEST_XDG}" "${GUEST_XDG}/config" "${GUEST_XDG}/state" \
    "${GUEST_XDG}/cache" "${GUEST_PRIVATE}"
  mark_failure fixture synthetic.endpoint
  start_synthetic_xray_endpoint
  # This binary is built separately from the exact upstream commit and must
  # never replace the installed Podlaz Xray or be packaged for production.
  [[ -n "${PODLAZ_NM_EXTERNAL_XRAY_BINARY:-}" &&
     -f "${PODLAZ_NM_EXTERNAL_XRAY_BINARY}" &&
     -x "${PODLAZ_NM_EXTERNAL_XRAY_BINARY}" &&
     ! -L "${PODLAZ_NM_EXTERNAL_XRAY_BINARY}" ]] || fail "missing pinned guest-only external TUN engine"
  install -m 0755 "${PODLAZ_NM_EXTERNAL_XRAY_BINARY}" "${XRAY_ROOT}/nm-owned-external-xray"
  install_tun_authorization
  mark_failure diagnostic_unknown profile.import
  guest_exec /bin/bash -lc "URI=\$(cat /run/podlaz-synthetic-xray/client-uri); runuser -u e2e -- env XDG_CONFIG_HOME='${GUEST_XDG}/config' XDG_STATE_HOME='${GUEST_XDG}/state' XDG_CACHE_HOME='${GUEST_XDG}/cache' /usr/bin/podlaz import \"\${URI}\" >${GUEST_PRIVATE}/import.stdout 2>${GUEST_PRIVATE}/import.stderr"
  guest_exec /bin/bash -lc "sed -n 's/^Profile: //p' '${GUEST_PRIVATE}/import.stdout' | head -n1 >'${GUEST_PRIVATE}/profile-selector' && test -s '${GUEST_PRIVATE}/profile-selector'"

  mark_failure product ordinary_user.boundary
  assert_ordinary_user_boundary
  record_evidence ordinary_user.boundary pass
  create_foreign_sentinel
  capture_guest_network_baseline
  qualify_explicit_proxy_only

  # Obtain the *packaged* Xray runtime config, then remove and verify all of
  # Podlaz's own network/session authority before handing the link to NM.
  mark_failure product tun.legacy_preparation
  run_guest_user /usr/bin/podlaz connect >"${PRIVATE_ROOT}/connect.stdout" 2>"${PRIVATE_ROOT}/connect.stderr"
  wait_guest_status verified-active 120
  assert_verified_active_authority
  record_evidence tun.verified_active pass
  run_active_traffic_checks
  guest_exec install -m 0600 /run/podlaz/generated/xray.json "${GUEST_PRIVATE}/nm-owned-generated-xray.json"
  mark_failure product tun.legacy_disconnected
  run_guest_user /usr/bin/podlaz disconnect >"${PRIVATE_ROOT}/disconnect.stdout" 2>"${PRIVATE_ROOT}/disconnect.stderr"
  wait_guest_status clean-inactive 80
  assert_terminal_authority_clean

  mark_failure capability tun.nm_owned_feasibility
  guest_exec python3 /workspace/scripts/e2e/nm_owned_tun_guest.py \
    "${GUEST_PRIVATE}/nm-owned-generated-xray.json" \
    >"${PRIVATE_ROOT}/nm-owned-result.stdout" 2>"${PRIVATE_ROOT}/nm-owned-result.stderr"

  local label
  for label in nm-owned-link-and-l3 nm-owned-xray-attached nm-owned-protected-traffic nm-owned-natural-full nm-owned-nm-post-xray-active nm-owned-result; do
    grep -Fx "${label}=pass" "${PRIVATE_ROOT}/nm-owned-result.stdout" >/dev/null
  done
  record_evidence tun.nm_owned_link pass
  record_evidence tun.xray_attached pass
  record_evidence tun.protected_traffic pass
  record_evidence tun.natural_full pass
  record_evidence tun.nm_post_xray_active pass

  mark_failure product tun.terminal_cleanup
  assert_terminal_authority_clean
  record_evidence tun.terminal_cleanup pass
  mark_failure product guest.baseline_restored
  assert_guest_network_baseline_restored
  record_evidence guest.baseline_restored pass
  mark_failure product guest.connectivity_restored
  guest_exec resolvectl flush-caches
  guest_exec timeout 20 getent ahostsv4 example.com >/dev/null
  guest_exec timeout 30 curl -4 -fsS -o /dev/null https://example.com/
  mark_failure product tun.recovery
  run_clean_recovery
  record_evidence tun.recovery_clean pass
  FAILURE_CLASS=none
  FAILURE_STEP=none
}

if [[ "${1:-}" == validate-report ]]; then
  validate_report
else
  main "$@"
fi
