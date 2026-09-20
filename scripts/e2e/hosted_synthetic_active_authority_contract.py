#!/usr/bin/env python3
"""Negative matrix for hosted synthetic active-authority verification."""

from __future__ import annotations

import argparse
import copy
import json
import tempfile
from pathlib import Path

import hosted_synthetic_active_authority as authority


def match(left: dict, right: object, op: str = "==") -> dict:
    return {"match": {"op": op, "left": left, "right": right}}


def verdict(name: str) -> dict:
    return {name: None}


def rule(table: str, handle: int, expr: list[dict], comment: str) -> dict:
    statements = expr[:-1] + [{"counter": {"packets": 1, "bytes": 2}}] + expr[-1:]
    return {
        "rule": {
            "family": "inet",
            "table": table,
            "chain": "output",
            "handle": handle,
            "expr": statements,
            "comment": comment,
        }
    }


def nft_ruleset() -> dict:
    entries = [
        {"metainfo": {"version": "1.0.9", "release_name": "Old Doc Yak", "json_schema_version": 1}},
        {"table": {"family": "inet", "name": "podlaz", "handle": 10}},
        {"chain": {"family": "inet", "table": "podlaz", "name": "output", "handle": 11, "type": "filter", "hook": "output", "prio": 0, "policy": "accept"}},
        rule("podlaz", 12, [match({"payload": {"protocol": "ip", "field": "daddr"}}, "172.31.253.1"), verdict("accept")], "podlaz:firewall:server-bypass"),
        rule("podlaz", 13, [match({"meta": {"key": "oifname"}}, "lo"), verdict("accept")], "podlaz:firewall:loopback"),
        rule("podlaz", 14, [match({"meta": {"key": "oifname"}}, "podlaz0"), verdict("accept")], "podlaz:firewall:tun-egress"),
        rule("podlaz", 15, [match({"meta": {"key": "oifname"}}, "podlaz0", "!="), verdict("reject")], "podlaz:firewall:kill-switch"),
        {"table": {"family": "inet", "name": "podlaz_pe_0123456789ab", "handle": 20}},
        {"chain": {"family": "inet", "table": "podlaz_pe_0123456789ab", "name": "output", "handle": 21, "type": "filter", "hook": "output", "prio": -10, "policy": "accept"}},
        rule("podlaz_pe_0123456789ab", 22, [match({"meta": {"key": "oifname"}}, "lo"), verdict("accept")], "podlaz:privacy-envelope:loopback"),
        rule("podlaz_pe_0123456789ab", 23, [match({"meta": {"key": "oifname"}}, "podlaz0"), verdict("accept")], "podlaz:privacy-envelope:tun-egress"),
        rule("podlaz_pe_0123456789ab", 24, [match({"payload": {"protocol": "ip", "field": "daddr"}}, "172.31.253.1"), verdict("accept")], "podlaz:privacy-envelope:bootstrap"),
        rule("podlaz_pe_0123456789ab", 25, [match({"meta": {"key": "nfproto"}}, "ipv4"), match({"meta": {"key": "l4proto"}}, "udp"), match({"payload": {"protocol": "udp", "field": "sport"}}, 68), match({"payload": {"protocol": "udp", "field": "dport"}}, 67), verdict("accept")], "podlaz:privacy-envelope:dhcp4"),
        rule("podlaz_pe_0123456789ab", 26, [match({"meta": {"key": "nfproto"}}, "ipv6"), match({"meta": {"key": "l4proto"}}, "udp"), match({"payload": {"protocol": "udp", "field": "sport"}}, 546), match({"payload": {"protocol": "udp", "field": "dport"}}, 547), verdict("accept")], "podlaz:privacy-envelope:dhcp6"),
        rule("podlaz_pe_0123456789ab", 27, [match({"meta": {"key": "nfproto"}}, "ipv6"), match({"meta": {"key": "l4proto"}}, "icmpv6"), match({"payload": {"protocol": "icmpv6", "field": "type"}}, {"set": ["nd-router-solicit", "nd-neighbor-solicit", "nd-neighbor-advert"]}), verdict("accept")], "podlaz:privacy-envelope:ipv6-link-control"),
        rule("podlaz_pe_0123456789ab", 28, [verdict("reject")], "podlaz:privacy-envelope:block-direct"),
    ]
    return {"nftables": entries}


