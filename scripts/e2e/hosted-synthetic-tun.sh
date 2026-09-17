#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
# shellcheck source=lib/e2e.sh
source "${SCRIPT_DIR}/lib/e2e.sh"

REPORT="${E2E_ARTIFACT_DIR}/hosted-synthetic-tun.txt"
MACHINE="podlaz-synthetic-tun"
HOST_VETH="pzsynt0"
HOST_ENDPOINT_DEV="pzsyntsrv"
GUEST_IF="host0"
NFT_FAMILY="inet"
NFT_TABLE="pzsynt_hosted"
FOREIGN_NFT_TABLE="pzsynt_foreign"
NETWORK_CIDR="172.31.254.0/30"
HOST_CIDR="172.31.254.1/30"
GUEST_CIDR="172.31.254.2/30"
HOST_IP="172.31.254.1"
ENDPOINT_CIDR="172.31.253.1/32"
ENDPOINT_IP="172.31.253.1"
GUEST_ROOT="${E2E_TMP_ROOT}/system-guest"
PRIVATE_ROOT="${E2E_TMP_ROOT}/private"
XRAY_ROOT="${PRIVATE_ROOT}/synthetic-xray"
GUEST_CANDIDATE="/opt/podlaz-candidate.deb"
GUEST_XDG="/home/e2e/.local/share/podlaz-hosted-synthetic-tun"
TUN_RULE="/etc/polkit-1/rules.d/49-podlaz-hosted-synthetic-tun.rules"
GUEST_PRIVATE="/tmp/podlaz-hosted-synthetic-tun"
GUEST_MANIFEST="${GUEST_PRIVATE}/network-manifest.json"
FALLBACK_NETWORK_HELPER="/workspace/scripts/e2e/tun-package-fallback-network.py"
ACTIVE_AUTHORITY_HELPER="/workspace/scripts/e2e/hosted_synthetic_active_authority.py"

EVIDENCE_KEYS=(
  candidate.provenance
  ordinary_user.boundary
  tun.verified_active
  tun.system_dns
  tun.https_tls
  tun.doctor
  tun.clean_disconnect
  tun.terminal_cleanup
  tun.recovery_clean
  guest.baseline_restored
  outer.cleanup
  artifact.privacy
)

CANDIDATE_DEB=""
EXPECTED_COMMIT="${PODLAZ_E2E_CANDIDATE_COMMIT:-${GITHUB_SHA:-}}"
OUTER_DEFAULT_ROUTE=""
OUTER_RULES=""
OUTER_RESOLV_HASH=""
OUTER_IP_FORWARD=""
OUTER_EGRESS_IF=""
NSPAWN_PID=""
XRAY_PID=""
SYSTEM_GUEST_ACTIVE=false
TEARDOWN_RUNNING=false
FAILURE_CLASS=none
FAILURE_STEP=none

evidence_recorded() {
  local key="$1"
  [[ -f "${REPORT}" ]] && grep -Eq "^${key}=" "${REPORT}"
}

record_evidence() {
  local key="$1" state="$2"
  [[ "${key}" =~ ^[a-z0-9_.-]+$ ]] || fail "invalid evidence key"
  case "${state}" in
    pass|fail|observed|unavailable) ;;
    *) fail "invalid evidence state for ${key}" ;;
  esac
  ! evidence_recorded "${key}" || fail "duplicate evidence key: ${key}"
  printf '%s=%s\n' "${key}" "${state}" >>"${REPORT}"
}

record_if_missing() {
  local key="$1" state="$2"
  evidence_recorded "${key}" || record_evidence "${key}" "${state}"
}

mark_failure() {
  local class="$1" step="$2"
  case "${class}" in
    product|fixture|infrastructure|capability|diagnostic_unknown) ;;
    *) class=infrastructure ;;
  esac
  FAILURE_CLASS="${class}"
  FAILURE_STEP="${step//[^A-Za-z0-9_.-]/_}"
}

finalize_report() {
  local key
  for key in "${EVIDENCE_KEYS[@]}"; do
    record_if_missing "${key}" fail
  done
  printf 'failure.class=%s\n' "${FAILURE_CLASS}" >>"${REPORT}"
  printf 'failure.step=%s\n' "${FAILURE_STEP}" >>"${REPORT}"
}

