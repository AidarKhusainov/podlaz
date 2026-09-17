#!/usr/bin/env python3
"""Read-only exact active-authority verifier for hosted synthetic TUN qualification.

The verifier derives expectations from the active committed transaction and the
current-boot Network Session. It consumes pre-captured live observations only;
it never mutates networking or creates cleanup authority.
"""

from __future__ import annotations

import argparse
import ipaddress
import json
import re
import shlex
import stat
import sys
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

TRANSACTION_SCHEMA = "podlaz.transaction.v1"
TRANSACTION_OWNER = "podlaz"
SESSION_SCHEMA = "podlaz.network-session-state.v1"
SESSION_OWNER = "podlaz"
TUN_OWNER = "xray:tun-inbound"
DNS_OWNER = "podlaz:dns-link"
FIREWALL_OWNER = "podlaz:nftables"
PRIVACY_COMPOSITION_VERSION = 1
SESSION_ID_RE = re.compile(r"^[0-9a-f]{32}$")
TRANSACTION_ID_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$")
PE_TABLE_RE = re.compile(r"^podlaz_pe_([0-9a-f]{12})(?:_[1-9][0-9]{0,2})?$")
LINK_LINE_RE = re.compile(r"^Link\s+[1-9][0-9]*\s+\(([^)]+)\):\s*(.*)$")


class AuthorityMismatch(ValueError):
    pass


def require(condition: bool, surface: str) -> None:
    if not condition:
        raise AuthorityMismatch(surface)


def load_json(path: Path, label: str) -> dict[str, Any]:
    data = path.read_text(encoding="utf-8")
    value = json.loads(data)
    require(isinstance(value, dict), f"{label} shape")
    return value


def regular_private_input(path: Path, label: str, *, nonempty: bool = True) -> None:
    info = path.lstat()
    require(stat.S_ISREG(info.st_mode), f"{label} file type")
    require(not path.is_symlink(), f"{label} symlink")
    if nonempty:
        require(info.st_size > 0, f"{label} empty")


def clean_string(value: Any) -> str:
    return str(value or "").strip()


def dict_value(value: Any, label: str) -> dict[str, Any]:
    require(isinstance(value, dict), f"{label} shape")
    return value


def list_value(value: Any, label: str) -> list[Any]:
    require(isinstance(value, list), f"{label} shape")
    return value


def read_current_boot_id(path: Path) -> str:
    boot_id = path.read_text(encoding="utf-8").strip()
    require(bool(boot_id), "boot id empty")
    return boot_id


def active_transaction(status: dict[str, Any], root: Path) -> dict[str, Any]:
    require(status.get("connection") == "active", "status connection")
    require(status.get("mode") == "tun", "status mode")
    health = dict_value(status.get("tun_health") or {}, "TUN health")
    require(health.get("state") == "verified", "TUN health state")
    tx_id = clean_string(status.get("active_transaction_id"))
    require(bool(TRANSACTION_ID_RE.fullmatch(tx_id)), "active transaction id")

    summaries = list_value(status.get("transactions") or [], "status transactions")
    matches = [
        item
        for item in summaries
        if isinstance(item, dict)
        and clean_string(item.get("id")) == tx_id
        and item.get("state") == "committed"
        and not bool(item.get("requires_cleanup"))
    ]
    require(len(matches) == 1, "active transaction status projection")

    tx_path = root / f"{tx_id}.json"
    regular_private_input(tx_path, "active transaction")
    tx = load_json(tx_path, "active transaction")
    require(tx.get("schema_version") == TRANSACTION_SCHEMA, "transaction schema")
    require(tx.get("owner") == TRANSACTION_OWNER, "transaction owner")
    require(clean_string(tx.get("id")) == tx_id, "transaction identity")
    require(tx.get("mode") == "tun", "transaction mode")
    require(tx.get("state") == "committed", "transaction state")
    return tx


