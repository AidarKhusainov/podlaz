#!/usr/bin/env python3
"""Manage a rules-only foreign fixture derived from exact hosted authority.

The fixture is test-owned. It is copied from a current committed network-authority
manifest only after the product has cleanly disconnected. No historical table or
priority value is embedded here, and removal targets only the exact copied tuples.
"""

from __future__ import annotations

import ipaddress
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

MANIFEST_SCHEMA = "podlaz.e2e.hosted-network-authority.v1"


class FixtureError(RuntimeError):
    pass


def _load(path: Path) -> dict[str, object]:
    try:
        if not path.is_file() or path.is_symlink():
            raise FixtureError("fixture manifest path is not a regular file")
        with path.open(encoding="utf-8") as handle:
            root = json.load(handle)
    except (OSError, json.JSONDecodeError) as exc:
        raise FixtureError("fixture manifest is unreadable or invalid") from exc
    if not isinstance(root, dict) or root.get("schema_version") != MANIFEST_SCHEMA:
        raise FixtureError("fixture manifest schema is unsupported")
    return root


def _normalize_prefix(value: object) -> str:
    text = str(value or "").strip()
    if text in {"", "all"}:
        return text
    try:
        return str(ipaddress.ip_network(text, strict=False))
    except ValueError as exc:
        raise FixtureError("policy-rule selector is invalid") from exc


def _rule(value: object) -> dict[str, object]:
    if not isinstance(value, dict) or value.get("family") != "ipv4":
        raise FixtureError("policy-rule fixture is not IPv4")
    try:
        priority = int(value.get("priority"))
    except (TypeError, ValueError) as exc:
        raise FixtureError("policy-rule priority is invalid") from exc
    if priority <= 0:
        raise FixtureError("policy-rule priority is invalid")
    table = str(value.get("table") or "").strip()
    if not table:
        raise FixtureError("policy-rule table is missing")
    source = _normalize_prefix(value.get("source"))
    destination = _normalize_prefix(value.get("destination"))
    mark = str(value.get("mark") or "").strip()
    if source == "":
        source = "all"
    return {
        "family": "ipv4",
        "priority": priority,
        "source": source,
        "destination": destination,
        "mark": mark,
        "table": table,
    }


def _rules(root: dict[str, object]) -> list[dict[str, object]]:
    values = root.get("rules")
    if not isinstance(values, list) or not values:
        raise FixtureError("authority manifest has no policy rules")
    rules = [_rule(item) for item in values]
    identities = {
        (
            item["priority"],
            item["source"],
            item["destination"],
            item["mark"],
            item["table"],
        )
        for item in rules
    }
    if len(identities) != len(rules):
        raise FixtureError("authority manifest contains duplicate policy rules")
    return rules


def _write_fixture(source: Path, target: Path) -> None:
    root = _load(source)
    fixture = {
        "schema_version": MANIFEST_SCHEMA,
        "routes": [],
        "rules": _rules(root),
    }
    target.parent.mkdir(parents=True, exist_ok=True)
    fd, tmp_name = tempfile.mkstemp(prefix=target.name + ".", dir=target.parent)
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8") as handle:
            json.dump(fixture, handle, sort_keys=True, separators=(",", ":"))
            handle.write("\n")
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(tmp_name, target)
    except BaseException:
        try:
            os.unlink(tmp_name)
        except OSError:
            pass
        raise


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
        raise FixtureError("policy-rule command could not start") from exc


def _table_aliases() -> dict[str, str]:
    aliases: dict[str, str] = {"main": "254", "default": "253", "local": "255"}
    for path in (Path("/etc/iproute2/rt_tables"), Path("/usr/lib/iproute2/rt_tables")):
        try:
            text = path.read_text(encoding="utf-8")
        except OSError:
            continue
        for raw in text.splitlines():
            line = raw.split("#", 1)[0].strip()
            fields = line.split()
            if len(fields) != 2 or not fields[0].isdigit():
                continue
            aliases[fields[1]] = fields[0]
    return aliases


