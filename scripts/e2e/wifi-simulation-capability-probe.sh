#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"
# shellcheck source=lib/hosted_vm.sh
source "${SCRIPT_DIR}/lib/hosted_vm.sh"

PRIVATE_ROOT="${E2E_TMP_ROOT}/wifi-simulation-capability"
VM_ROOT="${PRIVATE_ROOT}/vm"
REPORT="${E2E_ARTIFACT_DIR}/wifi-simulation-capability.txt"
FINALIZED=false
PROBE_RESULT=fail

finalize_report() {
  [[ "${FINALIZED}" == false ]] || return 0
  FINALIZED=true
  {
    printf 'wifi.simulation=%s\n' "${PROBE_RESULT}"
    printf 'capability.kvm=%s\n' "$([[ "${HOSTED_VM_ACCEL}" == kvm ]] && printf pass || printf unavailable)"
  } >"${REPORT}"
  chmod 0600 "${REPORT}"
}

cleanup() {
  local saved=$?
  trap - EXIT
  set +e
  hosted_vm_stop >/dev/null 2>&1 || true
  finalize_report
  set -e
  exit "${saved}"
}

run_probe() {
  hosted_vm_probe_acceleration
  hosted_vm_prepare_image
  hosted_vm_start
  hosted_vm_wait_ssh 180
  hosted_vm_wait_cloud_init

  hosted_vm_ssh sudo bash -s <<'GUEST'
set -Eeuo pipefail
export DEBIAN_FRONTEND=noninteractive

apt-get update -qq
apt-get install -y -qq --no-install-recommends   dnsmasq-base   hostapd   iw   network-manager \
  wpasupplicant

if ! modprobe mac80211_hwsim radios=2; then
  apt-get install -y -qq --no-install-recommends "linux-modules-extra-$(uname -r)"
  modprobe mac80211_hwsim radios=2
fi

systemctl enable --now NetworkManager.service
systemctl stop hostapd.service >/dev/null 2>&1 || true
nmcli radio wifi on

mapfile -t wifi_ifaces < <(iw dev | awk '$1 == "Interface" {print $2}')
(("${#wifi_ifaces[@]}" >= 2))
ap_if="${wifi_ifaces[0]}"
client_if="${wifi_ifaces[1]}"

management_if="$(ip -4 route show default | awk 'NR == 1 {for (i=1; i<=NF; i++) if ($i == "dev") {print $(i+1); exit}}')"
[[ -n "${management_if}" && "${management_if}" != "${ap_if}" && "${management_if}" != "${client_if}" ]]

nmcli device set "${ap_if}" managed no
nmcli device set "${client_if}" managed yes
ip link set dev "${ap_if}" up
ip address add 192.0.2.1/24 dev "${ap_if}"
sysctl -q -w net.ipv4.ip_forward=1
iptables -t nat -A POSTROUTING -s 192.0.2.0/24 -o "${management_if}" -j MASQUERADE
iptables -A FORWARD -i "${ap_if}" -o "${management_if}" -j ACCEPT
iptables -A FORWARD -i "${management_if}" -o "${ap_if}" -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT

cat >/tmp/pzwifi-hostapd.conf <<EOF
interface=${ap_if}
driver=nl80211
ssid=podlaz-ci-wifi
hw_mode=g
channel=1
auth_algs=1
wpa=2
wpa_passphrase=podlaz-ci-passphrase
wpa_key_mgmt=WPA-PSK
rsn_pairwise=CCMP
EOF

ip netns exec pzwifiap hostapd -B -P /run/pzwifi-hostapd.pid /tmp/pzwifi-hostapd.conf
ip netns exec pzwifiap dnsmasq   --conf-file=   --interface="${ap_if}"   --bind-interfaces   --dhcp-range=192.0.2.10,192.0.2.20,255.255.255.0,1h   --dhcp-option=3,192.0.2.1   --dhcp-option=6,1.1.1.1   --pid-file=/run/pzwifi-dnsmasq.pid

nmcli device set "${client_if}" managed yes
printf 'wifi-probe: ap=%s client=%s\n' "${ap_if}" "${client_if}"
iw dev
nmcli -f GENERAL.DEVICE,GENERAL.TYPE,GENERAL.STATE device show "${client_if}"
systemctl is-active --quiet wpa_supplicant.service || true
for _ in $(seq 1 30); do
  nmcli device wifi rescan ifname "${client_if}" >/dev/null 2>&1 || true
  if nmcli -t -f SSID device wifi list ifname "${client_if}" | grep -Fx podlaz-ci-wifi >/dev/null; then
    break
  fi
  sleep 1
done
nmcli -f IN-USE,SSID,BSSID,CHAN,SIGNAL device wifi list ifname "${client_if}" || true
nmcli -t -f SSID device wifi list ifname "${client_if}" | grep -Fx podlaz-ci-wifi >/dev/null

nmcli --wait 30 device wifi connect podlaz-ci-wifi   password podlaz-ci-passphrase   ifname "${client_if}"   name podlaz-ci-wifi
nmcli -g GENERAL.CONNECTION device show "${client_if}" | grep -Fx podlaz-ci-wifi >/dev/null
iw dev "${client_if}" link | grep -F 'Connected to ' >/dev/null
ip -4 address show dev "${client_if}" | grep -F '192.0.2.' >/dev/null
ip -4 route show dev "${client_if}" | grep -F 'default via 192.0.2.1' >/dev/null
timeout 30 curl -4 -fsS --interface "${client_if}" -o /dev/null https://example.com/

nmcli --wait 20 connection down podlaz-ci-wifi
! iw dev "${client_if}" link | grep -F 'Connected to ' >/dev/null

nmcli --wait 30 connection up podlaz-ci-wifi ifname "${client_if}"
nmcli -g GENERAL.CONNECTION device show "${client_if}" | grep -Fx podlaz-ci-wifi >/dev/null
iw dev "${client_if}" link | grep -F 'Connected to ' >/dev/null
ip -4 address show dev "${client_if}" | grep -F '192.0.2.' >/dev/null
timeout 30 curl -4 -fsS --interface "${client_if}" -o /dev/null https://example.com/
GUEST

  PROBE_RESULT=pass
}

main() {
  require_cmd bash cloud-localds curl qemu-img qemu-system-x86_64 scp sha256sum ssh ssh-keygen
  install -d -m 0700 "${PRIVATE_ROOT}" "${E2E_ARTIFACT_DIR}"
  hosted_vm_init "${VM_ROOT}"
  trap cleanup EXIT
  run_probe
}

main "$@"