def validate_runtime_authority(tx: dict[str, Any], runtime_config: Path) -> str:
    desired = dict_value(tx.get("desired_plan") or {}, "desired plan")
    tun = dict_value(desired.get("tun") or {}, "desired TUN")
    tun_interface = clean_string(tun.get("interface_name"))
    require(bool(tun_interface) and tun.get("owner") == TUN_OWNER, "desired TUN identity")
    require(isinstance(tun.get("mtu"), int) and int(tun.get("mtu")) > 0, "desired TUN MTU")

    core = dict_value(desired.get("core") or {}, "desired core")
    config_path = clean_string(core.get("runtime_config_path"))
    require(config_path == "/run/podlaz/generated/xray.json", "generated config authority path")
    require(core.get("process_label") == "xray" and core.get("owner") == TRANSACTION_OWNER, "core authority")
    regular_private_input(runtime_config, "generated runtime config")

    rollback = dict_value(tx.get("rollback") or {}, "rollback")
    configs = list_value(rollback.get("generated_configs") or [], "generated config rollback")
    config_matches = [
        item
        for item in configs
        if isinstance(item, dict)
        and clean_string(item.get("path")) == config_path
        and item.get("owner") == TRANSACTION_OWNER
    ]
    require(len(config_matches) == 1, "generated config rollback authority")

    children = list_value(rollback.get("child_processes") or [], "child process rollback")
    child_matches = [
        item
        for item in children
        if isinstance(item, dict)
        and item.get("owner") == TRANSACTION_OWNER
        and item.get("label") == "xray"
        and clean_string(item.get("config_ref")) == config_path
        and isinstance(item.get("pid"), int)
        and int(item.get("pid")) > 1
    ]
    require(len(child_matches) == 1, "Xray rollback authority")
    return tun_interface


def validate_session(
    session: dict[str, Any], tx: dict[str, Any], boot_id: str, tun_interface: str
) -> dict[str, Any]:
    require(session.get("schema_version") == SESSION_SCHEMA, "Network Session schema")
    require(session.get("owner") == SESSION_OWNER, "Network Session owner")
    require(clean_string(session.get("boot_id")) == boot_id, "Network Session boot id")
    session_id = clean_string(session.get("session_id"))
    require(bool(SESSION_ID_RE.fullmatch(session_id)), "Network Session id")
    require(session.get("intent") == "resume", "Network Session intent")

    request = dict_value(session.get("request") or {}, "Network Session request")
    require(request.get("mode") == "tun", "Network Session request mode")
    profile = dict_value(request.get("profile") or {}, "Network Session profile")
    require(clean_string(profile.get("id")) == clean_string(tx.get("profile_id")), "Network Session profile identity")

    protection = dict_value(session.get("protection") or {}, "Privacy Envelope authority")
    require(protection.get("state") == "armed", "Privacy Envelope state")
    require(protection.get("composition_version") == PRIVACY_COMPOSITION_VERSION, "Privacy Envelope composition version")
    require(protection.get("family") == "inet", "Privacy Envelope family")
    require(clean_string(protection.get("tun_interface")) == tun_interface, "Privacy Envelope TUN identity")
    table = clean_string(protection.get("table"))
    match = PE_TABLE_RE.fullmatch(table)
    require(match is not None and match.group(1) == session_id[:12], "Privacy Envelope table identity")

    bootstrap = normalize_ipv4_list(protection.get("bootstrap_ipv4"), "Privacy Envelope bootstrap")
    previous = normalize_ipv4_list(
        protection.get("previous_bootstrap_ipv4") or [],
        "Privacy Envelope previous bootstrap",
        allow_empty=True,
    )
    combined = sorted(set(bootstrap + previous))
    require(bool(combined), "Privacy Envelope bootstrap authority")
    result = dict(protection)
    result["bootstrap_ipv4"] = combined
    return result


def normalize_ipv4_list(value: Any, label: str, *, allow_empty: bool = False) -> list[str]:
    values = list_value(value, label)
    normalized: list[str] = []
    for item in values:
        try:
            address = ipaddress.ip_address(clean_string(item))
        except ValueError as exc:
            raise AuthorityMismatch(f"{label} address") from exc
        require(address.version == 4, f"{label} family")
        normalized.append(str(address))
    normalized = sorted(set(normalized))
    if not allow_empty:
        require(bool(normalized), f"{label} empty")
    return normalized


