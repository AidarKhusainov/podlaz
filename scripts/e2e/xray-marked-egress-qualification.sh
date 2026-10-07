#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
OVERLAY="${SCRIPT_DIR}/lib/xray_mark_overlay.py"

: "${XRAY_BIN:=}"
[[ -n "${XRAY_BIN}" && -x "${XRAY_BIN}" ]] || {
  echo "XRAY_BIN must point to the bundled executable" >&2
  exit 2
}

MARK_DEC=20548
MARK_HEX=0x5044
ROUTE_TABLE=20548
NS="pz-xray-mark"
GW_NS="pz-xray-mark-gw"
TMP_ROOT="$(mktemp -d)"
CONFIG_SOURCE="${TMP_ROOT}/provider.json"
CONFIG="${TMP_ROOT}/xray.json"
CONFLICT_SOURCE="${TMP_ROOT}/provider-conflict.json"
CONFLICT_RENDERED="${TMP_ROOT}/provider-conflict-rendered.json"
FIXTURE="${TMP_ROOT}/fixture.py"
FIXTURE_LOG="${TMP_ROOT}/fixture.log"
XRAY_LOG="${TMP_ROOT}/xray.log"
XRAY_PID=""

cleanup() {
  set +e
  if [[ -n "${XRAY_PID}" ]]; then
    sudo -n kill "${XRAY_PID}" >/dev/null 2>&1 || true
    wait "${XRAY_PID}" >/dev/null 2>&1 || true
  fi
  sudo -n ip netns pids "${NS}" 2>/dev/null | xargs -r sudo -n kill >/dev/null 2>&1 || true
  sudo -n ip netns pids "${GW_NS}" 2>/dev/null | xargs -r sudo -n kill >/dev/null 2>&1 || true
  sudo -n ip netns del "${NS}" >/dev/null 2>&1 || true
  sudo -n ip netns del "${GW_NS}" >/dev/null 2>&1 || true
  rm -rf "${TMP_ROOT}"
}
trap cleanup EXIT

for cmd in curl ip nft python3; do
  command -v "${cmd}" >/dev/null 2>&1 || {
    echo "${cmd} is required" >&2
    exit 2
  }
done

cat >"${CONFIG_SOURCE}" <<'JSON'
{
  "log": {"loglevel": "warning"},
  "dns": {
    "servers": ["203.0.113.53"],
    "queryStrategy": "UseIPv4"
  },
  "inbounds": [
    {
      "tag": "socks",
      "listen": "127.0.0.1",
      "port": 1080,
      "protocol": "socks",
      "settings": {"udp": true}
    },
    {
      "tag": "udp-in",
      "listen": "127.0.0.1",
      "port": 1090,
      "protocol": "dokodemo-door",
      "settings": {
        "address": "203.0.113.10",
        "port": 19090,
        "network": "udp"
      }
    }
  ],
  "outbounds": [
    {
      "tag": "direct-a",
      "protocol": "freedom",
      "sendThrough": "192.0.2.2",
      "settings": {},
      "streamSettings": {
        "sockopt": {
          "domainStrategy": "UseIP",
          "interface": "uplink0"
        }
      }
    },
    {
      "tag": "direct-b",
      "protocol": "freedom",
      "sendThrough": "192.0.2.3",
      "settings": {},
      "streamSettings": {
        "sockopt": {
          "domainStrategy": "UseIP",
          "tcpFastOpen": false
        }
      }
    }
  ],
  "routing": {
    "domainStrategy": "AsIs",
    "rules": [
      {
        "type": "field",
        "ip": ["203.0.113.53"],
        "port": "53",
        "network": "udp",
        "outboundTag": "direct-a"
      },
      {
        "type": "field",
        "domain": ["full:tcp-a.test"],
        "outboundTag": "direct-a"
      },
      {
        "type": "field",
        "domain": ["full:tcp-b.test"],
        "outboundTag": "direct-b"
      },
      {
        "type": "field",
        "inboundTag": ["udp-in"],
        "outboundTag": "direct-b"
      }
    ]
  }
}
JSON

python3 "${OVERLAY}" "${CONFIG_SOURCE}" "${CONFIG}" --mark "${MARK_DEC}"

python3 - "${CONFIG}" "${MARK_DEC}" <<'PY'
import json
import sys

path, mark = sys.argv[1], int(sys.argv[2])
doc = json.load(open(path, encoding="utf-8"))
a, b = doc["outbounds"]
assert a["streamSettings"]["sockopt"]["mark"] == mark
assert a["streamSettings"]["sockopt"]["interface"] == "uplink0"
assert a["streamSettings"]["sockopt"]["domainStrategy"] == "UseIP"
assert b["streamSettings"]["sockopt"]["mark"] == mark
assert b["streamSettings"]["sockopt"]["tcpFastOpen"] is False
PY

