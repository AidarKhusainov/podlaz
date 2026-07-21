#!/usr/bin/env python3
"""Remove and verify only routes owned by persisted podlaz E2E state."""

from __future__ import annotations

import ipaddress
import json
import re
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path

TRANSACTION_SCHEMA = "podlaz.transaction.v1"
TRANSACTION_OWNER = "podlaz"
ROUTE_OWNER = "podlaz:route"
ALLOWED_TABLES = {"main", "51820", "podlaz"}
DEV_RE = re.compile(r"^[A-Za-z0-9_.:@-]{1,64}$")
SERVER_RULE_PRIORITY = "9999:"


@dataclass(frozen=True)
class OwnedRoute:
    family: str
    table: str
    cidr: str
    via: str = ""
    dev: str = ""

    def delete_command(self) -> list[str]:
        args = ["sudo", "-n", "ip", self.family, "route", "del", self.cidr]
        if self.via:
            args.extend(["via", self.via])
        if self.dev:
            args.extend(["dev", self.dev])
        args.extend(["table", self.table])
        return args

    def show_command(self) -> list[str]:
        return ["sudo", "-n", "ip", self.family, "route", "show", "table", self.table, "exact", self.cidr]


def validated_families(cidr: str, via: str) -> list[str]:
    if cidr == "default":
        if not via:
            return ["-4", "-6"]
        try:
            return ["-4" if ipaddress.ip_address(via).version == 4 else "-6"]
        except ValueError:
            return []
    try:
        network = ipaddress.ip_network(cidr, strict=False)
    except ValueError:
        return []
    if via:
        try:
            address = ipaddress.ip_address(via)
        except ValueError:
            return []
        if address.version != network.version:
            return []
    return ["-4" if network.version == 4 else "-6"]


def validated_routes(route: object) -> list[OwnedRoute]:
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
    return [OwnedRoute(family, table, cidr, via, dev) for family in validated_families(cidr, via)]


def transaction_routes(path: Path) -> list[OwnedRoute]:
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
    validated: list[OwnedRoute] = []
    for route in routes:
        validated.extend(validated_routes(route))
    return validated


def reserved_rule_routes() -> list[OwnedRoute]:
    routes: list[OwnedRoute] = []
    for family in ("-4", "-6"):
        result = subprocess.run(
            ["sudo", "-n", "ip", family, "rule", "show"],
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            text=True,
            check=False,
        )
        for line in result.stdout.splitlines():
            fields = line.split()
            if not fields or fields[0] != SERVER_RULE_PRIORITY or "to" not in fields:
                continue
            index = fields.index("to")
            if index + 1 >= len(fields):
                continue
            cidr = fields[index + 1]
            try:
                network = ipaddress.ip_network(cidr, strict=False)
            except ValueError:
                continue
            expected = "-4" if network.version == 4 else "-6"
            if family == expected:
                routes.append(OwnedRoute(family, "main", cidr))
    return routes


def main(argv: list[str]) -> int:
    root = Path(argv[1] if len(argv) > 1 else "/run/podlaz/transactions")
    routes: set[OwnedRoute] = set(reserved_rule_routes())
    if root.is_dir():
        for path in sorted(root.glob("*.json")):
            routes.update(transaction_routes(path))
    ordered = sorted(routes, key=lambda item: (item.family, item.table, item.cidr, item.via, item.dev))
    for route in ordered:
        subprocess.run(route.delete_command(), stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=False)
    failures = 0
    for route in ordered:
        result = subprocess.run(route.show_command(), stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, check=False)
        if result.stdout.strip():
            failures += 1
    return 1 if failures else 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
