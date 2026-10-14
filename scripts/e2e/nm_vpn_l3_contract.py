#!/usr/bin/env python3
"""Pure guest-only NetworkManager VPN payload contract; never mutates host state.

The address pool and identifiers are disposable-fixture inputs, not a public
Podlaz backend or a general opaque-Xray-profile gateway extractor.
"""
from __future__ import annotations

from dataclasses import dataclass
import ipaddress
import sys
import uuid


@dataclass(frozen=True)
class GuestVPNConfig:
    interface: str
    gateway: str
    address: str
    dns: tuple[str, ...]


def _ip4_integer(value: str) -> int:
    address = ipaddress.IPv4Address(value)
    if address.is_unspecified or address.is_loopback or address.is_multicast or address.is_link_local or address.is_reserved and not address.is_private:
        raise ValueError("unsuitable IPv4 address")
    return int.from_bytes(address.packed, byteorder=sys.byteorder)


def config_dicts(config: GuestVPNConfig) -> tuple[dict[str, object], dict[str, object]]:
    """Return NM's generic Config and subsequent Ip4Config payload values.

    The real adapter must translate integers to dbus.UInt32, booleans to
    dbus.Boolean, DNS to an au and send Config before Ip4Config.
    """
    if config.interface != "podlaz0":
        raise ValueError("unexpected synthetic TUN")
    try:
        iface = ipaddress.IPv4Interface(config.address)
    except (ValueError, ipaddress.AddressValueError) as exc:
        raise ValueError("invalid guest address") from exc
    if iface.network.prefixlen != 32 or iface.ip not in ipaddress.IPv4Network("198.18.0.0/15"):
        raise ValueError("guest-only IPv4 /32 required")
    if not config.dns or len(config.dns) > 4:
        raise ValueError("bounded DNS servers required")
    # IPv4 gateway represents a real known provider transport, not a fake
    # tunnel gateway. A separate authoritative check validates its live route.
    external = _ip4_integer(config.gateway)
    address = _ip4_integer(str(iface.ip))
    dns = [_ip4_integer(value) for value in config.dns]
    if len(dns) != len(set(dns)):
        raise ValueError("duplicate DNS server")
    return (
        {"tundev": config.interface, "gateway": external, "has-ip4": True,
         "has-ip6": False, "can-persist": False},
        {"address": address, "ptp": address, "prefix": 32,
         "dns": dns, "never-default": False},
    )


def vpn_profile_settings(service: str, name: str, profile_uuid: str,
                         metric: int) -> dict[str, dict[str, object]]:
    """Ephemeral NM profile settings; caller enforces collision-free metric.

    Reject an unqualified metric instead of using it to override other
    connections or assuming a successful connectivity result.
    """
    if service != "org.freedesktop.NetworkManager.podlazresearch" or name != "podlaz-guest-nm-l3":
        raise ValueError("not the disposable VPN service")
    try:
        if str(uuid.UUID(profile_uuid)) != profile_uuid:
            raise ValueError("noncanonical UUID")
    except ValueError as exc:
        raise ValueError("invalid guest UUID") from exc
    if isinstance(metric, bool) or not isinstance(metric, int) or not 1 <= metric <= 100:
        raise ValueError("unqualified NM metric")
    return {
        "connection": {"id": name, "uuid": profile_uuid, "type": "vpn", "autoconnect": False},
        "vpn": {"service-type": service},
        "ipv4": {"method": "auto", "route-table": 254, "never-default": False,
                 "auto-route-ext-gw": False, "route-metric": metric,
                 "dns-priority": -30000, "dns-search": ["~."]},
        "ipv6": {"method": "ignore", "auto-route-ext-gw": False,
                 "never-default": True},
    }


def emit_nm_ipv4_config(config: GuestVPNConfig, dbus_types: object,
                        emit: object) -> None:
    """Emit a real generic Config followed by IPv4 config, in contract order.

    The containing guest adapter already verified the tracked TUN identity and
    absence of a competing Podlaz L3 session; this function is serialization
    only, not the authority for applying an IP address or a route.
    """
    generic, ipv4 = config_dicts(config)
    typed_generic = dbus_types.Dictionary({
        "tundev": dbus_types.String(generic["tundev"]),
        "gateway": dbus_types.UInt32(generic["gateway"]),
        "has-ip4": dbus_types.Boolean(generic["has-ip4"]),
        "has-ip6": dbus_types.Boolean(generic["has-ip6"]),
        "can-persist": dbus_types.Boolean(generic["can-persist"]),
    }, signature="sv")
    typed_ipv4 = dbus_types.Dictionary({
        "address": dbus_types.UInt32(ipv4["address"]),
        "ptp": dbus_types.UInt32(ipv4["ptp"]),
        "prefix": dbus_types.UInt32(ipv4["prefix"]),
        "dns": dbus_types.Array([dbus_types.UInt32(ip) for ip in ipv4["dns"]], signature="u"),
        "never-default": dbus_types.Boolean(ipv4["never-default"]),
    }, signature="sv")
    emit("Config", typed_generic)
    emit("Ip4Config", typed_ipv4)