def parse_resolved_links(path: Path) -> dict[str, list[str]]:
    result: dict[str, list[str]] = {}
    for raw in path.read_text(encoding="utf-8").splitlines():
        match = LINK_LINE_RE.fullmatch(raw.strip())
        if not match:
            continue
        values = [item for item in match.group(2).split() if item not in {"(none)", "none"}]
        result[match.group(1)] = values
    return result


def validate_resolved(
    tx: dict[str, Any], dns_path: Path, domain_path: Path, default_path: Path, tun_interface: str
) -> None:
    desired = dict_value(
        dict_value(tx.get("desired_plan") or {}, "desired plan").get("dns") or {},
        "desired DNS",
    )
    require(desired.get("owner") == TRANSACTION_OWNER, "desired DNS owner")
    require(desired.get("backend") == "systemd-resolved per-link DNS", "desired DNS backend")
    require(clean_string(desired.get("link")) == tun_interface, "desired DNS link")
    servers = sorted(
        {
            clean_string(item)
            for item in list_value(desired.get("servers") or [], "desired DNS servers")
            if clean_string(item)
        }
    )
    domains = sorted(
        {
            clean_string(item)
            for item in list_value(desired.get("search_domains") or [], "desired DNS domains")
            if clean_string(item)
        }
    )
    require(bool(servers), "desired DNS servers empty")
    require(domains == ["~."], "desired DNS route-only domain")

    rollback = dict_value(tx.get("rollback") or {}, "rollback")
    rollback_dns = list_value(rollback.get("dns") or [], "DNS rollback")
    matches = [
        item
        for item in rollback_dns
        if isinstance(item, dict)
        and item.get("owner") == DNS_OWNER
        and item.get("backend") == desired.get("backend")
        and clean_string(item.get("link")) == tun_interface
        and sorted(
            {
                clean_string(value)
                for value in item.get("search_domains") or []
                if clean_string(value)
            }
        )
        == domains
    ]
    require(len(matches) == 1, "DNS rollback authority")

    observed_dns = parse_resolved_links(dns_path)
    observed_domains = parse_resolved_links(domain_path)
    observed_default = parse_resolved_links(default_path)
    require(sorted(set(observed_dns.get(tun_interface, []))) == servers, "resolved DNS composition")
    require(sorted(set(observed_domains.get(tun_interface, []))) == domains, "resolved domain composition")
    require(observed_default.get(tun_interface, []) == ["yes"], "resolved default-route composition")

    for link, link_domains in observed_domains.items():
        if link == tun_interface or "~." not in link_domains:
            continue
        if observed_dns.get(link) or observed_default.get(link) == ["yes"]:
            raise AuthorityMismatch("foreign resolved route-only DNS ownership")


@dataclass
class NftChain:
    name: str
    type: str
    hook: str
    priority: int
    policy: str
    rules: list[tuple[str, ...]] = field(default_factory=list)


@dataclass
class NftTable:
    family: str
    name: str
    flags: tuple[str, ...]
    chains: dict[str, NftChain] = field(default_factory=dict)


