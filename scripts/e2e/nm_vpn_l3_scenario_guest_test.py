"""Fail-closed dataplane observations for a disposable NM L3 guest."""
import unittest
from nm_vpn_l3_scenario_guest import verify_route_selection


class RouteSelectionTest(unittest.TestCase):
    def test_only_genuine_main_tun_default_and_distinct_marked_underlay(self):
        verify_route_selection(
            main_defaults=[{"dst": "default", "dev": "host0", "metric":100},
                           {"dst": "default", "dev": "podlaz0", "metric":10}],
            ordinary=[{"dst": "1.1.1.1", "dev": "podlaz0"}],
            marked=[{"dst": "1.1.1.1", "gateway": "172.31.254.1", "dev": "host0", "mark": 20570}],
            uplink="host0", mark=20570)

    def test_rejects_metadata_route_that_cannot_carry_traffic(self):
        bad = [
            [{"dst":"default", "dev":"host0", "metric":100}],
            [{"dst":"default", "dev":"host0", "metric":5}, {"dst":"default", "dev":"podlaz0", "metric":10}],
            [{"dst":"default", "dev":"host0", "metric":100}, {"dst":"default", "dev":"podlaz0", "metric":10, "table":53000}],
        ]
        for defaults in bad:
            with self.subTest(defaults=defaults), self.assertRaises(ValueError):
                verify_route_selection(defaults, [{"dev":"podlaz0"}], [{"dev":"host0", "mark":20570}], "host0", 20570)

    def test_rejects_looping_marked_socket_or_leaking_ordinary_route(self):
        defaults=[{"dst":"default", "dev":"podlaz0", "metric":10}, {"dst":"default", "dev":"host0", "metric":100}]
        for ordinary, marked in (
            ([{"dev":"host0"}], [{"dev":"host0", "mark":20570}]),
            ([{"dev":"podlaz0"}], [{"dev":"podlaz0", "mark":20570}]),
            ([{"dev":"podlaz0"}], [{"dev":"host0", "mark":33}]),
        ):
            with self.subTest(ordinary=ordinary, marked=marked), self.assertRaises(ValueError):
                verify_route_selection(defaults, ordinary, marked, "host0", 20570)


class PinnedNMReactivationTest(unittest.TestCase):
    def test_active_same_uuid_requires_no_second_activation(self):
        from unittest.mock import Mock, patch
        from nm_vpn_l3_scenario_guest import _reactivate_pinned_nm_vpn
        xray=Mock()
        xray.poll.return_value=None
        with patch('nm_vpn_l3_scenario_guest._owned_nm_profile_present',return_value=True) as present, patch('nm_vpn_l3_scenario_guest.run') as writer:
            _reactivate_pinned_nm_vpn('pinned-session-uuid', xray)
            present.assert_called_once_with('pinned-session-uuid', active=True)
            writer.assert_not_called()

    def test_missing_session_or_exited_xray_cannot_create_another_profile(self):
        from unittest.mock import Mock, patch
        from nm_vpn_l3_scenario_guest import _reactivate_pinned_nm_vpn
        for stopped in (True,False):
            with self.subTest(stopped=stopped):
                xray=Mock()
                xray.poll.return_value=1 if stopped else None
                with patch('nm_vpn_l3_scenario_guest._owned_nm_profile_present',return_value=False), patch('nm_vpn_l3_scenario_guest.run') as writer:
                    with self.assertRaises(ValueError):
                        _reactivate_pinned_nm_vpn('pinned-session-uuid', xray)
                    writer.assert_not_called()

    def test_exact_tracked_tun_is_requalified_before_uuid_reactivation(self):
        from unittest.mock import Mock, patch, call
        from nm_vpn_l3_scenario_guest import _reactivate_pinned_nm_vpn
        xray=Mock()
        xray.poll.return_value=None
        with patch('nm_vpn_l3_scenario_guest._owned_nm_profile_present',side_effect=[False,True,True]) as profiles, patch('nm_vpn_l3_scenario_guest.load_guest_intent',return_value=(Mock(),42)), patch('nm_vpn_l3_scenario_guest.ip_json',side_effect=[[{'ifindex':42}],[{'ifindex':42}]]), patch('nm_vpn_l3_scenario_guest.validate_xray_tun') as guard, patch('nm_vpn_l3_scenario_guest.run') as writer:
            _reactivate_pinned_nm_vpn('pinned-session-uuid',xray)
            guard.assert_called_once()
            writer.assert_called_once_with('nmcli','-w','40','connection','up',
                                           'pinned-session-uuid','ifname','host0',timeout=45)
            self.assertEqual(profiles.call_args_list,[
                call('pinned-session-uuid',active=True),
                call('pinned-session-uuid'),
                call('pinned-session-uuid',active=True)])

    def test_rejects_tun_ownership_collision_before_nm_mutation(self):
        from unittest.mock import Mock, patch
        from nm_vpn_l3_scenario_guest import _reactivate_pinned_nm_vpn
        xray=Mock()
        xray.poll.return_value=None
        with patch('nm_vpn_l3_scenario_guest._owned_nm_profile_present',side_effect=[False,True]), patch('nm_vpn_l3_scenario_guest.load_guest_intent',return_value=(Mock(),42)), patch('nm_vpn_l3_scenario_guest.ip_json',return_value=[]), patch('nm_vpn_l3_scenario_guest.validate_xray_tun',side_effect=ValueError('foreign TUN')) as guard, patch('nm_vpn_l3_scenario_guest.run') as writer:
            with self.assertRaises(ValueError):
                _reactivate_pinned_nm_vpn('pinned-session-uuid',xray)
            guard.assert_called_once()
            writer.assert_not_called()