validate_report() {
  python3 - "${REPORT}" "${EVIDENCE_KEYS[@]}" <<'PY'
import re
import sys
from pathlib import Path

path = Path(sys.argv[1])
expected = sys.argv[2:]
if not path.is_file() or path.is_symlink():
    raise SystemExit("hosted synthetic TUN report is missing or invalid")
lines = path.read_text(encoding="utf-8").splitlines()
values = {}
meta = {}
for line in lines:
    m = re.fullmatch(r"([a-z0-9_.-]+)=(pass|fail|observed|unavailable)", line)
    if m:
        key, value = m.groups()
        if key in values:
            raise SystemExit(f"duplicate evidence key: {key}")
        values[key] = value
        continue
    m = re.fullmatch(r"failure\.(class|step)=([A-Za-z0-9_.-]+)", line)
    if m:
        key, value = m.groups()
        if key in meta:
            raise SystemExit(f"duplicate failure metadata: {key}")
        meta[key] = value
        continue
    raise SystemExit("report contains non-normalized data")
if set(values) != set(expected):
    raise SystemExit(f"evidence schema mismatch: expected={sorted(expected)} got={sorted(values)}")
if set(meta) != {"class", "step"}:
    raise SystemExit("failure metadata is incomplete")
if meta["class"] not in {"none", "product", "fixture", "infrastructure", "capability", "diagnostic_unknown"}:
    raise SystemExit("invalid failure class")
for key in expected:
    allowed = {"pass", "observed"} if key == "tun.doctor" else {"pass"}
    if values[key] not in allowed:
        raise SystemExit(f"required evidence is not successful: {key}={values[key]}")
if meta != {"class": "none", "step": "none"}:
    raise SystemExit("successful evidence report contains failure metadata")
PY
}

assert_public_artifact_privacy() {
  local extra
  extra="$(find "${E2E_ARTIFACT_DIR}" -mindepth 1 -maxdepth 1 ! -name 'hosted-synthetic-tun.txt' -print -quit)"
  [[ -z "${extra}" ]] || return 1
  [[ -f "${REPORT}" && ! -L "${REPORT}" ]] || return 1
  ! grep -Eq 'vless://|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}|172[.]31[.](253|254)[.]' "${REPORT}"
}

validate_candidate() {
  local path="$1" arch
  [[ -f "${path}" && ! -L "${path}" ]] || fail "candidate package must be a regular non-symlink file"
  [[ "$(dpkg-deb --field "${path}" Package)" == podlaz ]] || fail "candidate package is not podlaz"
  arch="$(dpkg-deb --field "${path}" Architecture)"
  [[ "${arch}" == "$(dpkg --print-architecture)" ]] || fail "candidate package architecture does not match runner"
  CANDIDATE_DEB="$(readlink -f -- "${path}")"
}

assert_synthetic_range_available() {
  local target="$1"
  python3 - "${target}" <(ip -j -4 addr show) <(ip -j -4 route show table all) <<'PY'
import ipaddress
import json
import sys

target = ipaddress.ip_network(sys.argv[1], strict=False)
with open(sys.argv[2], encoding="utf-8") as f:
    addresses = json.load(f)
for link in addresses:
    for info in link.get("addr_info", []):
        if info.get("family") != "inet":
            continue
        local, prefix = info.get("local"), info.get("prefixlen")
        if local is None or prefix is None:
            continue
        if ipaddress.ip_network(f"{local}/{prefix}", strict=False).overlaps(target):
            raise SystemExit("synthetic range overlaps an existing address")
with open(sys.argv[3], encoding="utf-8") as f:
    routes = json.load(f)
for route in routes:
    dst = route.get("dst")
    if not dst or dst == "default":
        continue
    try:
        candidate = ipaddress.ip_network(dst, strict=False)
    except ValueError:
        continue
    if candidate.overlaps(target):
        raise SystemExit("synthetic range overlaps an existing route")
PY
}

capture_outer_baseline() {
  install -d -m 0700 "${PRIVATE_ROOT}"
  OUTER_DEFAULT_ROUTE="${PRIVATE_ROOT}/outer-default-route.json"
  OUTER_RULES="${PRIVATE_ROOT}/outer-rules.json"
  OUTER_RESOLV_HASH="${PRIVATE_ROOT}/outer-resolv.sha256"
  OUTER_IP_FORWARD="$(cat /proc/sys/net/ipv4/ip_forward)"
  ip -4 -j route show default | jq -S . >"${OUTER_DEFAULT_ROUTE}"
  ip -4 -j rule show | jq -S . >"${OUTER_RULES}"
  sha256sum /etc/resolv.conf | awk '{print $1}' >"${OUTER_RESOLV_HASH}"
  OUTER_EGRESS_IF="$(ip -4 route show default | awk 'NR == 1 {for (i=1; i<=NF; i++) if ($i == "dev") {print $(i+1); exit}}')"
  [[ -n "${OUTER_EGRESS_IF}" ]] || return 1
  ! ip link show dev "${HOST_VETH}" >/dev/null 2>&1 || return 1
  ! ip link show dev "${HOST_ENDPOINT_DEV}" >/dev/null 2>&1 || return 1
  ! sudo -n nft list table "${NFT_FAMILY}" "${NFT_TABLE}" >/dev/null 2>&1 || return 1
  assert_synthetic_range_available "${NETWORK_CIDR}"
  assert_synthetic_range_available "${ENDPOINT_CIDR}"
  timeout 30 curl -4 -fsS --max-time 10 -o /dev/null https://github.com/
}

