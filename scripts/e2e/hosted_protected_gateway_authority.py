#!/usr/bin/env python3
"""Read-only protected-gateway authority checks for hosted TUN lifecycle tests."""

from __future__ import annotations

import hashlib
import ipaddress
import json
import os
import re
import stat
import subprocess
import sys
from pathlib import Path
from typing import Any

import hosted_synthetic_network_authority as network

TX_ID_RE = re.compile(r"^[A-Za-z0-9._:-]{1,128}$")
SESSION_ID_RE = re.compile(r"^[0-9a-f]{32}$")
PE_TABLE_RE = re.compile(r"^podlaz_pe_[0-9a-f]{12}(?:_[1-9][0-9]{0,2})?$")


class AuthorityError(ValueError):
    pass


def require(condition: bool, label: str) -> None:
    if not condition:
        raise AuthorityError(label)


def private_file(path: Path, label: str) -> None:
    try:
        info = path.lstat()
    except OSError as exc:
        raise AuthorityError(f"{label} is unavailable") from exc
    require(stat.S_ISREG(info.st_mode) and not path.is_symlink(), f"{label} is not a regular file")


def load_json(path: Path, label: str) -> dict[str, Any]:
    private_file(path, label)
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise AuthorityError(f"{label} is invalid") from exc
    require(isinstance(value, dict), f"{label} is not an object")
    return value


def digest(path: Path, label: str) -> str:
    private_file(path, label)
    try:
        return hashlib.sha256(path.read_bytes()).hexdigest()
    except OSError as exc:
        raise AuthorityError(f"{label} cannot be hashed") from exc


def active_transaction(status: dict[str, Any], transactions: Path) -> tuple[str, Path]:
    require(status.get("connection") == "active", "status is not active")
    require(status.get("mode") == "tun", "status mode is not tun")
    health = status.get("tun_health")
    require(isinstance(health, dict) and health.get("state") == "verified", "TUN health is not verified")
    tx_id = str(status.get("active_transaction_id") or "").strip()
    require(bool(TX_ID_RE.fullmatch(tx_id)), "active transaction identity is invalid")
    summaries = status.get("transactions") or []
    require(isinstance(summaries, list), "status transactions are invalid")
    matches = [
        item
        for item in summaries
        if isinstance(item, dict)
        and str(item.get("id") or "").strip() == tx_id
        and item.get("state") == "committed"
        and not bool(item.get("requires_cleanup"))
    ]
    require(len(matches) == 1, "status does not publish one clean committed transaction")
    tx_path = transactions / f"{tx_id}.json"
    tx = load_json(tx_path, "active transaction")
    require(tx.get("schema_version") == network.TRANSACTION_SCHEMA, "transaction schema is unsupported")
    require(tx.get("owner") == network.TRANSACTION_OWNER, "transaction owner is unsupported")
    require(str(tx.get("id") or "").strip() == tx_id, "transaction identity mismatch")
    require(tx.get("mode") == "tun" and tx.get("state") == "committed", "transaction is not committed TUN authority")
    return tx_id, tx_path


def protected_gateway(manifest: network.Manifest) -> tuple[network.Route, network.Rule]:
    main_routes = [item for item in manifest.routes if item.table == "main"]
    main_rules = [item for item in manifest.rules if item.table == "main"]
    require(len(main_routes) == 1, "expected one exact main-table protected endpoint route")
    require(len(main_rules) == 1, "expected one exact main-table protected endpoint rule")
    route = main_routes[0]
    rule = main_rules[0]
    require(bool(route.via) and bool(route.dev), "protected endpoint route lacks current gateway/device")
    require(
        rule.destination == route.cidr and not rule.source and not rule.mark,
        "protected endpoint policy rule does not bind the route destination",
    )
    try:
        endpoint = ipaddress.ip_network(route.cidr, strict=False)
    except ValueError as exc:
        raise AuthorityError("protected endpoint route is invalid") from exc
    require(endpoint.version == 4 and endpoint.prefixlen == 32, "protected endpoint route is not an IPv4 host route")
    return route, rule


def verify_current(manifest_path: Path) -> None:
    manifest = network.load_manifest(manifest_path)
    require(network.verify(manifest, present=True), "exact protected gateway route/rule authority is not present")
    route, _ = protected_gateway(manifest)
    endpoint = str(ipaddress.ip_network(route.cidr, strict=False).network_address)
    try:
        result = subprocess.run(
            ["ip", "-4", "-j", "route", "get", endpoint],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            check=False,
        )
    except OSError as exc:
        raise AuthorityError("protected endpoint route observation could not start") from exc
    require(result.returncode == 0 and not result.stderr.strip(), "protected endpoint route observation failed")
    try:
        rows = json.loads(result.stdout)
    except json.JSONDecodeError as exc:
        raise AuthorityError("protected endpoint route observation is invalid JSON") from exc
    require(isinstance(rows, list) and len(rows) >= 1, "protected endpoint route observation is empty")
    matches = [
        row
        for row in rows
        if isinstance(row, dict)
        and str(row.get("gateway") or "") == route.via
        and str(row.get("dev") or "") == route.dev
    ]
    require(len(matches) >= 1, "protected endpoint no longer resolves through committed gateway/device")


