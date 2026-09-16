#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"
# shellcheck source=lib/private_command.sh
source "${SCRIPT_DIR}/lib/private_command.sh"

: "${PODLAZ_E2E_CAPABILITY_SOURCE_ONLY:=false}"

CAPABILITY_REPORT="${E2E_ARTIFACT_DIR}/hosted-e2e-capability.txt"
CAPABILITY_MACHINE="podlaz-capability"
CAPABILITY_HOST_VETH="pzcap0"
CAPABILITY_GUEST_IF="host0"
CAPABILITY_NFT_FAMILY="inet"
CAPABILITY_NFT_TABLE="pzcap_hosted_e2e"
CAPABILITY_NETWORK_CIDR="172.31.255.0/30"
CAPABILITY_HOST_CIDR="172.31.255.1/30"
CAPABILITY_GUEST_CIDR="172.31.255.2/30"
CAPABILITY_HOST_IP="172.31.255.1"
CAPABILITY_GUEST_IP="172.31.255.2"
CAPABILITY_GUEST_ROOT="${E2E_TMP_ROOT}/system-guest"
CAPABILITY_PRIVATE="${E2E_TMP_ROOT}/hosted-capability-private"
CAPABILITY_XRAY_ROOT="${CAPABILITY_PRIVATE}/synthetic-xray"
CAPABILITY_QEMU_ROOT="${E2E_TMP_ROOT}/qemu"
CAPABILITY_TUN_RULE="/etc/polkit-1/rules.d/49-podlaz-hosted-capability.rules"
CAPABILITY_GUEST_XDG="/home/e2e/.local/share/podlaz-capability-xdg"
CAPABILITY_GUEST_CANDIDATE="/opt/podlaz-candidate.deb"
CAPABILITY_QEMU_IMAGE_URL="https://cloud-images.ubuntu.com/releases/noble/release/ubuntu-24.04-server-cloudimg-amd64.img"
CAPABILITY_QEMU_SUMS_URL="https://cloud-images.ubuntu.com/releases/noble/release/SHA256SUMS"

CAPABILITY_KEYS=(
  outer.baseline
  outer.control_plane.before
  kernel.tun
  kernel.netns
  kernel.route_rule
  kernel.nftables
  guest.bootstrap.prepare
  guest.bootstrap.start
  guest.prepare.debootstrap
  guest.prepare.networking
  guest.prepare.user
  guest.prepare.services
  guest.prepare.candidate
  guest.start.nspawn
  guest.start.control
  guest.start.interface
  guest.start.uplink
  guest.start.services
  guest.start.tun
  guest.start.internet
  guest.systemd
  guest.resolved
  guest.networkmanager
  guest.uplink
  guest.internet.before
  package.installed
  package.runtime_provenance
  authorization.ordinary_user
  authorization.polkit
  proxy.lifecycle
  synthetic.xray_endpoint
  tun.authorization
  tun.synthetic_uri_loaded
  tun.profile_import_usage_error
  tun.profile_import_arg_error
  tun.profile_import_vless_error
  tun.profile_import_profile_validation_error
  tun.profile_import_usage_other
  tun.profile_import_runtime_error
  tun.profile_import_other_error
  tun.profile_import_command
  tun.profile_import_output
  tun.profile_import
  tun.profile_validate
  tun.connect_requested
  tun.verified_active
  tun.system_dns
  tun.https_tls
  tun.doctor
  tun.ipv6
  tun.pmtu
  tun.networkmanager_postcondition
  tun.clean_disconnect
  tun.recovery_clean
  artifact.privacy
  qemu.available
  qemu.kvm_present
  qemu.kvm_usable
  qemu.tcg_usable
  qemu.image_checksum
  qemu.disk_budget
  qemu.boot
  qemu.reboot_boot_id
  outer.control_plane.after
  outer.cleanup
)

declare -A CAPABILITY_SEEN=()
CANDIDATE_DEB=""
EXPECTED_COMMIT="${GITHUB_SHA:-}"
OUTER_DEFAULT_ROUTE_BASELINE=""
OUTER_RULES_BASELINE=""
OUTER_RESOLV_CONF_BASELINE=""
OUTER_IP_FORWARD_BASELINE=""
OUTER_EGRESS_IF=""
NSPAWN_PID=""
SYSTEM_GUEST_ACTIVE=false
SYSTEM_GUEST_PREPARE_STAGE=""
SYSTEM_GUEST_START_STAGE=""
XRAY_PID=""
QEMU_PID=""
QEMU_SSH_PORT=""
QEMU_SSH_KEY=""
TEARDOWN_RUNNING=false

capability_recorded() {
  local key="$1"
  [[ -f "${CAPABILITY_REPORT}" ]] && grep -Eq "^${key}=" "${CAPABILITY_REPORT}"
}

record_capability() {
  local key="${1:-}" state="${2:-}"
  [[ "${key}" =~ ^[A-Za-z0-9_.-]+$ ]] || fail "invalid capability evidence key"
  case "${state}" in
    pass|fail|unavailable|observed) ;;
    *) fail "invalid capability evidence state for ${key}" ;;
  esac
  ! capability_recorded "${key}" || fail "duplicate capability evidence key: ${key}"
  CAPABILITY_SEEN["${key}"]=1
  printf '%s=%s\n' "${key}" "${state}" >>"${CAPABILITY_REPORT}"
}

record_capability_if_missing() {
  local key="$1" state="$2"
  if ! capability_recorded "${key}"; then
    record_capability "${key}" "${state}"
  fi
}

finalize_capability_report() {
  local key
  for key in "${CAPABILITY_KEYS[@]}"; do
    record_capability_if_missing "${key}" unavailable
  done
}

validate_report() {
  require_cmd python3
  python3 - "${CAPABILITY_REPORT}" "${CAPABILITY_KEYS[@]}" <<'PY'
import re
import sys
from pathlib import Path

path = Path(sys.argv[1])
expected = sys.argv[2:]
if not path.is_file():
    raise SystemExit("capability report is missing")
lines = path.read_text(encoding="utf-8").splitlines()
if len(lines) != len(expected):
    raise SystemExit(f"capability report line count mismatch: {len(lines)} != {len(expected)}")
allowed = {"pass", "fail", "unavailable", "observed"}
seen = {}
for line in lines:
    match = re.fullmatch(r"([A-Za-z0-9_.-]+)=(pass|fail|unavailable|observed)", line)
    if not match:
        raise SystemExit("capability report contains non-normalized data")
    key, state = match.groups()
    if key in seen:
        raise SystemExit(f"duplicate capability key: {key}")
    if state not in allowed:
        raise SystemExit(f"invalid capability state: {state}")
    seen[key] = state
if set(seen) != set(expected):
    missing = sorted(set(expected) - set(seen))
    extra = sorted(set(seen) - set(expected))
    raise SystemExit(f"capability report schema mismatch: missing={missing} extra={extra}")
PY
}

