"""A retained global result is evidence only with actual updater provenance."""
import unittest

import nm_route_selection_probe as probe
from nm_route_selection_probe_test import SELECTOR_SOURCE as SOURCE, UPDATE_GETTER, BEGIN, UPLINK, TUN, END


UPDATE = 'nm-selector-update first=7 last=8 state=3'
CACHE = [
    'nm-selector-cache-begin read=9 first=7 last=8 cached=3 vpn_active=1',
    'nm-selector-cache-device read=9 family=4 ordinal=1 role=uplink has_default=1 metric=20100 device=3',
    'nm-selector-cache-device read=9 family=4 ordinal=2 role=tun has_default=0 metric=0 device=4',
    'nm-selector-cache-device read=9 family=6 ordinal=1 role=uplink has_default=0 metric=0 device=1',
    'nm-selector-cache-device read=9 family=6 ordinal=2 role=tun has_default=0 metric=0 device=3',
    'nm-selector-cache-end read=9',
]


def natural_calls():
    first = [BEGIN, UPLINK, TUN, END]
    second = [line.replace('call=7', 'call=8').replace('family=4', 'family=6')
              for line in first]
    # The second family still reports actual inputs and its actual return;
    # tests never infer overall state with a parallel aggregation model.
    return first + second


def artifact(cache=None):
    lines = probe.selector_records(natural_calls() + [UPDATE], 'managed')
    lines += probe.selector_records(CACHE if cache is None else cache, 'vpn')
    for phase in ('managed', 'vpn'):
        lines.append(f'nm-decision-concheck=phase_{phase} role_tun family_4 result_full reason_other')
    lines.append('nm-decision-selector-vpn-window=complete')
    return lines


class CacheEvidenceContract(unittest.TestCase):
    def test_stable_vpn_needs_no_fabricated_selector_invocation(self):
        lines = artifact()
        self.assertFalse(any(line.startswith('nm-decision-selector=phase_vpn ') for line in lines))
        probe.validate_selector_lines(lines)

    def test_unknown_or_inconsistent_cache_provenance_fails(self):
        for replacement in (
            ('first=7', 'first=6'), ('last=8', 'last=9'),
            ('cached=3', 'cached=4'), ('vpn_active=1', 'vpn_active=0'),
        ):
            with self.subTest(replacement=replacement), self.assertRaises(ValueError):
                probe.validate_selector_lines(artifact([line.replace(*replacement) for line in CACHE]))

    def test_missing_current_inputs_or_probe_never_reuses_old_phase_as_proof(self):
        lines = artifact()
        for needle in (
            'event_end', 'first_7 last_8 state_3',
            'family_4 ordinal_2 role_tun', 'family_6 ordinal_2 role_tun',
            'phase_vpn role_tun family_4 result_full',
        ):
            with self.subTest(needle=needle), self.assertRaises(ValueError):
                probe.validate_selector_lines([line for line in lines if needle not in line])

    def test_last_read_must_still_be_active_and_full(self):
        ended = [line.replace('read=9', 'read=10').replace('vpn_active=1', 'vpn_active=0') for line in CACHE]
        with self.assertRaises(ValueError):
            probe.validate_selector_lines(artifact(CACHE + ended))

    def test_truncated_last_read_is_not_silently_ignored(self):
        partial = [line.replace('read=9', 'read=10') for line in CACHE[:-1]]
        with self.assertRaises(ValueError):
            probe.validate_selector_lines(artifact(CACHE + partial))

    def test_cache_rows_are_closed_bounded_and_distinct_from_actual_selection(self):
        records = probe.selector_records(['private.example ' + line for line in CACHE], 'vpn')
        self.assertTrue(all(probe.public_selector_record(line) for line in records))
        self.assertTrue(all(line.startswith('nm-decision-selector-cache=') for line in records))
        self.assertNotIn('private.example', '\n'.join(records))
        for line in (CACHE[0] + ' private.example', CACHE[1].replace('20100', '4294967296'),
                     CACHE[2].replace('role=tun', 'role=private.example')):
            with self.subTest(line=line), self.assertRaises(ValueError):
                probe.selector_records([line], 'vpn')

    def test_missing_active_window_boundary_cannot_claim_end_of_cycle(self):
        lines = artifact()
        with self.assertRaises(ValueError):
            probe.validate_selector_lines([row for row in lines if 'vpn-window=complete' not in row])

    def test_expected_post_window_disconnect_is_not_a_vpn_cycle_failure(self):
        lines = artifact()
        ended = [row.replace('read=9', 'read=10').replace('vpn_active=1', 'vpn_active=0') for row in CACHE]
        probe.validate_selector_lines(lines + probe.selector_records(ended, 'vpn'))

    def test_duplicate_windows_do_not_multiply_or_conflict_with_provenance(self):
        lines = artifact()
        probe.validate_selector_lines(lines + lines)
        conflicting = probe.selector_records([UPDATE.replace('state=3', 'state=4')], 'managed')
        with self.assertRaises(ValueError):
            probe.validate_selector_lines(conflicting + lines)


class CacheInstrumentationContract(unittest.TestCase):
    def test_read_side_only_observes_and_does_not_recompute(self):
        original = SOURCE + UPDATE_GETTER
        result = probe.instrument_manager(original)
        self.assertEqual(probe.strip_instrumentation(result), original)
        self.assertIn('_podlaz_research_cache_trace(self)', result)
        self.assertIn('nm-selector-update first=%u last=%u state=%u', result)
        # No new call to any of the functions which recompute/notify global state.
        for function in ('_get_best_connectivity(', 'update_connectivity_value('):
            self.assertEqual(result.count(function), original.count(function))
        getter = result[result.index('get_property('):]
        self.assertIn('_podlaz_research_cache_trace(self)', getter)
        self.assertIn('nm_active_connection_get_state', result)
        self.assertNotIn('CheckConnectivity', result)
        self.assertEqual(result.count('priv->connectivity_state ='), original.count('priv->connectivity_state ='))

    def test_missing_getter_or_update_anchor_is_rejected(self):
        for source in (SOURCE, SOURCE + UPDATE_GETTER.replace('    case PROP_CONNECTIVITY:', '    case OTHER:')):
            with self.assertRaises(ValueError):
                probe.instrument_manager(source)


if __name__ == '__main__':
    unittest.main()