assert_outer_baseline_restored() {
  local current
  cmp -s "${OUTER_DEFAULT_ROUTE}" <(ip -4 -j route show default | jq -S .) || return 1
  cmp -s "${OUTER_RULES}" <(ip -4 -j rule show | jq -S .) || return 1
  current="$(sha256sum /etc/resolv.conf | awk '{print $1}')"
  [[ "${current}" == "$(cat "${OUTER_RESOLV_HASH}")" ]] || return 1
  [[ "$(cat /proc/sys/net/ipv4/ip_forward)" == "${OUTER_IP_FORWARD}" ]] || return 1
  ! ip link show dev "${HOST_VETH}" >/dev/null 2>&1 || return 1
  ! ip link show dev "${HOST_ENDPOINT_DEV}" >/dev/null 2>&1 || return 1
  ! sudo -n nft list table "${NFT_FAMILY}" "${NFT_TABLE}" >/dev/null 2>&1 || return 1
  ! sudo -n iptables -S DOCKER-USER | grep -F 'podlaz-hosted-synthetic-tun-' >/dev/null || return 1
  timeout 30 curl -4 -fsS --max-time 10 -o /dev/null https://github.com/
}

cleanup_outer_plumbing() {
  set +e
  if [[ -n "${OUTER_EGRESS_IF}" ]]; then
    sudo -n iptables -D DOCKER-USER -i "${OUTER_EGRESS_IF}" -o "${HOST_VETH}" -d "${NETWORK_CIDR}" -m conntrack --ctstate ESTABLISHED,RELATED -m comment --comment podlaz-hosted-synthetic-tun-in -j ACCEPT >/dev/null 2>&1 || true
    sudo -n iptables -D DOCKER-USER -i "${HOST_VETH}" -o "${OUTER_EGRESS_IF}" -s "${NETWORK_CIDR}" -m comment --comment podlaz-hosted-synthetic-tun-out -j ACCEPT >/dev/null 2>&1 || true
  fi
  sudo -n nft delete table "${NFT_FAMILY}" "${NFT_TABLE}" >/dev/null 2>&1 || true
  sudo -n ip link del dev "${HOST_VETH}" >/dev/null 2>&1 || true
  sudo -n ip link del dev "${HOST_ENDPOINT_DEV}" >/dev/null 2>&1 || true
  if [[ -n "${OUTER_IP_FORWARD}" ]]; then
    printf '%s\n' "${OUTER_IP_FORWARD}" | sudo -n tee /proc/sys/net/ipv4/ip_forward >/dev/null || return 1
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

stop_system_guest() {
  if [[ "${SYSTEM_GUEST_ACTIVE}" == true ]]; then
    sudo -n machinectl terminate "${MACHINE}" >/dev/null 2>&1 || true
    for _ in $(seq 1 100); do
      ! machinectl show "${MACHINE}" >/dev/null 2>&1 && break
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
  local saved=$? cleanup_failed=0
  [[ "${TEARDOWN_RUNNING}" == false ]] || return
  TEARDOWN_RUNNING=true
  trap - EXIT
  set +e
  stop_synthetic_xray_endpoint || cleanup_failed=1
  stop_system_guest || cleanup_failed=1
  cleanup_outer_plumbing || cleanup_failed=1
  if [[ -n "${OUTER_DEFAULT_ROUTE}" ]]; then
    assert_outer_baseline_restored || cleanup_failed=1
  fi
  if (( cleanup_failed == 0 )); then
    record_if_missing outer.cleanup pass
  else
    record_if_missing outer.cleanup fail
    mark_failure infrastructure outer.cleanup
  fi
  if assert_public_artifact_privacy; then
    record_if_missing artifact.privacy pass
  else
    record_if_missing artifact.privacy fail
    mark_failure fixture artifact.privacy
  fi
  if (( saved != 0 )) && [[ "${FAILURE_CLASS}" == none ]]; then
    mark_failure infrastructure scenario
  fi
  finalize_report
  validate_report || cleanup_failed=1
  set -e
  if (( saved == 0 && cleanup_failed != 0 )); then saved=1; fi
  exit "${saved}"
}

prepare_system_guest() {
  local policy_rc="${GUEST_ROOT}/usr/sbin/policy-rc.d" nm_tmp sudoers_tmp
  sudo -n rm -rf "${GUEST_ROOT}"
  sudo -n debootstrap --variant=minbase noble "${GUEST_ROOT}" http://archive.ubuntu.com/ubuntu >"${PRIVATE_ROOT}/debootstrap.log" 2>&1
  printf '#!/bin/sh\nexit 101\n' | sudo -n tee "${policy_rc}" >/dev/null
  sudo -n chmod 0755 "${policy_rc}"
  sudo -n chroot "${GUEST_ROOT}" /usr/bin/env DEBIAN_FRONTEND=noninteractive apt-get update >"${PRIVATE_ROOT}/guest-apt.log" 2>&1
  sudo -n chroot "${GUEST_ROOT}" /usr/bin/env DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    systemd systemd-sysv dbus ca-certificates sudo iproute2 nftables curl python3 gawk grep sed procps util-linux iputils-ping \
    network-manager systemd-resolved polkitd jq openssl >>"${PRIVATE_ROOT}/guest-apt.log" 2>&1
  sudo -n rm -f "${policy_rc}"
  sudo -n chroot "${GUEST_ROOT}" apt-get clean >/dev/null 2>&1

  nm_tmp="$(mktemp "${PRIVATE_ROOT}/uplink.XXXXXX")"
  cat >"${nm_tmp}" <<EOF_NM
[connection]
id=synthetic-uplink
type=ethernet
interface-name=${GUEST_IF}
autoconnect=true

[ipv4]
method=manual
address1=${GUEST_CIDR},${HOST_IP}
dns=1.1.1.1;1.0.0.1;
never-default=false

[ipv6]
method=disabled
EOF_NM
  sudo -n install -D -m 0600 "${nm_tmp}" "${GUEST_ROOT}/etc/NetworkManager/system-connections/synthetic-uplink.nmconnection"
  rm -f "${nm_tmp}"
  sudo -n mkdir -p "${GUEST_ROOT}/etc/NetworkManager/conf.d"
  sudo -n install -D -m 0644 /dev/null "${GUEST_ROOT}/etc/NetworkManager/conf.d/10-globally-managed-devices.conf"
  printf '[main]\ndns=systemd-resolved\n' | sudo -n tee "${GUEST_ROOT}/etc/NetworkManager/conf.d/10-synthetic-dns.conf" >/dev/null
  cat <<EOF_NM_MANAGED | sudo -n tee "${GUEST_ROOT}/etc/NetworkManager/conf.d/20-synthetic-uplink.conf" >/dev/null
[device-synthetic-uplink]
match-device=interface-name:=${GUEST_IF}
managed=1
EOF_NM_MANAGED
  sudo -n rm -f "${GUEST_ROOT}/etc/resolv.conf"
  sudo -n ln -s /run/systemd/resolve/stub-resolv.conf "${GUEST_ROOT}/etc/resolv.conf"

  sudo -n chroot "${GUEST_ROOT}" useradd -m -s /bin/bash e2e
  sudoers_tmp="$(mktemp "${PRIVATE_ROOT}/sudoers.XXXXXX")"
  printf 'e2e ALL=(ALL) NOPASSWD: ALL\n' >"${sudoers_tmp}"
  sudo -n install -D -m 0440 "${sudoers_tmp}" "${GUEST_ROOT}/etc/sudoers.d/e2e-harness"
  rm -f "${sudoers_tmp}"
  sudo -n mkdir -p "${GUEST_ROOT}/workspace" "${GUEST_ROOT}/opt"
  sudo -n touch "${GUEST_ROOT}${GUEST_CANDIDATE}"
  sudo -n chmod 0644 "${GUEST_ROOT}${GUEST_CANDIDATE}"
  sudo -n systemctl --root="${GUEST_ROOT}" enable NetworkManager.service systemd-resolved.service >/dev/null
}

setup_outer_plumbing() {
  for _ in $(seq 1 100); do
    ip link show dev "${HOST_VETH}" >/dev/null 2>&1 && break
    sleep 0.1
  done
  ip link show dev "${HOST_VETH}" >/dev/null 2>&1 || return 1
  sudo -n ip addr add "${HOST_CIDR}" dev "${HOST_VETH}"
  sudo -n ip link set dev "${HOST_VETH}" up
  sudo -n ip link add dev "${HOST_ENDPOINT_DEV}" type dummy
  sudo -n ip addr add "${ENDPOINT_CIDR}" dev "${HOST_ENDPOINT_DEV}"
  sudo -n ip link set dev "${HOST_ENDPOINT_DEV}" up
  printf '1\n' | sudo -n tee /proc/sys/net/ipv4/ip_forward >/dev/null
  sudo -n iptables -I DOCKER-USER 1 -i "${HOST_VETH}" -o "${OUTER_EGRESS_IF}" -s "${NETWORK_CIDR}" -m comment --comment podlaz-hosted-synthetic-tun-out -j ACCEPT
  sudo -n iptables -I DOCKER-USER 1 -i "${OUTER_EGRESS_IF}" -o "${HOST_VETH}" -d "${NETWORK_CIDR}" -m conntrack --ctstate ESTABLISHED,RELATED -m comment --comment podlaz-hosted-synthetic-tun-in -j ACCEPT
  sudo -n nft add table "${NFT_FAMILY}" "${NFT_TABLE}"
  sudo -n nft "add chain ${NFT_FAMILY} ${NFT_TABLE} postrouting { type nat hook postrouting priority srcnat; policy accept; }"
  sudo -n nft add rule "${NFT_FAMILY}" "${NFT_TABLE}" postrouting ip saddr "${NETWORK_CIDR}" oifname "${OUTER_EGRESS_IF}" masquerade
}

guest_exec() {
  sudo -n systemd-run --machine="${MACHINE}" --expand-environment=no --wait --pipe --collect --quiet -- "$@"
}

start_system_guest() {
  sudo -n systemd-nspawn \
    --quiet --boot \
    --directory="${GUEST_ROOT}" \
    --machine="${MACHINE}" \
    --settings=no --private-network \
    --network-veth-extra="${HOST_VETH}:${GUEST_IF}" \
    --bind-ro="${REPO_ROOT}:/workspace" \
    --bind-ro="${XRAY_ROOT}:/run/podlaz-synthetic-xray" \
    --bind-ro="${CANDIDATE_DEB}:${GUEST_CANDIDATE}" \
    --bind=/dev/net/tun \
    --capability=CAP_NET_ADMIN,CAP_NET_RAW \
    --link-journal=no >"${PRIVATE_ROOT}/nspawn.log" 2>&1 &
  NSPAWN_PID=$!
  SYSTEM_GUEST_ACTIVE=true
  setup_outer_plumbing
  for _ in $(seq 1 200); do
    if machinectl show "${MACHINE}" >/dev/null 2>&1 && guest_exec /bin/true >/dev/null 2>&1; then break; fi
    sleep 0.2
  done
  guest_exec /bin/true >/dev/null
  # Expansion is intentionally evaluated by guest bash.
  # shellcheck disable=SC2016
  guest_exec /bin/bash -lc 'state="$(timeout 30 systemctl is-system-running --wait 2>/dev/null || true)"; [[ "$state" == running || "$state" == degraded ]]'
  guest_exec nmcli connection reload
  guest_exec nmcli connection up synthetic-uplink >/dev/null
  guest_exec nmcli -t -f NAME,DEVICE connection show --active | grep -Fx "synthetic-uplink:${GUEST_IF}" >/dev/null
  guest_exec systemctl is-active --quiet NetworkManager.service
  guest_exec systemctl is-active --quiet systemd-resolved.service
  guest_exec test -c /dev/net/tun
  guest_exec ip tuntap add dev pzsynt-probe mode tun
  guest_exec ip link del dev pzsynt-probe
  guest_exec timeout 20 getent ahostsv4 example.com >/dev/null
  guest_exec timeout 30 curl -4 -fsS -o /dev/null https://example.com/
}

install_candidate_in_guest() {
  guest_exec test -r "${GUEST_CANDIDATE}"
  guest_exec /bin/bash -lc "DEBIAN_FRONTEND=noninteractive apt-get install -y '${GUEST_CANDIDATE}' >/run/podlaz-synthetic-install.log 2>&1"
  guest_exec systemctl daemon-reload
  guest_exec systemctl start podlazd.service
  guest_exec systemctl is-active --quiet podlazd.service
}

assert_guest_package_provenance() {
  [[ -n "${EXPECTED_COMMIT}" ]] || return 1
  guest_exec /bin/bash -lc "cd /workspace && source scripts/e2e/lib/e2e.sh && source scripts/e2e/lib/package_provenance.sh && assert_native_deb_arch '${GUEST_CANDIDATE}' \"\$(dpkg --print-architecture)\" && assert_exact_podlaz_package_runtime_provenance '${GUEST_CANDIDATE}' '${EXPECTED_COMMIT}'"
}

start_synthetic_xray_endpoint() {
  local extract="${XRAY_ROOT}/package" config="${XRAY_ROOT}/server.json" uuid port
  install -d -m 0700 "${XRAY_ROOT}" "${extract}"
  dpkg-deb -x "${CANDIDATE_DEB}" "${extract}"
  uuid="$("${extract}/usr/lib/podlaz/xray" uuid | tr -d '[:space:]')"
  [[ "${uuid}" =~ ^[0-9a-fA-F-]{36}$ ]] || return 1
  port="$(python3 - "${ENDPOINT_IP}" <<'PY'
import socket, sys
sock = socket.socket()
sock.bind((sys.argv[1], 0))
print(sock.getsockname()[1])
sock.close()
PY
)"
  cat >"${config}" <<EOF_XRAY
{
  "log": {"loglevel": "warning"},
  "inbounds": [{
    "listen": "${ENDPOINT_IP}",
    "port": ${port},
    "protocol": "vless",
    "settings": {"clients": [{"id": "${uuid}"}], "decryption": "none"},
    "streamSettings": {"security": "none"}
  }],
  "outbounds": [{"protocol": "freedom", "settings": {}}]
}
EOF_XRAY
  chmod 0600 "${config}"
  "${extract}/usr/lib/podlaz/xray" run -test -config "${config}" >"${XRAY_ROOT}/config-test.log" 2>&1
  "${extract}/usr/lib/podlaz/xray" run -config "${config}" >"${XRAY_ROOT}/server.log" 2>&1 &
  XRAY_PID=$!
  for _ in $(seq 1 100); do
    if ss -H -ltn | awk '{print $4}' | grep -Fx "${ENDPOINT_IP}:${port}" >/dev/null; then break; fi
    kill -0 "${XRAY_PID}" >/dev/null 2>&1 || return 1
    sleep 0.1
  done
  ss -H -ltn | awk '{print $4}' | grep -Fx "${ENDPOINT_IP}:${port}" >/dev/null || return 1
  printf 'vless://%s@%s:%s?type=tcp&security=none&encryption=none#hosted-synthetic\n' "${uuid}" "${ENDPOINT_IP}" "${port}" >"${XRAY_ROOT}/client-uri"
  chmod 0600 "${XRAY_ROOT}/client-uri"
}