class NMProfileOwnershipTest(unittest.TestCase):
    def test_exact_uuid_and_name_are_required_for_mutation(self):
        from nm_vpn_l3_scenario_guest import exact_owned_nm_profile_present
        owned='12345678-1234-4234-8234-1234567890ab'
        other='12345678-1234-4234-8234-1234567890ac'
        self.assertTrue(exact_owned_nm_profile_present([(owned,'podlaz-guest-nm-l3')],owned))
        self.assertFalse(exact_owned_nm_profile_present([(other,'synthetic-uplink')],owned))
        for inventory in ([(other,'podlaz-guest-nm-l3')],
                          [(owned,'foreign')],
                          [(owned,'podlaz-guest-nm-l3'),(other,'podlaz-guest-nm-l3')],
                          [(owned,'podlaz-guest-nm-l3'),(owned,'podlaz-guest-nm-l3')],
                          [('', 'podlaz-guest-nm-l3')]):
            with self.subTest(inventory=inventory), self.assertRaises(ValueError):
                exact_owned_nm_profile_present(inventory,owned)


class IPv6PhysicalGuardTest(unittest.TestCase):
    def test_ipv6_has_independent_drop_counter_and_physical_packet_probe(self):
        from nm_vpn_l3_scenario_guest import physical_unmarked_drop_packets
        ipv6 = {'rule': {'family':'inet','table':'podlaz_nm_l3_guest',
                         'chain':'egress','comment':'podlaz_nm_l3_ipv6',
                         'expr':[{'counter':{'packets':4}}, {'drop':None}]}}
        self.assertEqual(physical_unmarked_drop_packets(
            {'nftables':[ipv6]}, marker='podlaz_nm_l3_ipv6'), 4)
        with self.assertRaises(ValueError):
            physical_unmarked_drop_packets({'nftables':[ipv6]})
        from pathlib import Path
        source=Path(__file__).with_name('nm_vpn_l3_scenario_guest.py').read_text()
        self.assertIn("oifname','!=','podlaz0'", source)
        self.assertIn("oifname','!=','lo'", source)
        self.assertIn('socket.AF_INET6', source)
        self.assertIn('_qualify_uplink_churn(xray,plan,profile_uuid)', source)



