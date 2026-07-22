#!/usr/bin/env python3
"""Fail-closed cleanup for exact route and policy-rule rollback metadata."""

from __future__ import annotations

import ipaddress
import json
import os
import re
import subprocess
import sys
import tempfile
from dataclasses import asdict, dataclass
from pathlib import Path

TRANSACTION_SCHEMA = "podlaz.transaction.v1"
TRANSACTION_OWNER = "podlaz"
ROUTE_OWNERS = {"podlaz:route", TRANSACTION_OWNER}
POLICY_RULE_OWNERS = {"podlaz:policy-rule", TRANSACTION_OWNER}
KNOWN_STATES = {
    "planned",
    "applying",
    "applied",
    "verifying",
    "committed",
    "rolling_back",
    "rolled_back",
    "failed",
}
CLEANUP_REQUIRED_STATES = {"planned", "applying", "applied", "verifying", "rolling_back", "failed"}
MANAGED_TABLES = {"51820", "podlaz"}
MAIN_TABLE = "main"
MANAGED_LINK = "podlaz0"
SERVER_RULE_PRIORITY = 9999
TUN_RULE_PRIORITY = 10000
DEV_RE = re.compile(r"^[A-Za-z0-9_.:@-]{1,64}$")
MARK_RE = re.compile(r"^(?:0x[0-9A-Fa-f]+|[0-9]+)(?:/(?:0x[0-9A-Fa-f]+|[0-9]+))?$")


class MetadataError(ValueError):
    """Rollback metadata is missing, malformed, or ambiguous."""


@dataclass(frozen=True, order=True)
class OwnedRoute:
    family: str
    table: str
    cidr: str
    via: str = ""
    dev: str = ""

    def delete_command(self) -> list[str]:
        args = ["ip", self.family, "route", "del", self.cidr]
        if self.via:
            args.extend(["via", self.via])
        if self.dev:
            args.extend(["dev", self.dev])
        args.extend(["table", self.table])
        return args

    def show_command(self) -> list[str]:
        return ["ip", self.family, "route", "show", "table", self.table, "exact", self.cidr]


@dataclass(frozen=True, order=True)
class OwnedPolicyRule:
    family: str
    priority: int
    source: str
    destination: str
    mark: str
    table: str

    def delete_command(self) -> list[str]:
        args = ["ip", self.family, "rule", "del", "priority", str(self.priority)]
        if self.source:
            args.extend(["from", self.source])
        if self.destination:
            args.extend(["to", self.destination])
        if self.mark:
            args.extend(["fwmark", self.mark])
        args.extend(["lookup", self.table])
        return args

    def show_command(self) -> list[str]:
        return ["ip", self.family, "rule", "show"]


@dataclass(frozen=True)
class NetworkManifest:
    routes: tuple[OwnedRoute, ...]
    rules: tuple[OwnedPolicyRule, ...]

    def to_json(self) -> dict[str, object]:
        return {
            "schema_version": "podlaz.e2e.rollback-network.v1",
            "routes": [asdict(route) for route in self.routes],
            "rules": [asdict(rule) for rule in self.rules],
        }


def _dict(value: object, label: str) -> dict[str, object]:
    if not isinstance(value, dict):
        raise MetadataError(f"{label} is not an object")
    return value


def _list(value: object, label: str) -> list[object]:
    if not isinstance(value, list):
        raise MetadataError(f"{label} is not an array")
    return value


def _normalize_ipv4_prefix(value: str, *, host_only: bool = False) -> str:
    text = value.strip()
    if not text:
        raise MetadataError("empty IPv4 prefix")
    try:
        if "/" not in text:
            text += "/32"
        prefix = ipaddress.ip_network(text, strict=False)
    except ValueError as exc:
        raise MetadataError("invalid IPv4 prefix") from exc
    if prefix.version != 4 or (host_only and prefix.prefixlen != 32):
        raise MetadataError("unsupported IPv4 prefix")
    return prefix.with_prefixlen


def _normalize_ipv4_address(value: str) -> str:
    try:
        address = ipaddress.ip_address(value.strip())
    except ValueError as exc:
        raise MetadataError("invalid IPv4 address") from exc
    if address.version != 4:
        raise MetadataError("unsupported non-IPv4 address")
    return str(address)


def validated_route(value: object) -> OwnedRoute:
    route = _dict(value, "rollback route")
    if str(route.get("owner", "")).strip() not in ROUTE_OWNERS:
        raise MetadataError("rollback route has invalid owner")
    table = str(route.get("table", "")).strip()
    cidr_raw = str(route.get("cidr", "")).strip()
    via = str(route.get("via", "")).strip()
    dev = str(route.get("dev", "")).strip()
    if dev and not DEV_RE.fullmatch(dev):
        raise MetadataError("rollback route has invalid device")

    if table == MAIN_TABLE:
        cidr = _normalize_ipv4_prefix(cidr_raw, host_only=True)
        if not via or not dev:
            raise MetadataError("main-table rollback route lacks exact via/dev tuple")
        return OwnedRoute("-4", table, cidr, _normalize_ipv4_address(via), dev)

    if table not in MANAGED_TABLES:
        raise MetadataError("rollback route has unmanaged table")
    if cidr_raw == "default":
        cidr = "default"
    else:
        cidr = _normalize_ipv4_prefix(cidr_raw)
    if via:
        via = _normalize_ipv4_address(via)
    if dev and dev != MANAGED_LINK:
        raise MetadataError("managed-table rollback route has foreign device")
    return OwnedRoute("-4", table, cidr, via, dev)


