"""Contract tests for the disposable selector probe, never product networking."""
import unittest

import nm_route_selection_probe as probe

# A minimal selector-shaped input. Real distro source is checked separately in CI.
SOURCE = '''preamble\nstatic NMConnectivityState
_get_best_connectivity(NMManager *self, int addr_family)
{
    NMConnectivityState best_state;
    NMDevice *dev;
    gint64 best_metric;

    if (addr_family == AF_UNSPEC) {
        return _get_best_connectivity(self, AF_INET);
    }

    nm_assert_addr_family(addr_family);

    best_state = NM_CONNECTIVITY_UNKNOWN;
    best_metric = G_MAXINT64;
    c_list_for_each_entry (dev, &priv->devices_lst_head, devices_lst) {
        const NMPObject *r;
        NMConnectivityState state;
        gint64 metric;
        if (NM_IS_DEVICE_LOOPBACK(dev))
            continue;
        r = nm_device_get_best_default_route(dev, addr_family);
        metric = r ? NMP_OBJECT_CAST_IP_ROUTE(r)->metric : G_MAXINT64;

        if (metric > best_metric) {
            continue;
        }

        state = nm_device_get_connectivity_state(dev, addr_family);
        if (metric < best_metric) {
            best_metric = metric;
            best_state = state;
        } else {
            best_state = NM_MAX_WITH_CMP(nm_connectivity_state_cmp, best_state, state);
        }

        if (nm_connectivity_state_cmp(best_state, NM_CONNECTIVITY_FULL) >= 0) {
            break;
        }
    }

    return best_state;
}

static void
device_connectivity_changed(NMDevice *device) {}
'''


SELECTOR_SOURCE = SOURCE
UPDATE_GETTER = '''
static void
update_connectivity_value(NMManager *self)
{
    NMManagerPrivate *priv = NM_MANAGER_GET_PRIVATE(self);
    NMConnectivityState best_state;
    best_state = _get_best_connectivity(self, AF_UNSPEC);
    if (best_state == priv->connectivity_state)
        return;
    priv->connectivity_state = best_state;
}
static void
get_property(NMManager *self, int property, unsigned *value)
{
    NMManagerPrivate *priv = NM_MANAGER_GET_PRIVATE(self);
    switch (property) {
    case PROP_CONNECTIVITY:
        g_value_set_uint(value, priv->connectivity_state);
        break;
    }
}
'''

SOURCE += UPDATE_GETTER

class InstrumentationTest(unittest.TestCase):
    def test_insertions_are_reversible_and_leave_selector_unchanged(self):
        result = probe.instrument_manager(SOURCE)
        self.assertNotEqual(result, SOURCE)
        self.assertEqual(probe.strip_instrumentation(result), SOURCE)
        self.assertIn('"skip"', result)
        self.assertIn('"consider"', result)
        self.assertIn('nm-selector-end', result)
        self.assertNotIn('gateway', result)

    def test_ipv4_early_return_is_not_confused_with_loop_full_break(self):
        source = SOURCE.replace('        return _get_best_connectivity(self, AF_INET);',
            '        if (nm_connectivity_state_cmp(best_state, NM_CONNECTIVITY_FULL) >= 0) {\n'
            '            return best_state;\n        }\n'
            '        return _get_best_connectivity(self, AF_INET);')
        self.assertEqual(probe.strip_instrumentation(probe.instrument_manager(source)), source)

    def test_changed_or_duplicate_selector_fails_closed(self):
        for source in (SOURCE.replace('if (metric > best_metric)', 'if (metric >= best_metric)'),
                       SOURCE + SOURCE, SOURCE.replace('nm_assert_addr_family(addr_family);', '')):
            with self.subTest(source=source[:20]), self.assertRaises(ValueError):
                probe.instrument_manager(source)

    def test_double_instrumentation_is_rejected(self):
        with self.assertRaises(ValueError):
            probe.instrument_manager(probe.instrument_manager(SOURCE))

    def test_fixture_hook_precedes_removal_of_policy_rc(self):
        original = 'prepare_system_guest() {\n  sudo -n rm -f "${policy_rc}"\n}\n    guest_exec nmcli -w 20 connection down podlaz-guest-research >/dev/null\n'
        result = probe.prepare_harness(original)
        self.assertLess(result.index('nm_route_selection_guest.sh'), result.index('rm -f'))
        self.assertEqual(probe.strip_instrumentation(result), original)
        with self.assertRaises(ValueError):
            probe.prepare_harness(result)

    def test_trace_extension_preserves_existing_validator(self):
        original = 'PUBLIC_PATTERNS = ()\n\ndef command(*args):\n    pass\n\ndef report():\n    records = concheck_records(messages, phase)\n    for record in records:\n        print(record)\n'
        result = probe.prepare_trace(original)
        self.assertEqual(probe.strip_instrumentation(result), original)
        self.assertIn('selector_records(messages, phase)', result)
        self.assertIn('PUBLIC_PATTERNS +=', result)


