#!/usr/bin/env python3
"""Fail-closed structural network isolation for installed-package TUN soaks."""

from __future__ import annotations

import argparse
import copy
import ipaddress
import json
import os
import re
import selectors
import subprocess
import sys
import time
from pathlib import Path
from typing import Any, Mapping, Sequence

SCHEMA_VERSION = 1
MAX_COMMAND_BYTES = 8 * 1024 * 1024
COMMAND_TIMEOUT_SECONDS = 15
PODLAZ_LINK = "podlaz0"
PODLAZ_NFT_FAMILY = "inet"
PODLAZ_NFT_TABLE = "podlaz"
DEFAULT_RULES = frozenset(
    {
        (0, "local"),
        (32766, "main"),
        (32767, "default"),
    }
)
SUSPICIOUS_LINK_KINDS = frozenset(
    {
        "bareudp",
        "erspan",
        "geneve",
        "gre",
        "gretap",
        "gtp",
        "ip6gre",
        "ip6gretap",
        "ip6tnl",
        "ipip",
        "l2tpeth",
        "macsec",
        "rmnet",
        "sit",
        "tap",
        "tun",
        "vti",
        "vti6",
        "vxlan",
        "wireguard",
        "xfrm",
    }
)


class IsolationError(RuntimeError):
    """Raised when a clean, stable host-network boundary cannot be proved."""


def _terminate_process(process: subprocess.Popen[bytes]) -> None:
    if process.poll() is not None:
        return
    process.terminate()
    try:
        process.wait(timeout=0.5)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait(timeout=1)


def _bounded_command(args: Sequence[str]) -> bytes:
    try:
        process = subprocess.Popen(
            list(args),
            stdin=subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            bufsize=0,
        )
    except OSError as exc:
        raise IsolationError("structural network inspection did not complete") from exc

    if process.stdout is None or process.stderr is None:
        _terminate_process(process)
        raise IsolationError("structural network inspection did not complete")

    streams = {process.stdout: bytearray(), process.stderr: bytearray()}
    selector = selectors.DefaultSelector()
    deadline = time.monotonic() + COMMAND_TIMEOUT_SECONDS
    try:
        for stream in streams:
            os.set_blocking(stream.fileno(), False)
            selector.register(stream, selectors.EVENT_READ)

        while selector.get_map():
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                _terminate_process(process)
                raise IsolationError("structural network inspection did not complete")
            events = selector.select(timeout=min(remaining, 0.1))
            for key, _ in events:
                stream = key.fileobj
                try:
                    chunk = os.read(stream.fileno(), 65536)
                except BlockingIOError:
                    continue
                if not chunk:
                    selector.unregister(stream)
                    stream.close()
                    continue
                buffer = streams[stream]
                buffer.extend(chunk)
                if len(buffer) > MAX_COMMAND_BYTES:
                    _terminate_process(process)
                    raise IsolationError("structural network inspection exceeded the byte limit")

        remaining = deadline - time.monotonic()
        if remaining <= 0:
            _terminate_process(process)
            raise IsolationError("structural network inspection did not complete")
        try:
            return_code = process.wait(timeout=remaining)
        except subprocess.TimeoutExpired as exc:
            _terminate_process(process)
            raise IsolationError("structural network inspection did not complete") from exc
    except IsolationError:
        _terminate_process(process)
        raise
    finally:
        selector.close()
        for stream in streams:
            if not stream.closed:
                stream.close()

    if return_code != 0:
        raise IsolationError("structural network inspection command failed")
    return bytes(streams[process.stdout])


def _json_command(args: Sequence[str]) -> Any:
    payload = _bounded_command(args)
    try:
        return json.loads(payload.decode("utf-8"))
    except (UnicodeError, json.JSONDecodeError) as exc:
        raise IsolationError("structural network inspection returned invalid JSON") from exc


