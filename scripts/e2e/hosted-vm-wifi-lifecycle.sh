#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"
# shellcheck source=lib/hosted_vm.sh
source "${SCRIPT_DIR}/lib/hosted_vm.sh"
# shellcheck source=lib/hosted_vm_tun.sh
source "${SCRIPT_DIR}/lib/hosted_vm_tun.sh"

REPORT="${E2E_ARTIFACT_DIR}/hosted-vm-wifi-lifecycle.txt"
PRIVATE_ROOT="${E2E_TMP_ROOT}/hosted-vm-wifi-lifecycle"
VM_ROOT="${PRIVATE_ROOT}/vm"
XRAY_ROOT="${PRIVATE_ROOT}/synthetic-xray"
EXPECTED_COMMIT="${PODLAZ_E2E_CANDIDATE_COMMIT:-${GITHUB_SHA:-}}"
CANDIDATE_DEB=""
FAILURE_CLASS=diagnostic_unknown
FAILURE_STEP=bootstrap
FINALIZED=false
WIFI_CONNECTION=podlaz-ci-wifi
WIFI_SSID=podlaz-ci-wifi
WIFI_PASSPHRASE=podlaz-ci-passphrase
WIFI_AP_CIDR=198.51.100.1/24
WIFI_AP_IP=198.51.100.1
WIFI_DHCP_RANGE=198.51.100.10,198.51.100.20,255.255.255.0,1h
WIFI_CLIENT_IF=""
SESSION_BEFORE=""

EVIDENCE_KEYS=(
  vm.acceleration
  vm.image_checksum
  vm.boot
  candidate.provenance
  wifi.simulation_stack
  wifi.associated_before_tun
  wifi.ordinary_connectivity_before_tun
  fixture.synthetic_endpoint
  fixture.foreign_state
  tun.verified_active_before_disconnect
  tun.exact_authority_before_disconnect
  privacy.direct_uplink_blocked_before_disconnect
  tun.traffic_before_disconnect
  wifi.disconnected
  privacy.envelope_retained
  privacy.direct_uplink_blocked
  fixture.foreign_state_during_disconnect
  wifi.reassociated
  tun.verified_active_after_reconnect
  tun.same_network_session
  tun.exact_authority_after_reconnect
  fixture.foreign_state_after_reconnect
  tun.traffic_after_reconnect
  tun.clean_disconnect
  tun.exact_terminal_cleanup
  tun.recovery_clean
  guest.ordinary_connectivity_restored
  fixture.foreign_state_terminal
  artifact.privacy
)

record_evidence() {
  local key="$1" state="$2"
  [[ "${key}" =~ ^[a-z0-9_.-]+$ ]] || fail "invalid Wi-Fi evidence key"
  case "${state}" in
    pass|fail|unavailable) ;;
    *) fail "invalid Wi-Fi evidence state for ${key}" ;;
  esac
  grep -Eq "^${key}=" "${REPORT}" 2>/dev/null && fail "duplicate Wi-Fi evidence key: ${key}"
  printf '%s=%s\n' "${key}" "${state}" >>"${REPORT}"
}

record_if_missing() {
  local key="$1" state="$2"
  grep -Eq "^${key}=" "${REPORT}" 2>/dev/null || record_evidence "${key}" "${state}"
}

mark_failure() {
  local class="$1" step="$2"
  case "${class}" in
    product|fixture|infrastructure|capability|diagnostic_unknown) ;;
    *) class=diagnostic_unknown ;;
  esac
  FAILURE_CLASS="${class}"
  FAILURE_STEP="${step//[^A-Za-z0-9_.-]/_}"
}