cat >"${CONFLICT_SOURCE}" <<'JSON'
{
  "outbounds": [
    {
      "tag": "provider-owned-mark",
      "protocol": "freedom",
      "streamSettings": {"sockopt": {"mark": 7, "interface": "uplink0"}}
    }
  ]
}
JSON
set +e
python3 "${OVERLAY}" "${CONFLICT_SOURCE}" "${CONFLICT_RENDERED}" --mark "${MARK_DEC}" >"${TMP_ROOT}/conflict.txt" 2>&1
conflict_rc=$?
set -e
[[ "${conflict_rc}" -eq 3 ]] || {
  cat "${TMP_ROOT}/conflict.txt" >&2
  echo "provider mark conflict was not rejected" >&2
  exit 1
}
[[ ! -e "${CONFLICT_RENDERED}" ]]
grep -F "conflicts with qualification mark" "${TMP_ROOT}/conflict.txt" >/dev/null

cat >"${FIXTURE}" <<'PY'
import http.server
import ipaddress
import socket
import socketserver
import struct
import sys
import threading

log_path = sys.argv[1]
lock = threading.Lock()

def record(line):
    with lock:
        with open(log_path, "a", encoding="utf-8") as handle:
            handle.write(line + "\n")
            handle.flush()

class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        record(f"http:{self.server.server_port}:{self.client_address[0]}")
        body = ("A" if self.server.server_port == 18080 else "B").encode()
        self.send_response(200)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *_args):
        pass

def http_server(port):
    server = http.server.ThreadingHTTPServer(("203.0.113.10", port), Handler)
    server.serve_forever()

def qname(data):
    labels = []
    pos = 12
    while pos < len(data):
        size = data[pos]
        pos += 1
        if size == 0:
            break
        labels.append(data[pos:pos+size].decode("ascii"))
        pos += size
    return ".".join(labels), pos + 4

def dns_server():
    sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    sock.bind(("203.0.113.53", 53))
    while True:
        data, peer = sock.recvfrom(4096)
        name, question_end = qname(data)
        record(f"dns:{name}:{peer[0]}")
        header = data[:2] + b"\x81\x80" + data[4:6] + b"\x00\x01\x00\x00\x00\x00"
        question = data[12:question_end]
        answer = b"\xc0\x0c" + struct.pack("!HHIH", 1, 1, 30, 4) + ipaddress.ip_address("203.0.113.10").packed
        sock.sendto(header + question + answer, peer)

def udp_server():
    sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    sock.bind(("203.0.113.10", 19090))
    while True:
        data, peer = sock.recvfrom(4096)
        record(f"udp:{peer[0]}:{data.decode('ascii', 'replace')}")
        sock.sendto(data, peer)

for port in (18080, 18081):
    threading.Thread(target=http_server, args=(port,), daemon=True).start()
threading.Thread(target=dns_server, daemon=True).start()
threading.Thread(target=udp_server, daemon=True).start()
threading.Event().wait()
PY

sudo -n ip netns del "${NS}" >/dev/null 2>&1 || true
sudo -n ip netns del "${GW_NS}" >/dev/null 2>&1 || true
sudo -n ip netns add "${NS}"
sudo -n ip netns add "${GW_NS}"
sudo -n ip link add uplink0 type veth peer name gateway0
sudo -n ip link set uplink0 netns "${NS}"
sudo -n ip link set gateway0 netns "${GW_NS}"

sudo -n ip -n "${NS}" link set lo up
sudo -n ip -n "${NS}" addr add 192.0.2.2/29 dev uplink0
sudo -n ip -n "${NS}" addr add 192.0.2.3/29 dev uplink0
sudo -n ip -n "${NS}" link set uplink0 up
sudo -n ip -n "${NS}" link add podlaz0 type dummy
sudo -n ip -n "${NS}" addr add 198.18.0.2/30 dev podlaz0
sudo -n ip -n "${NS}" link set podlaz0 up
sudo -n ip -n "${NS}" route add default dev podlaz0
sudo -n ip -n "${NS}" route add table "${ROUTE_TABLE}" default via 192.0.2.1 dev uplink0
sudo -n ip -n "${NS}" rule add priority 100 fwmark "${MARK_HEX}/0xffffffff" table "${ROUTE_TABLE}"

sudo -n ip -n "${GW_NS}" link set lo up
sudo -n ip -n "${GW_NS}" addr add 192.0.2.1/29 dev gateway0
sudo -n ip -n "${GW_NS}" link set gateway0 up
sudo -n ip -n "${GW_NS}" addr add 203.0.113.10/32 dev lo
sudo -n ip -n "${GW_NS}" addr add 203.0.113.53/32 dev lo

sudo -n ip netns exec "${NS}" nft add table inet pz_xray_mark
sudo -n ip netns exec "${NS}" nft 'add chain inet pz_xray_mark output { type filter hook output priority 0; policy accept; }'
sudo -n ip netns exec "${NS}" nft add rule inet pz_xray_mark output oifname lo accept
sudo -n ip netns exec "${NS}" nft add rule inet pz_xray_mark output meta mark "${MARK_HEX}" counter accept comment podlaz-test-mark
sudo -n ip netns exec "${NS}" nft add rule inet pz_xray_mark output counter drop comment podlaz-test-drop

