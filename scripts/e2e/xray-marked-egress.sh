#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
MARK_DEC=20570
MARK_HEX=0x505a
ROUTE_TABLE=150
RULE_PRIORITY=150
CLIENT_NS="pzmark-client-$$"
EDGE_NS="pzmark-edge-$$"
CLIENT_IF="pzmarkc0"
EDGE_IF="pzmarke0"
CLIENT_IP="198.18.0.1"
EDGE_IP="198.18.0.2"
UUID="00000000-0000-4000-8000-000000000405"
TMP_ROOT="${E2E_TMP_ROOT:-$(mktemp -d /tmp/podlaz-xray-mark.XXXXXX)}"
ARTIFACT_DIR="${E2E_ARTIFACT_DIR:-${TMP_ROOT}/artifacts}"
PRIVATE_DIR="${TMP_ROOT}/private"
PACKAGE_ROOT="${PRIVATE_DIR}/package-root"
RUNTIME_CACHE="${PRIVATE_DIR}/runtime-cache"
XRAY="${PACKAGE_ROOT}/usr/lib/podlaz/xray"
EVIDENCE="${ARTIFACT_DIR}/xray-marked-egress.txt"
CLIENT_CONFIG="${PRIVATE_DIR}/client.json"
SERVER_CONFIG="${PRIVATE_DIR}/server.json"
EDGE_HELPER="${PRIVATE_DIR}/edge_services.py"
PROBE_HELPER="${PRIVATE_DIR}/socks_probe.py"
CLIENT_LOG="${PRIVATE_DIR}/client.log"
SERVER_LOG="${PRIVATE_DIR}/server.log"
EDGE_LOG="${PRIVATE_DIR}/edge.log"
CLIENT_PID=""
SERVER_PID=""
EDGE_PID=""
DNS_PID=""
OWN_TMP=false

if [[ -z "${E2E_TMP_ROOT:-}" ]]; then
  OWN_TMP=true
fi

record() {
  printf '%s=pass\n' "$1" >>"${EVIDENCE}"
}

fail() {
  printf 'xray-marked-egress qualification failed: %s\n' "$*" >&2
  exit 1
}

cleanup() {
  local saved=$?
  if [[ "${saved}" -ne 0 ]]; then
    printf '%s\n' '--- client Xray stdout ---' >&2
    cat "${PRIVATE_DIR}/client.stdout" >&2 2>/dev/null || true
    printf '%s\n' '--- client Xray stderr ---' >&2
    cat "${CLIENT_LOG}" >&2 2>/dev/null || true
    printf '%s\n' '--- server Xray stdout ---' >&2
    cat "${PRIVATE_DIR}/server.stdout" >&2 2>/dev/null || true
    printf '%s\n' '--- server Xray stderr ---' >&2
    cat "${SERVER_LOG}" >&2 2>/dev/null || true
    printf '%s\n' '--- synthetic edge log ---' >&2
    cat "${EDGE_LOG}" >&2 2>/dev/null || true
    printf '%s\n' '--- synthetic DNS log ---' >&2
    cat "${PRIVATE_DIR}/dnsmasq.log" >&2 2>/dev/null || true
    sudo -n ip netns exec "${CLIENT_NS}" ip rule show >&2 2>/dev/null || true
    sudo -n ip netns exec "${CLIENT_NS}" ip route show table "${ROUTE_TABLE}" >&2 2>/dev/null || true
    sudo -n ip netns exec "${CLIENT_NS}" nft list table inet pzmark >&2 2>/dev/null || true
    sudo -n ip netns exec "${EDGE_NS}" nft list table inet pzedge >&2 2>/dev/null || true
  fi
  set +e
  [[ -z "${CLIENT_PID}" ]] || sudo -n kill "${CLIENT_PID}" >/dev/null 2>&1 || true
  [[ -z "${SERVER_PID}" ]] || sudo -n kill "${SERVER_PID}" >/dev/null 2>&1 || true
  [[ -z "${EDGE_PID}" ]] || sudo -n kill "${EDGE_PID}" >/dev/null 2>&1 || true
  [[ -z "${DNS_PID}" ]] || sudo -n kill "${DNS_PID}" >/dev/null 2>&1 || true
  sudo -n ip netns del "${CLIENT_NS}" >/dev/null 2>&1 || true
  sudo -n ip netns del "${EDGE_NS}" >/dev/null 2>&1 || true
  if [[ "${OWN_TMP}" == true && "${saved}" -eq 0 ]]; then
    rm -rf "${PRIVATE_DIR}"
  fi
  exit "${saved}"
}
trap cleanup EXIT