def _text_command(args: Sequence[str]) -> str:
    try:
        return _bounded_command(args).decode("utf-8")
    except UnicodeError as exc:
        raise IsolationError("structural network inspection returned invalid text") from exc


def _canonical_table(value: Any, *, default: str = "main") -> str:
    if value is None or value == "":
        return default
    aliases = {
        253: "default",
        254: "main",
        255: "local",
        "253": "default",
        "254": "main",
        "255": "local",
        "default": "default",
        "main": "main",
        "local": "local",
        "podlaz": "51820",
        51820: "51820",
        "51820": "51820",
    }
    return aliases.get(value, str(value))


def _canonical_prefix(value: Any, family: str, *, allow_all: bool = False) -> str:
    text = str(value or "").strip()
    if allow_all and text in {"", "all"}:
        return "all"
    if text == "default":
        return text
    if not text:
        return ""
    try:
        address = ipaddress.ip_network(text if "/" in text else f"{text}/{'32' if family == 'ipv4' else '128'}", strict=False)
    except ValueError as exc:
        raise IsolationError("network prefix evidence is malformed") from exc
    if (family == "ipv4" and address.version != 4) or (family == "ipv6" and address.version != 6):
        raise IsolationError("network prefix family is inconsistent")
    return address.with_prefixlen


def _canonical_address(value: Any, family: str) -> str:
    text = str(value or "").strip()
    if not text:
        return ""
    try:
        address = ipaddress.ip_address(text)
    except ValueError as exc:
        raise IsolationError("network address evidence is malformed") from exc
    if (family == "ipv4" and address.version != 4) or (family == "ipv6" and address.version != 6):
        raise IsolationError("network address family is inconsistent")
    return str(address)


def _canonical_mark_part(value: Any) -> str:
    text = str(value or "").strip()
    if not text:
        return ""
    try:
        parsed = int(text, 0)
    except ValueError as exc:
        raise IsolationError("policy-rule mark evidence is malformed") from exc
    if not 0 <= parsed <= 0xFFFFFFFF:
        raise IsolationError("policy-rule mark evidence is out of range")
    return str(parsed)


def _split_mark(value: Any, mask: Any = "") -> tuple[str, str]:
    text = str(value or "").strip()
    explicit_mask = str(mask or "").strip()
    if "/" in text:
        if explicit_mask:
            raise IsolationError("policy-rule mark mask evidence is ambiguous")
        mark_text, explicit_mask = text.split("/", 1)
        text = mark_text
    return _canonical_mark_part(text), _canonical_mark_part(explicit_mask)


def _required_mapping(value: Any, label: str) -> Mapping[str, Any]:
    if not isinstance(value, Mapping):
        raise IsolationError(f"{label} is malformed")
    return value


def _normalize_links(value: Any) -> list[dict[str, Any]]:
    if not isinstance(value, list):
        raise IsolationError("link inventory is malformed")
    links: list[dict[str, Any]] = []
    for raw in value:
        entry = _required_mapping(raw, "link inventory entry")
        ifindex = entry.get("ifindex")
        ifname = entry.get("ifname")
        mtu = entry.get("mtu")
        if not isinstance(ifindex, int) or ifindex <= 0 or not isinstance(ifname, str) or not ifname:
            raise IsolationError("link identity is incomplete")
        if not isinstance(mtu, int) or mtu <= 0:
            raise IsolationError("link MTU evidence is incomplete")
        linkinfo = entry.get("linkinfo")
        kind = ""
        if linkinfo is not None:
            info = _required_mapping(linkinfo, "link kind evidence")
            raw_kind = info.get("info_kind", "")
            if not isinstance(raw_kind, str):
                raise IsolationError("link kind evidence is malformed")
            kind = raw_kind
        master = entry.get("master")
        if master is not None and not isinstance(master, (int, str)):
            raise IsolationError("link master evidence is malformed")
        links.append(
            {
                "ifindex": ifindex,
                "ifname": ifname,
                "kind": kind,
                "master": master,
                "mtu": mtu,
                "link_type": str(entry.get("link_type", "")),
            }
        )
    return sorted(links, key=lambda item: (item["ifindex"], item["ifname"]))