class IPv6FirewallSocketContractTest(unittest.TestCase):
    def test_output_drop_eperm_requires_counter_proof_in_caller(self):
        import errno
        from unittest.mock import patch
        from nm_vpn_l3_scenario_guest import _send_ipv6_probe
        with patch('nm_vpn_l3_scenario_guest.socket.socket') as factory:
            sock = factory.return_value.__enter__.return_value
            sock.sendto.side_effect = PermissionError(errno.EPERM, 'Operation not permitted')
            self.assertEqual(_send_ipv6_probe('2001:db8:460::1'), 'firewall-denied')
            sock.sendto.assert_called_once()

    def test_unrelated_network_errors_still_fail_closed(self):
        import errno
        from unittest.mock import patch
        from nm_vpn_l3_scenario_guest import _send_ipv6_probe
        for code in (errno.ENETUNREACH, errno.EHOSTUNREACH,
                     errno.EADDRNOTAVAIL, errno.EINVAL):
            with self.subTest(code=code), patch('nm_vpn_l3_scenario_guest.socket.socket') as factory:
                factory.return_value.__enter__.return_value.sendto.side_effect = OSError(code, 'socket failure')
                with self.assertRaises(OSError) as failure:
                    _send_ipv6_probe('2001:db8:460::1')
                self.assertEqual(failure.exception.errno, code)

    def test_successful_send_is_not_proof_without_nft_counter(self):
        from unittest.mock import patch
        from nm_vpn_l3_scenario_guest import _send_ipv6_probe
        with patch('nm_vpn_l3_scenario_guest.socket.socket') as factory:
            factory.return_value.__enter__.return_value.sendto.return_value = 18
            self.assertEqual(_send_ipv6_probe('2001:db8:460::1'), 'sent')

class LiveNetworkManagerRestartTest(unittest.TestCase):
    def test_restart_requires_running_xray_and_owned_active_vpn(self):
        from unittest.mock import Mock, patch
        from nm_vpn_l3_scenario_guest import _qualify_live_nm_restart
        plan=Mock(tunnel_mark=20570)
        for stopped, active in ((True, True), (False, False)):
            with self.subTest(stopped=stopped, active=active):
                xray=Mock()
                xray.poll.return_value=1 if stopped else None
                with patch('nm_vpn_l3_scenario_guest._owned_nm_profile_present',
                           return_value=active), patch(
                           'nm_vpn_l3_scenario_guest.run') as mutation:
                    with self.assertRaises(ValueError):
                        _qualify_live_nm_restart(xray,plan,'pinned-uuid')
                    mutation.assert_not_called()

    def test_revalidates_underlay_before_exact_uuid_reactivation_and_full(self):
        from unittest.mock import Mock, patch
        from nm_vpn_l3_scenario_guest import _qualify_live_nm_restart
        stages=[]
        pids=iter(('1001','1002'))
        xray=Mock()
        xray.poll.return_value=None
        plan=Mock(tunnel_mark=20570)
        def command(*args, **kwargs):
            if args[:3] == ('systemctl','show','-P'):
                return next(pids)
            if args[:5] == ('nmcli','-t','-f','NAME','connection'):
                return 'synthetic-uplink\n'
            if args[:3] == ('systemctl','restart','NetworkManager.service'):
                stages.append('restart')
                return ''
            self.fail(f'unexpected guest command {args!r}')
        with patch('nm_vpn_l3_scenario_guest.run',side_effect=command), patch(
             'nm_vpn_l3_scenario_guest._owned_nm_profile_present',return_value=True), patch(
             'nm_vpn_l3_scenario_guest._prove_physical_drop'), patch(
             'nm_vpn_l3_scenario_guest._prove_ipv6_no_leak'), patch(
             'nm_vpn_l3_scenario_guest._reconcile_guest_underlay',
             side_effect=lambda p: stages.append('underlay')), patch(
             'nm_vpn_l3_scenario_guest._reactivate_pinned_nm_vpn',
             side_effect=lambda u,x: stages.append('vpn')), patch(
             'nm_vpn_l3_scenario_guest._verify_paths',
             side_effect=lambda m: stages.append('traffic')), patch(
             'nm_vpn_l3_scenario_guest._natural_full',
             side_effect=lambda x: stages.append('full')):
            _qualify_live_nm_restart(xray,plan,'pinned-uuid')
        self.assertEqual(stages,['restart','underlay','vpn','traffic','full','traffic'])

    def test_pid_reuse_or_failed_restart_must_not_trigger_reactivation(self):
        from unittest.mock import Mock, patch
        from nm_vpn_l3_scenario_guest import _qualify_live_nm_restart
        xray=Mock()
        xray.poll.return_value=None
        with patch('nm_vpn_l3_scenario_guest._owned_nm_profile_present',return_value=True), patch(
             'nm_vpn_l3_scenario_guest.run',
             side_effect=lambda *a,**k: '1001' if a[:3] == ('systemctl','show','-P') else ''), patch(
             'nm_vpn_l3_scenario_guest._prove_physical_drop'), patch(
             'nm_vpn_l3_scenario_guest._prove_ipv6_no_leak'), patch(
             'nm_vpn_l3_scenario_guest._reactivate_pinned_nm_vpn') as reactivate:
            with self.assertRaises(ValueError):
                _qualify_live_nm_restart(xray,Mock(tunnel_mark=20570),'pinned-uuid')
            reactivate.assert_not_called()