for cmd in bash curl dnsmasq ip nft python3 sha256sum sudo unzip; do
  command -v "${cmd}" >/dev/null 2>&1 || fail "${cmd} is required"
done
sudo -n true || fail "passwordless sudo is required"
install -d -m 0700 "${PRIVATE_DIR}" "${ARTIFACT_DIR}"
: >"${EVIDENCE}"

cd "${REPO_ROOT}"
PODLAZ_RUNTIME_CACHE_DIR="${RUNTIME_CACHE}"   bash scripts/package-runtime-helpers.sh "${PACKAGE_ROOT}" amd64 amd64
"${XRAY}" version >"${ARTIFACT_DIR}/xray-version.txt"
grep -F 'Xray' "${ARTIFACT_DIR}/xray-version.txt" >/dev/null
record bundled_xray

# The test owns only this mark/rule/table. Provider sockopt fields are preserved;
# a different provider mark is an explicit conflict and never becomes authority.
provider='{"tag":"provider","protocol":"vless","streamSettings":{"network":"tcp","sockopt":{"tcpKeepAliveIdle":30,"tcpUserTimeout":10000}}}'
composed="$(python3 scripts/e2e/lib/xray_mark_qualification.py "${MARK_HEX}" "${provider}")"
python3 - "${composed}" "${MARK_DEC}" <<'PY'
import json, sys
doc=json.loads(sys.argv[1])
sock=doc["streamSettings"]["sockopt"]
assert sock["mark"] == int(sys.argv[2])
assert sock["tcpKeepAliveIdle"] == 30
assert sock["tcpUserTimeout"] == 10000
assert doc["streamSettings"]["network"] == "tcp"
PY
record provider_sockopt_coexistence

same_mark='{"tag":"provider","protocol":"vless","streamSettings":{"sockopt":{"mark":20570,"tcpKeepAliveIdle":45}}}'
python3 scripts/e2e/lib/xray_mark_qualification.py "${MARK_HEX}" "${same_mark}" >/dev/null
record equal_mark_compatible

conflicting='{"tag":"provider","protocol":"vless","streamSettings":{"sockopt":{"mark":4242,"tcpKeepAliveIdle":45}}}'
set +e
python3 scripts/e2e/lib/xray_mark_qualification.py "${MARK_HEX}" "${conflicting}" >"${PRIVATE_DIR}/conflict.stdout" 2>"${PRIVATE_DIR}/conflict.stderr"
rc=$?
set -e
[[ "${rc}" -eq 3 ]] || fail "provider mark conflict was not rejected"
grep -F 'conflicts with qualification mark' "${PRIVATE_DIR}/conflict.stderr" >/dev/null
record provider_mark_conflict