install_tun_authorization() {
  local rule_tmp
  rule_tmp="$(mktemp "${PRIVATE_ROOT}/tun-polkit.XXXXXX")"
  cat >"${rule_tmp}" <<'EOF_RULE'
polkit.addRule(function(action, subject) {
    if (subject.user == "e2e" &&
        (action.id == "io.github.aidarkhusainov.podlaz.connect-tun" ||
         action.id == "io.github.aidarkhusainov.podlaz.disconnect")) {
        return polkit.Result.YES;
    }
});
EOF_RULE
  sudo -n install -D -m 0644 "${rule_tmp}" "${GUEST_ROOT}${TUN_RULE}"
  rm -f "${rule_tmp}"
  sleep 1
}

run_guest_user() {
  guest_exec runuser -u e2e -- env \
    XDG_CONFIG_HOME="${GUEST_XDG}/config" \
    XDG_STATE_HOME="${GUEST_XDG}/state" \
    XDG_CACHE_HOME="${GUEST_XDG}/cache" \
    "$@"
}

wait_guest_status() {
  local target="$1" attempts="$2"
  for _ in $(seq 1 "${attempts}"); do
    if guest_exec /bin/bash -lc "curl --fail --silent --show-error --max-time 5 --unix-socket /run/podlaz/podlazd.sock http://localhost/v1/status >${GUEST_PRIVATE}/status.json 2>/dev/null && python3 /workspace/scripts/e2e/lib/daemon_status_semantics.py '${target}' ${GUEST_PRIVATE}/status.json" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
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
  capture_guest_network_snapshot "${PRIVATE_ROOT}/guest-baseline"
}