def parse_nft_ruleset(path: Path) -> dict[tuple[str, str], NftTable]:
    root = load_json(path, "nftables ruleset")
    entries = list_value(root.get("nftables") or [], "nftables entries")
    tables: dict[tuple[str, str], NftTable] = {}
    rules: list[dict[str, Any]] = []
    for entry in entries:
        if not isinstance(entry, dict) or len(entry) != 1:
            raise AuthorityMismatch("nftables entry shape")
        if "metainfo" in entry:
            continue
        if "table" in entry:
            table = dict_value(entry["table"], "nftables table")
            family, name = clean_string(table.get("family")), clean_string(table.get("name"))
            key = (family, name)
            require(family and name and key not in tables, "nftables table identity")
            flags = table.get("flags") or []
            require(isinstance(flags, list), "nftables table flags")
            tables[key] = NftTable(
                family,
                name,
                tuple(sorted(clean_string(item) for item in flags)),
            )
        elif "chain" in entry:
            chain = dict_value(entry["chain"], "nftables chain")
            key = (clean_string(chain.get("family")), clean_string(chain.get("table")))
            table = tables.get(key)
            require(table is not None, "nftables chain table")
            name = clean_string(chain.get("name"))
            require(name and name not in table.chains, "nftables chain identity")
            priority = chain.get("prio", chain.get("priority"))
            require(
                isinstance(priority, int) and not isinstance(priority, bool),
                "nftables chain priority",
            )
            table.chains[name] = NftChain(
                name=name,
                type=clean_string(chain.get("type")),
                hook=clean_string(chain.get("hook")),
                priority=priority,
                policy=clean_string(chain.get("policy")),
            )
        elif "rule" in entry:
            rules.append(dict_value(entry["rule"], "nftables rule"))
        else:
            continue

    for rule in rules:
        key = (clean_string(rule.get("family")), clean_string(rule.get("table")))
        table = tables.get(key)
        require(table is not None, "nftables rule table")
        chain = table.chains.get(clean_string(rule.get("chain")))
        require(chain is not None, "nftables rule chain")
        chain.rules.append(canonical_observed_rule(rule))
    return tables


def canonical_observed_rule(rule: dict[str, Any]) -> tuple[str, ...]:
    statements: list[str] = []
    for item in list_value(rule.get("expr") or [], "nftables rule expression"):
        require(isinstance(item, dict) and len(item) == 1, "nftables statement shape")
        kind, body = next(iter(item.items()))
        if kind == "match":
            match = dict_value(body, "nftables match")
            op = clean_string(match.get("op"))
            require(op in {"==", "!="}, "nftables match operator")
            left = canonical_observed_left(match.get("left"))
            right = canonical_observed_right(left, match.get("right"))
            statements.append(f"match={left}{op}{right}")
        elif kind == "counter":
            statements.append("counter")
        elif kind in {"accept", "drop"}:
            statements.append(kind)
        elif kind == "reject":
            statements.append("reject")
        else:
            raise AuthorityMismatch("unsupported nftables statement")
    statements = normalize_implicit_nft_dependencies(statements)
    comment = clean_string(rule.get("comment"))
    require(bool(comment), "nftables rule ownership comment")
    statements.append("comment=" + comment)
    return tuple(statements)


def canonical_observed_left(value: Any) -> str:
    obj = dict_value(value, "nftables match left")
    require(len(obj) == 1, "nftables match left shape")
    kind, body = next(iter(obj.items()))
    data = dict_value(body, "nftables match left body")
    if kind == "meta":
        key = clean_string(data.get("key"))
        require(key in {"oifname", "nfproto", "l4proto"}, "nftables meta key")
        return f"meta:{key}:"
    require(kind == "payload", "nftables match left kind")
    protocol = clean_string(data.get("protocol"))
    field_name = clean_string(data.get("field"))
    require(
        (protocol, field_name)
        in {
            ("ip", "daddr"),
            ("udp", "sport"),
            ("udp", "dport"),
            ("icmpv6", "type"),
        },
        "nftables payload key",
    )
    return f"payload:{protocol}:{field_name}:"


def canonical_observed_right(left: str, value: Any) -> str:
    if isinstance(value, dict):
        require(set(value) == {"set"}, "nftables set shape")
        items = list_value(value.get("set"), "nftables set")
        require(bool(items), "nftables set empty")
        return "{" + ",".join(sorted(canonical_nft_scalar(left, item) for item in items)) + "}"
    return canonical_nft_scalar(left, value)


