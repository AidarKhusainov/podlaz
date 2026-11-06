import copy
import importlib.util
import json
import time
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('observation_verdict', Path(__file__).parents[1] / 'lib/connect_observation_verdict.py')
verdict = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verdict)


def restored_trace():
    rows = []
    def event(role, generation, name, outcome='succeeded', **extra):
        rows.append(dict(source="4242-42", schema=1, operation=1, sequence=len(rows)+1, elapsed_us=len(rows)*100,
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
          protection_authority='armed', live_protection='unknown', product_state='Unknown', active_generation='unknown')
    return rows


class VerdictTest(unittest.TestCase):
    def validate(self, rows, boundary=None):
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / 'events.jsonl'
            path.write_text(''.join(json.dumps(r)+'\n' for r in rows))
            now = int(time.time())
            if boundary is None:
                boundary = dict(source='4242-42', last_operation=0, last_elapsed_us=-1,
                                captured_unix=now, window_until=now+60)
            return verdict.validate(path, 'b-first', '4242-42', boundary)

    def test_reconnecting_after_verified_restore_is_accepted(self):
        self.assertEqual('pass', self.validate(restored_trace())['verdict'])

    def test_contradictory_terminal_is_rejected(self):
        rows = restored_trace()
        rows[-1].update(lifecycle='restore-failed-recovery-required', session='absent',
                        protection_authority='absent', product_state='Disconnected', active_generation='unknown')
        with self.assertRaises(AssertionError):
            self.validate(rows)

    def test_completed_old_operation_from_same_process_does_not_qualify_new_request(self):
        rows = restored_trace()
        now = int(time.time())
        with self.assertRaises(AssertionError):
            self.validate(rows, dict(source='4242-42', last_operation=1,
                                     last_elapsed_us=rows[-1]['elapsed_us'],
                                     captured_unix=now, window_until=now+60))

    def test_observer_expired_or_exhausted_before_request(self):
        now = int(time.time())
        for boundary in (
            dict(source='4242-42', last_operation=0, last_elapsed_us=-1, captured_unix=now, window_until=now),
            dict(source='4242-42', last_operation=16, last_elapsed_us=-1, captured_unix=now, window_until=now+60),
        ):
            with self.subTest(boundary=boundary), self.assertRaises(AssertionError):
                self.validate(restored_trace(), boundary)

    def test_contradictory_new_operation_or_partial_stream_rejected(self):
        rows=restored_trace()
        other=copy.deepcopy(rows[0]); other.update(operation=2,sequence=1,elapsed_us=rows[-1]['elapsed_us']+1)
        with self.assertRaises(AssertionError): self.validate(rows+[other])
        with self.assertRaises(AssertionError): self.validate(rows[:-1])
        broken=copy.deepcopy(rows)
        broken[3]['sequence']=50
        with self.assertRaises(AssertionError): self.validate(broken)

    def test_capture_boundary_requires_unexpired_process_and_complete_prior_operations(self):
        now=int(time.time())
        with tempfile.TemporaryDirectory() as d:
            file=Path(d)/'events.jsonl'
            file.write_text(''.join(json.dumps(r)+'\n' for r in restored_trace()))
            b=verdict.capture_boundary(file,'4242-42',now+60,now=now)
            self.assertEqual(1,b['last_operation'])
            with self.assertRaises(AssertionError): verdict.capture_boundary(file,'4242-42',now,now=now)
            with self.assertRaises(AssertionError): verdict.capture_boundary(file,'4243-42',now+60,now=now)
            file.write_text(''.join(json.dumps(r)+'\n' for r in restored_trace()[:-1]))
            with self.assertRaises(AssertionError): verdict.capture_boundary(file,'4242-42',now+60,now=now)

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

class FreshnessTest(unittest.TestCase):
    def test_record_without_current_daemon_identity_is_not_admissible(self):
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / 'events.jsonl'
            path.write_text(''.join(json.dumps(r)+'\n' for r in restored_trace()))
            # An immutable record from an earlier process must not qualify a
            # new operation merely because its local operation ordinal matches.
            with self.assertRaises(AssertionError):
                verdict.validate(path, 'b-first')

    def test_stale_daemon_same_ordinal_is_rejected(self):
        with tempfile.TemporaryDirectory() as temp:
            path = Path(temp) / 'events.jsonl'
            path.write_text(''.join(json.dumps(r)+'\n' for r in restored_trace()))
            for identity in ('4243-42','4242-43'):
                with self.subTest(identity=identity), self.assertRaises(AssertionError):
                    now=int(time.time())
                    verdict.validate(path, 'b-first', identity, dict(source='4242-42', last_operation=0,
                        last_elapsed_us=-1, captured_unix=now, window_until=now+60))
