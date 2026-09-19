#!/usr/bin/env python3
"""Read-only exact route/rule authority verifier for hosted synthetic TUN tests.

This helper never mutates network state. It derives exact Podlaz-owned route and
policy-rule tuples from one committed TUN transaction by requiring desired,
applied, and rollback evidence to agree, persists only those tuples in a private
manifest, and can then prove them present or absent on the guest.
"""

from __future__ import annotations

import ipaddress
import json
import os
import re
import shlex
import stat
import subprocess
import sys
import tempfile
from collections import Counter, defaultdict
from dataclasses import asdict, dataclass
from pathlib import Path

TRANSACTION_SCHEMA = "podlaz.transaction.v1"
TRANSACTION_OWNER = "podlaz"
ROUTE_OWNER = "podlaz:route"
RULE_OWNER = "podlaz:policy-rule"
MANIFEST_SCHEMA = "podlaz.e2e.hosted-network-authority.v1"
DEV_RE = re.compile(r"^[A-Za-z0-9_.:@-]{1,64}$")
TABLE_RE = re.compile(r"^(?:main|[1-9][0-9]{0,9})$")
MARK_RE = re.compile(r"^(?:0x[0-9A-Fa-f]+|[0-9]+)(?:/(?:0x[0-9A-Fa-f]+|[0-9]+))?$")


class AuthorityError(ValueError):
    pass


class InspectionError(RuntimeError):
    pass


@dataclass(frozen=True, order=True)
class Route:
    family: str
    table: str
    cidr: str
    via: str = ""
    dev: str = ""

    def show_command(self) -> list[str]:
        return ["ip", "-4", "route", "show", "table", self.table, "exact", self.cidr]


@dataclass(frozen=True, order=True)
class Rule:
    family: str
    priority: int
    source: str
    destination: str
    mark: str
    table: str

    def show_command(self) -> list[str]:
        return ["ip", "-4", "rule", "show", "priority", str(self.priority)]


@dataclass(frozen=True)
class Manifest:
    routes: tuple[Route, ...]
    rules: tuple[Rule, ...]

    def to_json(self) -> dict[str, object]:
        return {
            "schema_version": MANIFEST_SCHEMA,
            "routes": [asdict(item) for item in self.routes],
            "rules": [asdict(item) for item in self.rules],
        }


def _dict(value: object, label: str) -> dict[str, object]:
    if not isinstance(value, dict):
        raise AuthorityError(f"{label} is not an object")
    return value


def _list(value: object, label: str) -> list[object]:
    if not isinstance(value, list):
        raise AuthorityError(f"{label} is not an array")
    return value


def _table(value: object) -> str:
    table = str(value or "").strip()
    if not TABLE_RE.fullmatch(table):
        raise AuthorityError("unsupported routing table identity")
    return table


def _prefix(value: object, *, host_only: bool = False, allow_default: bool = False) -> str:
    text = str(value or "").strip()
    if allow_default and text == "default":
        return text
    if not text:
        raise AuthorityError("missing IPv4 prefix")
    if "/" not in text:
        text += "/32"
    try:
        prefix = ipaddress.ip_network(text, strict=False)
    except ValueError as exc:
        raise AuthorityError("invalid IPv4 prefix") from exc
    if prefix.version != 4 or (host_only and prefix.prefixlen != 32):
        raise AuthorityError("unsupported IPv4 prefix")
    return prefix.with_prefixlen


def _address(value: object) -> str:
    try:
        address = ipaddress.ip_address(str(value or "").strip())
    except ValueError as exc:
        raise AuthorityError("invalid IPv4 address") from exc
    if address.version != 4:
        raise AuthorityError("unsupported non-IPv4 address")
    return str(address)