def canonical_nft_scalar(left: str, value: Any) -> str:
    text = clean_string(value)
    if left == "meta:oifname:":
        require(bool(text), "nftables output interface")
        return text
    if left == "meta:nfproto:":
        mapping = {"ipv4": "ipv4", "2": "ipv4", "ipv6": "ipv6", "10": "ipv6"}
        require(text in mapping, "nftables nfproto")
        return mapping[text]
    if left == "meta:l4proto:":
        mapping = {
            "udp": "udp",
            "17": "udp",
            "icmpv6": "icmpv6",
            "ipv6-icmp": "icmpv6",
            "58": "icmpv6",
        }
        require(text in mapping, "nftables l4proto")
        return mapping[text]
    if left == "payload:ip:daddr:":
        try:
            address = ipaddress.ip_address(text)
        except ValueError as exc:
            raise AuthorityMismatch("nftables IPv4 destination") from exc
        require(address.version == 4, "nftables IPv4 destination family")
        return str(address)
    if left in {"payload:udp:sport:", "payload:udp:dport:"}:
        try:
            port = int(text, 10)
        except ValueError as exc:
            raise AuthorityMismatch("nftables UDP port") from exc
        require(0 <= port <= 65535, "nftables UDP port range")
        return str(port)
    if left == "payload:icmpv6:type:":
        mapping = {
            "nd-router-solicit": "133",
            "133": "133",
            "nd-router-advert": "134",
            "134": "134",
            "nd-neighbor-solicit": "135",
            "135": "135",
            "nd-neighbor-advert": "136",
            "136": "136",
        }
        require(text in mapping, "nftables ICMPv6 type")
        return mapping[text]
    raise AuthorityMismatch("unsupported nftables scalar")


def normalize_implicit_nft_dependencies(statements: list[str]) -> list[str]:
    has_udp = any(item.startswith("match=payload:udp:") for item in statements)
    has_icmpv6 = any(item.startswith("match=payload:icmpv6:") for item in statements)
    has_ipv4_payload = any(item.startswith("match=payload:ip:") for item in statements)
    result: list[str] = []
    for item in statements:
        if has_udp and item == "match=meta:l4proto:==udp":
            continue
        if has_icmpv6 and item in {
            "match=meta:l4proto:==icmpv6",
            "match=meta:nfproto:==ipv6",
        }:
            continue
        if has_ipv4_payload and item == "match=meta:nfproto:==ipv4":
            continue
        result.append(item)
    return result


def canonical_planned_rule(raw: str) -> tuple[str, ...]:
    fields = shlex.split(raw)
    require(
        len(fields) >= 4 and fields[-2] == "owner",
        "persisted nftables rule ownership",
    )
    ownership = fields[-1]
    verdict = fields[-3]
    require(verdict in {"accept", "drop", "reject"}, "persisted nftables verdict")
    statements = canonical_planned_expression(fields[:-3])
    statements.append("counter")
    statements.append(verdict)
    statements = normalize_implicit_nft_dependencies(statements)
    statements.append("comment=" + ownership)
    return tuple(statements)


def canonical_planned_expression(fields: list[str]) -> list[str]:
    result: list[str] = []
    index = 0
    while index < len(fields):
        token = fields[index]
        if token == "oifname":
            require(index + 1 < len(fields), "persisted oifname expression")
            op = "=="
            value_index = index + 1
            if fields[value_index] == "!=":
                op = "!="
                value_index += 1
            require(value_index < len(fields), "persisted oifname value")
            value = canonical_nft_scalar("meta:oifname:", fields[value_index])
            result.append(f"match=meta:oifname:{op}{value}")
            index = value_index + 1
        elif token == "ip":
            require(
                index + 2 < len(fields) and fields[index + 1] == "daddr",
                "persisted IPv4 expression",
            )
            value = canonical_nft_scalar("payload:ip:daddr:", fields[index + 2])
            result.append("match=payload:ip:daddr:==" + value)
            index += 3
        elif token == "meta":
            require(
                index + 2 < len(fields) and fields[index + 1] == "nfproto",
                "persisted meta expression",
            )
            value = canonical_nft_scalar("meta:nfproto:", fields[index + 2])
            result.append("match=meta:nfproto:==" + value)
            index += 3
        elif token == "udp":
            require(
                index + 2 < len(fields) and fields[index + 1] in {"sport", "dport"},
                "persisted UDP expression",
            )
            left = f"payload:udp:{fields[index + 1]}:"
            value = canonical_nft_scalar(left, fields[index + 2])
            result.append(f"match={left}=={value}")
            index += 3
        elif token == "icmpv6":
            require(
                index + 3 < len(fields)
                and fields[index + 1] == "type"
                and fields[index + 2] == "{",
                "persisted ICMPv6 expression",
            )
            index += 3
            values: list[str] = []
            while index < len(fields) and fields[index] != "}":
                values.append(
                    canonical_nft_scalar(
                        "payload:icmpv6:type:", fields[index].rstrip(",")
                    )
                )
                index += 1
            require(
                index < len(fields) and fields[index] == "}" and values,
                "persisted ICMPv6 set",
            )
            result.append(
                "match=payload:icmpv6:type:=={" + ",".join(sorted(values)) + "}"
            )
            index += 1
        else:
            raise AuthorityMismatch("unsupported persisted nftables expression")
    return result