def _selector(value: object, *, allow_all: bool) -> str:
    text = str(value or "").strip()
    if not text:
        return ""
    if allow_all and text == "all":
        return text
    return _normalize_ipv4_prefix(text)


def validated_policy_rule(value: object) -> OwnedPolicyRule:
    rule = _dict(value, "rollback policy rule")
    if str(rule.get("owner", "")).strip() not in POLICY_RULE_OWNERS:
        raise MetadataError("rollback policy rule has invalid owner")
    priority = rule.get("priority")
    if not isinstance(priority, int) or isinstance(priority, bool) or priority <= 0:
        raise MetadataError("rollback policy rule has invalid priority")
    table = str(rule.get("table", "")).strip()
    source = _selector(rule.get("from"), allow_all=True)
    destination = _selector(rule.get("to"), allow_all=False)
    mark = str(rule.get("mark", "") or "").strip()
    if mark and not MARK_RE.fullmatch(mark):
        raise MetadataError("rollback policy rule has invalid fwmark")

    if table == MAIN_TABLE:
        if priority != SERVER_RULE_PRIORITY or source or mark or not destination:
            raise MetadataError("main-table rollback rule is not an exact server bypass tuple")
        destination = _normalize_ipv4_prefix(destination, host_only=True)
    elif table in MANAGED_TABLES:
        if priority != TUN_RULE_PRIORITY or not (source or destination or mark):
            raise MetadataError("managed-table rollback rule is ambiguous")
    else:
        raise MetadataError("rollback policy rule has unmanaged table")

    return OwnedPolicyRule("-4", priority, source, destination, mark, table)


def _transaction_network(path: Path) -> tuple[list[OwnedRoute], list[OwnedPolicyRule]]:
    try:
        with path.open(encoding="utf-8") as handle:
            payload = json.load(handle)
    except (OSError, json.JSONDecodeError) as exc:
        raise MetadataError("transaction file is unreadable or invalid JSON") from exc
    transaction = _dict(payload, "transaction")
    if transaction.get("schema_version") != TRANSACTION_SCHEMA:
        raise MetadataError("transaction schema is unsupported")
    if transaction.get("owner") != TRANSACTION_OWNER:
        raise MetadataError("transaction owner is not podlaz")
    state = str(transaction.get("state", "")).strip()
    if state not in KNOWN_STATES:
        raise MetadataError("transaction state is unsupported")

    rollback = _dict(transaction.get("rollback", {}), "transaction rollback")
    route_values = _list(rollback.get("routes", []), "transaction rollback routes")
    rule_values = _list(rollback.get("policy_rules", []), "transaction rollback policy rules")
    routes = [validated_route(value) for value in route_values]
    rules = [validated_policy_rule(value) for value in rule_values]

    if state in CLEANUP_REQUIRED_STATES:
        desired = _dict(transaction.get("desired_plan", {}), "transaction desired plan")
        desired_routes = _list(desired.get("routes", []), "transaction desired routes")
        desired_steps = _list(desired.get("steps", []), "transaction desired steps")
        applied_steps = _list(transaction.get("applied_steps", []), "transaction applied steps")
        planned_rule = any(isinstance(step, dict) and step.get("kind") == "policy-rule" for step in desired_steps)
        applied_route = any(isinstance(step, dict) and step.get("kind") == "route" for step in applied_steps)
        applied_rule = any(isinstance(step, dict) and step.get("kind") == "policy-rule" for step in applied_steps)
        if (desired_routes or applied_route) and not routes:
            raise MetadataError("cleanup-required transaction lacks exact route rollback metadata")
        if (planned_rule or applied_rule) and not rules:
            raise MetadataError("cleanup-required transaction lacks exact policy-rule rollback metadata")

    return routes, rules


def snapshot_transactions(root: Path, manifest_path: Path) -> NetworkManifest:
    routes: set[OwnedRoute] = set()
    rules: set[OwnedPolicyRule] = set()
    if root.exists():
        if not root.is_dir():
            raise MetadataError("transaction path is not a directory")
        entries = sorted(root.iterdir())
        for entry in entries:
            if entry.is_symlink() or not entry.is_file() or entry.suffix != ".json":
                raise MetadataError("transaction directory contains an unexpected entry")
        for path in entries:
            tx_routes, tx_rules = _transaction_network(path)
            routes.update(tx_routes)
            rules.update(tx_rules)

    manifest = NetworkManifest(tuple(sorted(routes)), tuple(sorted(rules)))
    _write_manifest(manifest_path, manifest)
    return manifest


