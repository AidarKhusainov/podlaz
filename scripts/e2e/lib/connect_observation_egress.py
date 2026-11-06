#!/usr/bin/env python3
"""Guest-owned, calibrated unmarked uplink probe plus kernel packet counter.

The counter only observes; it never drops traffic or supplies protection. Probe
traffic uses a reserved destination. Sampling gaps are reported, never hidden.
"""
import json
from pathlib import Path
import socket
import subprocess
import sys
import time

TABLE = 'pzobs_watch'
DESTINATION = ('198.51.100.254', 54321)


def nft(*args):
    return subprocess.check_output(['/usr/sbin/nft', *args], stderr=subprocess.DEVNULL)


def run(root, calibrate=False):
    root.mkdir(mode=0o700, parents=True, exist_ok=True)
    # Existing foreign state is never adopted, replaced or removed.
    if subprocess.run(['/usr/sbin/nft', 'list', 'table', 'inet', TABLE], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0:
        raise RuntimeError('watch table occupied')
    nft('add', 'table', 'inet', TABLE)
    try:
        nft('add', 'chain', 'inet', TABLE, 'output', '{ type filter hook output priority 300; policy accept; }')
        nft('add', 'rule', 'inet', TABLE, 'output', 'oifname', 'host0', 'ip', 'daddr', DESTINATION[0], 'udp', 'dport', str(DESTINATION[1]), 'counter')
        sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        sock.setsockopt(socket.SOL_SOCKET, socket.SO_BINDTODEVICE, b'host0\0')
        start = previous = time.monotonic_ns()
        maximum_gap = attempts = 0
        (root / 'ready').touch(mode=0o600)
        while time.monotonic_ns() - start < (100_000_000 if calibrate else 120_000_000_000):
            if not calibrate and (root / 'stop').exists():
                break
            now = time.monotonic_ns()
            maximum_gap = max(maximum_gap, now - previous)
            previous = now
            try:
                sock.sendto(b'synthetic-protection-probe', DESTINATION)
            except OSError:
                pass
            attempts += 1
            time.sleep(0.001)
        sock.close()
        result = json.loads(nft('-j', 'list', 'table', 'inet', TABLE))
        packets = sum(expr['counter']['packets'] for item in result['nftables'] if 'rule' in item for expr in item['rule']['expr'] if 'counter' in expr)
        assert attempts > 0
        if calibrate:
            assert packets > 0, 'uplink probe not independently calibrated'
        else:
            assert packets == 0, 'direct egress observed'
            assert (root / 'stop').exists(), 'watch window expired'
        print(json.dumps({'calibration': calibrate, 'probe_attempts': attempts, 'direct_packets': packets, 'max_sampling_gap_ns': maximum_gap, 'duration_ns': time.monotonic_ns() - start}))
    finally:
        nft('delete', 'table', 'inet', TABLE)


if __name__ == '__main__':
    run(Path(sys.argv[1]), len(sys.argv) > 2 and sys.argv[2] == 'calibrate')
