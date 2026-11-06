import copy
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('observation_verdict', Path(__file__).parents[1] / 'lib/connect_observation_verdict.py')
verdict = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verdict)


def restored_trace():
    rows = []
    def event(role, generation, name, outcome='succeeded', **extra):
        rows.append(dict(schema=1, operation=1, sequence=len(rows)+1, elapsed_us=len(rows)*100,
                         role=role, generation=generation, event=name, outcome=outcome,
                         phase='unknown', classification='none', dns_cause='unknown',
                         rollback='unknown', lost_before=0, **extra))
    event('B', 'unknown', 'operation-start', 'started')
    event('B', '1', 'generation-bound')
    event('B', '1', 'connectivity-1-start', 'started', **{'pass': 'first'})
    event('B', '1', 'connectivity-1-end', 'failed', **{'pass': 'first'})
    event('B', '1', 'rollback-end')
    event('B', '1', 'reconciliation-end')
    event('B', '1', 'connect-end', 'failed')
    rows[-1]['dns_cause'] = 'no-ipv4'
    event('A', 'unknown', 'restore-start', 'started')
    event('A', '2', 'generation-bound')
    event('A', '2', 'connectivity-1-end', **{'pass': 'first'})
    event('A', '2', 'envelope-verify-end')
    event('A', '2', 'connectivity-2-end', **{'pass': 'repeated'})
    event('A', '2', 'commit')
    event('A', '2', 'restore-end')
    event('B', '1', 'operation-end', 'failed', lifecycle='returned-failure', session='resume',
          protection_authority='armed', live_protection='unknown', product_state='Reconnecting', active_generation='2')
    return rows


class VerdictTest(unittest.TestCase):
    def validate(self, rows):
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / 'events.jsonl'
            path.write_text(''.join(json.dumps(r)+'\n' for r in rows))
            return verdict.validate(path, 'b-first')

    def test_reconnecting_after_verified_restore_is_accepted(self):
        self.assertEqual('pass', self.validate(restored_trace())['verdict'])

    def test_contradictory_terminal_is_rejected(self):
        rows = restored_trace()
        rows[-1].update(lifecycle='restore-failed-recovery-required', session='absent',
                        protection_authority='absent', product_state='Disconnected', active_generation='unknown')
        with self.assertRaises(AssertionError):
            self.validate(rows)

    def test_missing_restore_pass_cannot_be_hidden_by_commit(self):
        rows = [r for r in restored_trace() if r['event'] != 'connectivity-2-end']
        for i, row in enumerate(rows):
            row['sequence'] = i+1
        with self.assertRaises(AssertionError):
            self.validate(rows)

    def test_missing_terminal_loss_and_cross_generation_fail_closed(self):
        for kind in ('terminal', 'loss', 'generation'):
            rows = copy.deepcopy(restored_trace())
            if kind == 'terminal':
                rows.pop()
            elif kind == 'loss':
                rows[-1]['lost_before'] = 1
            else:
                rows[-2]['generation'] = '1'
            with self.subTest(kind=kind), self.assertRaises(AssertionError):
                self.validate(rows)