assert_capability_subnet_available() {
  python3 - \
    "${CAPABILITY_NETWORK_CIDR}" \
    <(ip -j -4 addr show) \
    <(ip -j -4 route show table all) <<'PY'
import ipaddress
import json
import sys

target = ipaddress.ip_network(sys.argv[1], strict=True)
rfc1918 = (
    ipaddress.ip_network("10.0.0.0/8"),
    ipaddress.ip_network("172.16.0.0/12"),
    ipaddress.ip_network("192.168.0.0/16"),
)
if target.version != 4 or target.prefixlen != 30 or not any(target.subnet_of(network) for network in rfc1918):
    raise SystemExit("capability synthetic subnet must be an RFC1918 IPv4 /30")

with open(sys.argv[2], encoding="utf-8") as handle:
    addresses = json.load(handle)
for link in addresses:
    for info in link.get("addr_info", []):
        local = info.get("local")
        prefixlen = info.get("prefixlen")
        if info.get("family") != "inet" or local is None or prefixlen is None:
            continue
        candidate = ipaddress.ip_network(f"{local}/{prefixlen}", strict=False)
        if candidate.overlaps(target):
            raise SystemExit("capability synthetic subnet overlaps an existing IPv4 address")

with open(sys.argv[3], encoding="utf-8") as handle:
    routes = json.load(handle)
for route in routes:
    destination = route.get("dst")
    if not destination or destination == "default":
        continue
    try:
        candidate = ipaddress.ip_network(destination, strict=False)
    except ValueError:
        continue
    if candidate.overlaps(target):
        raise SystemExit("capability synthetic subnet overlaps an existing IPv4 route")
PY
}

capture_outer_baseline() {
  require_cmd curl ip iptables python3 sha256sum
  install -d -m 0700 "${CAPABILITY_PRIVATE}"
  OUTER_DEFAULT_ROUTE_BASELINE="${CAPABILITY_PRIVATE}/outer-default-route.json"
  OUTER_RULES_BASELINE="${CAPABILITY_PRIVATE}/outer-rules.json"
  OUTER_RESOLV_CONF_BASELINE="${CAPABILITY_PRIVATE}/outer-resolv.sha256"
  OUTER_IP_FORWARD_BASELINE="$(cat /proc/sys/net/ipv4/ip_forward)"
  ip -4 -j route show default >"${OUTER_DEFAULT_ROUTE_BASELINE}"
  ip -4 -j rule show >"${OUTER_RULES_BASELINE}"
  sha256sum /etc/resolv.conf | awk '{print $1}' >"${OUTER_RESOLV_CONF_BASELINE}"
  OUTER_EGRESS_IF="$(ip -4 route show default | awk 'NR == 1 {for (i=1; i<=NF; i++) if ($i == "dev") {print $(i+1); exit}}')"
  [[ -n "${OUTER_EGRESS_IF}" ]] || return 1
  ! ip link show dev "${CAPABILITY_HOST_VETH}" >/dev/null 2>&1 || return 1
  ! sudo -n nft list table "${CAPABILITY_NFT_FAMILY}" "${CAPABILITY_NFT_TABLE}" >/dev/null 2>&1 || return 1
  sudo -n iptables -S DOCKER-USER >/dev/null 2>&1 || return 1
  assert_capability_subnet_available
  record_capability outer.baseline pass
}

assert_outer_control_plane_healthy() {
  local phase="$1" current
  timeout 25 curl -4 -fsS --retry 3 --retry-max-time 20 --max-time 8 -o /dev/null https://github.com/ || return 1
  cmp -s "${OUTER_DEFAULT_ROUTE_BASELINE}" <(ip -4 -j route show default) || return 1
  cmp -s "${OUTER_RULES_BASELINE}" <(ip -4 -j rule show) || return 1
  current="$(sha256sum /etc/resolv.conf | awk '{print $1}')"
  [[ "${current}" == "$(cat "${OUTER_RESOLV_CONF_BASELINE}")" ]] || return 1
  if [[ "${phase}" == after ]]; then
    [[ "$(cat /proc/sys/net/ipv4/ip_forward)" == "${OUTER_IP_FORWARD_BASELINE}" ]] || return 1
    ! ip link show dev "${CAPABILITY_HOST_VETH}" >/dev/null 2>&1 || return 1
    ! sudo -n nft list table "${CAPABILITY_NFT_FAMILY}" "${CAPABILITY_NFT_TABLE}" >/dev/null 2>&1 || return 1
    ! sudo -n iptables -S DOCKER-USER | grep -F 'podlaz-hosted-e2e-forward-' >/dev/null || return 1
  fi
}

cleanup_outer_plumbing() {
  set +e
  sudo -n iptables -D DOCKER-USER -i "${OUTER_EGRESS_IF}" -o "${CAPABILITY_HOST_VETH}" -d "${CAPABILITY_NETWORK_CIDR}" -m conntrack --ctstate ESTABLISHED,RELATED -m comment --comment podlaz-hosted-e2e-forward-in -j ACCEPT >/dev/null 2>&1 || true
  sudo -n iptables -D DOCKER-USER -i "${CAPABILITY_HOST_VETH}" -o "${OUTER_EGRESS_IF}" -s "${CAPABILITY_NETWORK_CIDR}" -m comment --comment podlaz-hosted-e2e-forward-out -j ACCEPT >/dev/null 2>&1 || true
  sudo -n nft delete table "${CAPABILITY_NFT_FAMILY}" "${CAPABILITY_NFT_TABLE}" >/dev/null 2>&1 || true
  sudo -n ip link del dev "${CAPABILITY_HOST_VETH}" >/dev/null 2>&1 || true
  if [[ -n "${OUTER_IP_FORWARD_BASELINE}" ]]; then
    printf '%s\n' "${OUTER_IP_FORWARD_BASELINE}" | sudo -n tee /proc/sys/net/ipv4/ip_forward >/dev/null || return 1
  fi
  set -e
}

stop_synthetic_xray_endpoint() {
  if [[ -n "${XRAY_PID}" ]]; then
    kill "${XRAY_PID}" >/dev/null 2>&1 || true
    wait "${XRAY_PID}" >/dev/null 2>&1 || true
    XRAY_PID=""
  fi
}

stop_qemu_guest() {
  if [[ -n "${QEMU_PID}" ]]; then
    kill "${QEMU_PID}" >/dev/null 2>&1 || true
    for _ in $(seq 1 50); do
      kill -0 "${QEMU_PID}" >/dev/null 2>&1 || break
      sleep 0.1
    done
    kill -9 "${QEMU_PID}" >/dev/null 2>&1 || true
    wait "${QEMU_PID}" >/dev/null 2>&1 || true
    QEMU_PID=""
  fi
}

stop_system_guest() {
  if [[ "${SYSTEM_GUEST_ACTIVE}" == true ]]; then
    sudo -n machinectl terminate "${CAPABILITY_MACHINE}" >/dev/null 2>&1 || true
    for _ in $(seq 1 100); do
      ! machinectl show "${CAPABILITY_MACHINE}" >/dev/null 2>&1 && break
      sleep 0.1
    done
    SYSTEM_GUEST_ACTIVE=false
  fi
  if [[ -n "${NSPAWN_PID}" ]]; then
    kill "${NSPAWN_PID}" >/dev/null 2>&1 || true
    wait "${NSPAWN_PID}" >/dev/null 2>&1 || true
    NSPAWN_PID=""
  fi
}

