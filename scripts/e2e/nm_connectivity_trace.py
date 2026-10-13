#!/usr/bin/env python3
"""Guest-only, passive NM diagnostics. Public records contain no network identities.

RouteData and kernel routes are observations, NOT the private committed L3
best_default_route used by NM's manager. No probe is forced, answered or bypassed.
"""
from __future__ import annotations

import json
import os
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path('/tmp/podlaz-hosted-synthetic-tun')
SERVICE = 'org.freedesktop.NetworkManager'
MAX_RECORDS = 20000
UINT = r'[0-9]{1,10}'
PUBLIC_PATTERNS = tuple(re.compile(pattern) for pattern in (
    rf'nm-decision-route=source_(?:nm|kernel) role_(?:tun|uplink|other) family_[46] table_(?:main|other) metric_{UINT} type_(?:unicast|other)',
    r'nm-decision-state=role_(?:tun|uplink|other|loopback) ipv4_[0-4] ipv6_[0-4]',
    r'nm-decision-global=[0-4] internal_winner_not_observed',
    r'nm-decision-concheck=phase_(?:managed|vpn) role_(?:tun|uplink|other) family_[46] result_(?:unknown|none|portal|limited|full|error|fake|cancelled|disposing) reason_(?:timeout|resolve|transport|other)',
    rf'nm-decision-journal=phase_(?:managed|vpn) records_{UINT} completions_{UINT}',
))


def command(*args: str) -> str:
    return subprocess.run(args, capture_output=True, text=True, timeout=15, check=True).stdout.strip()


def role(name: str) -> str:
    return 'tun' if name == 'podlaz0' else 'uplink' if name == 'host0' else 'other'


def uint(value: object) -> int:
    number = int(value)
    if number < 0 or number > 0xffffffff:
        raise ValueError('invalid numeric diagnostic')
    return number


def route_records(routes: list, source: str, family: int, device: str = '') -> list[str]:
    if source not in ('kernel', 'nm') or family not in (4, 6) or not isinstance(routes, list):
        raise ValueError('invalid route inventory')
    result = []
    for route in routes:
        if not isinstance(route, dict):
            raise ValueError('invalid route entry')
        is_default = (route.get('prefix') == 0 if source == 'nm'
                      else route.get('dst') in ('default', '0.0.0.0/0', '::/0'))
        if not is_default:
            continue
        table = 'main' if route.get('table', 254) in (254, '254', 'main') else 'other'
        kind = 'unicast' if route.get('type', 'unicast') in ('unicast', 1) else 'other'
        result.append(
            f'nm-decision-route=source_{source} role_{role(device or route.get("dev", ""))}'
            f' family_{family} table_{table} metric_{uint(route.get("metric", 0))} type_{kind}'
        )
    return result


def concheck_records(messages: list[str], phase: str) -> list[str]:
    if phase not in ('managed', 'vpn'):
        raise ValueError('invalid trace phase')
    pattern = re.compile(
        r'connectivity: \(([^,()]+),IPv([46]),[0-9]+\) check completed: '
        r'(UNKNOWN|NONE|PORTAL|LIMITED|FULL|ERROR|FAKE|CANCELLED|DISPOSING); (.*)'
    )
    result = []
    for message in messages:
        match = pattern.search(message)
        if not match:
            continue
        name, family, state, detail = match.groups()
        detail = detail.lower()
        reason = ('timeout' if 'timeout' in detail or 'timed out' in detail else
                  'resolve' if 'resolve' in detail or 'resolution' in detail else
                  'transport' if 'curl' in detail else 'other')
        result.append(f'nm-decision-concheck=phase_{phase} role_{role(name)} family_{family}'
                      f' result_{state.lower()} reason_{reason}')
    return result


def public_record(line: str) -> bool:
    if not any(pattern.fullmatch(line) for pattern in PUBLIC_PATTERNS):
        return False
    return all(int(number) <= 0xffffffff for number in re.findall(r'_(\d+)\b', line))


def private_trace_path(snapshot: Path) -> Path:
    if snapshot.parent != ROOT or ROOT.is_symlink() or ROOT.stat().st_mode & 0o077:
        raise ValueError('private guest directory required')
    if snapshot.name not in ('nm-before.json', 'nm-vpn-before.json'):
        raise ValueError('unknown research phase')
    return snapshot.with_suffix('.concheck.json')


def start_trace(snapshot: Path) -> None:
    path = private_trace_path(snapshot)
    entries = command('journalctl', '--no-pager', '-u', 'NetworkManager', '-n', '1', '-o', 'json')
    entry = json.loads(entries)
    cursor = entry['__CURSOR']
    if not isinstance(cursor, str) or not cursor:
        raise ValueError('journal cursor unavailable')
    descriptor = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY | os.O_NOFOLLOW, 0o600)
    with os.fdopen(descriptor, 'w', encoding='utf-8') as handle:
        json.dump({'cursor': cursor}, handle)
    # Runtime logging only. This guest (including its private journal) is destroyed
    # by the harness; no persistent NM configuration or network state is changed.
    command('nmcli', 'general', 'logging', 'level', 'TRACE', 'domains', 'CONCHECK,CORE')