def _route(value: object, label: str) -> Route:
    item = _dict(value, label)
    if str(item.get("owner") or "").strip() != ROUTE_OWNER:
        raise AuthorityError(f"{label} has invalid owner")
    table = _table(item.get("table"))
    cidr = _prefix(item.get("cidr"), allow_default=True)
    via = str(item.get("via") or "").strip()
    dev = str(item.get("dev") or "").strip()
    if via:
        via = _address(via)
    if dev and not DEV_RE.fullmatch(dev):
        raise AuthorityError(f"{label} has invalid device")
    if table == "main":
        if cidr == "default" or not via or not dev:
            raise AuthorityError(f"{label} main-table tuple is incomplete")
        cidr = _prefix(cidr, host_only=True)
    elif dev != "podlaz0":
        raise AuthorityError(f"{label} session route is not bound to podlaz0")
    return Route("ipv4", table, cidr, via, dev)


def _selector_prefix(value: str, *, allow_all: bool) -> str:
    text = value.strip()
    if allow_all and text == "all":
        return "all"
    return _prefix(text)


def _rule_from_mapping(value: object, label: str) -> Rule:
    item = _dict(value, label)
    if str(item.get("owner") or "").strip() != RULE_OWNER:
        raise AuthorityError(f"{label} has invalid owner")
    priority = item.get("priority")
    if not isinstance(priority, int) or isinstance(priority, bool) or not (0 < priority < 32766):
        raise AuthorityError(f"{label} has invalid priority")
    source = str(item.get("from") or "").strip()
    destination = str(item.get("to") or "").strip()
    mark = str(item.get("mark") or "").strip()
    if sum(bool(value) for value in (source, destination, mark)) != 1:
        raise AuthorityError(f"{label} must have one exact selector")
    if source:
        source = _selector_prefix(source, allow_all=True)
    if destination:
        destination = _selector_prefix(destination, allow_all=False)
    if mark and not MARK_RE.fullmatch(mark):
        raise AuthorityError(f"{label} has invalid fwmark")
    table = _table(item.get("table"))
    if table == "main" and (not destination or source or mark):
        raise AuthorityError(f"{label} main-table rule is not an exact destination bypass")
    return Rule("ipv4", priority, source, destination, mark, table)


def _rule_from_target(target: str, owner: str) -> Rule:
    if owner != RULE_OWNER:
        raise AuthorityError("policy-rule step has invalid owner")
    try:
        fields = shlex.split(target)
    except ValueError as exc:
        raise AuthorityError("policy-rule target is invalid") from exc
    if len(fields) != 6 or fields[0] != "priority" or fields[4] != "lookup":
        raise AuthorityError("policy-rule target is invalid")
    try:
        priority = int(fields[1])
    except ValueError as exc:
        raise AuthorityError("policy-rule priority is invalid") from exc
    selector = fields[2]
    if selector not in {"from", "to", "fwmark"}:
        raise AuthorityError("policy-rule selector is invalid")
    item: dict[str, object] = {
        "owner": owner,
        "priority": priority,
        "table": fields[5],
    }
    item[{"from": "from", "to": "to", "fwmark": "mark"}[selector]] = fields[3]
    return _rule_from_mapping(item, "policy-rule step")


def _route_target(value: dict[str, object]) -> str:
    return f"{str(value.get('table') or '').strip()} {str(value.get('cidr') or '').strip()}"


def _desired_routes(values: list[object]) -> dict[str, list[Route]]:
    out: dict[str, list[Route]] = defaultdict(list)
    for raw in values:
        item = _dict(raw, "desired route")
        if str(item.get("kind") or "").strip() != "route" or str(item.get("operation") or "").strip() != "add":
            continue
        route = _route(item, "desired route")
        out[_route_target(item)].append(route)
    return out


def _desired_rules(values: list[object]) -> Counter[tuple[str, str]]:
    out: Counter[tuple[str, str]] = Counter()
    for raw in values:
        item = _dict(raw, "desired step")
        if str(item.get("kind") or "").strip() != "policy-rule":
            continue
        target = str(item.get("target") or "").strip()
        owner = str(item.get("owner") or "").strip()
        _rule_from_target(target, owner)
        out[(target, owner)] += 1
    return out