finalize_report() {
  local key kvm_state
  [[ "${FINALIZED}" == false ]] || return 0
  FINALIZED=true
  for key in "${EVIDENCE_KEYS[@]}"; do
    record_if_missing "${key}" fail
  done
  if [[ "${HOSTED_VM_ACCEL}" == kvm ]]; then
    kvm_state=pass
  else
    kvm_state=unavailable
  fi
  {
    printf 'capability.kvm=%s\n' "${kvm_state}"
    printf 'failure.class=%s\n' "${FAILURE_CLASS}"
    printf 'failure.step=%s\n' "${FAILURE_STEP}"
  } >>"${REPORT}"
}

validate_report() {
  python3 - "${REPORT}" "${EVIDENCE_KEYS[@]}" <<'PY'
import re
import sys
from pathlib import Path

path = Path(sys.argv[1])
expected = set(sys.argv[2:])
if not path.is_file() or path.is_symlink():
    raise SystemExit("hosted VM Wi-Fi report is missing or invalid")
values = {}
meta = {}
kvm = None
for line in path.read_text(encoding="utf-8").splitlines():
    match = re.fullmatch(r"([a-z0-9_.-]+)=(pass|fail|unavailable)", line)
    if match:
        key, value = match.groups()
        if key == "capability.kvm":
            if kvm is not None:
                raise SystemExit("duplicate KVM capability evidence")
            kvm = value
            continue
        if key in values:
            raise SystemExit(f"duplicate evidence key: {key}")
        values[key] = value
        continue
    match = re.fullmatch(r"failure\.(class|step)=([A-Za-z0-9_.-]+)", line)
    if match:
        key, value = match.groups()
        if key in meta:
            raise SystemExit(f"duplicate failure metadata: {key}")
        meta[key] = value
        continue
    raise SystemExit("hosted VM Wi-Fi report contains non-normalized data")
if set(values) != expected:
    raise SystemExit(f"evidence schema mismatch: expected={sorted(expected)} got={sorted(values)}")
if any(values[key] != "pass" for key in expected):
    raise SystemExit("required hosted VM Wi-Fi evidence is not successful")
if kvm not in {"pass", "unavailable"}:
    raise SystemExit("KVM capability was not reported")
if meta != {"class": "none", "step": "none"}:
    raise SystemExit(f"scenario failure metadata is not clean: {meta}")
PY
}

validate_candidate() {
  local path="$1"
  [[ -f "${path}" && ! -L "${path}" ]] || fail "candidate package must be a regular non-symlink file"
  [[ "$(dpkg-deb --field "${path}" Package)" == podlaz ]] || fail "candidate package is not podlaz"
  [[ "$(dpkg-deb --field "${path}" Architecture)" == amd64 ]] || fail "hosted VM Wi-Fi candidate must be amd64"
  [[ "${EXPECTED_COMMIT}" =~ ^[0-9a-fA-F]{40}$ ]] || fail "candidate commit provenance is required"
  CANDIDATE_DEB="$(readlink -f -- "${path}")"
}