def _normalized_table(value: object) -> str:
    text = str(value or "").strip()
    aliases = _table_aliases()
    return aliases.get(text, text)


def _observed_rule(value: object) -> dict[str, object] | None:
    if not isinstance(value, dict):
        return None
    try:
        priority = int(value.get("priority"))
    except (TypeError, ValueError):
        return None
    source = value.get("src", value.get("from", "all"))
    destination = value.get("dst", value.get("to", ""))
    try:
        source_text = _normalize_prefix(source) or "all"
        destination_text = _normalize_prefix(destination)
    except FixtureError:
        return None
    mark = str(value.get("fwmark") or "").strip()
    table = _normalized_table(value.get("table"))
    return {
        "family": "ipv4",
        "priority": priority,
        "source": source_text,
        "destination": destination_text,
        "mark": mark,
        "table": table,
    }


def _rule_present(rule: dict[str, object]) -> bool:
    result = _run(["ip", "-4", "-j", "rule", "show", "priority", str(rule["priority"])])
    if result.returncode != 0:
        raise FixtureError("policy-rule inspection failed")
    try:
        values = json.loads(result.stdout or "[]")
    except json.JSONDecodeError as exc:
        raise FixtureError("policy-rule inspection returned invalid JSON") from exc
    expected = dict(rule)
    expected["table"] = _normalized_table(rule["table"])
    return any(_observed_rule(item) == expected for item in values)


def _rule_command(action: str, rule: dict[str, object]) -> list[str]:
    command = ["ip", "-4", "rule", action, "priority", str(rule["priority"])]
    source = str(rule["source"])
    destination = str(rule["destination"])
    mark = str(rule["mark"])
    if source:
        command += ["from", source]
    if destination:
        command += ["to", destination]
    if mark:
        command += ["fwmark", mark]
    command += ["lookup", str(rule["table"])]
    return command


def _verify(path: Path, present: bool) -> None:
    rules = _rules(_load(path))
    observations = [_rule_present(item) for item in rules]
    if not observations or not all(value is present for value in observations):
        state = "present" if present else "absent"
        raise FixtureError(f"exact test-owned policy-rule fixture is not fully {state}")


def _apply(path: Path) -> None:
    rules = _rules(_load(path))
    if any(_rule_present(item) for item in rules):
        raise FixtureError("test-owned policy-rule fixture collides with existing state")
    applied: list[dict[str, object]] = []
    try:
        for rule in rules:
            result = _run(_rule_command("add", rule))
            if result.returncode != 0:
                raise FixtureError("failed to create exact test-owned policy-rule fixture")
            applied.append(rule)
        _verify(path, True)
    except BaseException:
        for rule in reversed(applied):
            _run(_rule_command("del", rule))
        raise


def _remove(path: Path) -> None:
    _verify(path, True)
    for rule in reversed(_rules(_load(path))):
        result = _run(_rule_command("del", rule))
        if result.returncode != 0:
            raise FixtureError("failed to remove exact test-owned policy-rule fixture")
    _verify(path, False)


def main(argv: list[str]) -> int:
    try:
        if len(argv) == 4 and argv[1] == "prepare":
            _write_fixture(Path(argv[2]), Path(argv[3]))
        elif len(argv) == 3 and argv[1] == "apply":
            _apply(Path(argv[2]))
        elif len(argv) == 3 and argv[1] == "verify-present":
            _verify(Path(argv[2]), True)
        elif len(argv) == 3 and argv[1] == "verify-absent":
            _verify(Path(argv[2]), False)
        elif len(argv) == 3 and argv[1] == "remove":
            _remove(Path(argv[2]))
        else:
            raise FixtureError(
                "usage: hosted_orphan_routing_fixture.py prepare AUTHORITY FIXTURE | "
                "<apply|verify-present|verify-absent|remove> FIXTURE"
            )
    except FixtureError as exc:
        print(f"hosted orphan routing fixture rejected: {exc}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
