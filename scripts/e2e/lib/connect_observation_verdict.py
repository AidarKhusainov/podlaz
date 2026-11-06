#!/usr/bin/env python3
"""Strict synthetic guest evidence projection. Never print arbitrary input."""
import json
import re
import time
from pathlib import Path
import sys


def validate(path, mode, expected_source=None, boundary=None):
    assert expected_source and re.fullmatch(r"[0-9]+-[0-9]+", expected_source), "missing current daemon identity"
    assert isinstance(boundary, dict), "missing pre-request operation boundary"
    assert boundary.get('source') == expected_source, "cross-process operation boundary"
    assert type(boundary.get('last_operation')) is int and boundary['last_operation'] >= 0, "invalid previous operation"
    assert type(boundary.get('last_elapsed_us')) is int and boundary['last_elapsed_us'] >= -1, "invalid previous elapsed boundary"
    assert type(boundary.get('captured_unix')) is int, "missing pre-request capture time"
    assert type(boundary.get('window_until')) is int, "missing observer window"
    assert boundary['captured_unix'] < boundary['window_until'], "observer expired before request"
    assert boundary['last_operation'] < 16, "observer operation limit reached"
    data = Path(path).read_bytes()
    assert len(data) <= 512 * 1024, 'unbounded stream'
    assert data.endswith(b'\n'), 'partial record'
    events = [json.loads(line) for line in data.splitlines()]
    assert events, 'empty stream'
    by_operation = {}
    allowed = {'source', 'schema', 'operation', 'sequence', 'elapsed_us', 'role', 'generation', 'pass', 'event', 'outcome', 'phase', 'classification', 'dns_cause', 'rollback', 'lost_before', 'session', 'protection_authority', 'live_protection', 'lifecycle', 'product_state', 'health_generation', 'active_generation'}
    for e in events:
        assert not (e.keys() - allowed), 'unexpected field'
        assert e['schema'] == 1 and e['lost_before'] == 0, 'lost evidence'
        assert e['source'] == expected_source, 'stale or mixed daemon identity'
        assert e['role'] in ('A', 'B'), 'unknown role'
        group = by_operation.setdefault(e['operation'], [])
        assert e['sequence'] == len(group) + 1, 'sequence gap'
        group.append(e)
    # One and only one new operation must appear after the pre-request
    # snapshot. PID/start-time and a previous complete[-1] are insufficient.
    previous = boundary['last_operation']
    current = previous + 1
    assert all(type(op) is int and op > 0 for op in by_operation), 'invalid operation id'
    assert set(op for op in by_operation if op > previous) == {current}, 'missing or contradictory current operation'
    group = by_operation[current]
    assert group[0]['event'] == 'operation-start', 'missing current admission boundary'
    assert group[-1]['event'] == 'operation-end', 'missing current daemon terminal'
    assert group[0]['elapsed_us'] >= boundary['last_elapsed_us'], 'startup replay or stale elapsed boundary'
    assert all(a['elapsed_us'] <= b['elapsed_us'] for a,b in zip(group,group[1:])), 'nonmonotonic events'
    assert all(g[0]['event'] == 'operation-start' and g[-1]['event'] == 'operation-end'
               for g in by_operation.values()), 'unfinished operation'
    def event(role, name, outcome):
        matches = [e for e in group if (e['role'], e['event'], e['outcome']) == (role, name, outcome)]
        assert matches, 'missing independent boundary'
        return matches[-1]
    terminal = group[-1]
    def committed(role, generation):
        assert generation != 'unknown', 'missing generation authority'
        required = ('generation-bound','connectivity-1-end','envelope-verify-end','connectivity-2-end','commit')
        positions = []
        for name in required:
            e = event(role, name, 'succeeded')
            assert e['generation'] == generation, 'cross-generation boundary'
            positions.append(group.index(e))
        assert positions == sorted(positions) and len(set(positions)) == len(positions), 'invalid commit ordering'
        assert event(role,'connectivity-1-end','succeeded')['pass'] == 'first'
        assert event(role,'connectivity-2-end','succeeded')['pass'] == 'repeated'
    def terminal_protected(generation):
        # The terminal event intentionally performs no additional I/O; exact
        # verified-active, profile, generation and protection are asserted in
        # the independent installed-package post-operation verifier.
        assert terminal.get('active_generation') == 'unknown', 'invented terminal active authority'
        assert terminal.get('product_state') == 'Unknown', 'invented terminal status projection'
        assert generation != 'unknown', 'missing in-operation generation'

    if mode == 'none':
        event('B', 'commit', 'succeeded')
        event('B', 'connectivity-1-end', 'succeeded')
        event('B', 'connectivity-2-end', 'succeeded')
        committed('B', event('B','commit','succeeded')['generation'])
        terminal_protected(event('B','commit','succeeded')['generation'])
        assert terminal['lifecycle'] == 'returned-success', 'contradictory success result'
    else:
        b = event('B', 'connect-end', 'failed')
        expected = {'b-timeout': 'command-timeout', 'cancelled-client': 'command-failure'}.get(mode, 'no-ipv4')
        assert b['dns_cause'] == expected, 'wrong B classification'
        failed_pass = event('B', 'connectivity-2-end' if mode == 'b-second' else 'connectivity-1-end', 'failed')
        assert failed_pass['pass'] == ('repeated' if mode == 'b-second' else 'first'), 'wrong pass'
        if mode in ('rollback', 'reconciliation'):
            event('B', 'rollback-end', 'failed' if mode == 'rollback' else 'succeeded')
            assert not any(e['event'] == 'restore-start' for e in group), 'unsafe restoration admission'
            assert terminal['lifecycle'] == 'cleanup-incomplete-recovery-required', 'wrong cleanup authority'
            if mode == 'reconciliation': event('B','reconciliation-end','failed')
        else:
            a = event('A', 'restore-end', 'failed' if mode.startswith('restore-') else 'succeeded')
            if mode.startswith('restore-'):
                assert terminal['lifecycle'] == 'restore-failed-recovery-required', 'contradictory restore failure'
            if mode == 'restore-query':
                assert a['dns_cause'] == 'link-not-matched', 'A selected B cause'
                assert a['generation'] not in (b['generation'], 'unknown'), 'generations conflated'
            elif mode == 'restore-prequery':
                assert a['dns_cause'] == 'unknown' and a['generation'] == 'unknown', 'invented early A cause/generation'
            else:
                event('A', 'commit', 'succeeded')
                assert a['generation'] not in (b['generation'], 'unknown'), 'missing restored generation'
                committed('A', a['generation'])
                terminal_protected(a['generation'])
                assert terminal['lifecycle'] == 'returned-failure', 'contradictory restored terminal'
            event('B','rollback-end','succeeded')
            event('B','reconciliation-end','succeeded')
    assert terminal['live_protection'] == 'unknown', 'durable authority mistaken for live proof'
    return {'scenario': mode, 'verdict': 'pass', 'operation': current, 'operations': len(by_operation), 'events': len(events)}