sudo -n ip netns add "${CLIENT_NS}"
sudo -n ip netns add "${EDGE_NS}"
sudo -n ip link add "${CLIENT_IF}" type veth peer name "${EDGE_IF}"
sudo -n ip link set "${CLIENT_IF}" netns "${CLIENT_NS}"
sudo -n ip link set "${EDGE_IF}" netns "${EDGE_NS}"
sudo -n ip -n "${CLIENT_NS}" link set lo up
sudo -n ip -n "${EDGE_NS}" link set lo up
sudo -n ip -n "${CLIENT_NS}" addr add "${CLIENT_IP}/32" dev "${CLIENT_IF}"
sudo -n ip -n "${EDGE_NS}" addr add "${EDGE_IP}/32" dev "${EDGE_IF}"
sudo -n ip -n "${CLIENT_NS}" link set "${CLIENT_IF}" up
sudo -n ip -n "${EDGE_NS}" link set "${EDGE_IF}" up
sudo -n ip -n "${CLIENT_NS}" route add "${EDGE_IP}/32" dev "${CLIENT_IF}" scope link src "${CLIENT_IP}" table "${ROUTE_TABLE}"
sudo -n ip -n "${CLIENT_NS}" rule add priority "${RULE_PRIORITY}" fwmark "${MARK_HEX}/0xffffffff" lookup "${ROUTE_TABLE}"
sudo -n ip -n "${EDGE_NS}" route add "${CLIENT_IP}/32" dev "${EDGE_IF}" scope link src "${EDGE_IP}"

sudo -n ip netns exec "${CLIENT_NS}" nft add table inet pzmark
sudo -n ip netns exec "${CLIENT_NS}" nft 'add chain inet pzmark output { type filter hook output priority 0; policy accept; }'
sudo -n ip netns exec "${CLIENT_NS}" nft add rule inet pzmark output oifname lo accept
sudo -n ip netns exec "${CLIENT_NS}" nft add rule inet pzmark output meta mark "${MARK_DEC}" oifname "${CLIENT_IF}" counter accept comment xray-marked
sudo -n ip netns exec "${CLIENT_NS}" nft add rule inet pzmark output oifname "${CLIENT_IF}" counter drop comment privacy-envelope

sudo -n ip netns exec "${EDGE_NS}" nft add table inet pzedge
sudo -n ip netns exec "${EDGE_NS}" nft 'add chain inet pzedge input { type filter hook input priority 0; policy accept; }'
sudo -n ip netns exec "${EDGE_NS}" nft add rule inet pzedge input tcp dport 20001 counter comment provider-a
sudo -n ip netns exec "${EDGE_NS}" nft add rule inet pzedge input tcp dport 20002 counter comment provider-b

sudo -n ip netns exec "${CLIENT_NS}" ip route get "${EDGE_IP}" mark "${MARK_HEX}" | grep -F "dev ${CLIENT_IF}" >/dev/null
if sudo -n ip netns exec "${CLIENT_NS}" ip route get "${EDGE_IP}" >/dev/null 2>&1; then
  fail "unmarked traffic unexpectedly has an endpoint route"
fi
if sudo -n ip netns exec "${CLIENT_NS}" ip route get "${EDGE_IP}" mark 4242 >/dev/null 2>&1; then
  fail "provider-controlled conflicting mark became routing authority"
fi
record policy_routing
record provider_mark_not_authority

cat >"${EDGE_HELPER}" <<'PY'
import os, selectors, socket, struct, threading

EDGE_IP=os.environ["EDGE_IP"]
LOG=os.environ["EDGE_LOG"]

def log(value):
    with open(LOG, "a", encoding="utf-8") as handle:
        handle.write(value + "\n")

def tcp_server():
    s=socket.socket()
    s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s.bind((EDGE_IP, 18080))
    s.listen()
    while True:
        conn,_=s.accept()
        with conn:
            data=conn.recv(4096)
            conn.sendall(data)

def udp_server():
    s=socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    s.bind((EDGE_IP, 18081))
    while True:
        data,addr=s.recvfrom(4096)
        s.sendto(data,addr)


for fn in (tcp_server, udp_server):
    threading.Thread(target=fn, daemon=True).start()
threading.Event().wait()
PY

cat >"${PROBE_HELPER}" <<'PY'
import socket, struct, sys

def exact(sock, n):
    out=b""
    while len(out) < n:
        chunk=sock.recv(n-len(out))
        if not chunk:
            raise RuntimeError("unexpected EOF")
        out += chunk
    return out

