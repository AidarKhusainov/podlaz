#!/usr/bin/env bash
# Independent Gate A qualification in one disposable nspawn guest.
# Never run on the outer runner: only sourced synthetic guest helpers mutate state.
set -Eeuo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=hosted-synthetic-tun.sh
source "${SCRIPT_DIR}/hosted-synthetic-tun.sh"
PODLAZ_E2E_SYNTHETIC_PROTOCOL=vmess
PODLAZ_E2E_NM_L3_GUEST=true
case "${PODLAZ_E2E_GUEST_RELEASE:-noble}" in
  noble|resolute) ;;
  *) fail "unsupported disposable NM guest release" ;;
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
  tun.nm_l3_gate_a
  tun.clean_disconnect
  tun.terminal_cleanup
  tun.recovery_clean
  guest.baseline_restored
  outer.cleanup
  artifact.privacy
)

guest_prepare_adapter() {
  local policy_rc="${GUEST_ROOT}/usr/sbin/policy-rc.d" name
  # Service packages are installed into the private guest root, never the host.
  printf '#!/bin/sh\nexit 101\n' | sudo -n tee "${policy_rc}" >/dev/null
  sudo -n chmod 0755 "${policy_rc}"
  sudo -n chroot "${GUEST_ROOT}" /usr/bin/env DEBIAN_FRONTEND=noninteractive \
    apt-get install -y --no-install-recommends python3-dbus python3-gi \
    >"${PRIVATE_ROOT}/guest-nm-l3-dependencies.log" 2>&1
  sudo -n rm -f "${policy_rc}"
  sudo -n install -D -m 0644 "${SCRIPT_DIR}/nm_vpn_l3_guest_service.name" \
    "${GUEST_ROOT}/usr/lib/NetworkManager/VPN/podlaz-l3-research.name"
  sudo -n install -D -m 0644 "${SCRIPT_DIR}/nm_vpn_l3_guest_dbus.conf" \
    "${GUEST_ROOT}/etc/dbus-1/system.d/podlaz-nm-l3-guest.conf"
  sudo -n install -D -m 0755 "${SCRIPT_DIR}/nm_vpn_l3_guest_plugin.py" \
    "${GUEST_ROOT}/usr/libexec/podlaz-nm-l3-guest"
  for name in nm_vpn_l3_contract nm_vpn_l3_intent nm_vpn_l3_runtime_guard; do
    sudo -n install -D -m 0644 "${SCRIPT_DIR}/${name}.py" \
      "${GUEST_ROOT}/usr/libexec/${name}.py"
  done
}

run_scenario() {
  mark_failure infrastructure outer.baseline
  capture_outer_baseline
  mark_failure infrastructure guest.prepare
  prepare_system_guest
  guest_prepare_adapter
  start_system_guest
  # Prove the actual guest release; a matrix label alone is insufficient.
  guest_exec cat /etc/os-release | grep -Fx "VERSION_CODENAME=${GUEST_RELEASE}" >/dev/null
  # Bind the actual installed NetworkManager series, not just the distro label.
  local expected_nm_series
  case "${GUEST_RELEASE}" in
    noble) expected_nm_series=1.46 ;;
    resolute) expected_nm_series=1.54 ;;
  esac
  guest_exec nmcli --version | grep -Ex "nmcli tool, version ${expected_nm_series}\\.[0-9]+" >/dev/null
  mark_failure product candidate.provenance
  install_candidate_in_guest
  assert_guest_package_provenance
  record_evidence candidate.provenance pass

  guest_exec install -d -o e2e -g e2e -m 0700 \
    "${GUEST_XDG}" "${GUEST_XDG}/config" "${GUEST_XDG}/state" \
    "${GUEST_XDG}/cache" "${GUEST_PRIVATE}"
  mark_failure fixture synthetic.endpoint
  start_synthetic_xray_endpoint
  install_tun_authorization
  mark_failure diagnostic_unknown profile.import
  # Guest endpoint URI and credentials stay inside private guest state.
  guest_exec /bin/bash -lc "URI=\$(cat /run/podlaz-synthetic-xray/client-uri); runuser -u e2e -- env XDG_CONFIG_HOME='${GUEST_XDG}/config' XDG_STATE_HOME='${GUEST_XDG}/state' XDG_CACHE_HOME='${GUEST_XDG}/cache' /usr/bin/podlaz import \"\${URI}\" >${GUEST_PRIVATE}/import.stdout 2>${GUEST_PRIVATE}/import.stderr"
  guest_exec /bin/bash -lc "sed -n 's/^Profile: //p' '${GUEST_PRIVATE}/import.stdout' | head -n1 >'${GUEST_PRIVATE}/profile-selector' && test -s '${GUEST_PRIVATE}/profile-selector'"

  mark_failure product ordinary_user.boundary
  assert_ordinary_user_boundary
  record_evidence ordinary_user.boundary pass
  create_foreign_sentinel
  capture_guest_network_baseline
  qualify_explicit_proxy_only

  # The packaged Podlaz connects once only to generate and qualify the actual
  # synthetic VMess Xray runtime config. The entire Podlaz-owned L3 session is
  # then removed and proven absent BEFORE NM is granted ownership.
  mark_failure product tun.legacy_preparation
  run_guest_user /usr/bin/podlaz connect >"${PRIVATE_ROOT}/connect.stdout" 2>"${PRIVATE_ROOT}/connect.stderr"
  wait_guest_status verified-active 120
  assert_verified_active_authority
  record_evidence tun.verified_active pass
  run_active_traffic_checks
  guest_exec install -m 0600 /run/podlaz/generated/xray.json "${GUEST_PRIVATE}/nm-l3-generated-xray.json"
  mark_failure product tun.legacy_disconnected
  run_guest_user /usr/bin/podlaz disconnect >"${PRIVATE_ROOT}/disconnect.stdout" 2>"${PRIVATE_ROOT}/disconnect.stderr"
  wait_guest_status clean-inactive 80
  assert_terminal_authority_clean

  # NM is now the *only* IP configuration writer for the tracked new Xray TUN.
  # The Python guest scenario creates a separate underlay rule/table and arms a
  # private nft egress barrier before starting its Xray child or NM activation.
  mark_failure product tun.nm_l3_gate_a
  guest_exec python3 /workspace/scripts/e2e/nm_vpn_l3_scenario_guest.py \
    "${GUEST_PRIVATE}/nm-l3-generated-xray.json" "${ENDPOINT_IP}" \
    >"${PRIVATE_ROOT}/nm-l3-result.stdout" 2>"${PRIVATE_ROOT}/nm-l3-result.stderr"
  for label in nm-l3-foreign-collision-rejected nm-l3-invalid-ip4-rejected nm-l3-real-dbus-ip4 nm-l3-protected-routes-and-https nm-l3-natural-full nm-l3-failure-protection nm-l3-uplink-churn-protected nm-l3-live-nm-restart-recovered nm-l3-ipv6-physical-block nm-l3-exact-guest-cleanup nm-l3-result; do
    grep -Fx "${label}=pass" "${PRIVATE_ROOT}/nm-l3-result.stdout" >/dev/null
  done
  record_evidence tun.nm_l3_gate_a pass
  record_evidence tun.clean_disconnect pass

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