def _canonical_interface_address(value: Any, family: str) -> str:
    text = str(value or "").strip()
    if not text:
        return ""
    try:
        address = ipaddress.ip_address(text)
    except ValueError as exc:
        raise IsolationError("interface address evidence is malformed") from exc
    expected_version = 4 if family == "inet" else 6 if family == "inet6" else 0
    if expected_version == 0 or address.version != expected_version:
        raise IsolationError("interface address family is inconsistent")
    return str(address)


def _normalize_address_extras(entry: Mapping[str, Any]) -> dict[str, Any]:
    ignored = {
        "family",
        "local",
        "prefixlen",
        "scope",
        "label",
        "valid_life_time",
        "preferred_life_time",
        "cacheinfo",
        "cstamp",
        "tstamp",
    }
    boolean_flags = sorted(
        str(key)
        for key, value in entry.items()
        if str(key) not in ignored and isinstance(value, bool) and value
    )
    extras = {
        str(key): value
        for key, value in sorted(entry.items(), key=lambda pair: str(pair[0]))
        if str(key) not in ignored and not isinstance(value, bool)
    }
    return {"flags": boolean_flags, "extras": extras}


def _normalize_addresses(value: Any) -> list[dict[str, Any]]:
    if not isinstance(value, list):
        raise IsolationError("address inventory is malformed")
    result: list[dict[str, Any]] = []
    for raw in value:
        link = _required_mapping(raw, "address inventory entry")
        ifindex = link.get("ifindex")
        ifname = link.get("ifname")
        if not isinstance(ifindex, int) or ifindex <= 0 or not isinstance(ifname, str) or not ifname:
            raise IsolationError("address link identity is incomplete")
        raw_addresses = link.get("addr_info")
        if not isinstance(raw_addresses, list):
            raise IsolationError("interface address inventory is malformed")
        addresses: list[dict[str, Any]] = []
        for raw_address in raw_addresses:
            address = _required_mapping(raw_address, "interface address entry")
            family = address.get("family")
            prefixlen = address.get("prefixlen")
            if family not in {"inet", "inet6"} or not isinstance(prefixlen, int):
                raise IsolationError("interface address identity is incomplete")
            maximum = 32 if family == "inet" else 128
            if not 0 <= prefixlen <= maximum:
                raise IsolationError("interface address prefix is invalid")
            structural = _normalize_address_extras(address)
            addresses.append(
                {
                    "family": family,
                    "local": _canonical_interface_address(address.get("local"), family),
                    "prefixlen": prefixlen,
                    "scope": str(address.get("scope", "")),
                    "label": str(address.get("label", ifname)),
                    **structural,
                }
            )
        result.append(
            {
                "ifindex": ifindex,
                "ifname": ifname,
                "addresses": sorted(
                    addresses,
                    key=lambda item: json.dumps(item, sort_keys=True, separators=(",", ":")),
                ),
            }
        )
    return sorted(result, key=lambda item: (item["ifindex"], item["ifname"]))


