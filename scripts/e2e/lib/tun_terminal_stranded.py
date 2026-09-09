#!/usr/bin/env python3
import ipaddress
import json
import sys


def _norm_table(value):
    text = str("main" if value is None else value)
    return "main" if text in {"254", "main"} else text


def _norm_dst(value):
    return "0.0.0.0/0" if value in (None, "default") else value


def _tables(payload):
    result = set()
    for item in payload.get("nftables") or []:
        table = item.get("table") if isinstance(item, dict) else None
        if isinstance(table, dict):
            result.add((table.get("family"), table.get("name")))
    return result


def _address_present(expected, addrs):
    try:
        interface = ipaddress.ip_interface(expected.get("cidr"))
    except (TypeError, ValueError) as exc:
        raise ValueError("persisted TUN address authority is invalid") from exc
    name = expected.get("interface_name")
    for link in addrs:
        if link.get("ifname") != name:
            continue
        for info in link.get("addr_info") or []:
            if (
                info.get("family") == "inet"
                and info.get("local") == str(interface.ip)
                and info.get("prefixlen") == interface.network.prefixlen
            ):
                return True
    return False


def _route_present(expected, routes):
    for route in routes:
        if _norm_table(route.get("table")) != _norm_table(expected.get("table")):
            continue
        if _norm_dst(route.get("dst")) != _norm_dst(expected.get("cidr")):
            continue
        if (route.get("dev") or "") != (expected.get("dev") or ""):
            continue
        if (route.get("gateway") or "") != (expected.get("via") or ""):
            continue
        return True
    return False


def _rule_present(expected, rules):
    for rule in rules:
        if int(rule.get("priority", -1)) != int(expected.get("priority", -2)):
            continue
        if _norm_table(rule.get("table", "")) != _norm_table(expected.get("table", "")):
            continue
        if expected.get("from") and rule.get("from", "all") != expected.get("from"):
            continue
        if expected.get("to") and rule.get("to") != expected.get("to"):
            continue
        if expected.get("mark") and str(rule.get("fwmark", "")) != str(expected.get("mark")):
            continue
        return True
    return False


def validate_v0240_stranded_state(tx, session, addrs, routes, rules, nft):
    if tx.get("state") != "failed":
        raise ValueError("v0.2.40 transaction is not failed")
    if "missing nftables chains" not in (tx.get("failure_reason") or ""):
        raise ValueError("v0.2.40 failure reason is not the historical nftables reconstruction failure")
    if session.get("intent") not in {"disconnect", "terminal"}:
        raise ValueError("Network Session does not have terminal intent")

    rollback = tx.get("rollback") or {}
    addresses = rollback.get("tun_addresses") or []
    route_authority = rollback.get("routes") or []
    rule_authority = rollback.get("policy_rules") or []
    nft_authority = rollback.get("nftables") or []
    if not addresses or not route_authority or not rule_authority:
        raise ValueError("v0.2.40 transaction lacks exact address/route/rule rollback authority")
    if len(nft_authority) != 1:
        raise ValueError("v0.2.40 transaction does not have one exact nftables rollback authority")
    nft_entry = nft_authority[0]
    if nft_entry.get("family") != "inet" or nft_entry.get("table") != "podlaz":
        raise ValueError("v0.2.40 transaction nftables authority is not inet podlaz")

    protection = session.get("protection") or {}
    if (
        protection.get("state") != "armed"
        or not protection.get("family")
        or not protection.get("table")
    ):
        raise ValueError("Network Session lacks armed Privacy Envelope authority")

    if any(_address_present(expected, addrs) for expected in addresses):
        raise ValueError("exact v0.2.40 TUN address still exists; captured partial teardown shape was not reproduced")
    if any(_route_present(expected, routes) for expected in route_authority):
        raise ValueError("exact v0.2.40 route still exists; captured partial teardown shape was not reproduced")
    if any(_rule_present(expected, rules) for expected in rule_authority):
        raise ValueError("exact v0.2.40 policy rule still exists; captured partial teardown shape was not reproduced")

    tables = _tables(nft)
    if ("inet", "podlaz") not in tables:
        raise ValueError("exact v0.2.40 transaction nftables table is absent")
    privacy_identity = (protection.get("family"), protection.get("table"))
    if privacy_identity not in tables:
        raise ValueError("exact Privacy Envelope table is absent")


def _load(path):
    with open(path, encoding="utf-8") as handle:
        return json.load(handle)


def main(argv):
    if len(argv) != 7:
        print(
            "usage: tun_terminal_stranded.py TRANSACTION SESSION ADDRS ROUTES RULES NFT",
            file=sys.stderr,
        )
        return 2
    try:
        validate_v0240_stranded_state(*(_load(path) for path in argv[1:]))
    except (OSError, json.JSONDecodeError, ValueError) as exc:
        print(str(exc), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