def negotiate(port):
    s=socket.create_connection(("127.0.0.1", port), timeout=5)
    s.sendall(b"\x05\x01\x00")
    if exact(s,2) != b"\x05\x00":
        raise RuntimeError("SOCKS auth negotiation failed")
    return s

def consume_reply(s):
    head=exact(s,4)
    if head[0] != 5 or head[1] != 0:
        raise RuntimeError(f"SOCKS request failed: {head!r}")
    atyp=head[3]
    if atyp == 1:
        exact(s,4)
    elif atyp == 3:
        exact(s, exact(s,1)[0])
    elif atyp == 4:
        exact(s,16)
    else:
        raise RuntimeError("unknown SOCKS address type")
    return struct.unpack("!H", exact(s,2))[0], atyp

mode=sys.argv[1]
port=int(sys.argv[2])
target=sys.argv[3]
target_port=int(sys.argv[4])
payload=sys.argv[5].encode()

if mode == "tcp":
    with negotiate(port) as s:
        s.sendall(b"\x05\x01\x00\x01" + socket.inet_aton(target) + struct.pack("!H", target_port))
        consume_reply(s)
        s.sendall(payload)
        if exact(s,len(payload)) != payload:
            raise RuntimeError("TCP echo mismatch")
elif mode == "udp":
    with negotiate(port) as control:
        control.sendall(b"\x05\x03\x00\x01\x00\x00\x00\x00\x00\x00")
        head=exact(control,4)
        if head[0] != 5 or head[1] != 0:
            raise RuntimeError(f"SOCKS UDP associate failed: {head!r}")
        atyp=head[3]
        if atyp == 1:
            relay_host=socket.inet_ntoa(exact(control,4))
        elif atyp == 3:
            relay_host=exact(control, exact(control,1)[0]).decode()
        elif atyp == 4:
            relay_host=socket.inet_ntop(socket.AF_INET6, exact(control,16))
        else:
            raise RuntimeError("unknown UDP relay address type")
        relay_port=struct.unpack("!H", exact(control,2))[0]
        if relay_host in ("0.0.0.0", "::"):
            relay_host="127.0.0.1"
        u=socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        u.settimeout(5)
        packet=b"\x00\x00\x00\x01" + socket.inet_aton(target) + struct.pack("!H",target_port) + payload
        u.sendto(packet,(relay_host,relay_port))
        response,_=u.recvfrom(65535)
        if response[-len(payload):] != payload:
            raise RuntimeError("UDP echo mismatch")
else:
    raise RuntimeError("unknown mode")
PY

cat >"${SERVER_CONFIG}" <<JSON
{
  "log": {"loglevel": "debug"},
  "inbounds": [
    {"tag":"vless-a","listen":"${EDGE_IP}","port":20001,"protocol":"vless","settings":{"clients":[{"id":"${UUID}"}],"decryption":"none"},"streamSettings":{"security":"none"}},
    {"tag":"vless-b","listen":"${EDGE_IP}","port":20002,"protocol":"vless","settings":{"clients":[{"id":"${UUID}"}],"decryption":"none"},"streamSettings":{"security":"none"}}
  ],
  "outbounds": [{"tag":"direct","protocol":"freedom"}]
}
JSON

