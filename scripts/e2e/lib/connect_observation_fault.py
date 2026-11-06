#!/usr/bin/env python3
"""Disposable-guest command fault boundary; never installed by the package.

Success delegates by exec to the actual installed tool. Injected command output
is synthetic and is never evidence of a real provider/transport failure.
"""
import json
import os
from pathlib import Path
import sys

ROOT = Path('/run/podlaz/observation-fixture')
command = Path(sys.argv[0]).name
args = sys.argv[1:]
real = '/usr/bin/resolvectl' if command == 'resolvectl' else '/usr/sbin/ip'
if command not in ('ip', 'resolvectl'):
    raise SystemExit('invalid fixture command')
try:
    spec = json.loads((ROOT / 'fault.json').read_text())
except FileNotFoundError:
    os.execv(real, [real, *args])
mode = spec['mode']
if command == 'ip':
    if mode == 'rollback' and (ROOT / 'failed-query').exists() and 'del' in args and 'podlaz0' in args:
        raise SystemExit(2)
    os.execv(real, [real, *args])
if '--cache=no' not in args:
    os.execv(real, [real, *args])
count_path = ROOT / 'queries'
count = int(count_path.read_text()) + 1 if count_path.exists() else 1
count_path.write_text(str(count))
fail_at = 2 if mode == 'b-second' else 1
if mode == 'cancelled-client' and count == 2:
    import time
    (ROOT / 'restore-ready').touch(mode=0o600)
    deadline = time.monotonic() + 10
    while not (ROOT / 'restore-continue').exists():
        if time.monotonic() >= deadline:
            raise SystemExit(3)
        time.sleep(0.02)
if mode == 'restore-query' and count == 2:
    print('example.com: 192.0.2.1 -- link: other0')
    raise SystemExit(0)
if count == fail_at and mode != 'none':
    (ROOT / 'failed-query').touch(mode=0o600)
    if mode == 'restore-prequery':
        (ROOT / 'disable-core-ready').touch(mode=0o600)
        import time
        deadline = time.monotonic() + 10
        while not (ROOT / 'disable-core-continue').exists():
            if time.monotonic() >= deadline:
                raise SystemExit(3)
            time.sleep(0.02)
    if mode == 'reconciliation':
        # Exact harness-owned sentinel inside the observed table. Product must
        # refuse ambiguous composition; harness removes only this chain later.
        import subprocess
        state = json.loads(Path('/run/podlaz/network-session-continuation.json').read_text())
        subprocess.run(['/usr/sbin/nft', 'add', 'chain', 'inet', state['protection']['table'], 'observation_foreign'], check=True)
    if mode in ('b-timeout', 'cancelled-client'):
        os.execv('/usr/bin/sleep', ['sleep', '30'])
    print('example.com: 2001:db8::1 -- link: podlaz0')
    raise SystemExit(0)
os.execv(real, [real, *args])