def _transaction_manifest(path: Path) -> Manifest:
    try:
        payload = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise AuthorityError("transaction file is unreadable or invalid JSON") from exc
    tx = _dict(payload, "transaction")
    if tx.get("schema_version") != TRANSACTION_SCHEMA or tx.get("owner") != TRANSACTION_OWNER:
        raise AuthorityError("transaction identity is unsupported")
    if tx.get("mode") != "tun" or tx.get("state") != "committed":
        raise AuthorityError("transaction is not one committed TUN authority")

    desired = _dict(tx.get("desired_plan") or {}, "desired plan")
    desired_routes = _desired_routes(_list(desired.get("routes") or [], "desired routes"))
    desired_rules = _desired_rules(_list(desired.get("steps") or [], "desired steps"))

    applied_routes: list[Route] = []
    applied_rules: list[Rule] = []
    for raw in _list(tx.get("applied_steps") or [], "applied steps"):
        step = _dict(raw, "applied step")
        kind = str(step.get("kind") or "").strip()
        target = str(step.get("target") or "").strip()
        owner = str(step.get("owner") or "").strip()
        if kind == "route":
            if owner != ROUTE_OWNER:
                raise AuthorityError("applied route has invalid owner")
            candidates = desired_routes.get(target, [])
            if len(candidates) != 1:
                raise AuthorityError("applied route lacks one exact desired tuple")
            applied_routes.append(candidates[0])
        elif kind == "policy-rule":
            key = (target, owner)
            if desired_rules[key] <= 0:
                raise AuthorityError("applied policy rule is outside exact desired proof")
            desired_rules[key] -= 1
            applied_rules.append(_rule_from_target(target, owner))

    rollback = _dict(tx.get("rollback") or {}, "rollback")
    rollback_routes = [
        _route(item, "rollback route")
        for item in _list(rollback.get("routes") or [], "rollback routes")
    ]
    rollback_rules = [
        _rule_from_mapping(item, "rollback policy rule")
        for item in _list(rollback.get("policy_rules") or [], "rollback policy rules")
    ]
    if Counter(applied_routes) != Counter(rollback_routes):
        raise AuthorityError("rollback routes do not exactly match applied route proof")
    if Counter(applied_rules) != Counter(rollback_rules):
        raise AuthorityError("rollback policy rules do not exactly match applied rule proof")
    return Manifest(tuple(sorted(rollback_routes)), tuple(sorted(rollback_rules)))


def snapshot(root: Path, manifest_path: Path) -> Manifest:
    try:
        entries = sorted(root.iterdir())
    except OSError as exc:
        raise AuthorityError("transaction directory cannot be inspected") from exc
    files: list[Path] = []
    for entry in entries:
        try:
            info = entry.lstat()
        except OSError as exc:
            raise AuthorityError("transaction entry cannot be inspected") from exc
        if not stat.S_ISREG(info.st_mode) or entry.suffix != ".json":
            raise AuthorityError("transaction directory contains an unexpected entry")
        files.append(entry)
    if len(files) != 1:
        raise AuthorityError(f"expected one transaction authority, found {len(files)}")
    manifest = _transaction_manifest(files[0])
    _write_manifest(manifest_path, manifest)
    return manifest


def _write_manifest(path: Path, manifest: Manifest) -> None:
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


def load_manifest(path: Path) -> Manifest:
    try:
        info = path.lstat()
        if not stat.S_ISREG(info.st_mode) or path.is_symlink():
            raise AuthorityError("manifest is not a regular private file")
        payload = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise AuthorityError("manifest is unreadable or invalid JSON") from exc
    root = _dict(payload, "manifest")
    if root.get("schema_version") != MANIFEST_SCHEMA:
        raise AuthorityError("manifest schema is unsupported")
    routes = tuple(
        _manifest_route(item) for item in _list(root.get("routes"), "manifest routes")
    )
    rules = tuple(
        _manifest_rule(item) for item in _list(root.get("rules"), "manifest rules")
    )
    return Manifest(tuple(sorted(routes)), tuple(sorted(rules)))


def _manifest_route(value: object) -> Route:
    item = _dict(value, "manifest route")
    if item.get("family") != "ipv4":
        raise AuthorityError("manifest route family is unsupported")
    owner_form = dict(item)
    owner_form["owner"] = ROUTE_OWNER
    return _route(owner_form, "manifest route")


