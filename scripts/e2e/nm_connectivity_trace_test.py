#!/usr/bin/env python3
"""Fail-closed public diagnostics for the disposable NM research guests."""
import unittest
from contextlib import redirect_stdout
from io import StringIO


class ConnectivityTraceContractTest(unittest.TestCase):
    def test_ipv6_routes_and_rules_are_in_exact_snapshot(self):
        from nm_registration_evidence import COMMANDS
        commands = dict(COMMANDS)
        self.assertIn('routes6', commands)
        self.assertIn('rules6', commands)
        self.assertEqual(commands['routes6'], ('ip', '-j', '-6', 'route', 'show', 'table', 'all'))
        self.assertEqual(commands['rules6'], ('ip', '-j', '-6', 'rule', 'show'))

    def trace_module(self):
        import importlib.util
        self.assertIsNotNone(importlib.util.find_spec('nm_connectivity_trace'), 'passive trace collector missing')
        import nm_connectivity_trace
        return nm_connectivity_trace

    def test_concheck_redacts_destination_and_preserves_actual_result(self):
        trace = self.trace_module()
        lines = trace.concheck_records([
            'connectivity: (podlaz0,IPv4,42) check completed: LIMITED; timeout contacting 192.0.2.123',
            'connectivity: (host0,IPv6,43) check completed: ERROR; resolve failed for private.example',
            'connectivity: (private-iface,IPv4,44) check completed: FAKE; disabled',
            'connectivity: (podlaz0,IPv4,45) check completed: FULL; response from private.example',
        ], 'vpn')
        self.assertEqual(len(lines), 4)
        self.assertIn('result_limited reason_timeout', lines[0])
        self.assertIn('result_error reason_resolve', lines[1])
        self.assertIn('role_other family_4 result_fake', lines[2])
        self.assertIn('result_full', lines[3])
        self.assertNotIn('192.0.2.', '\n'.join(lines))
        self.assertNotIn('private', '\n'.join(lines))

    def test_route_inventory_distinguishes_main_from_policy_not_internal_winner(self):
        trace = self.trace_module()
        lines = trace.route_records([
            {'dst': 'default', 'dev': 'podlaz0', 'table': 51820, 'metric': 0},
            {'dst': 'default', 'dev': 'host0', 'metric': 20100, 'gateway': '192.0.2.1'},
            {'dst': '198.51.100.1/32', 'dev': 'host0', 'metric': 100},
        ], 'kernel', 4)
        self.assertEqual(len(lines), 2)
        self.assertIn('role_tun family_4 table_other metric_0', lines[0])
        self.assertIn('role_uplink family_4 table_main metric_20100', lines[1])
        self.assertNotIn('51820', '\n'.join(lines))
        self.assertNotIn('192.0.2.', '\n'.join(lines))
        self.assertNotIn('winner', '\n'.join(lines))

    def test_nm_routedata_default_prefix_only(self):
        trace = self.trace_module()
        self.assertEqual(trace.route_records([
            {'dest': '0.0.0.0', 'prefix': 0, 'metric': 100, 'next-hop': '192.0.2.1'},
            {'dest': '192.0.2.0', 'prefix': 24, 'metric': 0},
        ], 'nm', 4, 'host0'), [
            'nm-decision-route=source_nm role_uplink family_4 table_main metric_100 type_unicast scope_unknown'
        ])

    def test_unknown_trace_is_not_fabricated_success(self):
        trace = self.trace_module()
        self.assertEqual(trace.concheck_records(['something else containing FULL'], 'vpn'), [])
        with self.assertRaises(ValueError):
            trace.concheck_records([], 'private.example')

    def test_public_filter_rejects_trailing_or_unknown_data(self):
        trace = self.trace_module()
        safe = 'nm-decision-route=source_kernel role_tun family_4 table_other metric_0 type_unicast scope_global'
        self.assertTrue(trace.public_record(safe))
        self.assertFalse(trace.public_record(safe + ' next-hop=192.0.2.1'))
        self.assertFalse(trace.public_record('nm-decision-private=private.example'))
        self.assertFalse(trace.public_record(safe.replace('metric_0', 'metric_99999999999')))

    def test_ipv6_route_mutation_is_not_normalized(self):
        import json
        from unittest.mock import patch
        from types import SimpleNamespace
        from nm_registration_evidence import snapshot
        def execute(args, **kwargs):
            payload = [{'dst': 'default', 'dev': 'host0', 'metric': 100}]
            return SimpleNamespace(returncode=0, stdout=json.dumps(payload))
        with patch('nm_registration_evidence.subprocess.run', side_effect=execute):
            value = snapshot()
        self.assertIn('routes6', value)
        self.assertEqual(value['routes6'][0]['metric'], 100)


    def test_incomplete_artifact_does_not_validate(self):
        from pathlib import Path
        from tempfile import TemporaryDirectory
        trace = self.trace_module()
        with TemporaryDirectory() as directory:
            path = Path(directory) / 'decisions.txt'
            path.write_text('nm-decision-global=3 internal_winner_not_observed\n')
            with self.assertRaises(ValueError):
                trace.validate_output(path)

    def test_recording_rejects_private_suffix_before_printing(self):
        from pathlib import Path
        from tempfile import TemporaryDirectory
        from unittest.mock import patch
        trace = self.trace_module()
        output = StringIO()
        with TemporaryDirectory() as directory, patch('sys.stdin', StringIO(
            'nm-decision-global=3 internal_winner_not_observed private.example\n'
        )), redirect_stdout(output):
            with self.assertRaises(ValueError):
                trace.record_output(Path(directory) / 'decisions.txt')
        self.assertEqual(output.getvalue(), '')


    def test_platform_gate_is_not_reported_as_a_network_timeout(self):
        trace = self.trace_module()
        self.assertEqual(trace.concheck_records([
            'connectivity: (podlaz0,IPv4,42) check completed: LIMITED; no global route configured'
        ], 'vpn'), [
            'nm-decision-concheck=phase_vpn role_tun family_4 result_limited reason_no_global_route'
        ])

    def test_scope_and_routing_table_are_separate_observations(self):
        trace = self.trace_module()
        line = trace.route_records([
            {'dst': 'default', 'dev': 'podlaz0', 'table': 51820, 'scope': 'link'}
        ], 'kernel', 4)[0]
        self.assertIn('table_other', line)
        self.assertTrue(line.endswith('scope_link'))

    def test_workflow_keeps_decisions_outside_harness_single_file_directory(self):
        from pathlib import Path
        workflow = (Path(__file__).resolve().parents[2] / '.github/workflows/hosted-networkmanager-research.yml').read_text()
        self.assertIn('record "${NM_DECISION_ARTIFACT_DIR}/nm-connectivity-decisions.txt"', workflow)
        self.assertNotIn('record "${E2E_ARTIFACT_DIR}/nm-connectivity-decisions.txt"', workflow)


    def test_complete_artifact_requires_both_natural_cycle_windows(self):
        from pathlib import Path
        from tempfile import TemporaryDirectory
        trace = self.trace_module()
        records = [
            'nm-decision-global=3 internal_winner_not_observed',
            'nm-decision-state=role_tun ipv4_3 ipv6_1',
            'nm-decision-route=source_nm role_tun family_4 table_other metric_0 type_unicast scope_unknown',
            'nm-decision-route=source_kernel role_tun family_4 table_other metric_0 type_unicast scope_link',
        ]
        for phase in ('managed', 'vpn'):
            records.append(f'nm-decision-concheck=phase_{phase} role_tun family_4 result_limited reason_no_global_route')
            records.append(f'nm-decision-journal=phase_{phase} records_10 completions_1 elapsed_ms_330001')
        with TemporaryDirectory() as directory:
            path = Path(directory) / 'decisions.txt'
            path.write_text('\n'.join(records))
            trace.validate_output(path)
            path.write_text('\n'.join(records).replace('elapsed_ms_330001', 'elapsed_ms_1000'))
            with self.assertRaises(ValueError):
                trace.validate_output(path)


if __name__ == "__main__":
    unittest.main()