class PhysicalEgressCounterTest(unittest.TestCase):
    def test_proof_rejects_other_rules_and_missing_counter(self):
        from nm_vpn_l3_scenario_guest import physical_unmarked_drop_packets
        table='podlaz_nm_l3_guest'
        exact={'rule': {'family':'inet','table':table,'chain':'egress',
                        'comment':'podlaz_nm_l3_unmarked',
                        'expr':[{'counter':{'packets':7,'bytes':300}}, {'drop':None}]}}
        self.assertEqual(physical_unmarked_drop_packets({'nftables':[exact]}),7)
        for bad in ([], [{'rule':{**exact['rule'],'comment':'foreign'}}],
                    [{'rule':{**exact['rule'],'expr':[{'drop':None}]}}],
                    [{'rule':{**exact['rule'],'chain':'other'}}],
                    [exact,exact]):
            with self.subTest(bad=bad), self.assertRaises(ValueError):
                physical_unmarked_drop_packets({'nftables':bad})


class NegativeAcceptanceContractTest(unittest.TestCase):
    def test_invalid_ip4_must_emit_and_leave_no_address_or_main_route(self):
        from pathlib import Path
        source = Path(__file__).with_name('nm_vpn_l3_scenario_guest.py').read_text()
        adapter = Path(__file__).with_name('nm_vpn_l3_guest_plugin.py').read_text()
        self.assertIn("broken['prefix'] = dbus.UInt32(33)", adapter)
        self.assertIn("'/run/podlaz-nm-l3/bad-ip4-emitted'", adapter)
        self.assertIn('if not emitted.is_file()', source)
        self.assertIn("'NM accepted an invalid 33-bit IPv4 prefix'", source)
        self.assertIn("'rejected NM IP4 left a VPN route'", source)
        self.assertIn("'foreign profile name was mistaken for owned UUID'", source)
        self.assertIn("run('nmcli', 'connection', 'delete', foreign_uuid)", source)


class NaturalConnectivityLivenessTest(unittest.TestCase):
    def test_does_not_accept_cached_full_after_xray_has_exited(self):
        from unittest.mock import patch
        from nm_vpn_l3_scenario_guest import _natural_full

        class StoppedXray:
            def poll(self):
                return 1

        with patch('nm_vpn_l3_scenario_guest.run', return_value='full') as command:
            with self.assertRaises(ValueError):
                _natural_full(StoppedXray())
            command.assert_not_called()


if __name__=='__main__':
    unittest.main()



class UnderlayUplinkRecoveryTest(unittest.TestCase):
    def test_recovers_only_missing_exact_routes_after_verified_uplink(self):
        from nm_vpn_l3_scenario_guest import select_missing_underlay_recovery
        from nm_vpn_underlay_guest import select_underlay, UnderlayEvidence
        plan=select_underlay(UnderlayEvidence(
            'host0','172.31.254.1','172.31.254.0/30',100,
            ((0,255,0,0),(32766,254,0,0),(32767,253,0,0)),
            frozenset((253,254,255))),10)
        current_default=[{'dst':'default','dev':'host0',
                          'gateway':'172.31.254.1','metric':20100}]
        link=[{'ifname':'host0','addr_info':[
            {'family':'inet','local':'172.31.254.2','prefixlen':30}]}]
        rule=[{'priority':plan.rule_priority,'fwmark':hex(plan.tunnel_mark),
               'fwmask':'0xffffffff','table':plan.table}]
        connected={'dst':plan.connected_route[0],'dev':'host0','scope':'link','table':plan.table}
        default={'dst':'default','dev':'host0','gateway':'172.31.254.1','table':plan.table}
        decide=lambda routes,rules=rule,defaults=current_default,links=link: (
            select_missing_underlay_recovery(plan,defaults,links,routes,rules))
        self.assertEqual(decide([]),(True,True))
        self.assertEqual(decide([connected]),(False,True))
        self.assertEqual(decide([default]),(True,False))
        self.assertEqual(decide([connected,default]),(False,False))
        for routes,rules,defaults,links in (
            ([],rule,[{**current_default[0],'gateway':'172.31.254.2'}],link),
            ([],rule,[],link),
            ([],rule,current_default,[]),
            ([],rule,current_default,[{'ifname':'host0','addr_info':[]}]),
            ([],rule,current_default,[{'ifname':'host0','addr_info':[
                {'family':'inet','local':'172.31.254.1','prefixlen':30}]}]),
            ([],rule,current_default,[{'ifname':'host0','addr_info':[
                {'family':'inet','local':'172.31.254.6','prefixlen':30}]}]),
            ([],rule,current_default,[{'ifname':'foreign0','addr_info':[
                {'family':'inet','local':'172.31.254.2','prefixlen':30}]}]),
            ([],rule,current_default,[{'ifname':'host0','addr_info':[
                {'family':'inet','local':'172.31.254.2','prefixlen':30},
                {'family':'inet','local':'172.31.254.3','prefixlen':30}]}]),
            ([],[],current_default,link),
            ([{**default,'gateway':'172.31.254.2'}],rule,current_default,link),
            ([connected,connected],rule,current_default,link),
        ):
            with self.subTest(routes=routes,rules=rules,defaults=defaults,links=links), self.assertRaises(ValueError):
                decide(routes,rules,defaults,links)