def _normalize_rules(value: Any, family: str) -> list[dict[str, Any]]:
    if not isinstance(value, list):
        raise IsolationError("policy-rule inventory is malformed")
    rules: list[dict[str, Any]] = []
    for raw in value:
        entry = _required_mapping(raw, "policy-rule inventory entry")
        priority = entry.get("priority")
        if not isinstance(priority, int) or priority < 0:
            raise IsolationError("policy-rule priority is malformed")
        fwmark, fwmask = _split_mark(entry.get("fwmark", ""), entry.get("fwmask", ""))
        rule = {
            "family": family,
            "priority": priority,
            "table": _canonical_table(entry.get("table")),
            "action": str(entry.get("action", "to_tbl")),
            "source": _canonical_prefix(entry.get("src", entry.get("from", "all")), family, allow_all=True),
            "destination": _canonical_prefix(entry.get("dst", entry.get("to", "all")), family, allow_all=True),
            "fwmark": fwmark,
            "fwmask": fwmask,
            "iif": str(entry.get("iif", "")),
            "oif": str(entry.get("oif", "")),
            "l3mdev": bool(entry.get("l3mdev", False)),
            "suppress_prefixlength": entry.get("suppress_prefixlength"),
            "uidrange": str(entry.get("uidrange", "")),
        }
        rules.append(rule)
    return sorted(rules, key=lambda item: json.dumps(item, sort_keys=True, separators=(",", ":")))


def _normalize_multipath(value: Any, family: str) -> list[dict[str, Any]]:
    if value is None:
        return []
    if not isinstance(value, list):
        raise IsolationError("route multipath evidence is malformed")
    result = []
    for raw in value:
        entry = _required_mapping(raw, "route next-hop evidence")
        result.append(
            {
                "gateway": _canonical_address(entry.get("gateway", entry.get("via", "")), family),
                "dev": str(entry.get("dev", "")),
                "weight": entry.get("weight"),
            }
        )
    return sorted(result, key=lambda item: json.dumps(item, sort_keys=True, separators=(",", ":")))


def _normalize_routes(value: Any, family: str) -> list[dict[str, Any]]:
    if not isinstance(value, list):
        raise IsolationError("route inventory is malformed")
    routes: list[dict[str, Any]] = []
    for raw in value:
        entry = _required_mapping(raw, "route inventory entry")
        route = {
            "family": family,
            "table": _canonical_table(entry.get("table")),
            "type": str(entry.get("type", "unicast")),
            "dst": _canonical_prefix(entry.get("dst", "default"), family),
            "gateway": _canonical_address(entry.get("gateway", entry.get("via", "")), family),
            "dev": str(entry.get("dev", "")),
            "protocol": str(entry.get("protocol", "")),
            "scope": str(entry.get("scope", "")),
            "metric": entry.get("metric"),
            "prefsrc": _canonical_address(entry.get("prefsrc", ""), family),
            "src": _canonical_prefix(entry.get("src", ""), family),
            "mark": _canonical_mark_part(entry.get("mark", "")),
            "nhid": entry.get("nhid"),
            "multipath": _normalize_multipath(entry.get("multipath"), family),
        }
        routes.append(route)
    return sorted(routes, key=lambda item: json.dumps(item, sort_keys=True, separators=(",", ":")))


def _strip_nft_dynamic(value: Any) -> Any:
    if isinstance(value, list):
        return [_strip_nft_dynamic(item) for item in value]
    if isinstance(value, Mapping):
        ignored = {"bytes", "handle", "index", "packets", "use"}
        return {
            str(key): _strip_nft_dynamic(item)
            for key, item in sorted(value.items(), key=lambda pair: str(pair[0]))
            if str(key) not in ignored
        }
    return value


def _normalize_nftables(value: Any) -> list[Any]:
    root = _required_mapping(value, "nftables inventory")
    objects = root.get("nftables")
    if not isinstance(objects, list):
        raise IsolationError("nftables inventory is malformed")
    normalized = []
    for raw in objects:
        entry = _required_mapping(raw, "nftables inventory entry")
        if "metainfo" in entry:
            continue
        normalized.append(_strip_nft_dynamic(entry))
    return sorted(normalized, key=lambda item: json.dumps(item, sort_keys=True, separators=(",", ":")))


_LINK_HEADING = re.compile(r"^Link [0-9]+ \(([^)]+)\)$")