def _manifest_rule(value: object) -> Rule:
    item = _dict(value, "manifest rule")
    if item.get("family") != "ipv4":
        raise AuthorityError("manifest rule family is unsupported")
    owner_form: dict[str, object] = {
        "owner": RULE_OWNER,
        "priority": item.get("priority"),
        "from": item.get("source"),
        "to": item.get("destination"),
        "mark": item.get("mark"),
        "table": item.get("table"),
    }
    return _rule_from_mapping(owner_form, "manifest policy rule")


def _run(command: list[str]) -> subprocess.CompletedProcess[str]:
    try:
        return subprocess.run(
            command,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            check=False,
        )
    except OSError as exc:
        raise InspectionError("network inspection command could not start") from exc


def _missing_route_table(result: subprocess.CompletedProcess[str]) -> bool:
    text = (result.stdout + "\n" + result.stderr).lower()
    return "fib table does not exist" in text


def _route_present(route: Route) -> bool:
    result = _run(route.show_command())
    if result.returncode != 0:
        if _missing_route_table(result):
            return False
        raise InspectionError("route inspection failed")
    for raw in result.stdout.splitlines():
        fields = raw.split()
        if not fields:
            continue
        observed_cidr = fields[0]
        if route.cidr != "default":
            try:
                observed_cidr = _prefix(observed_cidr)
            except AuthorityError:
                continue
        via = fields[fields.index("via") + 1] if "via" in fields and fields.index("via") + 1 < len(fields) else ""
        dev = fields[fields.index("dev") + 1] if "dev" in fields and fields.index("dev") + 1 < len(fields) else ""
        if observed_cidr == route.cidr and via == route.via and dev == route.dev:
            return True
    return False


def _normalize_lookup(value: str) -> str:
    return "51820" if value == "podlaz" else value


def _rule_present(rule: Rule) -> bool:
    result = _run(rule.show_command())
    if result.returncode != 0:
        raise InspectionError("policy-rule inspection failed")
    for raw in result.stdout.splitlines():
        fields = raw.split()
        if not fields or fields[0] != f"{rule.priority}:":
            continue
        observed = {"from": "", "to": "", "fwmark": "", "lookup": ""}
        for key in tuple(observed):
            if key in fields:
                index = fields.index(key)
                if index + 1 < len(fields):
                    observed[key] = fields[index + 1]
        source = observed["from"]
        if source == "all":
            source = "all" if rule.source == "all" else ""
        elif source:
            source = _prefix(source)
        destination = _prefix(observed["to"]) if observed["to"] else ""
        if (
            source == rule.source
            and destination == rule.destination
            and observed["fwmark"] == rule.mark
            and _normalize_lookup(observed["lookup"]) == rule.table
        ):
            return True
    return False


def verify(manifest: Manifest, *, present: bool) -> bool:
    observations = [_route_present(item) for item in manifest.routes]
    observations.extend(_rule_present(item) for item in manifest.rules)
    return all(value is present for value in observations)


def main(argv: list[str]) -> int:
    if len(argv) == 4 and argv[1] == "snapshot":
        _, _, root, manifest_path = argv
        mode = "snapshot"
    elif len(argv) == 3 and argv[1] in {"verify", "verify-absent", "verify-present"}:
        _, mode, manifest_path = argv
        root = ""
    else:
        print(
            "usage: hosted_synthetic_network_authority.py snapshot <transaction-dir> <manifest> | <verify|verify-absent|verify-present> <manifest>",
            file=sys.stderr,
        )
        return 2
    try:
        if mode == "snapshot":
            snapshot(Path(root), Path(manifest_path))
            return 0
        manifest = load_manifest(Path(manifest_path))
        want_present = mode == "verify-present"
        return 0 if verify(manifest, present=want_present) else 1
    except InspectionError as exc:
        print(f"hosted network authority inspection failed: {exc}", file=sys.stderr)
        return 2
    except (AuthorityError, OSError) as exc:
        print(f"hosted network authority rejected: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