prepare_wifi_fixture() {
  local guest_script
  guest_script="$(cat <<'EOF'
set -Eeuo pipefail
export DEBIAN_FRONTEND=noninteractive
wifi_fixture_phase=packages
trap 'printf "wifi-fixture phase=%s failed\\n" "$wifi_fixture_phase" >&2' ERR

apt-get update -qq
apt-get install -y -qq --no-install-recommends \
  dnsmasq-base \
  hostapd \
  iw \
  iptables \
  network-manager \
  wpasupplicant \
  "linux-modules-extra-$(uname -r)"

systemctl stop NetworkManager.service >/dev/null 2>&1 || true
systemctl stop wpa_supplicant.service >/dev/null 2>&1 || true
systemctl stop hostapd.service >/dev/null 2>&1 || true

wifi_fixture_phase=hwsim_radios
modprobe mac80211_hwsim radios=2
wifi_count=0
for _ in $(seq 1 60); do
  udevadm settle >/dev/null 2>&1 || true
  wifi_count="$(iw dev | awk '$1 == \"Interface\" {count++} END {print count+0}')"
  if (( wifi_count >= 2 )); then
    break
  fi
  sleep 0.5
done
(( wifi_count >= 2 ))
ap_candidate="$(iw dev | awk '$1 == \"Interface\" {print $2; exit}')"
ap_phy="$(basename "$(readlink -f "/sys/class/net/${ap_candidate}/phy80211")")"
[[ "${ap_phy}" == phy* ]]

wifi_fixture_phase=uplink_identity
management_if="$(ip -4 route show default | awk 'NR == 1 {for (i=1; i<=NF; i++) if ($i == "dev") {print $(i+1); exit}}')"
management_gateway="$(ip -4 route show default dev "${management_if}" | awk 'NR == 1 {for (i=1; i<=NF; i++) if ($i == "via") {print $(i+1); exit}}')"
[[ -n "${management_if}" && -n "${management_gateway}" ]]

provider_if=""
for path in /sys/class/net/*/address; do
  [[ "$(cat "${path}")" == "${provider_mac}" ]] || continue
  provider_if="$(basename "$(dirname "${path}")")"
  break
done
[[ -n "${provider_if}" ]]
[[ "${provider_if}" != "${management_if}" && "${provider_if}" != "${ap_candidate}" ]]

wifi_fixture_phase=namespace_create
ip netns add pzwifiap
ip netns exec pzwifiap sleep infinity </dev/null >/dev/null 2>&1 &
ap_ns_pid=$!
printf '%s\n' "${ap_ns_pid}" >/var/tmp/podlaz-wifi-ap-ns.pid
kill -0 "${ap_ns_pid}"

wifi_fixture_phase=namespace_radios
iw phy "${ap_phy}" set netns "${ap_ns_pid}"
ip link set "${provider_if}" netns pzwifiap
ap_if=""
client_if=""
for _ in $(seq 1 60); do
  udevadm settle >/dev/null 2>&1 || true
  ap_if="$(ip netns exec pzwifiap iw dev | awk '$1 == \"Interface\" {print $2; exit}')"
  client_if="$(iw dev | awk '$1 == \"Interface\" {print $2; exit}')"
  if [[ -n "${ap_if}" && -n "${client_if}" ]] &&
     ip netns exec pzwifiap ip link show dev "${provider_if}" >/dev/null 2>&1; then
    break
  fi
  sleep 0.5
done
[[ -n "${ap_if}" && -n "${client_if}" ]]
ip netns exec pzwifiap ip link show dev "${provider_if}" >/dev/null
[[ "${management_if}" != "${client_if}" ]]

wifi_fixture_phase=ap_network
ip netns exec pzwifiap bash -s -- \
  "${ap_if}" "${provider_if}" "${provider_guest_cidr}" "${provider_host_ip}" "${wifi_ap_cidr}" <<'AP'
set -Eeuo pipefail
ap_if="$1"
provider_if="$2"
provider_guest_cidr="$3"
provider_host_ip="$4"
wifi_ap_cidr="$5"

ip link set lo up
ip address add "${provider_guest_cidr}" dev "${provider_if}"
ip link set "${provider_if}" up
ip route add default via "${provider_host_ip}" dev "${provider_if}"

ip address add "${wifi_ap_cidr}" dev "${ap_if}"
ip link set "${ap_if}" up

sysctl -q -w net.ipv4.ip_forward=1
iptables -t nat -A POSTROUTING -s 198.51.100.0/24 -o "${provider_if}" -j MASQUERADE
iptables -A FORWARD -i "${ap_if}" -o "${provider_if}" -j ACCEPT
iptables -A FORWARD -i "${provider_if}" -o "${ap_if}" \
  -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
AP

wifi_fixture_phase=hostapd
cat >/var/tmp/podlaz-wifi-hostapd.conf <<HOSTAPD
interface=${ap_if}
driver=nl80211
ssid=${wifi_ssid}
hw_mode=g
channel=1
auth_algs=1
wpa=2
wpa_passphrase=${wifi_passphrase}
wpa_key_mgmt=WPA-PSK
rsn_pairwise=CCMP
HOSTAPD

ip netns exec pzwifiap hostapd -B -P /run/podlaz-wifi-hostapd.pid /var/tmp/podlaz-wifi-hostapd.conf
ip netns exec pzwifiap dnsmasq \
  --conf-file= \
  --interface="${ap_if}" \
  --bind-interfaces \
  --dhcp-range="${dhcp_range}" \
  --dhcp-option=3,198.51.100.1 \
  --dhcp-option=6,1.1.1.1 \
  --pid-file=/run/podlaz-wifi-dnsmasq.pid

wifi_fixture_phase=networkmanager
systemctl start wpa_supplicant.service
systemctl start NetworkManager.service
nmcli radio wifi on

for _ in $(seq 1 30); do
  if nmcli -t -f DEVICE,TYPE device status | grep -F "${client_if}:wifi" >/dev/null; then
    break
  fi
  sleep 1
done
nmcli -t -f DEVICE,TYPE device status | grep -F "${client_if}:wifi" >/dev/null

for _ in $(seq 1 30); do
  nmcli device wifi rescan ifname "${client_if}" >/dev/null 2>&1 || true
  if nmcli -t -f SSID device wifi list ifname "${client_if}" | grep -Fx "${wifi_ssid}" >/dev/null; then
    break
  fi
  sleep 1
done
nmcli -t -f SSID device wifi list ifname "${client_if}" | grep -Fx "${wifi_ssid}" >/dev/null

wifi_fixture_phase=wifi_association
nmcli --wait 30 device wifi connect "${wifi_ssid}" \
  password "${wifi_passphrase}" \
  ifname "${client_if}" \
  name "${wifi_connection}"

nmcli -g GENERAL.CONNECTION device show "${client_if}" | grep -Fx "${wifi_connection}" >/dev/null
iw dev "${client_if}" link | grep -F 'Connected to ' >/dev/null
ip -4 address show dev "${client_if}" | grep -F '198.51.100.' >/dev/null
ip -4 route show dev "${client_if}" | grep -F 'default via 198.51.100.1' >/dev/null

ip route del default via "${management_gateway}" dev "${management_if}"
ip -4 route show default | grep -F "via 198.51.100.1 dev ${client_if}" >/dev/null

printf '%s\n' "${client_if}" >/var/tmp/podlaz-wifi-client-if
printf '%s\n' "${management_if}" >/var/tmp/podlaz-wifi-management-if
printf '%s\n' "${management_gateway}" >/var/tmp/podlaz-wifi-management-gateway

timeout 20 getent ahostsv4 example.com >/dev/null
timeout 30 curl -4 -fsS -o /dev/null https://example.com/
EOF
)"
  hosted_vm_ssh sudo env \
    "wifi_ssid=${WIFI_SSID}" \
    "wifi_passphrase=${WIFI_PASSPHRASE}" \
    "wifi_connection=${WIFI_CONNECTION}" \
    "wifi_ap_cidr=${WIFI_AP_CIDR}" \
    "provider_mac=${HOSTED_VM_PROVIDER_MAC}" \
    "provider_guest_cidr=${HOSTED_VM_PROVIDER_GUEST_CIDR}" \
    "provider_host_ip=${HOSTED_VM_PROVIDER_HOST_IP}" \
    "dhcp_range=${WIFI_DHCP_RANGE}" \
    bash -s <<<"${guest_script}"
  WIFI_CLIENT_IF="$(hosted_vm_ga_exec /bin/cat /var/tmp/podlaz-wifi-client-if | tr -d '[:space:]')"
  [[ -n "${WIFI_CLIENT_IF}" ]]
}

assert_wifi_associated() {
  local guest_script
  guest_script="$(cat <<'EOF'
set -Eeuo pipefail
client_if="$(cat /var/tmp/podlaz-wifi-client-if)"
nmcli -g GENERAL.CONNECTION device show "${client_if}" | grep -Fx "${wifi_connection}" >/dev/null
iw dev "${client_if}" link | grep -F 'Connected to ' >/dev/null
ip -4 address show dev "${client_if}" | grep -F '198.51.100.' >/dev/null
ip -4 route show default | grep -F "via 198.51.100.1 dev ${client_if}" >/dev/null
EOF
)"
  hosted_vm_ga_bash "wifi_connection=${WIFI_CONNECTION@Q}; ${guest_script}"
}

wifi_disconnect() {
  local guest_script
  guest_script="$(cat <<'EOF'
set -Eeuo pipefail
client_if="$(cat /var/tmp/podlaz-wifi-client-if)"
nmcli --wait 20 connection down "${wifi_connection}"
! iw dev "${client_if}" link | grep -F 'Connected to ' >/dev/null
! ip -4 route show default | grep -F "dev ${client_if}" >/dev/null
EOF
)"
  hosted_vm_ga_bash "wifi_connection=${WIFI_CONNECTION@Q}; ${guest_script}"
}

wifi_reassociate() {
  local guest_script
  guest_script="$(cat <<'EOF'
set -Eeuo pipefail
client_if="$(cat /var/tmp/podlaz-wifi-client-if)"
nmcli --wait 30 connection up "${wifi_connection}" ifname "${client_if}"
for _ in $(seq 1 60); do
  if nmcli -g GENERAL.CONNECTION device show "${client_if}" | grep -Fx "${wifi_connection}" >/dev/null &&
     iw dev "${client_if}" link | grep -F 'Connected to ' >/dev/null &&
     ip -4 route show default | grep -F "via 198.51.100.1 dev ${client_if}" >/dev/null; then
    exit 0
  fi
  sleep 0.5
done
exit 1
EOF
)"
  hosted_vm_ga_bash "wifi_connection=${WIFI_CONNECTION@Q}; ${guest_script}"
}

assert_wifi_ordinary_connectivity() {
  hosted_vm_ga_bash 'timeout 20 getent ahostsv4 example.com >/dev/null && timeout 30 curl -4 -fsS -o /dev/null https://example.com/'
}

diagnose_tun_connect_failure() {
  local doctor_json doctor_rc token
  set +e
  doctor_json="$(hosted_vm_tun_run_podlaz doctor --tun --json 2>/dev/null)"
  doctor_rc=$?
  set -e
  if (( doctor_rc != 0 && doctor_rc != 3 )); then
    printf 'doctor-unavailable\n'
    return 0
  fi
  token="$(jq -r '
    [
      .primary_classification,
      .failure_phase,
      ((.probes // [])[] | select(.status == "fail") | .id),
      ((.probes // [])[] | select(.status == "fail") | .classification),
      ((.probes // [])[] | select(.status == "fail") | .failure_phase)
    ]
    | map(select(. != null and . != ""))
    | .[0:5]
    | join(".")
  ' <<<"${doctor_json}" 2>/dev/null || true)"
  token="$(printf '%s' "${token}" | tr '[:upper:]' '[:lower:]' | tr -cs 'a-z0-9_.-' '-' | sed 's/^-*//; s/-*$//')"
  printf '%s\n' "${token:-doctor-unclassified}"
}
assert_public_artifact_privacy() {
  local extra
  extra="$(find "${E2E_ARTIFACT_DIR}" -mindepth 1 -maxdepth 1 ! -name 'hosted-vm-wifi-lifecycle.txt' -print -quit)"
  [[ -z "${extra}" ]] || return 1
  [[ -f "${REPORT}" && ! -L "${REPORT}" ]] || return 1
  ! grep -Eiq 'vless://|vmess://|trojan://|ss://|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|198[.]51[.]100[.]|203[.]0[.]113[.]' "${REPORT}"
}