def identity(
    status_path: Path,
    transactions: Path,
    session_path: Path,
    runtime_config: Path,
    manifest_path: Path,
) -> dict[str, Any]:
    status = load_json(status_path, "status")
    tx_id, tx_path = active_transaction(status, transactions)
    session = load_json(session_path, "Network Session")
    session_id = str(session.get("session_id") or "").strip()
    require(bool(SESSION_ID_RE.fullmatch(session_id)), "Network Session identity is invalid")
    protection = session.get("protection")
    require(isinstance(protection, dict) and protection.get("state") == "armed", "Privacy Envelope is not armed")
    pe_family = str(protection.get("family") or "").strip()
    pe_table = str(protection.get("table") or "").strip()
    require(pe_family == "inet" and bool(PE_TABLE_RE.fullmatch(pe_table)), "Privacy Envelope identity is invalid")
    require(pe_table.startswith(f"podlaz_pe_{session_id[:12]}"), "Privacy Envelope is not bound to Network Session")

    manifest = network.load_manifest(manifest_path)
    route, rule = protected_gateway(manifest)
    verify_current(manifest_path)
    return {
        "transaction_id": tx_id,
        "transaction_sha256": digest(tx_path, "active transaction"),
        "session_id": session_id,
        "session_sha256": digest(session_path, "Network Session"),
        "privacy_family": pe_family,
        "privacy_table": pe_table,
        "runtime_config_sha256": digest(runtime_config, "runtime config"),
        "protected_endpoint": route.cidr,
        "protected_gateway": route.via,
        "protected_device": route.dev,
        "protected_rule_priority": rule.priority,
        "network_manifest_sha256": digest(manifest_path, "network manifest"),
    }


def write_identity(path: Path, value: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(path.name + ".tmp")
    temporary.write_text(json.dumps(value, sort_keys=True) + "\n", encoding="utf-8")
    os.chmod(temporary, 0o600)
    os.replace(temporary, path)


def same(first_path: Path, second_path: Path) -> None:
    first = load_json(first_path, "first authority identity")
    second = load_json(second_path, "second authority identity")
    require(first == second, "active protected authority changed during read/recovery observation")


def fresh(first_path: Path, second_path: Path) -> None:
    first = load_json(first_path, "first generation identity")
    second = load_json(second_path, "second generation identity")
    for key in ("transaction_id", "session_id", "privacy_table"):
        require(
            isinstance(first.get(key), str)
            and isinstance(second.get(key), str)
            and first[key] != second[key],
            f"reconnect reused stale {key}",
        )


def privacy_absent(identity_path: Path) -> None:
    value = load_json(identity_path, "retired authority identity")
    family = str(value.get("privacy_family") or "")
    table = str(value.get("privacy_table") or "")
    require(family == "inet" and bool(PE_TABLE_RE.fullmatch(table)), "retired Privacy Envelope identity is invalid")
    try:
        result = subprocess.run(
            ["nft", "list", "table", family, table],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            check=False,
        )
    except OSError as exc:
        raise AuthorityError("Privacy Envelope absence observation could not start") from exc
    require(result.returncode != 0, "retired Privacy Envelope table is still present")


def main(argv: list[str]) -> int:
    try:
        if len(argv) == 3 and argv[1] == "verify-current":
            verify_current(Path(argv[2]))
            return 0
        if len(argv) == 8 and argv[1] == "identity":
            _, _, status, transactions, session, runtime_config, manifest, output = argv
            write_identity(
                Path(output),
                identity(
                    Path(status),
                    Path(transactions),
                    Path(session),
                    Path(runtime_config),
                    Path(manifest),
                ),
            )
            return 0
        if len(argv) == 4 and argv[1] == "same":
            same(Path(argv[2]), Path(argv[3]))
            return 0
        if len(argv) == 4 and argv[1] == "fresh":
            fresh(Path(argv[2]), Path(argv[3]))
            return 0
        if len(argv) == 3 and argv[1] == "privacy-absent":
            privacy_absent(Path(argv[2]))
            return 0
        print(
            "usage: hosted_protected_gateway_authority.py "
            "verify-current MANIFEST | "
            "identity STATUS TRANSACTIONS SESSION RUNTIME_CONFIG MANIFEST OUTPUT | "
            "same FIRST SECOND | fresh FIRST SECOND | privacy-absent IDENTITY",
            file=sys.stderr,
        )
        return 2
    except (AuthorityError, network.AuthorityError, network.InspectionError, OSError) as exc:
        print(f"hosted protected gateway authority rejected: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