teardown_all() {
  local saved=$? failed=0
  [[ "${TEARDOWN_RUNNING}" == false ]] || return
  TEARDOWN_RUNNING=true
  trap - EXIT
  set +e
  stop_synthetic_xray_endpoint || failed=1
  stop_qemu_guest || failed=1
  stop_system_guest || failed=1
  cleanup_outer_plumbing || failed=1
  if [[ -n "${OUTER_DEFAULT_ROUTE_BASELINE}" ]]; then
    if assert_outer_control_plane_healthy after; then
      record_capability_if_missing outer.control_plane.after pass
    else
      record_capability_if_missing outer.control_plane.after fail
      failed=1
    fi
  fi
  if [[ "${failed}" == 0 ]]; then
    record_capability_if_missing outer.cleanup pass
  else
    record_capability_if_missing outer.cleanup fail
  fi
  finalize_capability_report
  validate_report || failed=1
  set -e
  if [[ "${saved}" == 0 && "${failed}" != 0 ]]; then
    saved=1
  fi
  exit "${saved}"
}

probe_hosted_kernel_primitives() {
  local ns="pzcap-probe"
  sudo -n ip netns del "${ns}" >/dev/null 2>&1 || true
  sudo -n ip netns add "${ns}"
  if sudo -n ip netns exec "${ns}" ip tuntap add dev pzcap-tun mode tun && \
      sudo -n ip netns exec "${ns}" ip link set dev pzcap-tun up && \
      sudo -n ip netns exec "${ns}" test -c /dev/net/tun; then
    record_capability kernel.tun pass
    record_capability kernel.netns pass
  else
    record_capability_if_missing kernel.tun fail
    sudo -n ip netns del "${ns}" >/dev/null 2>&1 || true
    return 1
  fi

  if sudo -n ip netns exec "${ns}" ip -4 route add blackhole 198.51.100.0/24 table 4242 && \
      sudo -n ip netns exec "${ns}" ip -4 rule add priority 4242 to 198.51.100.0/24 table 4242 && \
      sudo -n ip netns exec "${ns}" ip -4 route show table 4242 | grep -F 'blackhole 198.51.100.0/24' >/dev/null && \
      sudo -n ip netns exec "${ns}" ip -4 rule show priority 4242 | grep -F 'lookup 4242' >/dev/null; then
    record_capability kernel.route_rule pass
  else
    record_capability kernel.route_rule fail
    sudo -n ip netns del "${ns}" >/dev/null 2>&1 || true
    return 1
  fi

  if sudo -n ip netns exec "${ns}" nft add table inet pzcap_probe && \
      sudo -n ip netns exec "${ns}" nft list table inet pzcap_probe >/dev/null && \
      sudo -n ip netns exec "${ns}" nft delete table inet pzcap_probe; then
    record_capability kernel.nftables pass
  else
    record_capability kernel.nftables fail
    sudo -n ip netns del "${ns}" >/dev/null 2>&1 || true
    return 1
  fi
  sudo -n ip netns del "${ns}"
}

write_system_guest_files() {
  local nm_tmp sudoers_tmp

  SYSTEM_GUEST_PREPARE_STAGE=networking
  nm_tmp="$(mktemp "${CAPABILITY_PRIVATE}/nm.XXXXXX")"
  cat >"${nm_tmp}" <<EOF
[connection]
id=capability-uplink
type=ethernet
interface-name=${CAPABILITY_GUEST_IF}
autoconnect=true

[ipv4]
method=manual
address1=${CAPABILITY_GUEST_CIDR},${CAPABILITY_HOST_IP}
dns=1.1.1.1;1.0.0.1;
never-default=false

[ipv6]
method=disabled
EOF
  sudo -n install -D -m 0600 "${nm_tmp}" "${CAPABILITY_GUEST_ROOT}/etc/NetworkManager/system-connections/capability-uplink.nmconnection"
  rm -f -- "${nm_tmp}"
  sudo -n mkdir -p "${CAPABILITY_GUEST_ROOT}/etc/NetworkManager/conf.d"
  sudo -n install -D -m 0644 /dev/null "${CAPABILITY_GUEST_ROOT}/etc/NetworkManager/conf.d/10-globally-managed-devices.conf"
  printf '[main]\ndns=systemd-resolved\n' | sudo -n tee "${CAPABILITY_GUEST_ROOT}/etc/NetworkManager/conf.d/10-capability-dns.conf" >/dev/null
  cat <<EOF | sudo -n tee "${CAPABILITY_GUEST_ROOT}/etc/NetworkManager/conf.d/20-capability-uplink.conf" >/dev/null
[device-capability-uplink]
match-device=interface-name:=${CAPABILITY_GUEST_IF}
managed=1
EOF
  sudo -n rm -f "${CAPABILITY_GUEST_ROOT}/etc/resolv.conf"
  sudo -n ln -s /run/systemd/resolve/stub-resolv.conf "${CAPABILITY_GUEST_ROOT}/etc/resolv.conf"
  record_capability guest.prepare.networking pass

  SYSTEM_GUEST_PREPARE_STAGE=user
  sudo -n chroot "${CAPABILITY_GUEST_ROOT}" useradd -m -s /bin/bash e2e
  sudoers_tmp="$(mktemp "${CAPABILITY_PRIVATE}/sudoers.XXXXXX")"
  printf 'e2e ALL=(ALL) NOPASSWD: ALL\n' >"${sudoers_tmp}"
  sudo -n install -D -m 0440 "${sudoers_tmp}" "${CAPABILITY_GUEST_ROOT}/etc/sudoers.d/e2e-capability"
  rm -f -- "${sudoers_tmp}"
  record_capability guest.prepare.user pass

  SYSTEM_GUEST_PREPARE_STAGE=services
  sudo -n mkdir -p "${CAPABILITY_GUEST_ROOT}/workspace" "${CAPABILITY_GUEST_ROOT}/opt"
  sudo -n systemctl --root="${CAPABILITY_GUEST_ROOT}" enable NetworkManager.service systemd-resolved.service >/dev/null
  record_capability guest.prepare.services pass

  SYSTEM_GUEST_PREPARE_STAGE=candidate
  sudo -n touch "${CAPABILITY_GUEST_ROOT}${CAPABILITY_GUEST_CANDIDATE}"
  sudo -n chmod 0644 "${CAPABILITY_GUEST_ROOT}${CAPABILITY_GUEST_CANDIDATE}"
  [[ -r "${CANDIDATE_DEB}" ]] || return 1
  record_capability guest.prepare.candidate pass
  SYSTEM_GUEST_PREPARE_STAGE=""
}

prepare_system_guest() {
  local policy_rc="${CAPABILITY_GUEST_ROOT}/usr/sbin/policy-rc.d"
  require_cmd debootstrap systemd-nspawn machinectl systemd-run
  sudo -n rm -rf "${CAPABILITY_GUEST_ROOT}"

  SYSTEM_GUEST_PREPARE_STAGE=debootstrap
  sudo -n debootstrap --variant=minbase noble "${CAPABILITY_GUEST_ROOT}" http://archive.ubuntu.com/ubuntu >"${CAPABILITY_PRIVATE}/debootstrap.log" 2>&1
  printf '#!/bin/sh\nexit 101\n' | sudo -n tee "${policy_rc}" >/dev/null
  sudo -n chmod 0755 "${policy_rc}"
  sudo -n chroot "${CAPABILITY_GUEST_ROOT}" /usr/bin/env DEBIAN_FRONTEND=noninteractive apt-get update >"${CAPABILITY_PRIVATE}/guest-apt.log" 2>&1
  sudo -n chroot "${CAPABILITY_GUEST_ROOT}" /usr/bin/env DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    systemd systemd-sysv dbus ca-certificates sudo iproute2 nftables curl python3 gawk grep sed procps util-linux iputils-ping \
    network-manager systemd-resolved polkitd jq openssl git >>"${CAPABILITY_PRIVATE}/guest-apt.log" 2>&1
  sudo -n rm -f "${policy_rc}"
  sudo -n chroot "${CAPABILITY_GUEST_ROOT}" apt-get clean >/dev/null 2>&1
  record_capability guest.prepare.debootstrap pass
  write_system_guest_files
}