def report_trace(snapshot: Path) -> None:
    path = private_trace_path(snapshot)
    if path.is_symlink() or path.stat().st_mode & 0o077:
        raise ValueError('unsafe private trace')
    cursor = json.loads(path.read_text(encoding='utf-8'))['cursor']
    if not isinstance(cursor, str) or not cursor:
        raise ValueError('invalid journal cursor')
    raw = command('journalctl', '--no-pager', '-u', 'NetworkManager', '-o', 'json',
                  '--after-cursor', cursor, '-n', str(MAX_RECORDS + 1))
    lines = raw.splitlines()
    if len(lines) > MAX_RECORDS:
        raise ValueError('trace window exceeded')
    messages = [json.loads(line)['MESSAGE'] for line in lines]
    if not all(isinstance(message, str) for message in messages):
        raise ValueError('invalid journal message')
    phase = 'vpn' if snapshot.name == 'nm-vpn-before.json' else 'managed'
    records = concheck_records(messages, phase)
    print(f'nm-decision-journal=phase_{phase} records_{len(lines)} completions_{len(records)}')
    for record in records:
        print(record)
    observe()


def observe() -> None:
    try:
        _observe()
    except Exception:
        # D-Bus failures may carry private values. Fail without echoing them.
        raise RuntimeError("private NM inspection failed") from None


def _observe() -> None:
    # python3-dbus is already a research-guest dependency, not a package dependency.
    import dbus
    bus = dbus.SystemBus()
    root = bus.get_object(SERVICE, '/org/freedesktop/NetworkManager')
    props = dbus.Interface(root, 'org.freedesktop.DBus.Properties')
    global_state = uint(props.Get(SERVICE, 'Connectivity'))
    if global_state > 4:
        raise ValueError('invalid global connectivity')
    print(f'nm-decision-global={global_state} internal_winner_not_observed')
    for device_path in dbus.Interface(root, SERVICE).GetDevices():
        device = dbus.Interface(bus.get_object(SERVICE, device_path), 'org.freedesktop.DBus.Properties')
        name = str(device.Get(SERVICE + '.Device', 'Interface'))
        values = [uint(device.Get(SERVICE + '.Device', f'Ip{family}Connectivity')) for family in (4, 6)]
        if max(values) > 4:
            raise ValueError('invalid device connectivity')
        label = 'loopback' if name == 'lo' else role(name)
        print(f'nm-decision-state=role_{label} ipv4_{values[0]} ipv6_{values[1]}')
        for family in (4, 6):
            ip_path = str(device.Get(SERVICE + '.Device', f'Ip{family}Config'))
            if ip_path == '/':
                continue
            ip_props = dbus.Interface(bus.get_object(SERVICE, ip_path), 'org.freedesktop.DBus.Properties')
            routes = ip_props.Get(f'{SERVICE}.IP{family}Config', 'RouteData')
            for record in route_records([dict(route) for route in routes], 'nm', family, name):
                print(record)
    for family in (4, 6):
        routes = json.loads(command('ip', '-j', f'-{family}', 'route', 'show', 'table', 'all'))
        for record in route_records(routes, 'kernel', family):
            print(record)


def record_output(path: Path) -> None:
    count = 0
    descriptor = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY | os.O_NOFOLLOW, 0o600)
    with os.fdopen(descriptor, 'w', encoding='utf-8') as handle:
        for line in sys.stdin:
            line = line.rstrip('\n')
            if line.startswith('nm-decision-'):
                if not public_record(line) or count >= MAX_RECORDS:
                    raise ValueError('invalid public decision record')
                handle.write(line + '\n')
                handle.flush()
                count += 1
            print(line, flush=True)


def validate_output(path: Path) -> None:
    lines = path.read_text(encoding='utf-8').splitlines()
    if not lines or len(lines) > MAX_RECORDS or not all(public_record(line) for line in lines):
        raise ValueError('invalid public decision artifact')
    for required in ('nm-decision-global=', 'nm-decision-state=',
                     'nm-decision-route=source_kernel', 'nm-decision-route=source_nm',
                     'nm-decision-concheck=phase_managed', 'nm-decision-concheck=phase_vpn'):
        if not any(line.startswith(required) for line in lines):
            raise ValueError('missing passive connectivity evidence')


if __name__ == '__main__':
    try:
        if len(sys.argv) != 3 or sys.argv[1] not in ('record', 'validate'):
            raise ValueError('invalid invocation')
        (record_output if sys.argv[1] == 'record' else validate_output)(Path(sys.argv[2]))
    except (OSError, ValueError, RuntimeError):
        print('nm-connectivity-trace=failed', file=sys.stderr)
        sys.exit(1)