def _normalize_resolved(value: str) -> dict[str, Any]:
    if not isinstance(value, str):
        raise IsolationError("resolver inventory is malformed")
    global_lines: list[str] = []
    links: list[dict[str, Any]] = []
    current: dict[str, Any] | None = None
    for raw in value.splitlines():
        line = raw.strip()
        if not line:
            continue
        match = _LINK_HEADING.fullmatch(line)
        if match is not None:
            current = {"ifname": match.group(1), "lines": []}
            links.append(current)
            continue
        if current is None:
            global_lines.append(line)
        else:
            current["lines"].append(line)
    for link in links:
        link["lines"] = sorted(link["lines"])
    return {
        "global": sorted(global_lines),
        "links": sorted(links, key=lambda item: item["ifname"]),
    }


def normalize_snapshot(raw: Mapping[str, Any], *, network_namespace_inode: int) -> dict[str, Any]:
    if network_namespace_inode <= 0:
        raise IsolationError("network namespace identity is unavailable")
    return {
        "schema_version": SCHEMA_VERSION,
        "network_namespace_inode": network_namespace_inode,
        "links": _normalize_links(raw.get("links")),
        "addresses": _normalize_addresses(raw.get("addresses")),
        "rules_v4": _normalize_rules(raw.get("rules_v4"), "ipv4"),
        "rules_v6": _normalize_rules(raw.get("rules_v6"), "ipv6"),
        "routes_v4": _normalize_routes(raw.get("routes_v4"), "ipv4"),
        "routes_v6": _normalize_routes(raw.get("routes_v6"), "ipv6"),
        "nftables": _normalize_nftables(raw.get("nftables")),
        "resolved": _normalize_resolved(raw.get("resolved")),
    }


def _default_route_devices(snapshot: Mapping[str, Any]) -> set[str]:
    devices = set()
    for key in ("routes_v4", "routes_v6"):
        routes = snapshot.get(key)
        if not isinstance(routes, list):
            raise IsolationError("route inventory is unavailable")
        for route in routes:
            if (
                isinstance(route, Mapping)
                and route.get("table") == "main"
                and route.get("dst") == "default"
                and route.get("type") == "unicast"
                and isinstance(route.get("dev"), str)
                and route.get("dev")
            ):
                devices.add(str(route["dev"]))
    return devices