cleanup_wifi_fixture() {
  local guest_script
  [[ "${HOSTED_VM_GA_READY}" == true ]] || return 0
  guest_script="$(cat <<'EOF'
set +e
client_if="$(cat /var/tmp/podlaz-wifi-client-if 2>/dev/null)"
management_if="$(cat /var/tmp/podlaz-wifi-management-if 2>/dev/null)"
management_gateway="$(cat /var/tmp/podlaz-wifi-management-gateway 2>/dev/null)"
ap_ns_pid="$(cat /var/tmp/podlaz-wifi-ap-ns.pid 2>/dev/null)"
[[ -z "${client_if}" ]] || nmcli connection down "${wifi_connection}" >/dev/null 2>&1
[[ -z "${management_if}" || -z "${management_gateway}" ]] || ip route replace default via "${management_gateway}" dev "${management_if}"
[[ -z "${ap_ns_pid}" ]] || kill "${ap_ns_pid}" >/dev/null 2>&1
ip netns del pzwifiap >/dev/null 2>&1
rm -f /var/tmp/podlaz-wifi-*.pid /var/tmp/podlaz-wifi-hostapd.conf /var/tmp/podlaz-wifi-client-if /var/tmp/podlaz-wifi-management-if /var/tmp/podlaz-wifi-management-gateway
exit 0
EOF
)"
  hosted_vm_ga_bash "wifi_connection=${WIFI_CONNECTION@Q}; ${guest_script}" >/dev/null 2>&1 || true
}