def _write_manifest(path: Path, manifest: NetworkManifest) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(prefix=path.name + ".", dir=path.parent)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as handle:
            json.dump(manifest.to_json(), handle, sort_keys=True)
            handle.write("\n")
            handle.flush()
            os.fsync(handle.fileno())
        os.chmod(temporary, 0o600)
        os.replace(temporary, path)
    except Exception:
        try:
            os.unlink(temporary)
        except OSError:
            pass
        raise


def load_manifest(path: Path) -> NetworkManifest:
    try:
        with path.open(encoding="utf-8") as handle:
            payload = json.load(handle)
    except (OSError, json.JSONDecodeError) as exc:
        raise MetadataError("rollback manifest is unreadable or invalid") from exc
    root = _dict(payload, "rollback manifest")
    if root.get("schema_version") != "podlaz.e2e.rollback-network.v1":
        raise MetadataError("rollback manifest schema is unsupported")
    routes = tuple(validated_manifest_route(item) for item in _list(root.get("routes"), "manifest routes"))
    rules = tuple(validated_manifest_rule(item) for item in _list(root.get("rules"), "manifest rules"))
    return NetworkManifest(tuple(sorted(set(routes))), tuple(sorted(set(rules))))


def validated_manifest_route(value: object) -> OwnedRoute:
    route = _dict(value, "manifest route")
    family = str(route.get("family", ""))
    if family != "-4":
        raise MetadataError("manifest route family is unsupported")
    owner_form = {
        "owner": "podlaz:route",
        "table": route.get("table"),
        "cidr": route.get("cidr"),
        "via": route.get("via"),
        "dev": route.get("dev"),
    }
    return validated_route(owner_form)


def validated_manifest_rule(value: object) -> OwnedPolicyRule:
    rule = _dict(value, "manifest rule")
    family = str(rule.get("family", ""))
    if family != "-4":
        raise MetadataError("manifest rule family is unsupported")
    owner_form = {
        "owner": "podlaz:policy-rule",
        "priority": rule.get("priority"),
        "from": rule.get("source"),
        "to": rule.get("destination"),
        "mark": rule.get("mark"),
        "table": rule.get("table"),
    }
    return validated_policy_rule(owner_form)


def _run(command: list[str]) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        command,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        check=False,
    )


def _rule_present(rule: OwnedPolicyRule, output: str) -> bool:
    for line in output.splitlines():
        fields = line.split()
        if not fields or fields[0] != f"{rule.priority}:":
            continue
        observed: dict[str, str] = {"from": "", "to": "", "fwmark": "", "lookup": ""}
        for key in tuple(observed):
            if key in fields:
                index = fields.index(key)
                if index + 1 < len(fields):
                    observed[key] = fields[index + 1]
        if observed["from"] == "all":
            source = "all" if rule.source == "all" else ""
        else:
            source = "" if not observed["from"] else _normalize_ipv4_prefix(observed["from"])
        destination = "" if not observed["to"] else _normalize_ipv4_prefix(observed["to"])
        if (
            source == rule.source
            and destination == rule.destination
            and observed["fwmark"] == rule.mark
            and observed["lookup"] == rule.table
        ):
            return True
    return False


def _route_present(route: OwnedRoute, output: str) -> bool:
    for line in output.splitlines():
        fields = line.split()
        if not fields:
            continue
        observed_cidr = fields[0]
        if route.cidr != "default":
            try:
                observed_cidr = _normalize_ipv4_prefix(observed_cidr)
            except MetadataError:
                continue
        via = fields[fields.index("via") + 1] if "via" in fields and fields.index("via") + 1 < len(fields) else ""
        dev = fields[fields.index("dev") + 1] if "dev" in fields and fields.index("dev") + 1 < len(fields) else ""
        if observed_cidr == route.cidr and via == route.via and dev == route.dev:
            return True
    return False


def verify_manifest(manifest: NetworkManifest) -> bool:
    for rule in manifest.rules:
        if _rule_present(rule, _run(rule.show_command()).stdout):
            return False
    for route in manifest.routes:
        if _route_present(route, _run(route.show_command()).stdout):
            return False
    return True


def cleanup_manifest(manifest: NetworkManifest) -> bool:
    for rule in manifest.rules:
        _run(rule.delete_command())
    for route in manifest.routes:
        _run(route.delete_command())
    return verify_manifest(manifest)


def main(argv: list[str]) -> int:
    if len(argv) == 4 and argv[1] == "snapshot":
        mode, source, manifest_arg = argv[1:]
    elif len(argv) == 3 and argv[1] in {"cleanup", "verify"}:
        mode, manifest_arg = argv[1:]
        source = ""
    else:
        print("usage: tun-package-fallback-network.py snapshot <transaction-dir> <manifest> | <cleanup|verify> <manifest>", file=sys.stderr)
        return 2
    try:
        if mode == "snapshot":
            snapshot_transactions(Path(source), Path(manifest_arg))
            return 0
        manifest = load_manifest(Path(manifest_arg))
        return 0 if (cleanup_manifest(manifest) if mode == "cleanup" else verify_manifest(manifest)) else 1
    except (MetadataError, OSError) as exc:
        print(f"fallback network metadata rejected: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