assert_guest_network_baseline_restored() {
  local before="${PRIVATE_ROOT}/guest-baseline" after="${PRIVATE_ROOT}/guest-after" suffix
  capture_guest_network_snapshot "${after}"
  for suffix in addr.json routes.json rules.json nft.json nm.txt resolved.txt; do
    cmp -s "${before}.${suffix}" "${after}.${suffix}" || return 1
  done
}

create_foreign_sentinel() {
  ! guest_exec nft list table inet "${FOREIGN_NFT_TABLE}" >/dev/null 2>&1 || return 1
  guest_exec nft add table inet "${FOREIGN_NFT_TABLE}"
}

assert_foreign_sentinel() {
  guest_exec nft list table inet "${FOREIGN_NFT_TABLE}" >/dev/null 2>&1
}

assert_ordinary_user_boundary() {
  local code
  guest_exec /bin/bash -lc '! id -nG e2e | tr " " "\n" | grep -Fx podlaz >/dev/null'
  # Expansion is intentionally evaluated by guest bash.
  # shellcheck disable=SC2016
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
  guest_exec /bin/bash -lc "id=\$(cat ${GUEST_PRIVATE}/profile-id); runuser -u e2e -- env XDG_CONFIG_HOME='${GUEST_XDG}/config' XDG_STATE_HOME='${GUEST_XDG}/state' XDG_CACHE_HOME='${GUEST_XDG}/cache' /usr/bin/podlaz connect --mode proxy-only \"\${id}\" >${GUEST_PRIVATE}/proxy-only.stdout 2>${GUEST_PRIVATE}/proxy-only.stderr"
  code=$?
  set -e
  [[ "${code}" == 1 ]] || return 1
  guest_exec grep -Eq 'authorization (denied|unavailable)' "${GUEST_PRIVATE}/proxy-only.stderr" >/dev/null
  wait_guest_status clean-inactive 20
}