setup_outer_plumbing() {
  for _ in $(seq 1 100); do
    ip link show dev "${CAPABILITY_HOST_VETH}" >/dev/null 2>&1 && break
    sleep 0.1
  done
  ip link show dev "${CAPABILITY_HOST_VETH}" >/dev/null 2>&1 || return 1
  sudo -n ip addr add "${CAPABILITY_HOST_CIDR}" dev "${CAPABILITY_HOST_VETH}"
  sudo -n ip link set dev "${CAPABILITY_HOST_VETH}" up
  printf '1\n' | sudo -n tee /proc/sys/net/ipv4/ip_forward >/dev/null
  sudo -n iptables -I DOCKER-USER 1 -i "${CAPABILITY_HOST_VETH}" -o "${OUTER_EGRESS_IF}" -s "${CAPABILITY_NETWORK_CIDR}" -m comment --comment podlaz-hosted-e2e-forward-out -j ACCEPT
  sudo -n iptables -I DOCKER-USER 1 -i "${OUTER_EGRESS_IF}" -o "${CAPABILITY_HOST_VETH}" -d "${CAPABILITY_NETWORK_CIDR}" -m conntrack --ctstate ESTABLISHED,RELATED -m comment --comment podlaz-hosted-e2e-forward-in -j ACCEPT
  sudo -n nft add table "${CAPABILITY_NFT_FAMILY}" "${CAPABILITY_NFT_TABLE}"
  sudo -n nft "add chain ${CAPABILITY_NFT_FAMILY} ${CAPABILITY_NFT_TABLE} postrouting { type nat hook postrouting priority srcnat; policy accept; }"
  sudo -n nft add rule "${CAPABILITY_NFT_FAMILY}" "${CAPABILITY_NFT_TABLE}" postrouting ip saddr "${CAPABILITY_NETWORK_CIDR}" oifname "${OUTER_EGRESS_IF}" masquerade
}

guest_exec() {
  sudo -n systemd-run --machine="${CAPABILITY_MACHINE}" --expand-environment=no --wait --pipe --collect --quiet -- "$@"
}

start_system_guest() {
  local nspawn_log="${CAPABILITY_PRIVATE}/nspawn.log" systemd_state
  SYSTEM_GUEST_START_STAGE=nspawn
  sudo -n systemd-nspawn \
    --quiet --boot \
    --directory="${CAPABILITY_GUEST_ROOT}" \
    --machine="${CAPABILITY_MACHINE}" \
    --settings=no --private-network \
    --network-veth-extra="${CAPABILITY_HOST_VETH}:${CAPABILITY_GUEST_IF}" \
    --bind-ro="${REPO_ROOT}:/workspace" \
    --bind-ro="${CAPABILITY_XRAY_ROOT}:/run/podlaz-capability-xray" \
    --bind-ro="${CANDIDATE_DEB}:${CAPABILITY_GUEST_CANDIDATE}" \
    --bind=/dev/net/tun \
    --capability=CAP_NET_ADMIN,CAP_NET_RAW \
    --link-journal=no >"${nspawn_log}" 2>&1 &
  NSPAWN_PID=$!
  SYSTEM_GUEST_ACTIVE=true
  setup_outer_plumbing
  kill -0 "${NSPAWN_PID}" >/dev/null 2>&1
  record_capability guest.start.nspawn pass

  SYSTEM_GUEST_START_STAGE=control
  for _ in $(seq 1 200); do
    if machinectl show "${CAPABILITY_MACHINE}" >/dev/null 2>&1 && guest_exec /bin/true >/dev/null 2>&1; then
      break
    fi
    sleep 0.2
  done
  guest_exec /bin/true >/dev/null 2>&1 || return 1
  record_capability guest.start.control pass
  systemd_state="$(guest_exec /bin/bash -lc 'timeout 30 systemctl is-system-running --wait 2>/dev/null || true' | tr -d '[:space:]')"
  case "${systemd_state}" in
    running|degraded) record_capability guest.systemd pass ;;
    *) record_capability guest.systemd fail; return 1 ;;
  esac

  SYSTEM_GUEST_START_STAGE=interface
  guest_exec ip -o link show dev "${CAPABILITY_GUEST_IF}" >/dev/null
  record_capability guest.start.interface pass

  SYSTEM_GUEST_START_STAGE=uplink
  guest_exec nmcli connection reload
  guest_exec nmcli connection up capability-uplink >/dev/null
  if guest_exec nmcli -t -f NAME,DEVICE connection show --active | grep -Fx "capability-uplink:${CAPABILITY_GUEST_IF}" >/dev/null; then
    record_capability guest.uplink pass
    record_capability guest.start.uplink pass
  else
    record_capability guest.uplink fail
    return 1
  fi

  SYSTEM_GUEST_START_STAGE=services
  guest_exec systemctl is-active --quiet systemd-resolved.service
  guest_exec systemctl is-active --quiet NetworkManager.service
  record_capability guest.resolved pass
  record_capability guest.networkmanager pass
  record_capability guest.start.services pass

  SYSTEM_GUEST_START_STAGE=tun
  guest_exec test -c /dev/net/tun
  guest_exec ip tuntap add dev pzcap-guest-tun mode tun
  guest_exec ip link del dev pzcap-guest-tun
  record_capability guest.start.tun pass

  SYSTEM_GUEST_START_STAGE=internet
  guest_exec timeout 5 ping -4 -c 1 -W 2 "${CAPABILITY_HOST_IP}" >/dev/null
  guest_exec timeout 10 curl -4 -k -fsS --connect-timeout 5 -o /dev/null https://1.1.1.1/
  guest_exec timeout 20 getent ahostsv4 example.com >/dev/null
  guest_exec timeout 30 curl -4 -fsS -o /dev/null https://example.com/
  record_capability guest.internet.before pass
  record_capability guest.start.internet pass
  SYSTEM_GUEST_START_STAGE=""
}

install_candidate_in_guest() {
  guest_exec test -r "${CAPABILITY_GUEST_CANDIDATE}"
  guest_exec /bin/bash -lc "DEBIAN_FRONTEND=noninteractive apt-get install -y '${CAPABILITY_GUEST_CANDIDATE}' >/run/podlaz-capability-apt.log 2>&1"
  guest_exec systemctl daemon-reload
  guest_exec systemctl reset-failed podlazd.service >/dev/null 2>&1 || true
  guest_exec systemctl start podlazd.service
  guest_exec systemctl is-active --quiet podlazd.service
  record_capability package.installed pass
}

assert_guest_package_provenance() {
  [[ -n "${EXPECTED_COMMIT}" ]] || return 1
  guest_exec /bin/bash -lc "cd /workspace && source scripts/e2e/lib/e2e.sh && source scripts/e2e/lib/package_provenance.sh && assert_exact_podlaz_package_runtime_provenance '${CAPABILITY_GUEST_CANDIDATE}' '${EXPECTED_COMMIT}'"
  record_capability package.runtime_provenance pass
}