def require_exact_table(
    tables: dict[tuple[str, str], NftTable],
    family: str,
    name: str,
    expected_chains: list[dict[str, Any]],
    expected_rules: dict[str, list[tuple[str, ...]]],
    label: str,
) -> None:
    table = tables.get((family, name))
    require(table is not None, f"{label} table")
    require(not table.flags, f"{label} table flags")
    expected_names = [clean_string(item.get("name")) for item in expected_chains]
    require(set(table.chains) == set(expected_names), f"{label} chain set")
    for expected in expected_chains:
        chain_name = clean_string(expected.get("name"))
        chain = table.chains[chain_name]
        require(chain.type == clean_string(expected.get("type")), f"{label} chain type")
        require(chain.hook == clean_string(expected.get("hook")), f"{label} chain hook")
        require(chain.priority == expected.get("priority"), f"{label} chain priority")
        require(chain.policy == clean_string(expected.get("policy")), f"{label} chain policy")
        require(chain.rules == expected_rules.get(chain_name, []), f"{label} rule composition")


def validate_data_plane_nft(tx: dict[str, Any], tables: dict[tuple[str, str], NftTable]) -> None:
    desired = dict_value(
        dict_value(tx.get("desired_plan") or {}, "desired plan").get("nftables") or {},
        "desired nftables",
    )
    require(desired.get("owner") == FIREWALL_OWNER, "desired nftables owner")
    family, table = clean_string(desired.get("family")), clean_string(desired.get("table"))
    require(family and table, "desired nftables identity")
    chains = list_value(desired.get("chains") or [], "desired nftables chains")
    require(bool(chains), "desired nftables chains empty")
    if len(chains) > 1:
        require(
            not any((item.get("rules") or []) for item in chains if isinstance(item, dict)),
            "ambiguous desired nftables rule mapping",
        )

    rollback = list_value(
        dict_value(tx.get("rollback") or {}, "rollback").get("nftables") or [],
        "nftables rollback",
    )
    rollback_matches = [
        item
        for item in rollback
        if isinstance(item, dict)
        and item.get("owner") == FIREWALL_OWNER
        and clean_string(item.get("family")) == family
        and clean_string(item.get("table")) == table
    ]
    require(len(rollback_matches) == 1, "nftables rollback authority")

    expected_rules: dict[str, list[tuple[str, ...]]] = {}
    normalized_chains: list[dict[str, Any]] = []
    for item in chains:
        chain = dict_value(item, "desired nftables chain")
        require(
            chain.get("owner") in {None, "", FIREWALL_OWNER},
            "desired nftables chain owner",
        )
        name = clean_string(chain.get("name"))
        require(name, "desired nftables chain name")
        priority = chain.get("priority")
        if priority is None:
            priority = 0
        require(
            isinstance(priority, int) and not isinstance(priority, bool),
            "desired nftables chain priority",
        )
        normalized_chains.append(
            {
                "name": name,
                "type": clean_string(chain.get("type")),
                "hook": clean_string(chain.get("hook")),
                "priority": priority,
                "policy": clean_string(chain.get("policy")),
            }
        )
        expected_rules[name] = [
            canonical_planned_rule(clean_string(raw))
            for raw in list_value(chain.get("rules") or [], "desired nftables rules")
        ]
    require_exact_table(tables, family, table, normalized_chains, expected_rules, "TUN nftables")


