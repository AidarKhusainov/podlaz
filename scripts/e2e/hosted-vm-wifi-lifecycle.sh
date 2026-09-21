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
WIFI_UPSTREAM_ROOT_CIDR=172.31.254.1/30
WIFI_UPSTREAM_AP_CIDR=172.31.254.2/30
WIFI_UPSTREAM_AP_IP=172.31.254.2
WIFI_POLICY_TABLE=51821
WIFI_POLICY_PRIORITY=100
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

prepare_endpoint_material() {
  local extract uuid port=18080
  extract="${XRAY_ROOT}/package"
  install -d -m 0700 "${XRAY_ROOT}" "${extract}"
  dpkg-deb -x "${CANDIDATE_DEB}" "${extract}"
  uuid="$("${extract}/usr/lib/podlaz/xray" uuid | tr -d '[:space:]')"
  [[ "${uuid}" =~ ^[0-9a-fA-F-]{36}$ ]] || return 1

  cat >"${XRAY_ROOT}/server.json" <<EOF
{
  "log": {"loglevel": "warning"},
  "inbounds": [{
    "listen": "${WIFI_AP_IP}",
    "port": ${port},
    "protocol": "vless",
    "settings": {"clients": [{"id": "${uuid}"}], "decryption": "none"},
    "streamSettings": {"security": "none"}
  }],
  "outbounds": [{"protocol": "freedom", "settings": {}}]
}
EOF
  chmod 0600 "${XRAY_ROOT}/server.json"
  "${extract}/usr/lib/podlaz/xray" run -test -config "${XRAY_ROOT}/server.json" >"${XRAY_ROOT}/config-test.log" 2>&1
  printf 'vless://%s@%s:%s?type=tcp&security=none&encryption=none#hosted-vm-wifi\n' \
    "${uuid}" "${WIFI_AP_IP}" "${port}" >"${XRAY_ROOT}/client-uri"
  chmod 0600 "${XRAY_ROOT}/client-uri"
}

install_wifi_fixture_files() {
  hosted_vm_scp_to "${XRAY_ROOT}/server.json" /var/tmp/podlaz-wifi-server.json
}