run_guest_ordinary_user_acceptance() {
  guest_exec install -d -o e2e -g e2e -m 0700 /tmp/podlaz-capability-user /tmp/podlaz-capability-user/private /tmp/podlaz-capability-user/artifacts
  guest_exec runuser -u e2e -- env \
    RUNNER_TEMP=/tmp/podlaz-capability-user \
    E2E_TMP_ROOT=/tmp/podlaz-capability-user/private \
    E2E_ARTIFACT_DIR=/tmp/podlaz-capability-user/artifacts \
    bash /workspace/scripts/e2e/installed-user-lifecycle-acceptance.sh
  record_capability authorization.ordinary_user pass
  record_capability authorization.polkit pass
  record_capability proxy.lifecycle pass
}

start_synthetic_xray_endpoint() {
  local extract="${CAPABILITY_XRAY_ROOT}/package" config="${CAPABILITY_XRAY_ROOT}/server.json" uuid port
  install -d -m 0700 "${CAPABILITY_XRAY_ROOT}" "${extract}"
  dpkg-deb -x "${CANDIDATE_DEB}" "${extract}"
  uuid="$("${extract}/usr/lib/podlaz/xray" uuid | tr -d '[:space:]')"
  [[ "${uuid}" =~ ^[0-9a-fA-F-]{36}$ ]] || return 1
  port="$(python3 - "${CAPABILITY_HOST_IP}" <<'PY'
import socket,sys
sock=socket.socket(); sock.bind((sys.argv[1],0)); print(sock.getsockname()[1]); sock.close()
PY
)"
  cat >"${config}" <<EOF
{
  "log": {"loglevel": "info"},
  "inbounds": [{
    "listen": "${CAPABILITY_HOST_IP}",
    "port": ${port},
    "protocol": "vless",
    "settings": {"clients": [{"id": "${uuid}"}], "decryption": "none"},
    "streamSettings": {"security": "none"}
  }],
  "outbounds": [{"protocol": "freedom", "settings": {}}]
}
EOF
  chmod 0600 "${config}"
  "${extract}/usr/lib/podlaz/xray" run -test -config "${config}" >"${CAPABILITY_XRAY_ROOT}/config-test.log" 2>&1
  "${extract}/usr/lib/podlaz/xray" run -config "${config}" >"${CAPABILITY_XRAY_ROOT}/server.log" 2>&1 &
  XRAY_PID=$!
  for _ in $(seq 1 100); do
    if ss -H -ltn | awk '{print $4}' | grep -Fx "${CAPABILITY_HOST_IP}:${port}" >/dev/null; then
      break
    fi
    kill -0 "${XRAY_PID}" >/dev/null 2>&1 || return 1
    sleep 0.1
  done
  ss -H -ltn | awk '{print $4}' | grep -Fx "${CAPABILITY_HOST_IP}:${port}" >/dev/null || return 1
  printf 'vless://%s@%s:%s?type=tcp&security=none&encryption=none#hosted-capability\n' "${uuid}" "${CAPABILITY_HOST_IP}" "${port}" >"${CAPABILITY_XRAY_ROOT}/client-uri"
  chmod 0600 "${CAPABILITY_XRAY_ROOT}/client-uri"
  record_capability synthetic.xray_endpoint pass
}

install_tun_ci_authorization() {
  local rule_tmp
  rule_tmp="$(mktemp "${CAPABILITY_PRIVATE}/tun-polkit.XXXXXX")"
  cat >"${rule_tmp}" <<'EOF'
polkit.addRule(function(action, subject) {
    if (subject.user == "e2e" &&
        (action.id == "io.github.aidarkhusainov.podlaz.connect-tun" ||
         action.id == "io.github.aidarkhusainov.podlaz.disconnect")) {
        return polkit.Result.YES;
    }
});
EOF
  sudo -n install -D -m 0644 "${rule_tmp}" "${CAPABILITY_GUEST_ROOT}${CAPABILITY_TUN_RULE}"
  rm -f -- "${rule_tmp}"
  sleep 1
}

run_guest_user() {
  guest_exec runuser -u e2e -- env \
    XDG_CONFIG_HOME="${CAPABILITY_GUEST_XDG}/config" \
    XDG_STATE_HOME="${CAPABILITY_GUEST_XDG}/state" \
    XDG_CACHE_HOME="${CAPABILITY_GUEST_XDG}/cache" \
    "$@"
}

wait_guest_tun_status() {
  local mode="$1" attempts="$2" helper="/workspace/scripts/e2e/lib/daemon_status_semantics.py"
  for _ in $(seq 1 "${attempts}"); do
    if guest_exec /bin/bash -lc "curl --fail --silent --show-error --max-time 5 --unix-socket /run/podlaz/podlazd.sock http://localhost/v1/status >/run/podlaz-capability-status.json 2>/dev/null && python3 '${helper}' '${mode}' /run/podlaz-capability-status.json" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  return 1
}

wait_guest_synthetic_uri() {
  for _ in $(seq 1 50); do
    guest_exec test -s /run/podlaz-capability-xray/client-uri >/dev/null 2>&1 && return 0
    sleep 0.1
  done
  return 1
}

capture_guest_network_snapshot() {
  local prefix="$1"
  guest_exec ip -4 -j addr show | jq -S . >"${prefix}.addr.json"
  guest_exec ip -4 -j route show table all | jq -S . >"${prefix}.routes.json"
  guest_exec ip -4 -j rule show | jq -S . >"${prefix}.rules.json"
  guest_exec nft -j list ruleset | jq -S . >"${prefix}.nft.json"
  guest_exec /bin/bash -lc 'nmcli -t -f NAME,UUID,TYPE,DEVICE connection show --active | LC_ALL=C sort' >"${prefix}.nm.txt"
  guest_exec /bin/bash -lc '{ resolvectl dns; resolvectl domain; resolvectl default-route; }' >"${prefix}.resolved.txt"
  chmod 0600 "${prefix}."*
}

capture_guest_network_baseline() {
  capture_guest_network_snapshot "${CAPABILITY_PRIVATE}/guest-baseline"
}

assert_guest_network_baseline_restored() {
  local before="${CAPABILITY_PRIVATE}/guest-baseline" after="${CAPABILITY_PRIVATE}/guest-after"
  capture_guest_network_snapshot "${after}"
  local suffix
  for suffix in addr.json routes.json rules.json nft.json nm.txt resolved.txt; do
    cmp -s "${before}.${suffix}" "${after}.${suffix}" || return 1
  done
}

assert_tun_authorization_boundary() {
  local code
  guest_exec /bin/bash -lc '! id -nG e2e | tr " " "\n" | grep -Fx podlaz >/dev/null'
  # shellcheck disable=SC2016 -- command substitution intentionally runs inside the guest shell.
  guest_exec /bin/bash -lc '[[ "$(stat -c "%U:%G:%a" /run/podlaz/podlazd.sock)" == "root:podlaz:660" ]]'
  guest_exec runuser -u e2e -- python3 -c 'import errno,socket; s=socket.socket(socket.AF_UNIX,socket.SOCK_STREAM); ok=False
try:
 s.connect("/run/podlaz/podlazd.sock")
except OSError as exc:
 ok=exc.errno in (errno.EACCES,errno.EPERM)
finally:
 s.close()
raise SystemExit(0 if ok else 1)'

  set +e
  guest_exec /bin/bash -lc "id=\$(cat /tmp/podlaz-capability-tun-private/profile-id); runuser -u e2e -- env XDG_CONFIG_HOME='${CAPABILITY_GUEST_XDG}/config' XDG_STATE_HOME='${CAPABILITY_GUEST_XDG}/state' XDG_CACHE_HOME='${CAPABILITY_GUEST_XDG}/cache' /usr/bin/podlaz connect --mode proxy-only \"\${id}\" >/tmp/podlaz-capability-tun-private/unauthorized.stdout 2>/tmp/podlaz-capability-tun-private/unauthorized.stderr"
  code=$?
  set -e
  [[ "${code}" == 1 ]] || return 1
  guest_exec grep -F 'authorization denied' /tmp/podlaz-capability-tun-private/unauthorized.stderr >/dev/null
  wait_guest_tun_status clean-inactive 20
}