sudo -n ip netns exec "${GW_NS}" python3 "${FIXTURE}" "${FIXTURE_LOG}" &
fixture_pid=$!

for _ in $(seq 1 50); do
  if sudo -n ip netns exec "${GW_NS}" python3 - <<'PY'
import socket
for address in [("203.0.113.10", 18080), ("203.0.113.10", 18081)]:
    s = socket.create_connection(address, 0.2)
    s.close()
PY
  then
    break
  fi
  sleep 0.1
done

if sudo -n ip netns exec "${NS}" timeout 1 python3 - <<'PY'
import socket
socket.create_connection(("203.0.113.10", 18080), 0.5)
PY
then
  echo "unmarked ordinary egress unexpectedly escaped the test Privacy Envelope" >&2
  exit 1
fi

start_xray() {
  sudo -n ip netns exec "${NS}" "${XRAY_BIN}" run -config "${CONFIG}" >"${XRAY_LOG}" 2>&1 &
  XRAY_PID=$!
  for _ in $(seq 1 100); do
    if sudo -n ip netns exec "${NS}" ss -ltn | grep -F ':1080 ' >/dev/null; then
      return 0
    fi
    if ! sudo -n kill -0 "${XRAY_PID}" >/dev/null 2>&1; then
      cat "${XRAY_LOG}" >&2
      return 1
    fi
    sleep 0.1
  done
  cat "${XRAY_LOG}" >&2
  return 1
}

stop_xray() {
  if [[ -n "${XRAY_PID}" ]]; then
    sudo -n kill "${XRAY_PID}" >/dev/null 2>&1 || true
    wait "${XRAY_PID}" >/dev/null 2>&1 || true
    XRAY_PID=""
  fi
}

probe_tcp() {
  local host="$1" port="$2" want="$3"
  local got
  got="$(sudo -n ip netns exec "${NS}" curl --fail --silent --show-error --max-time 4 --socks5-hostname 127.0.0.1:1080 "http://${host}:${port}/")"
  [[ "${got}" == "${want}" ]]
}

probe_udp() {
  sudo -n ip netns exec "${NS}" python3 - <<'PY'
import socket
sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
sock.settimeout(3)
sock.sendto(b"udp-proof", ("127.0.0.1", 1090))
data, _ = sock.recvfrom(4096)
assert data == b"udp-proof", data
PY
}

start_xray
probe_tcp tcp-a.test 18080 A
probe_tcp tcp-b.test 18081 B
probe_udp

grep -F 'dns:tcp-a.test:192.0.2.2' "${FIXTURE_LOG}" >/dev/null
grep -F 'dns:tcp-b.test:192.0.2.2' "${FIXTURE_LOG}" >/dev/null
grep -F 'http:18080:192.0.2.2' "${FIXTURE_LOG}" >/dev/null
grep -F 'http:18081:192.0.2.3' "${FIXTURE_LOG}" >/dev/null
grep -F 'udp:192.0.2.3:udp-proof' "${FIXTURE_LOG}" >/dev/null

before_restart="$(wc -l <"${FIXTURE_LOG}")"
stop_xray
start_xray
probe_tcp tcp-a.test 18080 A
probe_udp
after_restart="$(wc -l <"${FIXTURE_LOG}")"
(( after_restart > before_restart ))

sudo -n ip netns exec "${NS}" ip rule show | grep -F "fwmark ${MARK_HEX}" >/dev/null
! sudo -n ip netns exec "${NS}" ip rule show | grep -F 'fwmark 0x7' >/dev/null
sudo -n ip netns exec "${NS}" nft list chain inet pz_xray_mark output >"${TMP_ROOT}/nft.txt"
grep -F 'comment "podlaz-test-mark"' "${TMP_ROOT}/nft.txt" >/dev/null
python3 - "${TMP_ROOT}/nft.txt" <<'PY'
import re
import sys
text = open(sys.argv[1], encoding="utf-8").read()
match = re.search(r'meta mark 0x5044 counter packets ([0-9]+)', text)
if not match or int(match.group(1)) == 0:
    raise SystemExit("marked Xray traffic did not traverse the Privacy Envelope allow rule")
PY

sudo -n kill "${fixture_pid}" >/dev/null 2>&1 || true
wait "${fixture_pid}" >/dev/null 2>&1 || true

cat <<EOF
xray_marked_egress=pass
tcp=pass
udp=pass
bootstrap_dns=pass
multi_outbound_selection=pass
restart_new_sockets=pass
provider_sockopt_preserved=pass
provider_mark_conflict=pass
privacy_envelope=pass
ownership_authority=test_mark_only
EOF