assert_verified_active_authority() {
  guest_exec ip link show dev podlaz0 >/dev/null
  guest_exec /bin/bash -lc "cd /workspace && source scripts/e2e/lib/e2e.sh && source scripts/e2e/lib/tun_package_assertions.sh && assert_tun_package_address_present active podlaz0 198.18.0.1/32"
  # Capture live composition into guest-private files, then compare it to the
  # exact persisted active transaction and current-boot Network Session.
  guest_exec /bin/bash -lc "resolvectl dns >'${GUEST_PRIVATE}/resolved-dns.txt'"
  guest_exec /bin/bash -lc "resolvectl domain >'${GUEST_PRIVATE}/resolved-domain.txt'"
  guest_exec /bin/bash -lc "resolvectl default-route >'${GUEST_PRIVATE}/resolved-default-route.txt'"
  guest_exec /bin/bash -lc "nft -j list ruleset >'${GUEST_PRIVATE}/nft-ruleset.json'"
  guest_exec python3 "${ACTIVE_AUTHORITY_HELPER}" \
    --status "${GUEST_PRIVATE}/status.json" \
    --transactions /run/podlaz/transactions \
    --session /run/podlaz/network-session-continuation.json \
    --boot-id /proc/sys/kernel/random/boot_id \
    --runtime-config /run/podlaz/generated/xray.json \
    --resolved-dns "${GUEST_PRIVATE}/resolved-dns.txt" \
    --resolved-domain "${GUEST_PRIVATE}/resolved-domain.txt" \
    --resolved-default-route "${GUEST_PRIVATE}/resolved-default-route.txt" \
    --nft-ruleset "${GUEST_PRIVATE}/nft-ruleset.json"
  # Expansion is intentionally evaluated by guest bash.
  # shellcheck disable=SC2016
  guest_exec /bin/bash -lc 'daemon="$(systemctl show -p MainPID --value podlazd.service)"; found=false; for pid in $(pgrep -P "$daemon" 2>/dev/null || true); do if [[ "$(readlink -f "/proc/${pid}/exe" 2>/dev/null || true)" == /usr/lib/podlaz/xray ]]; then found=true; fi; done; "$found"'
  guest_exec python3 "${FALLBACK_NETWORK_HELPER}" snapshot /run/podlaz/transactions "${GUEST_MANIFEST}" >/dev/null
  guest_exec jq -e '(.routes | length) > 0 and (.rules | length) > 0' "${GUEST_MANIFEST}" >/dev/null
  guest_exec python3 "${FALLBACK_NETWORK_HELPER}" verify-present "${GUEST_MANIFEST}" >/dev/null
  assert_foreign_sentinel
}