assert_guest_terminal_authority_clean() {
  ! guest_exec ip link show dev podlaz0 >/dev/null 2>&1 || return 1
  guest_exec test ! -e /run/podlaz/generated/xray.json
  guest_exec test ! -e /run/podlaz/network-session-continuation.json
  # shellcheck disable=SC2016 -- command substitution intentionally runs inside the guest shell.
  guest_exec /bin/bash -lc 'test ! -d /run/podlaz/transactions || test -z "$(find /run/podlaz/transactions -mindepth 1 -maxdepth 1 -type f -print -quit)"'
  ! guest_exec nft list table inet podlaz >/dev/null 2>&1 || return 1
  ! guest_exec /bin/bash -lc "nft list tables | grep -E 'table inet podlaz_pe_[0-9a-f]+'" >/dev/null 2>&1 || return 1
  ! guest_exec nmcli -t -f NAME,DEVICE connection show --active | grep -F ':podlaz0' >/dev/null || return 1
  # shellcheck disable=SC2016 -- substitutions intentionally run inside the guest shell.
  guest_exec /bin/bash -lc 'daemon="$(systemctl show -p MainPID --value podlazd.service)"; for pid in $(pgrep -P "${daemon}" 2>/dev/null || true); do [[ "$(readlink -f "/proc/${pid}/exe" 2>/dev/null || true)" != /usr/lib/podlaz/xray ]] || exit 1; done'
}

assert_public_artifact_privacy() {
  local extra
  extra="$(find "${E2E_ARTIFACT_DIR}" -mindepth 1 -maxdepth 1 ! -name 'hosted-e2e-capability.txt' -print -quit)"
  [[ -z "${extra}" ]] || return 1
  ! grep -Eq 'vless://|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}' "${CAPABILITY_REPORT}" || return 1
}

run_synthetic_tun_lifecycle() {
  local import_code connect_code doctor_code
  guest_exec install -d -o e2e -g e2e -m 0700 \
    "${CAPABILITY_GUEST_XDG}" "${CAPABILITY_GUEST_XDG}/config" "${CAPABILITY_GUEST_XDG}/state" "${CAPABILITY_GUEST_XDG}/cache" /tmp/podlaz-capability-tun-private
  if ! wait_guest_synthetic_uri; then
    record_capability tun.synthetic_uri_loaded fail
    return 1
  fi

  set +e
  guest_exec /bin/bash -lc "URI=\"\$(cat /run/podlaz-capability-xray/client-uri)\"; [[ -n \"\${URI}\" ]] || exit 90; runuser -u e2e -- env XDG_CONFIG_HOME='${CAPABILITY_GUEST_XDG}/config' XDG_STATE_HOME='${CAPABILITY_GUEST_XDG}/state' XDG_CACHE_HOME='${CAPABILITY_GUEST_XDG}/cache' /usr/bin/podlaz profile import \"\${URI}\" >/tmp/podlaz-capability-tun-private/import.stdout 2>/tmp/podlaz-capability-tun-private/import.stderr"
  import_code=$?
  set -e
  if (( import_code == 90 )); then
    record_capability tun.synthetic_uri_loaded fail
  else
    record_capability tun.synthetic_uri_loaded pass
  fi
  if (( import_code != 0 )); then
    case "${import_code}" in
      2) record_capability tun.profile_import_usage_error observed ;;
      1) record_capability tun.profile_import_runtime_error observed ;;
      *) record_capability tun.profile_import_other_error observed ;;
    esac
    return 1
  fi
  record_capability tun.profile_import_command pass
  guest_exec chown -R e2e:e2e /tmp/podlaz-capability-tun-private
  guest_exec /bin/bash -lc "awk '/^Imported profile:/ {print \$3; exit}' /tmp/podlaz-capability-tun-private/import.stdout >/tmp/podlaz-capability-tun-private/profile-id"
  guest_exec test -s /tmp/podlaz-capability-tun-private/profile-id
  record_capability tun.profile_import_output pass
  record_capability tun.profile_import pass

  guest_exec /bin/bash -lc "id=\$(cat /tmp/podlaz-capability-tun-private/profile-id); runuser -u e2e -- env XDG_CONFIG_HOME='${CAPABILITY_GUEST_XDG}/config' XDG_STATE_HOME='${CAPABILITY_GUEST_XDG}/state' XDG_CACHE_HOME='${CAPABILITY_GUEST_XDG}/cache' /usr/bin/podlaz profile validate \"\${id}\" --mode tun >/tmp/podlaz-capability-tun-private/validate.stdout 2>/tmp/podlaz-capability-tun-private/validate.stderr"
  record_capability tun.profile_validate pass

  capture_guest_network_baseline
  assert_tun_authorization_boundary

  set +e
  guest_exec /bin/bash -lc "id=\$(cat /tmp/podlaz-capability-tun-private/profile-id); runuser -u e2e -- env XDG_CONFIG_HOME='${CAPABILITY_GUEST_XDG}/config' XDG_STATE_HOME='${CAPABILITY_GUEST_XDG}/state' XDG_CACHE_HOME='${CAPABILITY_GUEST_XDG}/cache' /usr/bin/podlaz connect --mode tun \"\${id}\" >/tmp/podlaz-capability-tun-private/connect.stdout 2>/tmp/podlaz-capability-tun-private/connect.stderr"
  connect_code=$?
  set -e
  (( connect_code == 0 )) || return "${connect_code}"
  record_capability tun.connect_requested pass
  record_capability tun.authorization pass

  wait_guest_tun_status verified-active 120
  record_capability tun.verified_active pass
  guest_exec timeout 20 getent ahostsv4 example.com >/dev/null
  record_capability tun.system_dns pass
  guest_exec timeout 30 curl -4 -fsS -o /dev/null https://example.com/
  record_capability tun.https_tls pass

  set +e
  guest_exec /bin/bash -lc "timeout 90 runuser -u e2e -- env XDG_CONFIG_HOME='${CAPABILITY_GUEST_XDG}/config' XDG_STATE_HOME='${CAPABILITY_GUEST_XDG}/state' XDG_CACHE_HOME='${CAPABILITY_GUEST_XDG}/cache' /usr/bin/podlaz doctor --tun >/tmp/podlaz-capability-tun-private/doctor.stdout 2>/tmp/podlaz-capability-tun-private/doctor.stderr"
  doctor_code=$?
  set -e
  if [[ "${doctor_code}" == 0 || "${doctor_code}" == 3 ]]; then
    record_capability tun.doctor observed
    if guest_exec grep -Eiq 'ipv6' /tmp/podlaz-capability-tun-private/doctor.stdout; then record_capability tun.ipv6 observed; else record_capability tun.ipv6 unavailable; fi
    if guest_exec grep -Eiq 'pmtu|mtu' /tmp/podlaz-capability-tun-private/doctor.stdout; then record_capability tun.pmtu observed; else record_capability tun.pmtu unavailable; fi
  else
    record_capability tun.doctor fail
    return 1
  fi

  guest_exec /bin/bash -lc "runuser -u e2e -- env XDG_CONFIG_HOME='${CAPABILITY_GUEST_XDG}/config' XDG_STATE_HOME='${CAPABILITY_GUEST_XDG}/state' XDG_CACHE_HOME='${CAPABILITY_GUEST_XDG}/cache' /usr/bin/podlaz disconnect >/tmp/podlaz-capability-tun-private/disconnect.stdout 2>/tmp/podlaz-capability-tun-private/disconnect.stderr"
  wait_guest_tun_status clean-inactive 80
  record_capability tun.clean_disconnect pass

  if guest_exec nmcli -t -f NAME,DEVICE connection show --active | grep -Fx "capability-uplink:${CAPABILITY_GUEST_IF}" >/dev/null && \
      ! guest_exec nmcli -t -f NAME,DEVICE connection show --active | grep -F ':podlaz0' >/dev/null; then
    record_capability tun.networkmanager_postcondition pass
  else
    record_capability tun.networkmanager_postcondition fail
    return 1
  fi

  guest_exec /bin/bash -lc "runuser -u e2e -- env XDG_CONFIG_HOME='${CAPABILITY_GUEST_XDG}/config' XDG_STATE_HOME='${CAPABILITY_GUEST_XDG}/state' XDG_CACHE_HOME='${CAPABILITY_GUEST_XDG}/cache' /usr/bin/podlaz recover --json >/tmp/podlaz-capability-tun-private/recover.json 2>/tmp/podlaz-capability-tun-private/recover.stderr && cd /workspace && source scripts/e2e/lib/e2e.sh && source scripts/e2e/lib/recovery_json.sh && assert_clean_recovery_json_file /tmp/podlaz-capability-tun-private/recover.json"
  assert_guest_terminal_authority_clean
  assert_guest_network_baseline_restored
  record_capability tun.recovery_clean pass
}

