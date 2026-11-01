"""Fail-closed configuration and identity preflight for guest-only inherited TUN FD.

This deliberately does not authorize product use of an NM-owned device. A
running process cannot atomically pin the kernel link name to an ifindex via
upstream Xray's current environment-variable API. Firewall protection must
remain armed throughout the lifetime and any replacement attempt.
"""
from __future__ import annotations

import json
from pathlib import Path

TUN_NAME = "podlaz0"
TUN_KIND = "tun"
TUN_FLAGS = 0x0001 | 0x1000  # IFF_TUN | IFF_NO_PI
# No Xray-owned link/L3/route/DNS mutation is legal in inherited-FD mode.
FORBIDDEN_TUN_SETTINGS = frozenset({
    "gateway", "autoSystemRoutingTable", "autoSystemDnsToGateway",
    "autoOutboundsInterface",
})


def verify_xray_config(config_path: Path, *, ifname: str, mtu: int) -> None:
    if (config_path.is_symlink() or not config_path.is_file()
            or config_path.stat().st_mode & 0o077):
        raise ValueError("Xray configuration must be a private regular file")
    config = json.loads(config_path.read_text(encoding="utf-8"))
    if not isinstance(config, dict) or not isinstance(config.get("inbounds"), list):
        raise ValueError("invalid Xray inbound configuration")
    tun = [entry for entry in config["inbounds"]
           if isinstance(entry, dict) and entry.get("protocol") == TUN_KIND]
    if len(tun) != 1:
        raise ValueError("expected exactly one inherited TUN inbound")
    settings = tun[0].get("settings")
    if not isinstance(settings, dict):
        raise ValueError("missing TUN settings")
    if settings.get("name") != TUN_NAME or ifname != TUN_NAME:
        raise ValueError("TUN name collision")
    if type(mtu) is not int or mtu <= 0 or type(settings.get("MTU")) is not int:
        raise ValueError("invalid exact TUN MTU")
    if settings["MTU"] != mtu:
        raise ValueError("NM TUN MTU mismatch")
    if FORBIDDEN_TUN_SETTINGS.intersection(settings):
        raise ValueError("Xray must not manage NM link, routes or system DNS")
    # Never let an unrecognized second TUN inbound silently create another link.
    if any(not isinstance(entry, dict) for entry in config["inbounds"]):
        raise ValueError("invalid additional Xray inbound")


def verify_tun_fd_identity(
    *, name: str, flags: int, expected_name: str, before: dict, after: dict,
    expected_index: int, expected_mtu: int,
) -> None:
    if name != expected_name or expected_name != TUN_NAME:
        raise ValueError("inherited TUN descriptor name mismatch")
    if type(flags) is not int or flags & TUN_FLAGS != TUN_FLAGS:
        raise ValueError("inherited descriptor is not an IFF_TUN|IFF_NO_PI TUN")
    if flags & (0x0002 | 0x0100 | 0x4000):  # IFF_TAP, IFF_MULTI_QUEUE, IFF_VNET_HDR
        raise ValueError("unqualified inherited TUN packet mode")
    for snapshot in (before, after):
        if (not isinstance(snapshot, dict)
                or snapshot.get("ifname") != expected_name
                or snapshot.get("linkinfo", {}).get("info_kind") != TUN_KIND
                or type(snapshot.get("ifindex")) is not int
                or snapshot["ifindex"] != expected_index
                or type(snapshot.get("mtu")) is not int
                or snapshot["mtu"] != expected_mtu
                or "UP" not in snapshot.get("flags", [])):
            raise ValueError("NM TUN exact ifindex/MTU/UP ownership changed")
