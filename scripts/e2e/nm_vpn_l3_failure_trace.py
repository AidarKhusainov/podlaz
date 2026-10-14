#!/usr/bin/env python3
"""Emit one allowlisted NM guest stage, never raw stdout or addresses."""
from pathlib import Path
import sys

STAGES = frozenset(('foreign-collision','invalid-ip4','nm-activation',
                    'natural-full','uplink-churn','uplink-down',
                    'uplink-reconnect','core-preflight','core-stopped',
                    'nm-restarted','nm-restart-request','core-stop-request','cleanup-entry','cleanup-active-query','cleanup-nm-down',
                    'cleanup-owned-query','cleanup-nm-delete','cleanup-xray',
                    'cleanup-underlay-inspect','cleanup-underlay-verify',
                    'cleanup-underlay-no-inventory','cleanup-underlay-no-routes',
                    'cleanup-underlay-one-route','cleanup-underlay-extra-routes',
                    'cleanup-underlay-rule-count','cleanup-underlay-route-or-rule-mismatch',
                    'cleanup-underlay-rule','cleanup-underlay-default',
                    'cleanup-underlay-link','cleanup-nft','cleanup-intent','ipv6-sysctl','ipv6-route',
                    'ipv6-counter','ipv6-send','ipv6-observe','ipv6-no-increment','guest-cleanup'))
PROOFS = frozenset(('nm-l3-foreign-collision-rejected',
                    'nm-l3-invalid-ip4-rejected','nm-l3-real-dbus-ip4',
                    'nm-l3-natural-full','nm-l3-uplink-churn-protected',
                    'nm-l3-failure-protection','nm-l3-result'))

def last_safe_stage(lines: list[str]) -> str:
    for line in reversed(lines):
        if line.startswith('nm-l3-progress=') and line.split('=',1)[1] in STAGES:
            return line.split('=',1)[1]
        if line.endswith('=pass') and line[:-5] in PROOFS:
            return line[:-5]
    return 'not-reached'

if __name__ == '__main__':
    if len(sys.argv) != 2:
        raise SystemExit(2)
    try:
        content = Path(sys.argv[1]).read_text(encoding='utf-8').splitlines()
    except (OSError, UnicodeError):
        content = []
    print('nm-l3-last-safe-stage=' + last_safe_stage(content))