def validate_clean_baseline(snapshot: Mapping[str, Any]) -> None:
    links = snapshot.get("links")
    if not isinstance(links, list):
        raise IsolationError("link inventory is unavailable")
    link_by_name: dict[str, Mapping[str, Any]] = {}
    for raw in links:
        link = _required_mapping(raw, "link inventory entry")
        ifname = str(link.get("ifname", ""))
        kind = str(link.get("kind", ""))
        link_by_name[ifname] = link
        if ifname == PODLAZ_LINK:
            raise IsolationError("reserved Podlaz link exists before the soak")
        if kind in SUSPICIOUS_LINK_KINDS:
            raise IsolationError("foreign tunnel-style link exists before the soak")

    for key in ("rules_v4", "rules_v6"):
        rules = snapshot.get(key)
        if not isinstance(rules, list):
            raise IsolationError("policy-rule inventory is unavailable")
        for raw in rules:
            rule = _required_mapping(raw, "policy-rule inventory entry")
            if (rule.get("priority"), rule.get("table")) not in DEFAULT_RULES:
                raise IsolationError("foreign policy routing exists before the soak")

    for key in ("routes_v4", "routes_v6"):
        routes = snapshot.get(key)
        if not isinstance(routes, list):
            raise IsolationError("route inventory is unavailable")
        for raw in routes:
            route = _required_mapping(raw, "route inventory entry")
            table = route.get("table")
            if table not in {"default", "local", "main"}:
                raise IsolationError("foreign routing table exists before the soak")
            if table != "main":
                continue
            if route.get("type") != "unicast" or route.get("mark") or route.get("multipath"):
                raise IsolationError("foreign main-table route exists before the soak")
            if route.get("dst") == "default":
                if not route.get("dev") or route.get("protocol") not in {"boot", "dhcp", "kernel", "ra", "static"}:
                    raise IsolationError("default-route ownership is ambiguous before the soak")
                continue
            if (
                route.get("gateway")
                or route.get("scope") != "link"
                or route.get("protocol") not in {"dhcp", "kernel", "ra"}
            ):
                raise IsolationError("foreign main-table route exists before the soak")

    nftables = snapshot.get("nftables")
    if not isinstance(nftables, list):
        raise IsolationError("nftables inventory is unavailable")
    if nftables:
        raise IsolationError("pre-existing nftables packet-path state makes attribution ambiguous")

    default_devices = _default_route_devices(snapshot)
    if not default_devices:
        raise IsolationError("no authoritative default-route uplink exists before the soak")
    for device in default_devices:
        link = link_by_name.get(device)
        if link is None or str(link.get("kind", "")) in SUSPICIOUS_LINK_KINDS:
            raise IsolationError("default route uses an ambiguous tunnel-style link")

    resolved = _required_mapping(snapshot.get("resolved"), "resolver inventory")
    resolved_links = resolved.get("links")
    if not isinstance(resolved_links, list):
        raise IsolationError("resolver link inventory is malformed")
    for raw in resolved_links:
        link = _required_mapping(raw, "resolver link inventory entry")
        ifname = str(link.get("ifname", ""))
        lines = link.get("lines")
        if not isinstance(lines, list) or not all(isinstance(line, str) for line in lines):
            raise IsolationError("resolver link inventory is malformed")
        route_all = any("~." in line for line in lines)
        default_route = any("+DefaultRoute" in line or "DefaultRoute setting: yes" in line for line in lines)
        if (route_all or default_route) and ifname not in default_devices:
            raise IsolationError("foreign resolver default-route ownership exists before the soak")


def _nft_entry_belongs_to_podlaz(entry: Mapping[str, Any]) -> bool:
    for value in entry.values():
        if not isinstance(value, Mapping):
            continue
        family = value.get("family")
        table = value.get("table", value.get("name"))
        if family == PODLAZ_NFT_FAMILY and table == PODLAZ_NFT_TABLE:
            return True
    return False


def _load_manifest(path: Path) -> dict[str, list[dict[str, Any]]]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise IsolationError("exact Podlaz network manifest is unavailable") from exc
    root = _required_mapping(value, "network manifest")
    if root.get("schema_version") != "podlaz.e2e.rollback-network.v1":
        raise IsolationError("exact Podlaz network manifest has an unsupported schema")
    result: dict[str, list[dict[str, Any]]] = {"routes": [], "rules": []}
    routes = root.get("routes")
    rules = root.get("rules")
    if not isinstance(routes, list) or not isinstance(rules, list):
        raise IsolationError("exact Podlaz network manifest is malformed")
    for raw in routes:
        route = _required_mapping(raw, "manifest route")
        family = "ipv4" if route.get("family") == "-4" else "ipv6" if route.get("family") == "-6" else ""
        if not family:
            raise IsolationError("manifest route family is unsupported")
        result["routes"].append(
            {
                "family": family,
                "table": _canonical_table(route.get("table")),
                "dst": _canonical_prefix(route.get("cidr", ""), family),
                "gateway": _canonical_address(route.get("via", ""), family),
                "dev": str(route.get("dev", "")),
            }
        )
    for raw in rules:
        rule = _required_mapping(raw, "manifest rule")
        family = "ipv4" if rule.get("family") == "-4" else "ipv6" if rule.get("family") == "-6" else ""
        priority = rule.get("priority")
        if not family or not isinstance(priority, int):
            raise IsolationError("manifest rule identity is unsupported")
        fwmark, fwmask = _split_mark(rule.get("mark", ""))
        result["rules"].append(
            {
                "family": family,
                "priority": priority,
                "table": _canonical_table(rule.get("table")),
                "source": _canonical_prefix(rule.get("source", ""), family, allow_all=True),
                "destination": _canonical_prefix(rule.get("destination", ""), family, allow_all=True),
                "fwmark": fwmark,
                "fwmask": fwmask,
            }
        )
    return result


