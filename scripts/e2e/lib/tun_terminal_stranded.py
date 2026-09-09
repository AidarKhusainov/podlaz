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


def _exact_authority(tx, session):
    rollback = tx.get("rollback") or {}
    addresses = rollback.get("tun_addresses") or []
    routes = rollback.get("routes") or []
    rules = rollback.get("policy_rules") or []
    nft_authority = rollback.get("nftables") or []
    if not addresses or not routes or not rules:
        raise ValueError("transaction lacks exact address/route/rule rollback authority")
    if len(nft_authority) != 1:
        raise ValueError("transaction does not have one exact nftables rollback authority")
    nft_entry = nft_authority[0]
    if not nft_entry.get("family") or not nft_entry.get("table"):
        raise ValueError("transaction nftables rollback identity is incomplete")

    protection = session.get("protection") or {}
    if (
        protection.get("state") != "armed"
        or not protection.get("family")
        or not protection.get("table")
    ):
        raise ValueError("Network Session lacks armed Privacy Envelope authority")
    return addresses, routes, rules, nft_entry, protection


def _require_collection_state(flags, *, expected_present, label):
    if expected_present:
        if not flags or not all(flags):
            raise ValueError(f"{label} set is absent or incomplete, expected all exact tuples present")
        return
    if any(flags):
        raise ValueError(f"{label} residue remains, expected every exact tuple absent")


def validate_exact_live_state(tx, session, addrs, routes, rules, nft, *, data_plane_present, barriers_present):
    addresses, route_authority, rule_authority, nft_entry, protection = _exact_authority(tx, session)

    _require_collection_state(
        [_address_present(expected, addrs) for expected in addresses],
        expected_present=data_plane_present,
        label="exact TUN address",
    )
    _require_collection_state(
        [_route_present(expected, routes) for expected in route_authority],
        expected_present=data_plane_present,
        label="exact route",
    )
    _require_collection_state(
        [_rule_present(expected, rules) for expected in rule_authority],
        expected_present=data_plane_present,
        label="exact policy rule",
    )

    tables = _tables(nft)
    barrier_checks = [
        ((nft_entry.get("family"), nft_entry.get("table")) in tables, "exact transaction nftables table"),
        ((protection.get("family"), protection.get("table")) in tables, "exact Privacy Envelope table"),
    ]
    for found, label in barrier_checks:
        if found != barriers_present:
            actual = "present" if found else "absent"
            expected = "present" if barriers_present else "absent"
            raise ValueError(f"{label} is {actual}, expected {expected}")


def validate_v0240_stranded_state(tx, session, addrs, routes, rules, nft):
    if tx.get("state") != "failed":
        raise ValueError("v0.2.40 transaction is not failed")
    if "missing nftables chains" not in (tx.get("failure_reason") or ""):
        raise ValueError("v0.2.40 failure reason is not the historical nftables reconstruction failure")
    if session.get("intent") not in {"disconnect", "terminal"}:
        raise ValueError("Network Session does not have terminal intent")

    rollback = tx.get("rollback") or {}
    nft_authority = rollback.get("nftables") or []
    if len(nft_authority) != 1 or nft_authority[0].get("family") != "inet" or nft_authority[0].get("table") != "podlaz":
        raise ValueError("v0.2.40 transaction nftables authority is not inet podlaz")

    validate_exact_live_state(
        tx,
        session,
        addrs,
        routes,
        rules,
        nft,
        data_plane_present=False,
        barriers_present=True,
    )


def _load(path):
    with open(path, encoding="utf-8") as handle:
        return json.load(handle)


def main(argv):
    if len(argv) != 8:
        print(
            "usage: tun_terminal_stranded.py MODE TRANSACTION SESSION ADDRS ROUTES RULES NFT",
            file=sys.stderr,
        )
        return 2
    mode = argv[1]
    try:
        tx, session, addrs, routes, rules, nft = (_load(path) for path in argv[2:])
        if mode == "v0240-stranded":
            validate_v0240_stranded_state(tx, session, addrs, routes, rules, nft)
        elif mode == "active":
            validate_exact_live_state(
                tx, session, addrs, routes, rules, nft,
                data_plane_present=True, barriers_present=True,
            )
        elif mode == "absent":
            validate_exact_live_state(
                tx, session, addrs, routes, rules, nft,
                data_plane_present=False, barriers_present=False,
            )
        else:
            raise ValueError(f"unsupported terminal state mode {mode!r}")
    except (OSError, json.JSONDecodeError, ValueError) as exc:
        print(str(exc), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
