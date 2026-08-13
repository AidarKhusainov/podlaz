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
DEFAULT_RULE_LAYOUT = {
    "ipv4": ((0, "local"), (32766, "main"), (32767, "default")),
    "ipv6": ((0, "local"), (32766, "main")),
}
DEDICATED_UPLINK_LINK_TYPE = "ether"

RULE_RAW_FIELDS = frozenset(
    {
        "priority",
        "table",
        "action",
        "src",
        "from",
        "dst",
        "to",
        "fwmark",
        "fwmask",
        "iif",
        "oif",
        "l3mdev",
        "suppress_prefixlength",
        "uidrange",
    }
)
# The dedicated-runner gate currently ignores no rule/route JSON fields. Any
# future iproute2 field must be classified explicitly before acceptance so a
# new semantic selector or attribute can never disappear during normalization.
RULE_RUNTIME_NOISE_FIELDS = frozenset()
ROUTE_RAW_FIELDS = frozenset(
    {
        "table",
        "type",
        "dst",
        "gateway",
        "via",
        "dev",
        "protocol",
        "scope",
        "metric",
        "prefsrc",
        "src",
        "mark",
        "nhid",
        "multipath",
        "flags",
        "pref",
    }
)
ROUTE_RUNTIME_NOISE_FIELDS = frozenset()
MULTIPATH_RAW_FIELDS = frozenset({"gateway", "via", "dev", "weight", "flags"})
MULTIPATH_RUNTIME_NOISE_FIELDS = frozenset()



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


def _reject_unknown_raw_fields(
    entry: Mapping[str, Any],
    *,
    supported: frozenset[str],
    runtime_noise: frozenset[str],
    label: str,
) -> None:
    if any(not isinstance(key, str) for key in entry):
        raise IsolationError(f"unsupported {label} field")
    unknown = sorted(set(entry) - supported - runtime_noise)
    if unknown:
        raise IsolationError(f"unsupported {label} field: {unknown[0]}")


def _reject_ambiguous_aliases(entry: Mapping[str, Any], first: str, second: str, label: str) -> None:
    if first in entry and second in entry:
        raise IsolationError(f"{label} aliases are ambiguous")


def _normalize_string_flags(value: Any, label: str) -> list[str]:
    if value is None:
        return []
    if not isinstance(value, list) or any(not isinstance(item, str) or not item for item in value):
        raise IsolationError(f"{label} flags are malformed")
    if len(set(value)) != len(value):
        raise IsolationError(f"{label} flags are duplicated")
    return sorted(value)