assert_guest_tun_clean() {
  assert_guest_terminal_authority_clean
  assert_guest_network_baseline_restored
  guest_exec timeout 20 getent ahostsv4 example.com >/dev/null
  guest_exec timeout 30 curl -4 -fsS -o /dev/null https://example.com/
  sudo -n rm -f "${CAPABILITY_GUEST_ROOT}${CAPABILITY_TUN_RULE}"
  assert_public_artifact_privacy
  record_capability artifact.privacy pass
}

probe_qemu_accelerators() {
  require_cmd qemu-system-x86_64 qemu-img cloud-localds ssh ssh-keygen
  record_capability qemu.available pass
  if [[ -e /dev/kvm ]]; then
    record_capability qemu.kvm_present pass
    local pidfile="${CAPABILITY_QEMU_ROOT}/kvm-probe.pid"
    if qemu-system-x86_64 -accel kvm -machine none -nodefaults -display none -monitor none -serial none -S -daemonize -pidfile "${pidfile}" >/dev/null 2>&1; then
      record_capability qemu.kvm_usable pass
      kill "$(cat "${pidfile}")" >/dev/null 2>&1 || true
    else
      record_capability qemu.kvm_usable fail
    fi
  else
    record_capability qemu.kvm_present unavailable
    record_capability qemu.kvm_usable unavailable
  fi
  local tcg_pidfile="${CAPABILITY_QEMU_ROOT}/tcg-probe.pid"
  if qemu-system-x86_64 -accel tcg -machine none -nodefaults -display none -monitor none -serial none -S -daemonize -pidfile "${tcg_pidfile}" >/dev/null 2>&1; then
    record_capability qemu.tcg_usable pass
    kill "$(cat "${tcg_pidfile}")" >/dev/null 2>&1 || true
  else
    record_capability qemu.tcg_usable fail
    return 1
  fi
}

prepare_qemu_image() {
  local free_kb image="${CAPABILITY_QEMU_ROOT}/ubuntu.img" sums="${CAPABILITY_QEMU_ROOT}/SHA256SUMS" expected actual user_data
  install -d -m 0700 "${CAPABILITY_QEMU_ROOT}"
  free_kb="$(df -Pk "${E2E_TMP_ROOT}" | awk 'NR == 2 {print $4}')"
  if (( free_kb < 3 * 1024 * 1024 )); then record_capability qemu.disk_budget fail; return 1; fi
  record_capability qemu.disk_budget pass
  curl -fsSL "${CAPABILITY_QEMU_IMAGE_URL}" -o "${image}"
  curl -fsSL "${CAPABILITY_QEMU_SUMS_URL}" -o "${sums}"
  expected="$(awk '$2 == "*ubuntu-24.04-server-cloudimg-amd64.img" || $2 == "ubuntu-24.04-server-cloudimg-amd64.img" {print $1; exit}' "${sums}")"
  actual="$(sha256sum "${image}" | awk '{print $1}')"
  if [[ -z "${expected}" || "${actual}" != "${expected}" ]]; then record_capability qemu.image_checksum fail; return 1; fi
  record_capability qemu.image_checksum pass
  qemu-img create -q -f qcow2 -F qcow2 -b "${image}" "${CAPABILITY_QEMU_ROOT}/overlay.qcow2" 4G
  ssh-keygen -q -t ed25519 -N '' -f "${CAPABILITY_QEMU_ROOT}/id_ed25519"
  chmod 0600 "${CAPABILITY_QEMU_ROOT}/id_ed25519"
  QEMU_SSH_KEY="${CAPABILITY_QEMU_ROOT}/id_ed25519"
  user_data="${CAPABILITY_QEMU_ROOT}/user-data"
  cat >"${user_data}" <<EOF
#cloud-config
users:
  - name: e2e
    groups: [sudo]
    shell: /bin/bash
    sudo: ALL=(ALL) NOPASSWD:ALL
    ssh_authorized_keys:
      - $(cat "${CAPABILITY_QEMU_ROOT}/id_ed25519.pub")
ssh_pwauth: false
disable_root: true
EOF
  printf 'instance-id: podlaz-hosted-capability\nlocal-hostname: podlaz-capability-vm\n' >"${CAPABILITY_QEMU_ROOT}/meta-data"
  cloud-localds "${CAPABILITY_QEMU_ROOT}/seed.img" "${user_data}" "${CAPABILITY_QEMU_ROOT}/meta-data"
}

choose_qemu_accel() {
  if grep -q '^qemu.kvm_usable=pass$' "${CAPABILITY_REPORT}"; then printf 'kvm\n'; else printf 'tcg\n'; fi
}

find_loopback_port() {
  python3 - <<'PY'
import socket
sock=socket.socket(); sock.bind(("127.0.0.1",0)); print(sock.getsockname()[1]); sock.close()
PY
}