run_active_traffic_checks() {
  guest_exec timeout 20 getent ahostsv4 example.com >/dev/null
  record_evidence tun.system_dns pass
  guest_exec timeout 30 curl -4 -fsS -o /dev/null https://example.com/
  record_evidence tun.https_tls pass
}

run_tun_doctor() {
  local doctor_code doctor_state
  set +e
  run_guest_user timeout 90 /usr/bin/podlaz doctor --tun --json >"${PRIVATE_ROOT}/doctor.json" 2>"${PRIVATE_ROOT}/doctor.stderr"
  doctor_code=$?
  set -e
  (( doctor_code == 0 )) || return 1
  doctor_state="$(python3 - "${PRIVATE_ROOT}/doctor.json" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as handle:
    report = json.load(handle)
if report.get("schema_version") != 1:
    raise SystemExit("unexpected doctor schema")
status = report.get("status")
if status == "healthy":
    print("pass")
    raise SystemExit(0)
if status != "degraded":
    raise SystemExit("doctor status is not acceptable")
if report.get("primary_classification") != "ipv6_not_present":
    raise SystemExit("doctor degradation is not the allowed topology-dependent IPv6 observation")
if report.get("errors"):
    raise SystemExit("doctor degraded report contains errors")
for probe in report.get("probes") or []:
    if probe.get("status") == "pass":
        continue
    if probe.get("classification") != "ipv6_not_present":
        raise SystemExit("doctor contains a non-topology-dependent failing probe")
print("observed")
PY
)" || return 1
  record_evidence tun.doctor "${doctor_state}"
}

assert_terminal_authority_clean() {
  guest_exec /bin/bash -lc "cd /workspace && source scripts/e2e/lib/e2e.sh && source scripts/e2e/lib/tun_package_assertions.sh && verify_tun_package_resources_absent terminal '${FALLBACK_NETWORK_HELPER}' '${GUEST_MANIFEST}'"
  guest_exec test ! -e /run/podlaz/network-session-continuation.json
  if guest_exec /bin/bash -lc "nft list tables | grep -E 'table inet podlaz_pe_[0-9a-f]+'" >/dev/null 2>&1; then
    return 1
  fi
  if guest_exec nmcli -t -f NAME,DEVICE connection show --active | grep -F ':podlaz0' >/dev/null; then
    return 1
  fi
  guest_exec nmcli -t -f NAME,DEVICE connection show --active | grep -Fx "synthetic-uplink:${GUEST_IF}" >/dev/null
  # Expansion is intentionally evaluated by guest bash.
  # shellcheck disable=SC2016
  guest_exec /bin/bash -lc 'daemon="$(systemctl show -p MainPID --value podlazd.service)"; for pid in $(pgrep -P "$daemon" 2>/dev/null || true); do [[ "$(readlink -f "/proc/${pid}/exe" 2>/dev/null || true)" != /usr/lib/podlaz/xray ]] || exit 1; done'
  assert_foreign_sentinel
}

run_clean_recovery() {
  guest_exec /bin/bash -lc "runuser -u e2e -- env XDG_CONFIG_HOME='${GUEST_XDG}/config' XDG_STATE_HOME='${GUEST_XDG}/state' XDG_CACHE_HOME='${GUEST_XDG}/cache' /usr/bin/podlaz recover --json >'${GUEST_PRIVATE}/recover.json' 2>'${GUEST_PRIVATE}/recover.stderr'"
  guest_exec /bin/bash -lc "cd /workspace && source scripts/e2e/lib/e2e.sh && source scripts/e2e/lib/recovery_json.sh && assert_clean_recovery_json_file '${GUEST_PRIVATE}/recover.json'"
}