class ExactUnderlayCleanup(unittest.TestCase):

    def test_kernel_removed_underlay_routes_require_no_route_delete(self):
        from nm_vpn_l3_scenario_guest import verify_owned_underlay
        from nm_vpn_underlay_guest import select_underlay, UnderlayEvidence
        plan = select_underlay(UnderlayEvidence(
            'host0','172.31.254.1','172.31.254.0/30',100,
            ((0,255,0,0),(32766,254,0,0),(32767,253,0,0)),
            frozenset((253,254,255))),10)
        rule = [{'priority':plan.rule_priority,'fwmark':hex(plan.tunnel_mark),
                 'fwmask':'0xffffffff','table':plan.table}]
        connected = {'dst':plan.connected_route[0],'dev':'host0',
                     'scope':'link','table':plan.table}
        default = {'dst':'default','gateway':plan.default_route[1],
                   'dev':'host0','table':plan.table}
        self.assertEqual(verify_owned_underlay(plan, [], rule), (True,False,False))
        self.assertEqual(verify_owned_underlay(plan, [], []), (False,False,False))
        self.assertEqual(verify_owned_underlay(plan, [connected], rule), (True,False,True))
        self.assertEqual(verify_owned_underlay(plan, [default], rule), (True,True,False))
        self.assertEqual(verify_owned_underlay(plan, [connected,default],rule), (True,True,True))
        for routes, rules in (
            ([connected],[]),
            ([connected,connected],rule),
            ([{**connected,'dev':'foreign0'}],rule),
            ([{**default,'gateway':'172.31.254.2'}],rule),
            ([connected,default,{'dst':'198.51.100.0/24','dev':'host0','table':plan.table}],rule),
            ([connected,default],[{**rule[0],'fwmark':'0x1'}]),
        ):
            with self.subTest(routes=routes,rules=rules), self.assertRaises(ValueError):
                verify_owned_underlay(plan,routes,rules)

    def test_refuses_foreign_routes_and_rule_replacements(self):
        from nm_vpn_l3_scenario_guest import verify_owned_underlay
        from nm_vpn_underlay_guest import select_underlay, UnderlayEvidence
        evidence = UnderlayEvidence('host0', '172.31.254.1', '172.31.254.0/30', 100,
                                    ((0,255,0,0),(32766,254,0,0),(32767,253,0,0)),
                                    frozenset((253,254,255)))
        plan = select_underlay(evidence,10)
        routes = [
            {'dst':'172.31.254.0/30','dev':'host0','scope':'link','table':plan.table},
            {'dst':'default','dev':'host0','gateway':'172.31.254.1','table':plan.table},
        ]
        rules = [{'priority':10000,'fwmark':'0x505a','fwmask':'0xffffffff','table':plan.table}]
        verify_owned_underlay(plan,routes,rules)
        for mutated in (
            [routes[0], {**routes[1],'gateway':'172.31.254.2'}],
            routes+[{'dst':'198.51.100.0/24','dev':'host0','table':plan.table}],
        ):
            with self.assertRaises(ValueError):
                verify_owned_underlay(plan,mutated,rules)
        with self.assertRaises(ValueError):
            verify_owned_underlay(plan,routes,[{**rules[0],'fwmark':'0x505b'}])
