#!/usr/bin/env python3
"""Strict synthetic guest evidence projection. Never print arbitrary input."""
import json
import re
from pathlib import Path
import sys


def validate(path, mode, expected_source=None):
    assert expected_source and re.fullmatch(r"[0-9]+-[0-9]+",expected_source), "missing current daemon identity"
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
    complete = [group for group in by_operation.values() if group[-1]['event'] == 'operation-end']
    assert complete, 'missing daemon terminal'
    for group in complete:
        assert group[0]['event'] == 'operation-start', 'missing admission boundary'
        assert all(a['elapsed_us'] <= b['elapsed_us'] for a,b in zip(group,group[1:])), 'nonmonotonic events'
    # The driver archives/removes the stream before arming each scenario.
    if mode == 'none':
        group = complete[-1]
    else:
        expected = {'b-timeout':'command-timeout','cancelled-client':'command-failure'}.get(mode,'no-ipv4')
        matching = [g for g in complete if any(e['role']=='B' and e['event']=='connect-end' and e['dns_cause']==expected for e in g)]
        assert len(matching) == 1, 'ambiguous target operation'
        group = matching[0]
    assert len(complete) == len(by_operation), 'unfinished operation'
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
        assert terminal['session'] == 'resume' and terminal['protection_authority'] == 'armed', 'missing protected session authority'
        assert terminal['active_generation'] == generation, 'terminal not bound to current generation'
        assert terminal['product_state'] in ('Connected','Reconnecting'), 'terminal publication contradicts success'

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
            assert terminal['product_state'] != 'Connected', 'false connected after incomplete cleanup'
            if mode == 'reconciliation': event('B','reconciliation-end','failed')
        else:
            a = event('A', 'restore-end', 'failed' if mode.startswith('restore-') else 'succeeded')
            if mode.startswith('restore-'):
                assert terminal['lifecycle'] == 'restore-failed-recovery-required' and terminal['product_state'] != 'Connected', 'contradictory restore failure'
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
    return {'scenario': mode, 'verdict': 'pass', 'operations': len(by_operation), 'events': len(events)}


if __name__ == '__main__':
    try:
        result = validate(sys.argv[1], sys.argv[2], sys.argv[3])
    except (AssertionError, ValueError, KeyError, IndexError):
        raise SystemExit('connect observation qualification incomplete') from None
    print(json.dumps(result, sort_keys=True))