run_scenario() {
  local import_code connect_code
  mark_failure infrastructure outer.baseline
  capture_outer_baseline
  mark_failure infrastructure guest.prepare
  prepare_system_guest
  start_system_guest
  mark_failure product candidate.provenance
  install_candidate_in_guest
  assert_guest_package_provenance
  record_evidence candidate.provenance pass

  guest_exec install -d -o e2e -g e2e -m 0700 "${GUEST_XDG}" "${GUEST_XDG}/config" "${GUEST_XDG}/state" "${GUEST_XDG}/cache" "${GUEST_PRIVATE}"
  mark_failure fixture synthetic.endpoint
  start_synthetic_xray_endpoint
  install_tun_authorization

  mark_failure diagnostic_unknown profile.import
  set +e
  guest_exec /bin/bash -lc "URI=\$(cat /run/podlaz-synthetic-xray/client-uri); runuser -u e2e -- env XDG_CONFIG_HOME='${GUEST_XDG}/config' XDG_STATE_HOME='${GUEST_XDG}/state' XDG_CACHE_HOME='${GUEST_XDG}/cache' /usr/bin/podlaz profile import \"\${URI}\" >${GUEST_PRIVATE}/import.stdout 2>${GUEST_PRIVATE}/import.stderr"
  import_code=$?
  set -e
  (( import_code == 0 )) || return 1
  guest_exec /bin/bash -lc "awk '/^Imported profile:/ {print \$3; exit}' ${GUEST_PRIVATE}/import.stdout >${GUEST_PRIVATE}/profile-id && test -s ${GUEST_PRIVATE}/profile-id"
  guest_exec /bin/bash -lc "id=\$(cat ${GUEST_PRIVATE}/profile-id); runuser -u e2e -- env XDG_CONFIG_HOME='${GUEST_XDG}/config' XDG_STATE_HOME='${GUEST_XDG}/state' XDG_CACHE_HOME='${GUEST_XDG}/cache' /usr/bin/podlaz profile validate \"\${id}\" --mode tun >${GUEST_PRIVATE}/validate.stdout 2>${GUEST_PRIVATE}/validate.stderr"

  mark_failure product ordinary_user.boundary
  assert_ordinary_user_boundary
  record_evidence ordinary_user.boundary pass
  create_foreign_sentinel
  capture_guest_network_baseline

  mark_failure diagnostic_unknown tun.connect
  set +e
  guest_exec /bin/bash -lc "id=\$(cat ${GUEST_PRIVATE}/profile-id); runuser -u e2e -- env XDG_CONFIG_HOME='${GUEST_XDG}/config' XDG_STATE_HOME='${GUEST_XDG}/state' XDG_CACHE_HOME='${GUEST_XDG}/cache' /usr/bin/podlaz connect --mode tun \"\${id}\" >${GUEST_PRIVATE}/connect.stdout 2>${GUEST_PRIVATE}/connect.stderr"
  connect_code=$?
  set -e
  (( connect_code == 0 )) || return "${connect_code}"
  wait_guest_status verified-active 120
  mark_failure diagnostic_unknown tun.authority
  assert_verified_active_authority
  record_evidence tun.verified_active pass
  mark_failure diagnostic_unknown tun.active_traffic
  run_active_traffic_checks
  mark_failure diagnostic_unknown tun.doctor
  run_tun_doctor

  mark_failure diagnostic_unknown tun.disconnect
  run_guest_user /usr/bin/podlaz disconnect >"${PRIVATE_ROOT}/disconnect.stdout" 2>"${PRIVATE_ROOT}/disconnect.stderr"
  wait_guest_status clean-inactive 80
  record_evidence tun.clean_disconnect pass
  mark_failure diagnostic_unknown tun.terminal_cleanup
  assert_terminal_authority_clean
  record_evidence tun.terminal_cleanup pass
  assert_guest_network_baseline_restored
  record_evidence guest.baseline_restored pass
  mark_failure diagnostic_unknown guest.connectivity_restored
  guest_exec timeout 20 getent ahostsv4 example.com >/dev/null
  guest_exec timeout 30 curl -4 -fsS -o /dev/null https://example.com/

  mark_failure diagnostic_unknown tun.recovery
  run_clean_recovery
  record_evidence tun.recovery_clean pass
  FAILURE_CLASS=none
  FAILURE_STEP=none
}

main() {
  (($# == 1)) || fail "usage: $0 CANDIDATE.deb"
  require_cmd awk bash cmp curl debootstrap dpkg dpkg-deb find grep ip iptables jq mktemp nft python3 readlink sha256sum ss sudo systemd-nspawn systemd-run timeout
  validate_candidate "$1"
  install -d -m 0700 "${PRIVATE_ROOT}" "${E2E_ARTIFACT_DIR}" "${XRAY_ROOT}"
  : >"${REPORT}"
  chmod 0600 "${REPORT}"
  trap teardown_all EXIT
  run_scenario
}

if [[ "${1:-}" == validate-report ]]; then
  validate_report
  exit 0
fi

main "$@"