def capture_boundary(path, expected_source, window_until, *, now=None):
    assert re.fullmatch(r"[0-9]+-[0-9]+", expected_source), 'invalid process identity'
    now = int(time.time()) if now is None else now
    assert type(window_until) is int and now < window_until <= now + 15 * 60, 'observer window inactive'
    raw = Path(path).read_bytes()
    assert len(raw) <= 512 * 1024, 'unbounded pre-request stream'
    assert not raw or raw.endswith(b'\n'), 'partial pre-request record'
    events = [json.loads(line) for line in raw.splitlines()]
    last_operation = 0
    last_elapsed = -1
    by_operation = {}
    for e in events:
        assert e['source'] == expected_source, 'mixed pre-request process'
        assert e['schema'] == 1 and e['lost_before'] == 0, 'pre-request event loss'
        assert type(e['operation']) is int and e['operation'] > 0, 'invalid pre-request operation'
        group = by_operation.setdefault(e['operation'], [])
        assert e['sequence'] == len(group)+1, 'pre-request sequence gap'
        group.append(e)
        last_operation = max(last_operation, e['operation'])
        last_elapsed = max(last_elapsed, e['elapsed_us'])
    assert all(g[0]['event'] == 'operation-start' and g[-1]['event'] == 'operation-end'
               for g in by_operation.values()), 'unfinished pre-request operation'
    assert last_operation < 16, 'observer operation limit reached'
    return {'source': expected_source, 'last_operation': last_operation,
            'last_elapsed_us': last_elapsed, 'captured_unix': now, 'window_until': window_until}


if __name__ == '__main__':
    try:
        if len(sys.argv) == 5 and sys.argv[1] == 'boundary':
            result = capture_boundary(sys.argv[2], sys.argv[3], int(sys.argv[4]))
        elif len(sys.argv) == 5:
            result = validate(sys.argv[1], sys.argv[2], sys.argv[3],
                              json.loads(Path(sys.argv[4]).read_text(encoding='utf-8')))
        else:
            raise AssertionError('invalid observation verdict invocation')
    except (AssertionError, ValueError, KeyError, IndexError, OSError, json.JSONDecodeError):
        raise SystemExit('connect observation qualification incomplete') from None
    print(json.dumps(result, sort_keys=True))