cat >"${CLIENT_CONFIG}" <<JSON
{
  "log": {"loglevel": "debug"},
  "dns": {
    "queryStrategy": "UseIPv4",
    "disableFallback": true,
    "servers": [{"address":"${EDGE_IP}","port":53,"tag":"bootstrap-dns","queryStrategy":"UseIPv4","skipFallback":true}]
  },
  "inbounds": [
    {"tag":"client-a","listen":"127.0.0.1","port":1081,"protocol":"socks","settings":{"udp":true}},
    {"tag":"client-b","listen":"127.0.0.1","port":1082,"protocol":"socks","settings":{"udp":true}},
    {"tag":"client-udp","listen":"127.0.0.1","port":1083,"protocol":"socks","settings":{"udp":true}}
  ],
  "outbounds": [
    {
      "tag":"provider-a",
      "protocol":"vless",
      "settings":{"vnext":[{"address":"provider-a.example.test","port":20001,"users":[{"id":"${UUID}","encryption":"none"}]}]},
      "streamSettings":{"network":"tcp","security":"none","sockopt":{"mark":${MARK_DEC},"domainStrategy":"UseIPv4","tcpKeepAliveIdle":30}}
    },
    {
      "tag":"provider-b",
      "protocol":"vless",
      "settings":{"vnext":[{"address":"provider-b.example.test","port":20002,"users":[{"id":"${UUID}","encryption":"none"}]}]},
      "streamSettings":{"network":"tcp","security":"none","sockopt":{"mark":${MARK_DEC},"domainStrategy":"UseIPv4","tcpUserTimeout":10000}}
    },
    {
      "tag":"udp-marked",
      "protocol":"freedom",
      "settings":{"domainStrategy":"UseIPv4"},
      "streamSettings":{"sockopt":{"mark":${MARK_DEC},"domainStrategy":"UseIPv4"}}
    },
    {
      "tag":"dns-marked",
      "protocol":"freedom",
      "settings":{"domainStrategy":"UseIPv4"},
      "streamSettings":{"sockopt":{"mark":${MARK_DEC}}}
    }
  ],
  "routing": {
    "domainStrategy": "AsIs",
    "rules": [
      {"type":"field","inboundTag":["bootstrap-dns"],"outboundTag":"dns-marked"},
      {"type":"field","inboundTag":["client-a"],"outboundTag":"provider-a"},
      {"type":"field","inboundTag":["client-b"],"outboundTag":"provider-b"},
      {"type":"field","inboundTag":["client-udp"],"outboundTag":"udp-marked"}
    ]
  }
}
JSON

sudo -n env EDGE_IP="${EDGE_IP}" EDGE_LOG="${EDGE_LOG}"   ip netns exec "${EDGE_NS}" python3 "${EDGE_HELPER}" >"${PRIVATE_DIR}/edge.stdout" 2>"${PRIVATE_DIR}/edge.stderr" &
EDGE_PID=$!
sudo -n ip netns exec "${EDGE_NS}" dnsmasq \
  --keep-in-foreground \
  --bind-interfaces \
  --listen-address="${EDGE_IP}" \
  --port=53 \
  --no-resolv \
  --log-queries=extra \
  --log-facility=- \
  --address=/provider-a.example.test/"${EDGE_IP}" \
  --address=/provider-b.example.test/"${EDGE_IP}" \
  >"${PRIVATE_DIR}/dnsmasq.log" 2>&1 &
DNS_PID=$!
sudo -n ip netns exec "${EDGE_NS}" "${XRAY}" run -config "${SERVER_CONFIG}" >"${PRIVATE_DIR}/server.stdout" 2>"${SERVER_LOG}" &
SERVER_PID=$!

for _ in $(seq 1 100); do
  if sudo -n ip netns exec "${EDGE_NS}" ss -lnt | grep -q ':20002 '; then
    break
  fi
  sleep 0.05
done
sudo -n ip netns exec "${EDGE_NS}" ss -lnt | grep -q ':20001 ' || fail "provider-a server did not start"
sudo -n ip netns exec "${EDGE_NS}" ss -lnt | grep -q ':20002 ' || fail "provider-b server did not start"
record synthetic_provider_ready

if timeout 2 sudo -n ip netns exec "${CLIENT_NS}" python3 -c "import socket; socket.create_connection(('${EDGE_IP}',20001),1)" >/dev/null 2>&1; then
  fail "unmarked direct provider connection bypassed the policy boundary"
fi
record privacy_envelope_blocks_unmarked

start_client() {
  sudo -n ip netns exec "${CLIENT_NS}" "${XRAY}" run -config "${CLIENT_CONFIG}" >"${PRIVATE_DIR}/client.stdout" 2>"${CLIENT_LOG}" &
  CLIENT_PID=$!
  for _ in $(seq 1 100); do
    if sudo -n ip netns exec "${CLIENT_NS}" ss -lnt | grep -q ':1083 '; then
      return 0
    fi
    sleep 0.05
  done
  return 1
}