def _optional_non_negative_int(value: Any, label: str) -> int | None:
    if value is None:
        return None
    if not isinstance(value, int) or isinstance(value, bool) or value < 0:
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
        raw_flags = entry.get("flags")
        if not isinstance(raw_flags, list):
            raise IsolationError("link flag evidence is incomplete")
        flags = _normalize_string_flags(raw_flags, "link")
        links.append(
            {
                "ifindex": ifindex,
                "ifname": ifname,
                "kind": kind,
                "master": master,
                "mtu": mtu,
                "link_type": str(entry.get("link_type", "")),
                "flags": flags,
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
        _reject_unknown_raw_fields(
            entry,
            supported=RULE_RAW_FIELDS,
            runtime_noise=RULE_RUNTIME_NOISE_FIELDS,
            label="policy-rule",
        )
        _reject_ambiguous_aliases(entry, "src", "from", "policy-rule source")
        _reject_ambiguous_aliases(entry, "dst", "to", "policy-rule destination")
        priority = entry.get("priority")
        if not isinstance(priority, int) or priority < 0:
            raise IsolationError("policy-rule priority is malformed")
        fwmark, fwmask = _split_mark(entry.get("fwmark", ""), entry.get("fwmask", ""))
        l3mdev = entry.get("l3mdev", False)
        if not isinstance(l3mdev, bool):
            raise IsolationError("policy-rule l3mdev evidence is malformed")
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
            "l3mdev": l3mdev,
            "suppress_prefixlength": _optional_non_negative_int(
                entry.get("suppress_prefixlength"),
                "policy-rule suppress_prefixlength evidence",
            ),
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
        _reject_unknown_raw_fields(
            entry,
            supported=MULTIPATH_RAW_FIELDS,
            runtime_noise=MULTIPATH_RUNTIME_NOISE_FIELDS,
            label="route next-hop",
        )
        _reject_ambiguous_aliases(entry, "gateway", "via", "route next-hop gateway")
        result.append(
            {
                "gateway": _canonical_address(entry.get("gateway", entry.get("via", "")), family),
                "dev": str(entry.get("dev", "")),
                "weight": _optional_non_negative_int(entry.get("weight"), "route next-hop weight evidence"),
                "flags": _normalize_string_flags(entry.get("flags", []), "route next-hop"),
            }
        )
    return sorted(result, key=lambda item: json.dumps(item, sort_keys=True, separators=(",", ":")))


def _normalize_routes(value: Any, family: str) -> list[dict[str, Any]]:
    if not isinstance(value, list):
        raise IsolationError("route inventory is malformed")
    routes: list[dict[str, Any]] = []
    for raw in value:
        entry = _required_mapping(raw, "route inventory entry")
        _reject_unknown_raw_fields(
            entry,
            supported=ROUTE_RAW_FIELDS,
            runtime_noise=ROUTE_RUNTIME_NOISE_FIELDS,
            label="route",
        )
        _reject_ambiguous_aliases(entry, "gateway", "via", "route gateway")
        preference = str(entry.get("pref", ""))
        if preference not in {"", "low", "medium", "high"}:
            raise IsolationError("route preference evidence is malformed")
        route = {
            "family": family,
            "table": _canonical_table(entry.get("table")),
            "type": str(entry.get("type", "unicast")),
            "dst": _canonical_prefix(entry.get("dst", "default"), family),
            "gateway": _canonical_address(entry.get("gateway", entry.get("via", "")), family),
            "dev": str(entry.get("dev", "")),
            "protocol": str(entry.get("protocol", "")),
            "scope": str(entry.get("scope", "")),
            "metric": _optional_non_negative_int(entry.get("metric"), "route metric evidence"),
            "prefsrc": _canonical_address(entry.get("prefsrc", ""), family),
            "src": _canonical_prefix(entry.get("src", ""), family),
            "mark": _canonical_mark_part(entry.get("mark", "")),
            "nhid": _optional_non_negative_int(entry.get("nhid"), "route nexthop identity evidence"),
            "multipath": _normalize_multipath(entry.get("multipath"), family),
            "flags": _normalize_string_flags(entry.get("flags", []), "route"),
            "preference": preference,
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


def _structural_sort(items: Sequence[Mapping[str, Any]]) -> list[dict[str, Any]]:
    return sorted(
        (dict(item) for item in items),
        key=lambda item: json.dumps(item, sort_keys=True, separators=(",", ":")),
    )


def _canonical_default_rule(family: str, priority: int, table: str) -> dict[str, Any]:
    return {
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


def _validate_canonical_default_rules(rules: Sequence[Mapping[str, Any]], family: str) -> None:
    layout = DEFAULT_RULE_LAYOUT.get(family)
    if layout is None:
        raise IsolationError("policy-rule family is unsupported")
    expected = _structural_sort(
        [_canonical_default_rule(family, priority, table) for priority, table in layout]
    )
    if _structural_sort(rules) != expected:
        raise IsolationError("canonical default policy-rule set is missing, modified, or duplicated")


def _route_shape(
    *,
    family: str,
    table: str,
    type_name: str,
    destination: str,
    device: str,
    protocol: str,
    scope: str,
    metric: int | None,
    preferred_source: str,
    preference: str = "",
    flags: Sequence[str] = (),
) -> dict[str, Any]:
    return {
        "family": family,
        "table": table,
        "type": type_name,
        "dst": destination,
        "gateway": "",
        "dev": device,
        "protocol": protocol,
        "scope": scope,
        "metric": metric,
        "prefsrc": preferred_source,
        "src": "",
        "mark": "",
        "nhid": None,
        "multipath": [],
        "flags": sorted(flags),
        "preference": preference,
    }


def _route_key(route: Mapping[str, Any]) -> str:
    return json.dumps(dict(route), sort_keys=True, separators=(",", ":"))


def _link_flags_by_name(snapshot: Mapping[str, Any]) -> dict[str, frozenset[str]]:
    links = snapshot.get("links")
    if not isinstance(links, list):
        raise IsolationError("link inventory is unavailable")
    result: dict[str, frozenset[str]] = {}
    for raw_link in links:
        link = _required_mapping(raw_link, "link inventory entry")
        ifname = link.get("ifname")
        flags = link.get("flags")
        if (
            not isinstance(ifname, str)
            or not ifname
            or ifname in result
            or not isinstance(flags, list)
            or any(not isinstance(flag, str) or not flag for flag in flags)
        ):
            raise IsolationError("link flag evidence is incomplete or ambiguous")
        result[ifname] = frozenset(flags)
    return result


def _local_route_expectations(snapshot: Mapping[str, Any]) -> tuple[set[str], set[str]]:
    """Derive mandatory and explicitly optional kernel-owned local-table routes."""

    addresses = snapshot.get("addresses")
    if not isinstance(addresses, list):
        raise IsolationError("address inventory is unavailable")
    link_flags = _link_flags_by_name(snapshot)

    required: set[str] = set()
    optional: set[str] = set()
    required_ipv6_multicast_links: set[str] = set()
    optional_ipv6_multicast_links: set[str] = set()
    for raw_link in addresses:
        link = _required_mapping(raw_link, "address inventory entry")
        ifname = link.get("ifname")
        raw_addresses = link.get("addresses")
        if (
            not isinstance(ifname, str)
            or not ifname
            or ifname not in link_flags
            or not isinstance(raw_addresses, list)
        ):
            raise IsolationError("address inventory entry is malformed")
        for raw_address in raw_addresses:
            address = _required_mapping(raw_address, "interface address entry")
            raw_family = address.get("family")
            family = "ipv4" if raw_family == "inet" else "ipv6" if raw_family == "inet6" else ""
            local = address.get("local")
            prefixlen = address.get("prefixlen")
            scope = address.get("scope")
            flags = address.get("flags")
            extras = address.get("extras")
            if (
                not family
                or not isinstance(local, str)
                or not local
                or not isinstance(prefixlen, int)
                or not isinstance(scope, str)
                or not isinstance(flags, list)
                or any(not isinstance(flag, str) or not flag for flag in flags)
                or not isinstance(extras, Mapping)
            ):
                raise IsolationError("interface address entry is malformed")
            try:
                interface = ipaddress.ip_interface(f"{local}/{prefixlen}")
            except ValueError as exc:
                raise IsolationError("interface address entry is malformed") from exc

            if family == "ipv4":
                host_destination = ipaddress.ip_network(f"{local}/32", strict=False).with_prefixlen
                required.add(
                    _route_key(
                        _route_shape(
                            family=family,
                            table="local",
                            type_name="local",
                            destination=host_destination,
                            device=ifname,
                            protocol="kernel",
                            scope="host",
                            metric=None,
                            preferred_source=local,
                        )
                    )
                )
                if ifname == "lo" and scope == "host":
                    required.add(
                        _route_key(
                            _route_shape(
                                family=family,
                                table="local",
                                type_name="local",
                                destination=interface.network.with_prefixlen,
                                device=ifname,
                                protocol="kernel",
                                scope="host",
                                metric=None,
                                preferred_source=local,
                            )
                        )
                    )

                # RTNH_F_LINKDOWN is a semantic route flag. It is required
                # only when independent link evidence shows that the attached
                # non-loopback device has no lower-layer carrier. Treating it
                # as generic optional noise would allow a foreign route
                # mutation to masquerade as a kernel variant.
                broadcast_route_flags = (
                    ["linkdown"]
                    if ifname != "lo" and "LOWER_UP" not in link_flags[ifname]
                    else []
                )

                explicit_broadcast = extras.get("broadcast")
                broadcast = ""
                if explicit_broadcast:
                    broadcast = _canonical_address(explicit_broadcast, family)
                elif interface.network.prefixlen <= 30:
                    broadcast = str(interface.network.broadcast_address)
                if broadcast:
                    broadcast_key = _route_key(
                        _route_shape(
                            family=family,
                            table="local",
                            type_name="broadcast",
                            destination=ipaddress.ip_network(f"{broadcast}/32", strict=False).with_prefixlen,
                            device=ifname,
                            protocol="kernel",
                            scope="link",
                            metric=None,
                            preferred_source=local,
                            flags=broadcast_route_flags,
                        )
                    )
                    # The primary address' explicit broadcast route and the
                    # loopback broadcast route are mandatory. A secondary
                    # address can share its primary address' broadcast entry;
                    # when the address dump contains no explicit broadcast,
                    # kernels/configurations may omit the derived variant.
                    if (explicit_broadcast and "secondary" not in flags) or ifname == "lo":
                        required.add(broadcast_key)
                    else:
                        optional.add(broadcast_key)

                # Some supported kernels expose the subnet-network address as
                # a second RTN_BROADCAST entry while others expose only the
                # high broadcast address. It is therefore an explicit optional
                # variant, never a substitute for a mandatory address-local
                # route or an explicitly advertised broadcast route.
                if ifname != "lo" and interface.network.prefixlen <= 30:
                    network_broadcast_key = _route_key(
                        _route_shape(
                            family=family,
                            table="local",
                            type_name="broadcast",
                            destination=ipaddress.ip_network(
                                f"{interface.network.network_address}/32", strict=False
                            ).with_prefixlen,
                            device=ifname,
                            protocol="kernel",
                            scope="link",
                            metric=None,
                            preferred_source=local,
                            flags=broadcast_route_flags,
                        )
                    )
                    optional.add(network_broadcast_key)
                continue

            required.add(
                _route_key(
                    _route_shape(
                        family=family,
                        table="local",
                        type_name="local",
                        destination=ipaddress.ip_network(f"{local}/128", strict=False).with_prefixlen,
                        device=ifname,
                        protocol="kernel",
                        scope="",
                        metric=0,
                        preferred_source="",
                        preference="medium",
                    )
                )
            )
            if ifname == "lo":
                optional_ipv6_multicast_links.add(ifname)
            else:
                required_ipv6_multicast_links.add(ifname)

    for ifname in required_ipv6_multicast_links | optional_ipv6_multicast_links:
        multicast_key = _route_key(
            _route_shape(
                family="ipv6",
                table="local",
                type_name="multicast",
                destination="ff00::/8",
                device=ifname,
                protocol="kernel",
                scope="",
                metric=256,
                preferred_source="",
                preference="medium",
            )
        )
        if ifname in required_ipv6_multicast_links:
            required.add(multicast_key)
        else:
            optional.add(multicast_key)

    optional.difference_update(required)
    return required, optional


def _validate_non_main_routes(snapshot: Mapping[str, Any]) -> None:
    required_local, optional_local = _local_route_expectations(snapshot)
    permitted_local = required_local | optional_local
    observed_local: set[str] = set()
    for key in ("routes_v4", "routes_v6"):
        routes = snapshot.get(key)
        if not isinstance(routes, list):
            raise IsolationError("route inventory is unavailable")
        for raw in routes:
            route = _required_mapping(raw, "route inventory entry")
            table = route.get("table")
            if table == "default":
                raise IsolationError("default routing table must be empty on the dedicated runner")
            if table != "local":
                continue
            identity = _route_key(route)
            if identity in observed_local:
                raise IsolationError("local-table route is duplicated")
            observed_local.add(identity)
            if identity not in permitted_local:
                raise IsolationError("local-table route is not derived from the positive link/address baseline")

    if required_local - observed_local:
        raise IsolationError("required local-table route is missing from the positive link/address baseline")


def _default_route_metrics(snapshot: Mapping[str, Any]) -> dict[tuple[str, str], int]:
    result: dict[tuple[str, str], int] = {}
    for key, family in (("routes_v4", "ipv4"), ("routes_v6", "ipv6")):
        routes = snapshot.get(key)
        if not isinstance(routes, list):
            raise IsolationError("route inventory is unavailable")
        for raw in routes:
            route = _required_mapping(raw, "route inventory entry")
            if route.get("table") != "main" or route.get("dst") != "default":
                continue
            device = route.get("dev")
            metric = route.get("metric")
            if not isinstance(device, str) or not device:
                raise IsolationError("default-route ownership is ambiguous before the soak")
            if metric is None:
                continue
            if not isinstance(metric, int) or isinstance(metric, bool) or metric < 0:
                raise IsolationError("default-route metric evidence is malformed")
            identity = (family, device)
            previous = result.get(identity)
            if previous is not None and previous != metric:
                raise IsolationError("default-route metric evidence is ambiguous")
            result[identity] = metric
    return result


def _main_connected_route_expectations(
    snapshot: Mapping[str, Any],
) -> tuple[list[frozenset[str]], list[frozenset[str]]]:
    """Derive exact required and prohibited-when-absent connected-route shapes."""

    addresses = snapshot.get("addresses")
    if not isinstance(addresses, list):
        raise IsolationError("address inventory is unavailable")
    link_flags = _link_flags_by_name(snapshot)
    default_metrics = _default_route_metrics(snapshot)
    grouped: dict[tuple[str, str, str], list[Mapping[str, Any]]] = {}

    for raw_link in addresses:
        link = _required_mapping(raw_link, "address inventory entry")
        ifname = link.get("ifname")
        raw_addresses = link.get("addresses")
        if (
            not isinstance(ifname, str)
            or not ifname
            or ifname not in link_flags
            or not isinstance(raw_addresses, list)
        ):
            raise IsolationError("address inventory entry is malformed")
        for raw_address in raw_addresses:
            address = _required_mapping(raw_address, "interface address entry")
            raw_family = address.get("family")
            family = "ipv4" if raw_family == "inet" else "ipv6" if raw_family == "inet6" else ""
            local = address.get("local")
            prefixlen = address.get("prefixlen")
            scope = address.get("scope")
            flags = address.get("flags")
            extras = address.get("extras")
            if (
                not family
                or not isinstance(local, str)
                or not local
                or not isinstance(prefixlen, int)
                or not isinstance(scope, str)
                or not isinstance(flags, list)
                or any(not isinstance(flag, str) or not flag for flag in flags)
                or not isinstance(extras, Mapping)
            ):
                raise IsolationError("interface address entry is malformed")
            try:
                interface = ipaddress.ip_interface(f"{local}/{prefixlen}")
            except ValueError as exc:
                raise IsolationError("interface address entry is malformed") from exc
            maximum_prefix = 32 if family == "ipv4" else 128
            if ifname == "lo" or scope == "host" or interface.network.prefixlen == maximum_prefix:
                continue
            grouped.setdefault((family, ifname, interface.network.with_prefixlen), []).append(address)

    required_groups: list[frozenset[str]] = []
    suppressed_groups: list[frozenset[str]] = []
    for (family, ifname, destination), group in sorted(grouped.items()):
        primary = [address for address in group if "secondary" not in address.get("flags", [])]
        if len(primary) != 1:
            raise IsolationError("connected-prefix primary address evidence is ambiguous")
        primary_address = primary[0]
        primary_flags = frozenset(str(flag) for flag in primary_address.get("flags", []))
        if "noprefixroute" in primary_flags:
            if any("noprefixroute" not in address.get("flags", []) for address in group):
                raise IsolationError("connected-prefix noprefixroute evidence is ambiguous")
            target = suppressed_groups
        else:
            target = required_groups

        local = str(primary_address["local"])
        extras = _required_mapping(primary_address.get("extras"), "interface address extras")
        explicit_metric = extras.get("metric")
        if explicit_metric is not None:
            if not isinstance(explicit_metric, int) or isinstance(explicit_metric, bool) or explicit_metric < 0:
                raise IsolationError("interface address route metric evidence is malformed")
            metric_candidates: set[int | None] = {explicit_metric}
        elif family == "ipv6":
            metric_candidates = {256}
        else:
            metric_candidates = {None}
            default_metric = default_metrics.get((family, ifname))
            if default_metric is not None:
                metric_candidates.add(default_metric)

        protocol_candidates = {"kernel"}
        if family == "ipv6" and extras.get("protocol") == "ra":
            protocol_candidates.add("ra")
        route_flags = ["linkdown"] if "LOWER_UP" not in link_flags[ifname] else []
        variants = frozenset(
            _route_key(
                _route_shape(
                    family=family,
                    table="main",
                    type_name="unicast",
                    destination=destination,
                    device=ifname,
                    protocol=protocol,
                    scope="link" if family == "ipv4" else "",
                    metric=metric,
                    preferred_source=local if family == "ipv4" else "",
                    preference="" if family == "ipv4" else "medium",
                    flags=route_flags,
                )
            )
            for protocol in sorted(protocol_candidates)
            for metric in sorted(metric_candidates, key=lambda value: -1 if value is None else value)
        )
        if not variants:
            raise IsolationError("connected-prefix route expectation is empty")
        target.append(variants)

    all_groups = required_groups + suppressed_groups
    for left, variants in enumerate(all_groups):
        for right in range(left + 1, len(all_groups)):
            if variants & all_groups[right]:
                raise IsolationError("connected-prefix route expectations are ambiguous")
    return required_groups, suppressed_groups


def _validate_main_connected_routes(snapshot: Mapping[str, Any]) -> None:
    required_groups, suppressed_groups = _main_connected_route_expectations(snapshot)
    groups = required_groups + suppressed_groups
    matches = [0] * len(groups)
    observed: set[str] = set()

    for key in ("routes_v4", "routes_v6"):
        routes = snapshot.get(key)
        if not isinstance(routes, list):
            raise IsolationError("route inventory is unavailable")
        for raw in routes:
            route = _required_mapping(raw, "route inventory entry")
            if route.get("table") != "main" or route.get("dst") == "default":
                continue
            identity = _route_key(route)
            if identity in observed:
                raise IsolationError("main-table connected route is duplicated")
            observed.add(identity)
            matching_groups = [index for index, variants in enumerate(groups) if identity in variants]
            if len(matching_groups) != 1:
                raise IsolationError(
                    "foreign main-table route is not derived from the positive link/address baseline"
                )
            index = matching_groups[0]
            matches[index] += 1
            if matches[index] > 1:
                raise IsolationError("main-table connected route cardinality is ambiguous")

    if any(matches[index] != 1 for index in range(len(required_groups))):
        raise IsolationError("required main-table connected route is missing")
    if any(matches[index] != 0 for index in range(len(required_groups), len(groups))):
        raise IsolationError("noprefixroute prefix unexpectedly owns a main-table connected route")


def _is_positive_physical_link(link: Mapping[str, Any]) -> bool:
    return (
        str(link.get("kind", "")) == ""
        and str(link.get("link_type", "")) == DEDICATED_UPLINK_LINK_TYPE
        and link.get("master") in {None, "", 0}
    )


def _default_routes(snapshot: Mapping[str, Any]) -> list[dict[str, Any]]:
    defaults: list[dict[str, Any]] = []
    for key in ("routes_v4", "routes_v6"):
        routes = snapshot.get(key)
        if not isinstance(routes, list):
            raise IsolationError("route inventory is unavailable")
        family_defaults = []
        for raw in routes:
            route = _required_mapping(raw, "route inventory entry")
            if route.get("table") == "main" and route.get("dst") == "default" and route.get("type") == "unicast":
                family_defaults.append(dict(route))
        if len(family_defaults) > 1:
            raise IsolationError("default-route ownership is ambiguous before the soak")
        defaults.extend(family_defaults)
    return defaults


def _default_route_devices(snapshot: Mapping[str, Any]) -> set[str]:
    devices: set[str] = set()
    for route in _default_routes(snapshot):
        device = route.get("dev")
        if not isinstance(device, str) or not device:
            raise IsolationError("default-route ownership is ambiguous before the soak")
        devices.add(device)
    return devices


def validate_clean_baseline(snapshot: Mapping[str, Any]) -> None:
    links = snapshot.get("links")
    if not isinstance(links, list):
        raise IsolationError("link inventory is unavailable")
    link_by_name: dict[str, Mapping[str, Any]] = {}
    link_indexes: set[int] = set()
    loopback_count = 0
    for raw in links:
        link = _required_mapping(raw, "link inventory entry")
        ifname = str(link.get("ifname", ""))
        ifindex = link.get("ifindex")
        if ifname in link_by_name or not isinstance(ifindex, int) or ifindex in link_indexes:
            raise IsolationError("link identity is duplicated or incomplete")
        link_by_name[ifname] = link
        link_indexes.add(ifindex)
        if ifname == PODLAZ_LINK:
            raise IsolationError("reserved Podlaz link exists before the soak")
        if ifname == "lo":
            loopback_count += 1
            if (
                str(link.get("kind", "")) != ""
                or str(link.get("link_type", "")) != "loopback"
                or link.get("master") not in {None, "", 0}
            ):
                raise IsolationError("dedicated-runner loopback identity is ambiguous")
            continue
        if not _is_positive_physical_link(link):
            raise IsolationError("link is not a positive physical dedicated-runner uplink candidate")
    if loopback_count != 1:
        raise IsolationError("dedicated-runner loopback cardinality is invalid")

    for family, key in (("ipv4", "rules_v4"), ("ipv6", "rules_v6")):
        rules = snapshot.get(key)
        if not isinstance(rules, list):
            raise IsolationError("policy-rule inventory is unavailable")
        _validate_canonical_default_rules(
            [_required_mapping(rule, "policy-rule inventory entry") for rule in rules],
            family,
        )

    _validate_non_main_routes(snapshot)
    _validate_main_connected_routes(snapshot)

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
            if route.get("dst") != "default":
                continue
            if (
                route.get("type") != "unicast"
                or route.get("mark")
                or route.get("multipath")
                or route.get("flags", [])
                or route.get("nhid") is not None
                or not route.get("dev")
                or route.get("protocol") not in {"boot", "dhcp", "kernel", "ra", "static"}
            ):
                raise IsolationError("default-route ownership is ambiguous before the soak")

    nftables = snapshot.get("nftables")
    if not isinstance(nftables, list):
        raise IsolationError("nftables inventory is unavailable")
    if nftables:
        raise IsolationError("pre-existing nftables packet-path state makes attribution ambiguous")

    default_devices = _default_route_devices(snapshot)
    if len(default_devices) != 1:
        raise IsolationError("dedicated runner must have exactly one authoritative default uplink")
    uplink = next(iter(default_devices))
    uplink_link = link_by_name.get(uplink)
    if uplink_link is None or not _is_positive_physical_link(uplink_link):
        raise IsolationError("default route does not use a positive physical dedicated-runner uplink")

    addresses = snapshot.get("addresses")
    if not isinstance(addresses, list):
        raise IsolationError("address inventory is unavailable")
    uplink_addresses = [
        _required_mapping(entry, "address inventory entry")
        for entry in addresses
        if isinstance(entry, Mapping) and entry.get("ifname") == uplink
    ]
    if len(uplink_addresses) != 1 or uplink_addresses[0].get("ifindex") != uplink_link.get("ifindex"):
        raise IsolationError("physical dedicated-runner uplink address identity is unavailable")
    address_values = uplink_addresses[0].get("addresses")
    if not isinstance(address_values, list) or not any(
        isinstance(address, Mapping)
        and address.get("scope") == "global"
        and address.get("family") in {"inet", "inet6"}
        and bool(address.get("local"))
        for address in address_values
    ):
        raise IsolationError("physical dedicated-runner uplink has no authoritative global address")

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
        table = _canonical_table(route.get("table"))
        destination = _canonical_prefix(route.get("cidr", ""), family)
        gateway = _canonical_address(route.get("via", ""), family)
        device = str(route.get("dev", ""))
        if table == "main":
            if destination == "default" or not gateway or not device:
                raise IsolationError("manifest main-table route is not an exact server-bypass projection")
            scope = ""
        elif table == "51820":
            if destination != "default" or gateway or device != PODLAZ_LINK:
                raise IsolationError("manifest managed-table route is not the canonical TUN projection")
            scope = "link"
        else:
            raise IsolationError("manifest route table is outside the Podlaz projection")
        result["routes"].append(
            {
                "family": family,
                "table": table,
                "type": "unicast",
                "dst": destination,
                "gateway": gateway,
                "dev": device,
                "protocol": "",
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
        )
    for raw in rules:
        rule = _required_mapping(raw, "manifest rule")
        family = "ipv4" if rule.get("family") == "-4" else "ipv6" if rule.get("family") == "-6" else ""
        priority = rule.get("priority")
        if not family or not isinstance(priority, int):
            raise IsolationError("manifest rule identity is unsupported")
        table = _canonical_table(rule.get("table"))
        source = _canonical_prefix(rule.get("source", ""), family, allow_all=True)
        destination = _canonical_prefix(rule.get("destination", ""), family, allow_all=True)
        fwmark, fwmask = _split_mark(rule.get("mark", ""))
        if family != "ipv4" or fwmark or fwmask:
            raise IsolationError("manifest policy rule is outside the current Podlaz projection")
        if priority == 9999:
            if table != "main" or source != "all" or destination in {"", "all", "default"}:
                raise IsolationError("manifest server-bypass rule is not canonical")
        elif priority == 10000:
            if table != "51820" or source != "all" or destination != "all":
                raise IsolationError("manifest TUN policy rule is not canonical")
        else:
            raise IsolationError("manifest policy-rule priority is outside the Podlaz projection")
        result["rules"].append(
            {
                "family": family,
                "priority": priority,
                "table": table,
                "action": "to_tbl",
                "source": source,
                "destination": destination,
                "fwmark": "",
                "fwmask": "",
                "iif": "",
                "oif": "",
                "l3mdev": False,
                "suppress_prefixlength": None,
                "uidrange": "",
            }
        )
    return result


def _remove_exact(items: list[dict[str, Any]], expected: Mapping[str, Any], matcher: Any, label: str) -> None:
    indexes = [index for index, item in enumerate(items) if matcher(item, expected)]
    if len(indexes) != 1:
        raise IsolationError(f"exact Podlaz {label} projection is missing or ambiguous")
    del items[indexes[0]]


def _route_matches(actual: Mapping[str, Any], expected: Mapping[str, Any]) -> bool:
    # Both mappings are complete normalized shapes. Dynamic iproute2 fields are
    # removed during normalization; every remaining semantic field is exact.
    return dict(actual) == dict(expected)


def _rule_matches(actual: Mapping[str, Any], expected: Mapping[str, Any]) -> bool:
    return dict(actual) == dict(expected)


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