def _remove_exact(items: list[dict[str, Any]], expected: Mapping[str, Any], matcher: Any, label: str) -> None:
    indexes = [index for index, item in enumerate(items) if matcher(item, expected)]
    if len(indexes) != 1:
        raise IsolationError(f"exact Podlaz {label} projection is missing or ambiguous")
    del items[indexes[0]]


def _route_matches(actual: Mapping[str, Any], expected: Mapping[str, Any]) -> bool:
    return all(actual.get(name) == expected.get(name) for name in ("family", "table", "dst", "gateway", "dev"))


def _rule_matches(actual: Mapping[str, Any], expected: Mapping[str, Any]) -> bool:
    def canonical_selector(value: Any) -> str:
        text = str(value or "")
        return "all" if text in {"", "all"} else text

    return (
        actual.get("family") == expected.get("family")
        and actual.get("priority") == expected.get("priority")
        and actual.get("table") == expected.get("table")
        and canonical_selector(actual.get("source")) == canonical_selector(expected.get("source"))
        and canonical_selector(actual.get("destination")) == canonical_selector(expected.get("destination"))
        and str(actual.get("fwmark", "")) == str(expected.get("fwmark", ""))
        and str(actual.get("fwmask", "")) == str(expected.get("fwmask", ""))
    )


def strip_exact_podlaz_state(snapshot: Mapping[str, Any], manifest: Mapping[str, Sequence[Mapping[str, Any]]]) -> dict[str, Any]:
    result = copy.deepcopy(dict(snapshot))
    links = result.get("links")
    if not isinstance(links, list):
        raise IsolationError("link inventory is unavailable")
    result["links"] = [link for link in links if isinstance(link, Mapping) and link.get("ifname") != PODLAZ_LINK]
    addresses = result.get("addresses")
    if not isinstance(addresses, list):
        raise IsolationError("address inventory is unavailable")
    result["addresses"] = [
        address
        for address in addresses
        if isinstance(address, Mapping) and address.get("ifname") != PODLAZ_LINK
    ]

    for family, key in (("ipv4", "routes_v4"), ("ipv6", "routes_v6")):
        routes = result.get(key)
        if not isinstance(routes, list):
            raise IsolationError("route inventory is unavailable")
        for route in manifest.get("routes", []):
            if route.get("family") == family:
                _remove_exact(routes, route, _route_matches, "route")

    for family, key in (("ipv4", "rules_v4"), ("ipv6", "rules_v6")):
        rules = result.get(key)
        if not isinstance(rules, list):
            raise IsolationError("policy-rule inventory is unavailable")
        for rule in manifest.get("rules", []):
            if rule.get("family") == family:
                _remove_exact(rules, rule, _rule_matches, "policy rule")

    nftables = result.get("nftables")
    if not isinstance(nftables, list):
        raise IsolationError("nftables inventory is unavailable")
    result["nftables"] = [
        entry
        for entry in nftables
        if not (isinstance(entry, Mapping) and _nft_entry_belongs_to_podlaz(entry))
    ]

    resolved = _required_mapping(result.get("resolved"), "resolver inventory")
    resolved_links = resolved.get("links")
    if not isinstance(resolved_links, list):
        raise IsolationError("resolver link inventory is malformed")
    resolved["links"] = [
        link
        for link in resolved_links
        if not (isinstance(link, Mapping) and link.get("ifname") == PODLAZ_LINK)
    ]

    if any(isinstance(link, Mapping) and link.get("ifname") == PODLAZ_LINK for link in result["links"]):
        raise IsolationError("reserved Podlaz link projection remains")
    for key in ("routes_v4", "routes_v6"):
        if any(isinstance(route, Mapping) and route.get("table") == "51820" for route in result[key]):
            raise IsolationError("unclaimed Podlaz routing-table state remains")
    for key in ("rules_v4", "rules_v6"):
        if any(
            isinstance(rule, Mapping)
            and (rule.get("table") == "51820" or rule.get("priority") in {9999, 10000})
            for rule in result[key]
        ):
            raise IsolationError("unclaimed Podlaz policy-rule state remains")
    if any(isinstance(entry, Mapping) and _nft_entry_belongs_to_podlaz(entry) for entry in result["nftables"]):
        raise IsolationError("unclaimed Podlaz nftables state remains")
    return result


