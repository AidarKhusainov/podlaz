#!/usr/bin/env python3
"""Validate an existing, *unconfigured* Xray TUN before NM becomes L3 writer."""
from __future__ import annotations

import ipaddress


def validate_xray_tun(expected_ifindex: int, links: object, addresses: object) -> None:
    if type(expected_ifindex) is not int or expected_ifindex <= 0:
        raise ValueError("invalid persisted TUN ifindex")
    if not isinstance(links, list) or len(links) != 1 or not isinstance(addresses, list) or len(addresses) != 1:
        raise ValueError("ambiguous TUN inventory")
    link, addr = links[0], addresses[0]
    if not isinstance(link, dict) or not isinstance(addr, dict):
        raise ValueError("malformed TUN inventory")
    if (link.get("ifname") != "podlaz0" or
        type(link.get("ifindex")) is not int or link["ifindex"] != expected_ifindex or
        not isinstance(link.get("linkinfo"), dict) or
        link["linkinfo"].get("info_kind") != "tun" or
        addr.get("ifname") != "podlaz0" or addr.get("ifindex") != expected_ifindex):
        raise ValueError("tracked Xray TUN identity mismatch")
    addr_info = addr.get("addr_info")
    if not isinstance(addr_info, list):
        raise ValueError("missing TUN address inventory")
    for item in addr_info:
        if not isinstance(item, dict):
            raise ValueError("malformed TUN address")
        # Link-local IPv6 may be implicit. Any global address belongs to
        # someone; NM must never race an existing L3 writer on that link.
        if item.get("family") == "inet":
            raise ValueError("VPN TUN already owns an IPv4 address")
        if item.get("family") == "inet6":
            try:
                if ipaddress.IPv6Address(item.get("local", "")).is_link_local:
                    continue
            except ValueError as exc:
                raise ValueError("malformed IPv6 address") from exc
            raise ValueError("unqualified IPv6 state on TUN")
        raise ValueError("unknown TUN address family")
