from __future__ import annotations

import copy
import sys
import time
import unittest
from unittest import mock

from scripts.e2e.lib import tun_soak_isolation


class TunSoakIsolationTests(unittest.TestCase):
    def baseline(self) -> dict[str, object]:
        return {
            "schema_version": 1,
            "network_namespace_inode": 101,
            "links": [
                {
                    "ifindex": 1,
                    "ifname": "lo",
                    "kind": "",
                    "master": None,
                    "mtu": 65536,
                    "link_type": "loopback",
                    "flags": ["LOOPBACK", "LOWER_UP", "UP"],
                },
                {
                    "ifindex": 2,
                    "ifname": "eth0",
                    "kind": "",
                    "master": None,
                    "mtu": 1500,
                    "link_type": "ether",
                    "flags": ["BROADCAST", "LOWER_UP", "MULTICAST", "UP"],
                },
            ],
            "addresses": [
                {
                    "ifindex": 1,
                    "ifname": "lo",
                    "addresses": [
                        {
                            "family": "inet",
                            "local": "127.0.0.1",
                            "prefixlen": 8,
                            "scope": "host",
                            "label": "lo",
                            "flags": [],
                            "extras": {},
                        }
                    ],
                },
                {
                    "ifindex": 2,
                    "ifname": "eth0",
                    "addresses": [
                        {
                            "family": "inet",
                            "local": "192.0.2.20",
                            "prefixlen": 24,
                            "scope": "global",
                            "label": "eth0",
                            "flags": ["dynamic"],
                            "extras": {"broadcast": "192.0.2.255"},
                        }
                    ],
                },
            ],
            "rules_v4": [
                self.rule("ipv4", 0, "local"),
                self.rule("ipv4", 32766, "main"),
                self.rule("ipv4", 32767, "default"),
            ],
            "rules_v6": [
                self.rule("ipv6", 0, "local"),
                self.rule("ipv6", 32766, "main"),
            ],
            "routes_v4": [
                self.route(
                    "ipv4",
                    "local",
                    "127.0.0.0/8",
                    dev="lo",
                    protocol="kernel",
                    scope="host",
                    prefsrc="127.0.0.1",
                    type="local",
                ),
                self.route(
                    "ipv4",
                    "local",
                    "127.0.0.1/32",
                    dev="lo",
                    protocol="kernel",
                    scope="host",
                    prefsrc="127.0.0.1",
                    type="local",
                ),
                self.route(
                    "ipv4",
                    "local",
                    "127.255.255.255/32",
                    dev="lo",
                    protocol="kernel",
                    scope="link",
                    prefsrc="127.0.0.1",
                    type="broadcast",
                ),
                self.route(
                    "ipv4",
                    "local",
                    "192.0.2.20/32",
                    dev="eth0",
                    protocol="kernel",
                    scope="host",
                    prefsrc="192.0.2.20",
                    type="local",
                ),
                self.route(
                    "ipv4",
                    "local",
                    "192.0.2.255/32",
                    dev="eth0",
                    protocol="kernel",
                    scope="link",
                    prefsrc="192.0.2.20",
                    type="broadcast",
                ),
                self.route("ipv4", "main", "default", gateway="192.0.2.1", dev="eth0", protocol="dhcp"),
                self.route(
                    "ipv4",
                    "main",
                    "192.0.2.0/24",
                    dev="eth0",
                    protocol="kernel",
                    scope="link",
                    prefsrc="192.0.2.20",
                ),
            ],
            "routes_v6": [],
            "nftables": [],
            "resolved": {
                "global": ["resolv.conf mode: stub"],
                "links": [
                    {
                        "ifname": "eth0",
                        "lines": [
                            "Current Scopes: DNS",
                            "DefaultRoute setting: yes",
                        ],
                    }
                ],
            },
            "network_manager": [
                {
                    "uuid": "11111111-2222-3333-4444-555555555555",
                    "device": "eth0",
                    "state": "activated",
                }
            ],
            "runtime_os": {"id": "ubuntu", "version_id": "24.04"},
        }

    @staticmethod
    def rule(family: str, priority: int, table: str, **overrides: object) -> dict[str, object]:
        value: dict[str, object] = {
            "family": family,
            "priority": priority,
            "table": table,
            "action": "to_tbl",
            "source": "all",
            "destination": "all",
            "fwmark": "",
            "fwmask": "",
            "iif": "",
            "oif": "",
            "l3mdev": False,
            "suppress_prefixlength": None,
            "uidrange": "",
        }
        value.update(overrides)
        return value

    @staticmethod
    def route(
        family: str,
        table: str,
        dst: str,
        *,
        gateway: str = "",
        dev: str = "",
        protocol: str = "",
        scope: str = "",
        **overrides: object,
    ) -> dict[str, object]:
        value: dict[str, object] = {
            "family": family,
            "table": table,
            "type": "unicast",
            "dst": dst,
            "gateway": gateway,
            "dev": dev,
            "protocol": protocol,
            "scope": scope,
            "metric": None,
            "prefsrc": "",
            "src": "",
            "mark": "",
            "nhid": None,
            "multipath": [],
            "flags": [],
            "preference": "",
        }
        value.update(overrides)
        return value

    def active_snapshot_and_manifest(self) -> tuple[dict[str, object], dict[str, object], dict[str, object]]:
        baseline = self.baseline()
        current = copy.deepcopy(baseline)
        current["links"].append(
            {
                "ifindex": 9,
                "ifname": "podlaz0",
                "kind": "tun",
                "master": None,
                "mtu": 1500,
                "link_type": "none",
            }
        )
        current["routes_v4"].extend(
            [
                self.route("ipv4", "main", "198.51.100.7/32", gateway="192.0.2.1", dev="eth0"),
                self.route("ipv4", "51820", "default", dev="podlaz0", scope="link"),
            ]
        )
        current["rules_v4"].extend(
            [
                self.rule("ipv4", 9999, "main", destination="198.51.100.7/32"),
                self.rule("ipv4", 10000, "51820"),
            ]
        )
        current["nftables"] = [
            {"table": {"family": "inet", "name": "podlaz"}},
            {"chain": {"family": "inet", "table": "podlaz", "name": "output"}},
        ]
        current["resolved"]["links"].append(
            {
                "ifname": "podlaz0",
                "lines": ["DNS Domain: ~.", "DefaultRoute setting: yes"],
            }
        )
        manifest = {
            "routes": [
                self.route(
                    "ipv4",
                    "main",
                    "198.51.100.7/32",
                    gateway="192.0.2.1",
                    dev="eth0",
                ),
                self.route(
                    "ipv4",
                    "51820",
                    "default",
                    dev="podlaz0",
                    scope="link",
                ),
            ],
            "rules": [
                self.rule(
                    "ipv4",
                    9999,
                    "main",
                    destination="198.51.100.7/32",
                ),
                self.rule("ipv4", 10000, "51820"),
            ],
        }
        return baseline, current, manifest

    def test_command_output_limit_terminates_producer_before_timeout(self) -> None:
        started = time.monotonic()
        with mock.patch.object(tun_soak_isolation, "MAX_COMMAND_BYTES", 1024), mock.patch.object(
            tun_soak_isolation, "COMMAND_TIMEOUT_SECONDS", 5
        ):
            with self.assertRaisesRegex(tun_soak_isolation.IsolationError, "byte limit"):
                tun_soak_isolation._bounded_command(
                    [
                        sys.executable,
                        "-c",
                        "import sys,time; sys.stdout.write('x'*2048); sys.stdout.flush(); time.sleep(30)",
                    ]
                )
        self.assertLess(time.monotonic() - started, 2.0)

    def test_command_timeout_terminates_hanging_inspection(self) -> None:
        started = time.monotonic()
        with mock.patch.object(tun_soak_isolation, "COMMAND_TIMEOUT_SECONDS", 0.2):
            with self.assertRaisesRegex(tun_soak_isolation.IsolationError, "did not complete"):
                tun_soak_isolation._bounded_command([sys.executable, "-c", "import time; time.sleep(30)"])
        self.assertLess(time.monotonic() - started, 2.0)

    def test_normalizes_link_addresses_without_lifetime_noise(self) -> None:
        raw = {
            "links": [
                {
                    "ifindex": 2,
                    "ifname": "eth0",
                    "mtu": 1500,
                    "link_type": "ether",
                    "flags": ["BROADCAST", "LOWER_UP", "MULTICAST", "UP"],
                }
            ],
            "addresses": [
                {
                    "ifindex": 2,
                    "ifname": "eth0",
                    "addr_info": [
                        {
                            "family": "inet",
                            "local": "192.0.2.20",
                            "prefixlen": 24,
                            "scope": "global",
                            "label": "eth0",
                            "dynamic": True,
                            "valid_life_time": 3456,
                            "preferred_life_time": 1234,
                        }
                    ],
                }
            ],
            "rules_v4": [],
            "rules_v6": [],
            "routes_v4": [],
            "routes_v6": [],
            "nftables": {"nftables": []},
            "resolved": "Global\n",
            "network_manager": "11111111-2222-3333-4444-555555555555:eth0:activated\n",
            "runtime_os": {"id": "ubuntu", "version_id": "24.04"},
        }

        normalized = tun_soak_isolation.normalize_snapshot(raw, network_namespace_inode=101)

        self.assertEqual(
            [
                {
                    "ifindex": 2,
                    "ifname": "eth0",
                    "kind": "",
                    "master": None,
                    "mtu": 1500,
                    "link_type": "ether",
                    "flags": ["BROADCAST", "LOWER_UP", "MULTICAST", "UP"],
                }
            ],
            normalized["links"],
        )
        self.assertEqual(
            [
                {
                    "ifindex": 2,
                    "ifname": "eth0",
                    "addresses": [
                        {
                            "family": "inet",
                            "local": "192.0.2.20",
                            "prefixlen": 24,
                            "scope": "global",
                            "label": "eth0",
                            "flags": ["dynamic"],
                            "extras": {},
                        }
                    ],
                }
            ],
            normalized["addresses"],
        )

    def test_rule_normalization_rejects_unknown_semantic_selector(self) -> None:
        with self.assertRaisesRegex(
            tun_soak_isolation.IsolationError,
            "unsupported policy-rule field",
        ):
            tun_soak_isolation._normalize_rules(
                [
                    {
                        "priority": 32766,
                        "src": "all",
                        "table": "main",
                        "sport": "443",
                    }
                ],
                "ipv4",
            )

    def test_route_normalization_rejects_unknown_semantic_attribute(self) -> None:
        with self.assertRaisesRegex(
            tun_soak_isolation.IsolationError,
            "unsupported route field",
        ):
            tun_soak_isolation._normalize_routes(
                [
                    {
                        "dst": "default",
                        "gateway": "192.0.2.1",
                        "dev": "eth0",
                        "flags": [],
                        "ttl-propagate": True,
                    }
                ],
                "ipv4",
            )

    def test_route_normalization_preserves_semantic_flags(self) -> None:
        normalized = tun_soak_isolation._normalize_routes(
            [
                {
                    "dst": "default",
                    "gateway": "192.0.2.1",
                    "dev": "eth0",
                    "flags": ["onlink"],
                }
            ],
            "ipv4",
        )

        self.assertEqual(["onlink"], normalized[0]["flags"])

    def test_foreign_uplink_address_mutation_fails_revalidation(self) -> None:
        baseline = self.baseline()
        current = copy.deepcopy(baseline)
        current["addresses"][1]["addresses"].append(
            {
                "family": "inet",
                "local": "198.51.100.20",
                "prefixlen": 32,
                "scope": "global",
                "label": "eth0",
                "flags": ["noprefixroute"],
                "extras": {},
            }
        )

        with self.assertRaisesRegex(tun_soak_isolation.IsolationError, "network state changed"):
            tun_soak_isolation.assert_matches_baseline(baseline=baseline, current=current)

    def test_rejects_inspection_outside_host_network_namespace(self) -> None:
        host = mock.Mock(st_ino=101)
        current = mock.Mock(st_ino=202)
        with mock.patch.object(tun_soak_isolation.os, "stat", side_effect=[host, current]):
            with self.assertRaisesRegex(tun_soak_isolation.IsolationError, "outside the host network namespace"):
                tun_soak_isolation._network_namespace_inode()

    def test_clean_dedicated_host_baseline_is_accepted(self) -> None:
        tun_soak_isolation.validate_clean_baseline(self.baseline())

    def test_kernel_derived_ipv6_local_route_is_accepted(self) -> None:
        snapshot = self.baseline()
        snapshot["addresses"][0]["addresses"].append(
            {
                "family": "inet6",
                "local": "::1",
                "prefixlen": 128,
                "scope": "host",
                "label": "lo",
                "flags": [],
                "extras": {"protocol": "kernel_lo"},
            }
        )
        snapshot["routes_v6"].append(
            self.route(
                "ipv6",
                "local",
                "::1/128",
                dev="lo",
                protocol="kernel",
                metric=0,
                preference="medium",
                type="local",
            )
        )

        tun_soak_isolation.validate_clean_baseline(snapshot)

    def test_missing_required_kernel_local_route_is_rejected(self) -> None:
        snapshot = self.baseline()
        snapshot["routes_v4"] = [
            route
            for route in snapshot["routes_v4"]
            if not (route["table"] == "local" and route["dst"] == "192.0.2.20/32")
        ]

        with self.assertRaisesRegex(tun_soak_isolation.IsolationError, "required local-table route is missing"):
            tun_soak_isolation.validate_clean_baseline(snapshot)

    def test_missing_required_explicit_broadcast_route_is_rejected(self) -> None:
        snapshot = self.baseline()
        snapshot["routes_v4"] = [
            route
            for route in snapshot["routes_v4"]
            if not (route["table"] == "local" and route["dst"] == "192.0.2.255/32")
        ]

        with self.assertRaisesRegex(tun_soak_isolation.IsolationError, "required local-table route is missing"):
            tun_soak_isolation.validate_clean_baseline(snapshot)

    def test_required_broadcast_linkdown_variant_is_derived_from_link_state(self) -> None:
        snapshot = self.baseline()
        snapshot["links"][1]["flags"] = ["BROADCAST", "MULTICAST", "UP"]
        broadcast = next(
            route
            for route in snapshot["routes_v4"]
            if route["table"] == "local"
            and route["type"] == "broadcast"
            and route["dev"] == "eth0"
        )
        broadcast["flags"] = ["linkdown"]
        connected = next(
            route
            for route in snapshot["routes_v4"]
            if route["table"] == "main" and route["dst"] == "192.0.2.0/24"
        )
        connected["flags"] = ["linkdown"]

        tun_soak_isolation.validate_clean_baseline(snapshot)

    def test_linkdown_broadcast_is_rejected_when_link_has_lower_up(self) -> None:
        snapshot = self.baseline()
        broadcast = next(
            route
            for route in snapshot["routes_v4"]
            if route["table"] == "local"
            and route["type"] == "broadcast"
            and route["dev"] == "eth0"
        )
        broadcast["flags"] = ["linkdown"]

        with self.assertRaisesRegex(tun_soak_isolation.IsolationError, "local-table route"):
            tun_soak_isolation.validate_clean_baseline(snapshot)

    def test_documented_optional_subnet_network_broadcast_route_is_accepted(self) -> None:
        snapshot = self.baseline()
        snapshot["routes_v4"].append(
            self.route(
                "ipv4",
                "local",
                "192.0.2.0/32",
                dev="eth0",
                protocol="kernel",
                scope="link",
                prefsrc="192.0.2.20",
                type="broadcast",
            )
        )

        tun_soak_isolation.validate_clean_baseline(snapshot)

    def test_renamed_kernel_wireguard_link_is_rejected_without_process_name_evidence(self) -> None:
        snapshot = self.baseline()
        snapshot["links"].append(
            {
                "ifindex": 7,
                "ifname": "ordinary-uplink-name",
                "kind": "wireguard",
                "master": None,
                "mtu": 1420,
                "link_type": "none",
            }
        )

        with self.assertRaisesRegex(tun_soak_isolation.IsolationError, "physical dedicated-runner uplink"):
            tun_soak_isolation.validate_clean_baseline(snapshot)

    def test_modified_default_priority_rule_is_rejected(self) -> None:
        snapshot = self.baseline()
        snapshot["rules_v4"][1]["fwmark"] = "1"
        snapshot["rules_v4"][1]["fwmask"] = "4294967295"

        with self.assertRaisesRegex(tun_soak_isolation.IsolationError, "canonical default policy-rule set"):
            tun_soak_isolation.validate_clean_baseline(snapshot)

    def test_duplicate_default_priority_rule_is_rejected(self) -> None:
        snapshot = self.baseline()
        snapshot["rules_v4"].append(copy.deepcopy(snapshot["rules_v4"][1]))

        with self.assertRaisesRegex(tun_soak_isolation.IsolationError, "canonical default policy-rule set"):
            tun_soak_isolation.validate_clean_baseline(snapshot)

    def test_virtual_default_uplink_is_rejected_without_process_name_evidence(self) -> None:
        snapshot = self.baseline()
        snapshot["links"][1]["kind"] = "veth"

        with self.assertRaisesRegex(tun_soak_isolation.IsolationError, "physical dedicated-runner uplink"):
            tun_soak_isolation.validate_clean_baseline(snapshot)

    def test_unknown_policy_routing_is_rejected_without_process_name_evidence(self) -> None:
        snapshot = self.baseline()
        snapshot["rules_v4"].append(self.rule("ipv4", 12000, "12000"))

        with self.assertRaisesRegex(tun_soak_isolation.IsolationError, "canonical default policy-rule set"):
            tun_soak_isolation.validate_clean_baseline(snapshot)

    def test_main_connected_route_with_wrong_preferred_source_is_rejected(self) -> None:
        snapshot = self.baseline()
        connected = next(
            route
            for route in snapshot["routes_v4"]
            if route["table"] == "main" and route["dst"] == "192.0.2.0/24"
        )
        connected["prefsrc"] = "198.51.100.20"

        with self.assertRaisesRegex(
            tun_soak_isolation.IsolationError,
            "main-table route is not derived from the positive link/address baseline",
        ):
            tun_soak_isolation.validate_clean_baseline(snapshot)

    def test_main_connected_route_matching_default_metric_is_accepted(self) -> None:
        snapshot = self.baseline()
        default_route = next(
            route
            for route in snapshot["routes_v4"]
            if route["table"] == "main" and route["dst"] == "default"
        )
        connected = next(
            route
            for route in snapshot["routes_v4"]
            if route["table"] == "main" and route["dst"] == "192.0.2.0/24"
        )
        default_route["metric"] = 600
        connected["metric"] = 600

        tun_soak_isolation.validate_clean_baseline(snapshot)

    def test_noprefixroute_requires_main_connected_route_to_be_absent(self) -> None:
        snapshot = self.baseline()
        address = snapshot["addresses"][1]["addresses"][0]
        address["flags"].append("noprefixroute")
        snapshot["routes_v4"] = [
            route
            for route in snapshot["routes_v4"]
            if not (route["table"] == "main" and route["dst"] == "192.0.2.0/24")
        ]

        tun_soak_isolation.validate_clean_baseline(snapshot)

    def test_noprefixroute_rejects_present_main_connected_route(self) -> None:
        snapshot = self.baseline()
        snapshot["addresses"][1]["addresses"][0]["flags"].append("noprefixroute")

        with self.assertRaisesRegex(
            tun_soak_isolation.IsolationError,
            "noprefixroute prefix unexpectedly owns",
        ):
            tun_soak_isolation.validate_clean_baseline(snapshot)

    def test_kernel_derived_ipv6_main_connected_route_is_accepted(self) -> None:
        snapshot = self.baseline()
        snapshot["addresses"][1]["addresses"].append(
            {
                "family": "inet6",
                "local": "fe80::20",
                "prefixlen": 64,
                "scope": "link",
                "label": "eth0",
                "flags": [],
                "extras": {"protocol": "kernel_ll"},
            }
        )
        snapshot["routes_v6"].extend(
            [
                self.route(
                    "ipv6",
                    "local",
                    "fe80::20/128",
                    dev="eth0",
                    protocol="kernel",
                    metric=0,
                    preference="medium",
                    type="local",
                ),
                self.route(
                    "ipv6",
                    "local",
                    "ff00::/8",
                    dev="eth0",
                    protocol="kernel",
                    metric=256,
                    preference="medium",
                    type="multicast",
                ),
                self.route(
                    "ipv6",
                    "main",
                    "fe80::/64",
                    dev="eth0",
                    protocol="kernel",
                    metric=256,
                    preference="medium",
                ),
            ]
        )

        tun_soak_isolation.validate_clean_baseline(snapshot)

    def test_missing_required_main_connected_route_is_rejected(self) -> None:
        snapshot = self.baseline()
        snapshot["routes_v4"] = [
            route
            for route in snapshot["routes_v4"]
            if not (route["table"] == "main" and route["dst"] == "192.0.2.0/24")
        ]

        with self.assertRaisesRegex(
            tun_soak_isolation.IsolationError,
            "required main-table connected route is missing",
        ):
            tun_soak_isolation.validate_clean_baseline(snapshot)

    def test_duplicate_main_connected_route_is_rejected(self) -> None:
        snapshot = self.baseline()
        connected = next(
            route
            for route in snapshot["routes_v4"]
            if route["table"] == "main" and route["dst"] == "192.0.2.0/24"
        )
        snapshot["routes_v4"].append(copy.deepcopy(connected))

        with self.assertRaisesRegex(
            tun_soak_isolation.IsolationError,
            "main-table connected route is duplicated",
        ):
            tun_soak_isolation.validate_clean_baseline(snapshot)

    def test_unrelated_kernel_onlink_main_route_is_rejected(self) -> None:
        snapshot = self.baseline()
        snapshot["routes_v4"].append(
            self.route(
                "ipv4",
                "main",
                "203.0.113.0/24",
                dev="eth0",
                protocol="kernel",
                scope="link",
            )
        )

        with self.assertRaisesRegex(
            tun_soak_isolation.IsolationError,
            "main-table route is not derived from the positive link/address baseline",
        ):
            tun_soak_isolation.validate_clean_baseline(snapshot)

    def test_unrelated_dhcp_onlink_main_route_is_rejected(self) -> None:
        snapshot = self.baseline()
        snapshot["routes_v4"].append(
            self.route(
                "ipv4",
                "main",
                "198.51.100.0/24",
                dev="eth0",
                protocol="dhcp",
                scope="link",
            )
        )

        with self.assertRaisesRegex(
            tun_soak_isolation.IsolationError,
            "main-table route is not derived from the positive link/address baseline",
        ):
            tun_soak_isolation.validate_clean_baseline(snapshot)

    def test_preexisting_main_table_bypass_route_is_rejected(self) -> None:
        snapshot = self.baseline()
        snapshot["routes_v4"].append(
            self.route("ipv4", "main", "203.0.113.9/32", gateway="192.0.2.1", dev="eth0", protocol="static")
        )

        with self.assertRaisesRegex(tun_soak_isolation.IsolationError, "foreign main-table route"):
            tun_soak_isolation.validate_clean_baseline(snapshot)

    def test_preexisting_foreign_local_table_route_is_rejected(self) -> None:
        snapshot = self.baseline()
        snapshot["routes_v4"].append(
            self.route(
                "ipv4",
                "local",
                "203.0.113.9/32",
                dev="eth0",
                protocol="static",
                scope="host",
            )
        )

        with self.assertRaisesRegex(tun_soak_isolation.IsolationError, "local-table route"):
            tun_soak_isolation.validate_clean_baseline(snapshot)

    def test_preexisting_foreign_default_table_route_is_rejected(self) -> None:
        snapshot = self.baseline()
        snapshot["routes_v4"].append(
            self.route(
                "ipv4",
                "default",
                "default",
                gateway="192.0.2.1",
                dev="eth0",
                protocol="static",
            )
        )

        with self.assertRaisesRegex(tun_soak_isolation.IsolationError, "default routing table"):
            tun_soak_isolation.validate_clean_baseline(snapshot)

    def test_preexisting_nftables_packet_path_is_rejected(self) -> None:
        snapshot = self.baseline()
        snapshot["nftables"] = [{"table": {"family": "inet", "name": "custom-firewall"}}]

        with self.assertRaisesRegex(tun_soak_isolation.IsolationError, "nftables"):
            tun_soak_isolation.validate_clean_baseline(snapshot)

    def test_resolver_default_route_on_non_uplink_is_rejected(self) -> None:
        snapshot = self.baseline()
        snapshot["resolved"]["links"].append(
            {
                "ifname": "eth1",
                "lines": ["DNS Domain: ~.", "DefaultRoute setting: yes"],
            }
        )

        with self.assertRaisesRegex(tun_soak_isolation.IsolationError, "resolver default-route"):
            tun_soak_isolation.validate_clean_baseline(snapshot)

    def test_exact_podlaz_projection_is_removed_before_baseline_comparison(self) -> None:
        baseline, current, manifest = self.active_snapshot_and_manifest()

        tun_soak_isolation.assert_matches_baseline(
            baseline=baseline,
            current=current,
            manifest=manifest,
        )

    def test_route_metric_change_is_not_subtracted_as_podlaz_owned(self) -> None:
        baseline, current, manifest = self.active_snapshot_and_manifest()
        managed_route = next(route for route in current["routes_v4"] if route["table"] == "51820")
        managed_route["metric"] = 50

        with self.assertRaisesRegex(tun_soak_isolation.IsolationError, "exact Podlaz route projection"):
            tun_soak_isolation.assert_matches_baseline(
                baseline=baseline,
                current=current,
                manifest=manifest,
            )

    def test_route_flag_change_is_not_subtracted_as_podlaz_owned(self) -> None:
        baseline, current, manifest = self.active_snapshot_and_manifest()
        managed_route = next(route for route in current["routes_v4"] if route["table"] == "51820")
        managed_route["flags"] = ["onlink"]

        with self.assertRaisesRegex(tun_soak_isolation.IsolationError, "exact Podlaz route projection"):
            tun_soak_isolation.assert_matches_baseline(
                baseline=baseline,
                current=current,
                manifest=manifest,
            )

    def test_rule_iif_change_is_not_subtracted_as_podlaz_owned(self) -> None:
        baseline, current, manifest = self.active_snapshot_and_manifest()
        managed_rule = next(rule for rule in current["rules_v4"] if rule["priority"] == 10000)
        managed_rule["iif"] = "eth0"

        with self.assertRaisesRegex(tun_soak_isolation.IsolationError, "exact Podlaz policy rule projection"):
            tun_soak_isolation.assert_matches_baseline(
                baseline=baseline,
                current=current,
                manifest=manifest,
            )

    def test_hex_manifest_mark_matches_decimal_kernel_rule_evidence(self) -> None:
        actual = self.rule(
            "ipv4",
            10000,
            "51820",
            fwmark="51820",
            fwmask="4294967295",
        )
        expected = self.rule(
            "ipv4",
            10000,
            "51820",
            fwmark="51820",
            fwmask="4294967295",
        )

        self.assertTrue(tun_soak_isolation._rule_matches(actual, expected))
        self.assertEqual(("51820", "4294967295"), tun_soak_isolation._split_mark("0xca6c/0xffffffff"))

    def test_foreign_route_mutation_during_active_soak_fails_revalidation(self) -> None:
        baseline = self.baseline()
        current = copy.deepcopy(baseline)
        current["routes_v4"].append(
            self.route("ipv4", "main", "203.0.113.99/32", gateway="192.0.2.1", dev="eth0")
        )

        with self.assertRaisesRegex(tun_soak_isolation.IsolationError, "network state changed"):
            tun_soak_isolation.assert_matches_baseline(
                baseline=baseline,
                current=current,
            )


    def trusted_host(self, snapshot: dict[str, object]) -> dict[str, object]:
        return {
            "schema_version": "podlaz.e2e.trusted-host.v1",
            "runtime_os": {"id": "ubuntu", "version_id": "24.04"},
            "uplink": {
                "ifname": "eth0",
                "ifindex": 2,
                "default_ipv4_gateway": "192.0.2.1",
                "global_ipv4_cidrs": ["192.0.2.20/24"],
                "network_manager_connection_id": "11111111-2222-3333-4444-555555555555",
            },
            "resolved": copy.deepcopy(snapshot["resolved"]),
        }

    def trusted_snapshot(self) -> dict[str, object]:
        snapshot = self.baseline()
        snapshot["runtime_os"] = {"id": "ubuntu", "version_id": "24.04"}
        snapshot["network_manager"] = [
            {
                "uuid": "11111111-2222-3333-4444-555555555555",
                "device": "eth0",
                "state": "activated",
            }
        ]
        return snapshot

    def test_trusted_uplink_rejects_gateway_mutation_on_same_physical_link(self) -> None:
        snapshot = self.trusted_snapshot()
        trusted = self.trusted_host(snapshot)
        default_route = next(
            route
            for route in snapshot["routes_v4"]
            if route["table"] == "main" and route["dst"] == "default"
        )
        default_route["gateway"] = "192.0.2.254"

        # The live snapshot remains internally consistent and would authenticate
        # itself without the independently provisioned fingerprint.
        tun_soak_isolation.validate_clean_baseline(snapshot)
        with self.assertRaisesRegex(tun_soak_isolation.IsolationError, "trusted uplink fingerprint"):
            tun_soak_isolation.validate_trusted_host(snapshot, trusted)

    def test_trusted_uplink_rejects_additional_global_prefix_even_when_self_consistent(self) -> None:
        snapshot = self.trusted_snapshot()
        trusted = self.trusted_host(snapshot)
        snapshot["addresses"][1]["addresses"].append(
            {
                "family": "inet",
                "local": "198.51.100.20",
                "prefixlen": 24,
                "scope": "global",
                "label": "eth0",
                "flags": [],
                "extras": {"broadcast": "198.51.100.255"},
            }
        )
        snapshot["routes_v4"].extend(
            [
                self.route(
                    "ipv4",
                    "local",
                    "198.51.100.20/32",
                    dev="eth0",
                    protocol="kernel",
                    scope="host",
                    prefsrc="198.51.100.20",
                    type="local",
                ),
                self.route(
                    "ipv4",
                    "local",
                    "198.51.100.255/32",
                    dev="eth0",
                    protocol="kernel",
                    scope="link",
                    prefsrc="198.51.100.20",
                    type="broadcast",
                ),
                self.route(
                    "ipv4",
                    "main",
                    "198.51.100.0/24",
                    dev="eth0",
                    protocol="kernel",
                    scope="link",
                    prefsrc="198.51.100.20",
                ),
            ]
        )

        # All address-derived kernel routes are deliberately self-consistent;
        # only the independent fingerprint proves that this extra prefix is foreign.
        tun_soak_isolation.validate_clean_baseline(snapshot)
        with self.assertRaisesRegex(tun_soak_isolation.IsolationError, "trusted uplink fingerprint"):
            tun_soak_isolation.validate_trusted_host(snapshot, trusted)

    def test_trusted_resolver_rejects_pre_capture_global_dns_mutation(self) -> None:
        snapshot = self.trusted_snapshot()
        trusted = self.trusted_host(snapshot)
        snapshot["resolved"]["global"].append("DNS Servers: 192.0.2.53")
        snapshot["resolved"]["global"].sort()

        tun_soak_isolation.validate_clean_baseline(snapshot)
        with self.assertRaisesRegex(tun_soak_isolation.IsolationError, "trusted resolver fingerprint"):
            tun_soak_isolation.validate_trusted_host(snapshot, trusted)

    def test_trusted_resolver_rejects_pre_capture_uplink_domain_mutation(self) -> None:
        snapshot = self.trusted_snapshot()
        trusted = self.trusted_host(snapshot)
        snapshot["resolved"]["links"][0]["lines"].append("DNS Domain: example.invalid")
        snapshot["resolved"]["links"][0]["lines"].sort()

        tun_soak_isolation.validate_clean_baseline(snapshot)
        with self.assertRaisesRegex(tun_soak_isolation.IsolationError, "trusted resolver fingerprint"):
            tun_soak_isolation.validate_trusted_host(snapshot, trusted)

    def test_active_trusted_host_validation_runs_after_exact_podlaz_projection_is_removed(self) -> None:
        baseline, current, manifest = self.active_snapshot_and_manifest()
        trusted = self.trusted_host(baseline)

        tun_soak_isolation.assert_matches_baseline(
            baseline=baseline,
            current=current,
            manifest=manifest,
            trusted=trusted,
        )

    def test_canonical_loopback_networkmanager_connection_does_not_replace_uplink_identity(self) -> None:
        snapshot = self.trusted_snapshot()
        trusted = self.trusted_host(snapshot)
        snapshot["network_manager"].append(
            {
                "uuid": "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
                "device": "lo",
                "state": "activated",
            }
        )
        snapshot["network_manager"].sort(key=lambda item: (item["device"], item["uuid"], item["state"]))

        tun_soak_isolation.validate_clean_baseline(snapshot)
        tun_soak_isolation.validate_trusted_host(snapshot, trusted)


if __name__ == "__main__":
    unittest.main()