cleanup() {
  local saved=$? cleanup_failed=0
  trap - EXIT
  set +e
  hosted_vm_tun_remove_foreign_state || cleanup_failed=1
  hosted_vm_tun_stop_endpoint || cleanup_failed=1
  hosted_vm_remove_polkit_rule || cleanup_failed=1
  cleanup_wifi_fixture || cleanup_failed=1
  hosted_vm_stop || cleanup_failed=1
  if (( cleanup_failed != 0 )) && (( saved == 0 )); then
    mark_failure infrastructure fixture.cleanup
    saved=1
  fi
  finalize_report
  set -e
  exit "${saved}"
}

run_scenario() {
  mark_failure capability vm.acceleration
  hosted_vm_probe_acceleration
  record_evidence vm.acceleration pass

  hosted_vm_tun_init "${CANDIDATE_DEB}" "${EXPECTED_COMMIT}" "${XRAY_ROOT}"

  mark_failure fixture synthetic.endpoint_material
  hosted_vm_tun_start_endpoint

  mark_failure infrastructure vm.image
  hosted_vm_prepare_image
  record_evidence vm.image_checksum pass

  mark_failure infrastructure vm.boot
  hosted_vm_start
  hosted_vm_wait_ssh 180
  hosted_vm_wait_cloud_init
  record_evidence vm.boot pass

  mark_failure product candidate.install
  hosted_vm_tun_install_candidate

  mark_failure fixture guest.control
  hosted_vm_ssh sudo env DEBIAN_FRONTEND=noninteractive apt-get install -y -qq curl jq
  hosted_vm_prepare_guest_agent
  hosted_vm_tun_install_helpers "${REPO_ROOT}"
  hosted_vm_tun_install_polkit "${PRIVATE_ROOT}/polkit.rules"

  mark_failure capability wifi.simulation_stack
  prepare_wifi_fixture
  record_evidence wifi.simulation_stack pass

  mark_failure product candidate.provenance
  hosted_vm_tun_assert_candidate_provenance
  record_evidence candidate.provenance pass

  mark_failure fixture wifi.association
  assert_wifi_associated
  record_evidence wifi.associated_before_tun pass

  assert_wifi_ordinary_connectivity
  record_evidence wifi.ordinary_connectivity_before_tun pass

  mark_failure fixture synthetic.endpoint
  hosted_vm_tun_assert_endpoint_reachable
  record_evidence fixture.synthetic_endpoint pass

  mark_failure fixture guest.profile
  hosted_vm_tun_prepare_profile
  hosted_vm_tun_capture_direct_baseline

  hosted_vm_tun_create_foreign_state
  hosted_vm_tun_assert_foreign_state
  record_evidence fixture.foreign_state pass

  mark_failure product tun.connect
  if ! hosted_vm_tun_connect; then
    mark_failure product "tun.connect.$(diagnose_tun_connect_failure)"
    return 1
  fi
  record_evidence tun.verified_active_before_disconnect pass

  mark_failure product tun.authority_before_disconnect
  hosted_vm_tun_capture_exact_active_authority yes
  SESSION_BEFORE="$(hosted_vm_tun_session_id)"
  [[ -n "${SESSION_BEFORE}" ]]
  record_evidence tun.exact_authority_before_disconnect pass

  mark_failure product privacy.before_disconnect
  hosted_vm_tun_assert_direct_blocked
  record_evidence privacy.direct_uplink_blocked_before_disconnect pass

  hosted_vm_tun_run_traffic
  record_evidence tun.traffic_before_disconnect pass

  mark_failure fixture wifi.disconnect
  wifi_disconnect
  record_evidence wifi.disconnected pass

  mark_failure product privacy.disconnect_window
  hosted_vm_tun_assert_armed_current_boot_session
  hosted_vm_tun_assert_direct_blocked
  record_evidence privacy.envelope_retained pass
  record_evidence privacy.direct_uplink_blocked pass

  hosted_vm_tun_assert_foreign_state
  record_evidence fixture.foreign_state_during_disconnect pass

  mark_failure fixture wifi.reassociate
  wifi_reassociate
  assert_wifi_associated
  record_evidence wifi.reassociated pass

  mark_failure product tun.revalidate
  hosted_vm_tun_wait_status verified-active 150
  record_evidence tun.verified_active_after_reconnect pass

  [[ "$(hosted_vm_tun_session_id)" == "${SESSION_BEFORE}" ]]
  record_evidence tun.same_network_session pass

  hosted_vm_tun_capture_exact_active_authority no
  record_evidence tun.exact_authority_after_reconnect pass

  hosted_vm_tun_assert_direct_blocked
  hosted_vm_tun_assert_foreign_state
  record_evidence fixture.foreign_state_after_reconnect pass

  hosted_vm_tun_run_traffic
  record_evidence tun.traffic_after_reconnect pass

  mark_failure product tun.disconnect
  hosted_vm_tun_disconnect
  record_evidence tun.clean_disconnect pass

  mark_failure product tun.terminal_cleanup
  hosted_vm_tun_assert_exact_terminal_cleanup
  record_evidence tun.exact_terminal_cleanup pass

  hosted_vm_tun_run_clean_recovery
  record_evidence tun.recovery_clean pass

  hosted_vm_tun_assert_foreign_state
  record_evidence fixture.foreign_state_terminal pass

  mark_failure infrastructure guest.connectivity
  hosted_vm_tun_capture_direct_baseline
  record_evidence guest.ordinary_connectivity_restored pass

  mark_failure diagnostic_unknown artifact.privacy
  assert_public_artifact_privacy
  record_evidence artifact.privacy pass

  FAILURE_CLASS=none
  FAILURE_STEP=none
}

main() {
  (($# == 1)) || fail "usage: $0 CANDIDATE.deb"
  require_cmd awk bash curl dpkg-deb find grep install ip jq mktemp python3 readlink sed seq sha256sum sleep ss tr
  validate_candidate "$1"
  install -d -m 0700 "${PRIVATE_ROOT}" "${E2E_ARTIFACT_DIR}" "${XRAY_ROOT}"
  : >"${REPORT}"
  chmod 0600 "${REPORT}"
  hosted_vm_init "${VM_ROOT}"
  trap cleanup EXIT
  run_scenario
  trap - EXIT
  finalize_report
  validate_report
}

if [[ "${1:-}" == validate-report ]]; then
  validate_report
  exit 0
fi

main "$@"