prepare_wifi_fixture() {
  local guest_script
  guest_script="$(cat <<'EOF'
set -Eeuo pipefail
export DEBIAN_FRONTEND=noninteractive

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

modprobe mac80211_hwsim radios=2
udevadm settle

mapfile -t wifi_ifaces < <(iw dev | awk '$1 == "Interface" {print $2}')
((${#wifi_ifaces[@]} >= 2))
ap_if="${wifi_ifaces[0]}"
client_if="${wifi_ifaces[1]}"
ap_phy="$(basename "$(readlink -f "/sys/class/net/${ap_if}/phy80211")")"
[[ "${ap_phy}" == phy* ]]

management_if="$(ip -4 route show default | awk 'NR == 1 {for (i=1; i<=NF; i++) if ($i == "dev") {print $(i+1); exit}}')"
management_gateway="$(ip -4 route show default dev "${management_if}" | awk 'NR == 1 {for (i=1; i<=NF; i++) if ($i == "via") {print $(i+1); exit}}')"
[[ -n "${management_if}" && -n "${management_gateway}" ]]
[[ "${management_if}" != "${ap_if}" && "${management_if}" != "${client_if}" ]]

ip netns add pzwifiap
ip netns exec pzwifiap sleep infinity &
ap_ns_pid=$!
printf '%s\n' "${ap_ns_pid}" >/var/tmp/podlaz-wifi-ap-ns.pid
iw phy "${ap_phy}" set netns "${ap_ns_pid}"

ip link add pzwifi-root type veth peer name pzwifi-up
ip link set pzwifi-up netns pzwifiap
ip address add "${upstream_root_cidr}" dev pzwifi-root
ip link set pzwifi-root up

sysctl -q -w net.ipv4.ip_forward=1
iptables -t nat -A POSTROUTING -s 172.31.254.0/30 -o "${management_if}" -j MASQUERADE
iptables -A FORWARD -i pzwifi-root -o "${management_if}" -j ACCEPT
iptables -A FORWARD -i "${management_if}" -o pzwifi-root \
  -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
ip route add table "${policy_table}" default via "${management_gateway}" dev "${management_if}"
ip rule add priority "${policy_priority}" from "${upstream_ap_ip}/32" table "${policy_table}"

ip netns exec pzwifiap bash -s -- "${ap_if}" "${upstream_ap_cidr}" "${wifi_ap_cidr}" <<'AP'
set -Eeuo pipefail
ap_if="$1"
upstream_ap_cidr="$2"
wifi_ap_cidr="$3"
ip link set lo up
ip address add "${upstream_ap_cidr}" dev pzwifi-up
ip link set pzwifi-up up
ip route add default via 172.31.254.1
ip address add "${wifi_ap_cidr}" dev "${ap_if}"
ip link set "${ap_if}" up
sysctl -q -w net.ipv4.ip_forward=1
iptables -t nat -A POSTROUTING -s 198.51.100.0/24 -o pzwifi-up -j MASQUERADE
iptables -A FORWARD -i "${ap_if}" -o pzwifi-up -j ACCEPT
iptables -A FORWARD -i pzwifi-up -o "${ap_if}" \
  -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
AP

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

ip netns exec pzwifiap sh -c \
  'nohup /usr/lib/podlaz/xray run -config /var/tmp/podlaz-wifi-server.json >/var/tmp/podlaz-wifi-xray.log 2>&1 </dev/null & echo $! >/var/tmp/podlaz-wifi-xray.pid'

for _ in $(seq 1 100); do
  if ip netns exec pzwifiap ss -H -ltn | awk '{print $4}' | grep -Fx "198.51.100.1:18080" >/dev/null; then
    break
  fi
  sleep 0.1
done
ip netns exec pzwifiap ss -H -ltn | awk '{print $4}' | grep -Fx "198.51.100.1:18080" >/dev/null

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
    "upstream_root_cidr=${WIFI_UPSTREAM_ROOT_CIDR}" \
    "upstream_ap_cidr=${WIFI_UPSTREAM_AP_CIDR}" \
    "upstream_ap_ip=${WIFI_UPSTREAM_AP_IP}" \
    "dhcp_range=${WIFI_DHCP_RANGE}" \
    "policy_table=${WIFI_POLICY_TABLE}" \
    "policy_priority=${WIFI_POLICY_PRIORITY}" \
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

assert_public_artifact_privacy() {
  local extra
  extra="$(find "${E2E_ARTIFACT_DIR}" -mindepth 1 -maxdepth 1 ! -name 'hosted-vm-wifi-lifecycle.txt' -print -quit)"
  [[ -z "${extra}" ]] || return 1
  [[ -f "${REPORT}" && ! -L "${REPORT}" ]] || return 1
  ! grep -Eiq 'vless://|vmess://|trojan://|ss://|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|198[.]51[.]100[.]' "${REPORT}"
}

cleanup_wifi_fixture() {
  [[ "${HOSTED_VM_GA_READY}" == true ]] || return 0
  hosted_vm_ga_bash "set +e
client_if=\$(cat /var/tmp/podlaz-wifi-client-if 2>/dev/null)
management_if=\$(cat /var/tmp/podlaz-wifi-management-if 2>/dev/null)
management_gateway=\$(cat /var/tmp/podlaz-wifi-management-gateway 2>/dev/null)
[[ -z \"\$client_if\" ]] || nmcli connection down '${WIFI_CONNECTION}' >/dev/null 2>&1
ip rule del priority '${WIFI_POLICY_PRIORITY}' from '${WIFI_UPSTREAM_AP_IP}/32' table '${WIFI_POLICY_TABLE}' >/dev/null 2>&1
ip route flush table '${WIFI_POLICY_TABLE}' >/dev/null 2>&1
[[ -z \"\$management_if\" || -z \"\$management_gateway\" ]] || ip route replace default via \"\$management_gateway\" dev \"\$management_if\"
[[ -z \"\$management_if\" ]] || iptables -t nat -D POSTROUTING -s 172.31.254.0/30 -o \"\$management_if\" -j MASQUERADE >/dev/null 2>&1
[[ -z \"\$management_if\" ]] || iptables -D FORWARD -i pzwifi-root -o \"\$management_if\" -j ACCEPT >/dev/null 2>&1
[[ -z \"\$management_if\" ]] || iptables -D FORWARD -i \"\$management_if\" -o pzwifi-root -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT >/dev/null 2>&1
ip netns del pzwifiap >/dev/null 2>&1
ip link del pzwifi-root >/dev/null 2>&1
rm -f /var/tmp/podlaz-wifi-*.pid /var/tmp/podlaz-wifi-hostapd.conf /var/tmp/podlaz-wifi-server.json /var/tmp/podlaz-wifi-client-if /var/tmp/podlaz-wifi-management-if /var/tmp/podlaz-wifi-management-gateway
exit 0" >/dev/null 2>&1 || true
}

cleanup() {
  local saved=$? cleanup_failed=0
  trap - EXIT
  set +e
  hosted_vm_tun_remove_foreign_state || cleanup_failed=1
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
  prepare_endpoint_material

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
  hosted_vm_tun_prepare_control
  hosted_vm_tun_install_helpers "${REPO_ROOT}"
  hosted_vm_tun_install_polkit "${PRIVATE_ROOT}/polkit.rules"
  install_wifi_fixture_files

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
  hosted_vm_tun_connect
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