stop_client() {
  sudo -n kill "${CLIENT_PID}" >/dev/null 2>&1 || true
  wait "${CLIENT_PID}" >/dev/null 2>&1 || true
  CLIENT_PID=""
}

start_client || fail "client Xray did not start"
sudo -n ip netns exec "${CLIENT_NS}" python3 "${PROBE_HELPER}" tcp 1081 "${EDGE_IP}" 18080 alpha
record tcp_egress
sudo -n ip netns exec "${CLIENT_NS}" python3 "${PROBE_HELPER}" tcp 1082 "${EDGE_IP}" 18080 beta
record multiple_provider_routing
sudo -n ip netns exec "${CLIENT_NS}" python3 "${PROBE_HELPER}" udp 1083 "${EDGE_IP}" 18081 gamma
record udp_egress

for _ in $(seq 1 100); do
  grep -F 'provider-a.example.test' "${PRIVATE_DIR}/dnsmasq.log" >/dev/null 2>&1 && \
    grep -F 'provider-b.example.test' "${PRIVATE_DIR}/dnsmasq.log" >/dev/null 2>&1 && break
  sleep 0.05
done
grep -F 'provider-a.example.test' "${PRIVATE_DIR}/dnsmasq.log" >/dev/null || fail "provider-a bootstrap DNS was not observed"
grep -F 'provider-b.example.test' "${PRIVATE_DIR}/dnsmasq.log" >/dev/null || fail "provider-b bootstrap DNS was not observed"
record marked_bootstrap_dns


nft_client="$(sudo -n ip netns exec "${CLIENT_NS}" nft list chain inet pzmark output)"
grep -E 'meta mark 0x0*505a .*counter packets [1-9][0-9]*' <<<"${nft_client}" >/dev/null ||   grep -E 'meta mark 20570 .*counter packets [1-9][0-9]*' <<<"${nft_client}" >/dev/null ||   fail "marked nftables accept rule saw no traffic"
record nftables_mark_accept

nft_edge="$(sudo -n ip netns exec "${EDGE_NS}" nft list chain inet pzedge input)"
grep -E 'tcp dport 20001 counter packets [1-9][0-9]*' <<<"${nft_edge}" >/dev/null || fail "provider-a was not selected"
grep -E 'tcp dport 20002 counter packets [1-9][0-9]*' <<<"${nft_edge}" >/dev/null || fail "provider-b was not selected"
record provider_selection_observed

stop_client
start_client || fail "client Xray did not restart"
sudo -n ip netns exec "${CLIENT_NS}" python3 "${PROBE_HELPER}" tcp 1081 "${EDGE_IP}" 18080 restart-tcp
sudo -n ip netns exec "${CLIENT_NS}" python3 "${PROBE_HELPER}" udp 1083 "${EDGE_IP}" 18081 restart-udp
record restart_new_sockets

python3 - "${EVIDENCE}" <<'PY'
from pathlib import Path
import sys
required = {
    "bundled_xray",
    "provider_sockopt_coexistence",
    "equal_mark_compatible",
    "provider_mark_conflict",
    "policy_routing",
    "provider_mark_not_authority",
    "synthetic_provider_ready",
    "privacy_envelope_blocks_unmarked",
    "tcp_egress",
    "multiple_provider_routing",
    "udp_egress",
    "marked_bootstrap_dns",
    "nftables_mark_accept",
    "provider_selection_observed",
    "restart_new_sockets",
}
lines=Path(sys.argv[1]).read_text(encoding="utf-8").splitlines()
seen={line.removesuffix("=pass") for line in lines if line.endswith("=pass")}
if seen != required:
    raise SystemExit(f"qualification evidence mismatch: missing={sorted(required-seen)} extra={sorted(seen-required)}")
PY

printf 'Xray marked egress qualification passed\n'
