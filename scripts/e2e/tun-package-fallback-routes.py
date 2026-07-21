#!/usr/bin/env python3
"""Remove only routes explicitly owned by persisted podlaz TUN transactions."""

from __future__ import annotations

import ipaddress
import json
import os
import re
import subprocess
import sys
from pathlib import Path

TRANSACTION_SCHEMA = "podlaz.transaction.v1"
TRANSACTION_OWNER = "podlaz"
ROUTE_OWNER = "podlaz:route"
ALLOWED_TABLES = {"main", "51820", "podlaz"}
DEV_RE = re.compile(r"^[A-Za-z0-9_.:@-]{1,64}$")


def validated_family(cidr: str, via: str) -> str | None:
    if cidr == "default":
        if via:
            try:
                return "-4" if ipaddress.ip_address(via).version == 4 else "-6"
            except ValueError:
                return None
        return None
    try:
        network = ipaddress.ip_network(cidr, strict=False)
    except ValueError:
        return None
    if via:
        try:
            address = ipaddress.ip_address(via)
        except ValueError:
            return None
        if address.version != network.version:
            return None
    return "-4" if network.version == 4 else "-6"


def route_commands(route: object) -> list[list[str]]:
    if not isinstance(route, dict) or route.get("owner") != ROUTE_OWNER:
        return []
    table = str(route.get("table", "")).strip()
    cidr = str(route.get("cidr", "")).strip()
    via = str(route.get("via", "")).strip()
    dev = str(route.get("dev", "")).strip()
    if table not in ALLOWED_TABLES or not cidr:
        return []
    if dev and not DEV_RE.fullmatch(dev):
        return []
    family = validated_family(cidr, via)
    families = [family] if family else (["-4", "-6"] if cidr == "default" and not via else [])
    commands: list[list[str]] = []
    for selected in families:
        args = ["sudo", "-n", "ip", selected, "route", "del", cidr]
        if via:
            args.extend(["via", via])
        if dev:
            args.extend(["dev", dev])
        args.extend(["table", table])
        commands.append(args)
    return commands


def transaction_routes(path: Path) -> list[list[str]]:
    try:
        with path.open(encoding="utf-8") as handle:
            payload = json.load(handle)
    except (OSError, json.JSONDecodeError):
        return []
    if payload.get("schema_version") != TRANSACTION_SCHEMA or payload.get("owner") != TRANSACTION_OWNER:
        return []
    rollback = payload.get("rollback")
    if not isinstance(rollback, dict):
        return []
    routes = rollback.get("routes")
    if not isinstance(routes, list):
        return []
    commands: list[list[str]] = []
    for route in routes:
        commands.extend(route_commands(route))
    return commands


def main(argv: list[str]) -> int:
    root = Path(argv[1] if len(argv) > 1 else "/run/podlaz/transactions")
    if not root.is_dir():
        return 0
    seen: set[tuple[str, ...]] = set()
    for path in sorted(root.glob("*.json")):
        for command in transaction_routes(path):
            key = tuple(command)
            if key in seen:
                continue
            seen.add(key)
            subprocess.run(command, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False)
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