def transaction() -> dict:
    return {
        "schema_version": "podlaz.transaction.v1",
        "owner": "podlaz",
        "id": "tx-1",
        "profile_id": "profile-1",
        "mode": "tun",
        "state": "committed",
        "desired_plan": {
            "tun": {"interface_name": "podlaz0", "mtu": 1500, "owner": "xray:tun-inbound"},
            "dns": {"backend": "systemd-resolved per-link DNS", "link": "podlaz0", "servers": ["1.1.1.1"], "search_domains": ["~."], "owner": "podlaz"},
            "nftables": {"family": "inet", "table": "podlaz", "owner": "podlaz:nftables", "chains": [{"name": "output", "hook": "output", "type": "filter", "priority": 0, "policy": "accept", "owner": "podlaz:nftables", "rules": ["ip daddr 172.31.253.1 accept owner podlaz:firewall:server-bypass", 'oifname "lo" accept owner podlaz:firewall:loopback', 'oifname "podlaz0" accept owner podlaz:firewall:tun-egress', 'oifname != "podlaz0" reject owner podlaz:firewall:kill-switch']}]},
            "core": {"runtime_config_path": "/run/podlaz/generated/xray.json", "process_label": "xray", "owner": "podlaz"},
        },
        "rollback": {
            "dns": [{"backend": "systemd-resolved per-link DNS", "link": "podlaz0", "search_domains": ["~."], "owner": "podlaz:dns-link"}],
            "nftables": [{"family": "inet", "table": "podlaz", "owner": "podlaz:nftables"}],
            "generated_configs": [{"path": "/run/podlaz/generated/xray.json", "owner": "podlaz"}],
            "child_processes": [{"pid": 123, "pid_file": "/run/podlaz/xray.pid", "label": "xray", "config_ref": "/run/podlaz/generated/xray.json", "start_time": "1", "owner": "podlaz"}],
        },
    }


def session() -> dict:
    return {
        "schema_version": "podlaz.network-session-state.v1",
        "owner": "podlaz",
        "boot_id": "boot-1",
        "session_id": "0123456789abcdef0123456789abcdef",
        "intent": "resume",
        "request": {"mode": "tun", "profile": {"id": "profile-1"}},
        "protection": {"state": "armed", "composition_version": 1, "family": "inet", "table": "podlaz_pe_0123456789ab", "tun_interface": "podlaz0", "bootstrap_ipv4": ["172.31.253.1"]},
    }


def write(path: Path, value: object) -> None:
    if isinstance(value, str):
        path.write_text(value, encoding="utf-8")
    else:
        path.write_text(json.dumps(value), encoding="utf-8")


def verify(root: Path) -> None:
    authority.verify(
        argparse.Namespace(
            status=str(root / "status.json"),
            transactions=str(root / "transactions"),
            session=str(root / "session.json"),
            boot_id=str(root / "boot-id"),
            runtime_config=str(root / "xray.json"),
            resolved_dns=str(root / "resolved-dns.txt"),
            resolved_domain=str(root / "resolved-domain.txt"),
            resolved_default_route=str(root / "resolved-default-route.txt"),
            nft_ruleset=str(root / "nft.json"),
        )
    )


def expect_mismatch(root: Path, message: str) -> None:
    try:
        verify(root)
    except authority.AuthorityMismatch:
        return
    raise SystemExit(message)


def main() -> int:
    proc_boot_id = Path("/proc/sys/kernel/random/boot_id")
    if proc_boot_id.is_file():
        authority.regular_private_input(proc_boot_id, "proc boot id", nonempty=False)
        if not authority.read_current_boot_id(proc_boot_id):
            raise SystemExit("procfs boot identity was empty")

    with tempfile.TemporaryDirectory() as tmp:
        root = Path(tmp)
        (root / "transactions").mkdir()
        write(root / "status.json", {"connection": "active", "mode": "tun", "active_transaction_id": "tx-1", "tun_health": {"state": "verified"}, "transactions": [{"id": "tx-1", "state": "committed", "requires_cleanup": False}]})
        write(root / "transactions" / "tx-1.json", transaction())
        write(root / "session.json", session())
        write(root / "boot-id", "boot-1\n")
        write(root / "xray.json", "{}\n")
        write(root / "resolved-dns.txt", "Global:\nLink 2 (host0): 1.0.0.1\nLink 3 (podlaz0): 1.1.1.1\n")
        write(root / "resolved-domain.txt", "Global:\nLink 2 (host0):\nLink 3 (podlaz0): ~.\n")
        write(root / "resolved-default-route.txt", "Global: no\nLink 2 (host0): yes\nLink 3 (podlaz0): yes\n")
        exact_nft = nft_ruleset()
        write(root / "nft.json", exact_nft)
        verify(root)

        write(root / "boot-id", "boot-2\n")
        expect_mismatch(root, "previous-boot Network Session authority was accepted")
        write(root / "boot-id", "boot-1\n")

        write(root / "resolved-dns.txt", "Global:\nLink 2 (host0): 1.0.0.1\nLink 3 (podlaz0): 9.9.9.9\n")
        expect_mismatch(root, "wrong resolved DNS composition was accepted")
        write(root / "resolved-dns.txt", "Global:\nLink 2 (host0): 1.0.0.1\nLink 3 (podlaz0): 1.1.1.1\n")

        bad_nft = copy.deepcopy(exact_nft)
        for entry in bad_nft["nftables"]:
            body = entry.get("rule") if isinstance(entry, dict) else None
            if body and body.get("comment") == "podlaz:privacy-envelope:block-direct":
                body["comment"] = "podlaz:privacy-envelope:foreign"
        write(root / "nft.json", bad_nft)
        expect_mismatch(root, "wrong Privacy Envelope composition was accepted")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