def assert_matches_baseline(
    *,
    baseline: Mapping[str, Any],
    current: Mapping[str, Any],
    manifest: Mapping[str, Sequence[Mapping[str, Any]]] | None = None,
) -> None:
    observed = strip_exact_podlaz_state(current, manifest) if manifest is not None else dict(current)
    if observed != dict(baseline):
        raise IsolationError("foreign or underlying network state changed during the soak")


def _network_namespace_inode() -> int:
    try:
        host_inode = os.stat("/proc/1/ns/net").st_ino
        self_inode = os.stat("/proc/self/ns/net").st_ino
    except OSError as exc:
        raise IsolationError("host network namespace identity is unavailable") from exc
    if host_inode <= 0 or self_inode != host_inode:
        raise IsolationError("structural inspection is outside the host network namespace")
    return host_inode


def collect_snapshot() -> dict[str, Any]:
    raw = {
        "links": _json_command(("ip", "-j", "-d", "link", "show")),
        "addresses": _json_command(("ip", "-j", "address", "show")),
        "rules_v4": _json_command(("ip", "-j", "-4", "rule", "show")),
        "rules_v6": _json_command(("ip", "-j", "-6", "rule", "show")),
        "routes_v4": _json_command(("ip", "-j", "-4", "route", "show", "table", "all")),
        "routes_v6": _json_command(("ip", "-j", "-6", "route", "show", "table", "all")),
        "nftables": _json_command(("nft", "-j", "list", "ruleset")),
        "resolved": _text_command(("resolvectl", "status", "--no-pager")),
    }
    return normalize_snapshot(raw, network_namespace_inode=_network_namespace_inode())


def _atomic_write(path: Path, payload: Mapping[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    temporary = path.with_name(path.name + ".tmp")
    with temporary.open("w", encoding="utf-8") as handle:
        json.dump(payload, handle, sort_keys=True, separators=(",", ":"))
        handle.write("\n")
    os.chmod(temporary, 0o600)
    os.replace(temporary, path)


def _load_snapshot(path: Path) -> Mapping[str, Any]:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise IsolationError("network isolation baseline is unavailable") from exc
    root = _required_mapping(value, "network isolation baseline")
    if root.get("schema_version") != SCHEMA_VERSION:
        raise IsolationError("network isolation baseline has an unsupported schema")
    return root


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="command", required=True)
    capture = subparsers.add_parser("capture")
    capture.add_argument("--output", type=Path, required=True)
    verify = subparsers.add_parser("verify")
    verify.add_argument("--baseline", type=Path, required=True)
    verify.add_argument("--manifest", type=Path)
    return parser


def main(argv: Sequence[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    try:
        if args.command == "capture":
            snapshot = collect_snapshot()
            validate_clean_baseline(snapshot)
            _atomic_write(args.output, snapshot)
            return 0
        baseline = _load_snapshot(args.baseline)
        current = collect_snapshot()
        manifest = _load_manifest(args.manifest) if args.manifest is not None else None
        assert_matches_baseline(baseline=baseline, current=current, manifest=manifest)
        return 0
    except IsolationError as exc:
        print(f"network isolation verification failed: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
