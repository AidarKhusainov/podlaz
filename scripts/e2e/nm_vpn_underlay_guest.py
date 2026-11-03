#!/usr/bin/env python3
"""Fail-closed policy plan for a disposable, already-isolated IPv4 guest.

This is only a guest-fixture plan. The production backend must use dynamic
allocation and durable authority; this module performs NO system mutations.
"""
from __future__ import annotations

from dataclasses import dataclass
import ipaddress


@dataclass(frozen=True)
class UnderlayEvidence:
    interface: str
    gateway: str
    uplink_network: str
    uplink_default_metric: int
    # Explicit snapshots: priority, routing table, mark, mark mask.
    ipv4_rules: tuple[tuple[int, int, int, int], ...]
    occupied_tables: frozenset[int]


@dataclass(frozen=True)
class UnderlayPlan:
    table: int
    tunnel_mark: int
    rule_priority: int
    default_route: tuple[str, str, str, int]
    connected_route: tuple[str, str, int]
    fwmark_rule: tuple[int, int, int, int]


def select_underlay(evidence: UnderlayEvidence, vpn_default_metric: int) -> UnderlayPlan:
    """Inspect fixture evidence and return an underlay isolated from main.

    The caller must independently verify these observations are fresh and
    persist exact-owned resource identity before performing any mutation.
    """
    if evidence.interface != "host0":
        raise ValueError("the guest uplink must be the verified physical host0")
    gateway = ipaddress.IPv4Address(evidence.gateway)
    network = ipaddress.IPv4Network(evidence.uplink_network, strict=True)
    if (network.prefixlen == 0 or gateway not in network or
            gateway in (network.network_address, network.broadcast_address) or
            gateway.is_loopback or gateway.is_multicast or gateway.is_link_local):
        raise ValueError("underlay gateway is not reachable on a validated link")
    if isinstance(evidence.uplink_default_metric, bool) or not 1 <= evidence.uplink_default_metric <= 0xffffffff:
        raise ValueError("unknown/default uplink metric")
    if isinstance(vpn_default_metric, bool) or not 1 <= vpn_default_metric < evidence.uplink_default_metric:
        raise ValueError("VPN default cannot win over uplink")
    # A foreign, high-priority policy rule can take precedence over the mark
    # or redirect ordinary traffic. The first qualifying backend stays strict.
    local_seen = False
    main_seen = False
    for priority, table, mark, mask in evidence.ipv4_rules:
        if priority == 0 and table == 255 and mark == mask == 0:
            if local_seen:
                raise ValueError("duplicated local RPDB rule")
            local_seen = True
        elif priority == 32766 and table == 254 and mark == mask == 0:
            if main_seen:
                raise ValueError("duplicated main RPDB rule")
            main_seen = True
        elif priority == 32767 and table == 253 and mark == mask == 0:
            pass
        else:
            raise ValueError("unqualified RPDB ownership/collision")
    if not local_seen or not main_seen:
        raise ValueError("required host RPDB baseline absent")
    table = next((value for value in range(53000, 53100)
                  if value not in evidence.occupied_tables), None)
    if table is None:
        raise ValueError("underlay routing table allocation exhausted")
    mark = 20570  # Synthetic guest fixture only; not a fixed product mark.
    priority = 10000
    return UnderlayPlan(
        table=table, tunnel_mark=mark, rule_priority=priority,
        default_route=("default", str(gateway), evidence.interface, table),
        connected_route=(str(network), evidence.interface, table),
        fwmark_rule=(mark, 0xffffffff, table, priority),
    )