start_qemu_guest() {
  local accel cpu pidfile="${CAPABILITY_QEMU_ROOT}/qemu.pid"
  accel="$(choose_qemu_accel)"
  if [[ "${accel}" == kvm ]]; then cpu=host; else cpu=max; fi
  QEMU_SSH_PORT="$(find_loopback_port)"
  qemu-system-x86_64 \
    -machine "q35,accel=${accel}" -cpu "${cpu}" -smp 2 -m 2048 \
    -drive "if=virtio,file=${CAPABILITY_QEMU_ROOT}/overlay.qcow2,format=qcow2" \
    -drive "if=virtio,file=${CAPABILITY_QEMU_ROOT}/seed.img,format=raw,readonly=on" \
    -nic "user,model=virtio-net-pci,hostfwd=tcp:127.0.0.1:${QEMU_SSH_PORT}-:22" \
    -display none -monitor none -serial "file:${CAPABILITY_QEMU_ROOT}/serial.log" \
    -daemonize -pidfile "${pidfile}"
  QEMU_PID="$(cat "${pidfile}")"
}

qemu_ssh() {
  ssh -i "${QEMU_SSH_KEY}" -p "${QEMU_SSH_PORT}" -o BatchMode=yes -o ConnectTimeout=5 \
    -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR e2e@127.0.0.1 "$@"
}

wait_qemu_ssh() {
  local attempts="${1:-180}"
  for _ in $(seq 1 "${attempts}"); do
    qemu_ssh true >/dev/null 2>&1 && return 0
    kill -0 "${QEMU_PID}" >/dev/null 2>&1 || return 1
    sleep 2
  done
  return 1
}

reboot_qemu_guest() {
  local before after disappeared=false
  before="$(qemu_ssh cat /proc/sys/kernel/random/boot_id | tr -d '[:space:]')"
  [[ -n "${before}" ]] || return 1
  qemu_ssh sudo systemctl reboot >/dev/null 2>&1 || true
  for _ in $(seq 1 60); do
    if ! qemu_ssh true >/dev/null 2>&1; then disappeared=true; break; fi
    sleep 1
  done
  [[ "${disappeared}" == true ]] || return 1
  wait_qemu_ssh 240
  after="$(qemu_ssh cat /proc/sys/kernel/random/boot_id | tr -d '[:space:]')"
  [[ -n "${after}" && "${after}" != "${before}" ]]
}

run_qemu_capability() (
  trap stop_qemu_guest EXIT
  set -Eeuo pipefail
  mkdir -p "${CAPABILITY_QEMU_ROOT}"
  probe_qemu_accelerators
  prepare_qemu_image
  if ! start_qemu_guest || ! wait_qemu_ssh; then record_capability_if_missing qemu.boot fail; return 1; fi
  # shellcheck disable=SC2016 -- variables intentionally expand on the QEMU guest.
  if ! qemu_ssh '. /etc/os-release; test "$ID" = ubuntu; test "$VERSION_ID" = 24.04' >/dev/null; then record_capability_if_missing qemu.boot fail; return 1; fi
  record_capability qemu.boot pass
  if ! reboot_qemu_guest; then record_capability qemu.reboot_boot_id fail; return 1; fi
  record_capability qemu.reboot_boot_id pass
  stop_qemu_guest
  local free_kb
  free_kb="$(df -Pk "${E2E_TMP_ROOT}" | awk 'NR == 2 {print $4}')"
  (( free_kb >= 1024 * 1024 ))
)

run_system_guest_capability() (
  local stage=prepare saved=0
  cleanup_system_probe() {
    saved=$?
    set +e
    if (( saved != 0 )); then
      case "${stage}" in
        prepare)
          case "${SYSTEM_GUEST_PREPARE_STAGE}" in
            debootstrap) record_capability_if_missing guest.prepare.debootstrap fail ;;
            networking) record_capability_if_missing guest.prepare.networking fail ;;
            user) record_capability_if_missing guest.prepare.user fail ;;
            services) record_capability_if_missing guest.prepare.services fail ;;
            candidate) record_capability_if_missing guest.prepare.candidate fail ;;
          esac
          record_capability_if_missing guest.bootstrap.prepare fail
          ;;
        start)
          case "${SYSTEM_GUEST_START_STAGE}" in
            nspawn) record_capability_if_missing guest.start.nspawn fail ;;
            control) record_capability_if_missing guest.start.control fail ;;
            interface) record_capability_if_missing guest.start.interface fail ;;
            uplink) record_capability_if_missing guest.start.uplink fail ;;
            services) record_capability_if_missing guest.start.services fail ;;
            tun) record_capability_if_missing guest.start.tun fail ;;
            internet) record_capability_if_missing guest.start.internet fail ;;
          esac
          record_capability_if_missing guest.bootstrap.start fail
          ;;
      esac
    fi
    stop_synthetic_xray_endpoint || true
    stop_system_guest || true
    cleanup_outer_plumbing || true
    exit "${saved}"
  }
  trap cleanup_system_probe EXIT
  set -Eeuo pipefail

  prepare_system_guest
  record_capability guest.bootstrap.prepare pass
  stage=start
  start_system_guest
  record_capability guest.bootstrap.start pass
  stage=product
  install_candidate_in_guest
  assert_guest_package_provenance
  run_guest_ordinary_user_acceptance
  start_synthetic_xray_endpoint
  install_tun_ci_authorization
  run_synthetic_tun_lifecycle
  assert_guest_tun_clean
  stage="done"
)

run_independent_capability_probes() (
  local failed=0 probe_status
  set +e
  run_system_guest_capability
  probe_status=$?
  (( probe_status == 0 )) || failed=1
  run_qemu_capability
  probe_status=$?
  (( probe_status == 0 )) || failed=1
  return "${failed}"
)

validate_candidate() {
  local path="$1" arch
  [[ -f "${path}" && ! -L "${path}" ]] || fail "candidate package must be a regular non-symlink file"
  [[ "$(dpkg-deb --field "${path}" Package)" == podlaz ]] || fail "candidate package is not podlaz"
  arch="$(dpkg-deb --field "${path}" Architecture)"
  [[ "${arch}" == "$(dpkg --print-architecture)" ]] || fail "candidate package architecture does not match host"
  CANDIDATE_DEB="$(readlink -f -- "${path}")"
}

main() {
  local failed=0 probe_status
  (($# == 1)) || fail "usage: $0 CANDIDATE.deb"
  require_cmd awk bash cmp curl debootstrap dpkg dpkg-deb find grep ip iptables jq mktemp nft python3 readlink sha256sum ss sudo systemd-nspawn systemd-run timeout
  validate_candidate "$1"
  install -d -m 0700 "${CAPABILITY_PRIVATE}" "${E2E_ARTIFACT_DIR}" "${CAPABILITY_XRAY_ROOT}"
  : >"${CAPABILITY_REPORT}"
  chmod 0600 "${CAPABILITY_REPORT}"
  trap teardown_all EXIT

  capture_outer_baseline
  if assert_outer_control_plane_healthy before; then record_capability outer.control_plane.before pass; else record_capability outer.control_plane.before fail; return 1; fi

  set +e
  (set -Eeuo pipefail; probe_hosted_kernel_primitives)
  probe_status=$?
  (( probe_status == 0 )) || failed=1
  run_independent_capability_probes
  probe_status=$?
  (( probe_status == 0 )) || failed=1
  set -e
  return "${failed}"
}

if [[ "${PODLAZ_E2E_CAPABILITY_SOURCE_ONLY}" == true ]]; then
  if [[ "${BASH_SOURCE[0]}" != "$0" ]]; then return 0; fi
  exit 0
fi

if [[ "${1:-}" == validate-report ]]; then
  validate_report
  exit 0
fi

main "$@"