BEGIN = 'nm-selector-begin call=7 family=4'
UPLINK = 'nm-selector-candidate call=7 family=4 ordinal=1 event=consider role=uplink has_default=1 metric=20100 device=3 best_has_default=1 best_metric=20100 best_state=3'
TUN = 'nm-selector-candidate call=7 family=4 ordinal=2 event=skip role=tun has_default=0 metric=0 device=4 best_has_default=1 best_metric=20100 best_state=3'
END = 'nm-selector-end call=7 family=4 state=3'


class SelectorEvidenceTest(unittest.TestCase):
    def test_private_prefix_not_published_and_actual_skip_retained(self):
        records = probe.selector_records(['private [203.0.113.7] ' + line for line in (BEGIN, UPLINK, TUN, END)], 'vpn')
        self.assertEqual(len(records), 4)
        self.assertTrue(all(probe.public_selector_record(line) for line in records))
        self.assertNotIn('203.0.113.7', '\n'.join(records))
        self.assertIn('event_skip role_tun', records[2])
        self.assertIn('device_4 best_has_default_1 best_metric_20100 best_state_3', records[2])

    def test_unrelated_journal_is_ignored(self):
        self.assertEqual(probe.selector_records(['secret unrelated log'], 'managed'), [])

    def test_malformed_marker_cannot_be_silently_dropped(self):
        for suffix in (' private.example', ' metric=-1'):
            with self.assertRaises(ValueError):
                probe.selector_records([TUN + suffix], 'vpn')
        with self.assertRaises(ValueError):
            probe.selector_records([TUN.replace('metric=0', 'metric=4294967296', 1)], 'vpn')
        with self.assertRaises(ValueError):
            probe.selector_records([TUN.replace('role=tun', 'role=private.example')], 'vpn')

    def test_phase_is_not_an_arbitrary_output_string(self):
        with self.assertRaises(ValueError):
            probe.selector_records([BEGIN], 'private.example')

    def test_validation_requires_complete_calls_and_full_tun_probe_in_both_phases(self):
        lines = []
        for phase in ('managed', 'vpn'):
            lines += probe.selector_records([BEGIN, UPLINK, TUN, END], phase)
            lines += [f'nm-decision-concheck=phase_{phase} role_tun family_4 result_full reason_other']
        probe.validate_selector_lines(lines)
        for needle in ('event_return', 'phase_vpn role_tun family_4 result_full'):
            with self.assertRaises(ValueError):
                probe.validate_selector_lines([line for line in lines if needle not in line])

    def test_cumulative_windows_are_deduplicated_not_additional_runs(self):
        lines = []
        for phase in ('managed', 'vpn'):
            rows = probe.selector_records([BEGIN, UPLINK, TUN, END], phase)
            lines += rows + rows
            lines += [f'nm-decision-concheck=phase_{phase} role_tun family_4 result_full reason_other']
        probe.validate_selector_lines(lines)


if __name__ == '__main__':
    unittest.main()