def privacy_expected(
    protection: dict[str, Any],
) -> tuple[list[dict[str, Any]], dict[str, list[tuple[str, ...]]]]:
    tun = clean_string(protection.get("tun_interface"))
    endpoints = list_value(
        protection.get("bootstrap_ipv4") or [], "Privacy Envelope bootstrap"
    )
    raw_rules = [
        f'oifname "lo" accept owner podlaz:privacy-envelope:loopback',
        f'oifname "{tun}" accept owner podlaz:privacy-envelope:tun-egress',
    ]
    raw_rules.extend(
        f"ip daddr {endpoint} accept owner podlaz:privacy-envelope:bootstrap"
        for endpoint in endpoints
    )
    raw_rules.extend(
        [
            "meta nfproto ipv4 udp sport 68 udp dport 67 accept owner podlaz:privacy-envelope:dhcp4",
            "meta nfproto ipv6 udp sport 546 udp dport 547 accept owner podlaz:privacy-envelope:dhcp6",
            "icmpv6 type { nd-router-solicit, nd-neighbor-solicit, nd-neighbor-advert } accept owner podlaz:privacy-envelope:ipv6-link-control",
            "reject owner podlaz:privacy-envelope:block-direct",
        ]
    )
    expected = [canonical_planned_rule(raw) for raw in raw_rules[:-1]]
    expected.append(("counter", "reject", "comment=podlaz:privacy-envelope:block-direct"))
    chains = [
        {
            "name": "output",
            "type": "filter",
            "hook": "output",
            "priority": -10,
            "policy": "accept",
        }
    ]
    return chains, {"output": expected}


def validate_privacy_envelope(
    protection: dict[str, Any], tables: dict[tuple[str, str], NftTable]
) -> None:
    family = clean_string(protection.get("family"))
    table = clean_string(protection.get("table"))
    chains, rules = privacy_expected(protection)
    require_exact_table(tables, family, table, chains, rules, "Privacy Envelope")


def parse_args(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser()
    parser.add_argument("--status", required=True)
    parser.add_argument("--transactions", required=True)
    parser.add_argument("--session", required=True)
    parser.add_argument("--boot-id", required=True)
    parser.add_argument("--runtime-config", required=True)
    parser.add_argument("--resolved-dns", required=True)
    parser.add_argument("--resolved-domain", required=True)
    parser.add_argument("--resolved-default-route", required=True)
    parser.add_argument("--nft-ruleset", required=True)
    return parser.parse_args(argv)


def verify(args: argparse.Namespace) -> None:
    status_path = Path(args.status)
    session_path = Path(args.session)
    boot_path = Path(args.boot_id)
    runtime_config = Path(args.runtime_config)
    dns_path = Path(args.resolved_dns)
    domain_path = Path(args.resolved_domain)
    default_path = Path(args.resolved_default_route)
    nft_path = Path(args.nft_ruleset)
    for path, label in [
        (status_path, "status"),
        (session_path, "Network Session"),
        (boot_path, "boot id"),
        (dns_path, "resolved DNS"),
        (domain_path, "resolved domain"),
        (default_path, "resolved default-route"),
        (nft_path, "nftables ruleset"),
    ]:
        regular_private_input(path, label)

    status = load_json(status_path, "status")
    tx = active_transaction(status, Path(args.transactions))
    tun_interface = validate_runtime_authority(tx, runtime_config)
    session = load_json(session_path, "Network Session")
    protection = validate_session(
        session, tx, read_current_boot_id(boot_path), tun_interface
    )
    validate_resolved(tx, dns_path, domain_path, default_path, tun_interface)
    tables = parse_nft_ruleset(nft_path)
    validate_data_plane_nft(tx, tables)
    validate_privacy_envelope(protection, tables)


def main(argv: list[str]) -> int:
    args = parse_args(argv)
    try:
        verify(args)
    except AuthorityMismatch as exc:
        print(f"hosted synthetic active authority mismatch: {exc}", file=sys.stderr)
        return 1
    except (OSError, json.JSONDecodeError) as exc:
        print(
            f"hosted synthetic active authority inspection failed: {type(exc).__name__}",
            file=sys.stderr,
        )
        return 2
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
