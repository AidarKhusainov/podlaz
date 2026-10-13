#!/usr/bin/env python3
"""Fail-closed, guest-private networking snapshot for NM registration research.

Never print raw interface configuration, endpoints, routes or resolver values.
An observed state match grants no cleanup ownership.
"""
from __future__ import annotations

import json
import os
import subprocess
import sys
from pathlib import Path

COMMANDS = (
    ("link", ("ip", "-j", "link", "show", "dev", "podlaz0")),
    ("address", ("ip", "-j", "address", "show", "dev", "podlaz0")),
    ("routes", ("ip", "-j", "-4", "route", "show", "table", "all")),
    ("rules", ("ip", "-j", "-4", "rule", "show")),
    ("dns", ("resolvectl", "dns")),
    ("domains", ("resolvectl", "domain")),
    ("default-route", ("resolvectl", "default-route")),
    ("firewall", ("nft", "-j", "list", "ruleset")),
)


def strip_runtime_counters(value: object) -> object:
    """Only ignore nftables packet counters, never ownership or rule identity."""
    if isinstance(value, list):
        return [strip_runtime_counters(item) for item in value]
    if isinstance(value, dict):
        cleaned = {}
        for key, item in value.items():
            if key == "counter" and isinstance(item, dict):
                cleaned[key] = {
                    subkey: number for subkey, number in item.items()
                    if subkey not in {"packets", "bytes"}
                }
            else:
                cleaned[key] = strip_runtime_counters(item)
        return cleaned
    return value


def snapshot() -> dict[str, object]:
    result: dict[str, object] = {}
    for name, args in COMMANDS:
        proc = subprocess.run(args, capture_output=True, text=True, timeout=15, check=False)
        if proc.returncode:
            raise RuntimeError(f"inspection failed for {name}")
        value = proc.stdout
        if name in {"link", "address", "routes", "rules", "firewall"}:
            parsed = json.loads(value)
            result[name] = strip_runtime_counters(parsed) if name == "firewall" else parsed
        else:
            result[name] = value
    return result


def main() -> int:
    if len(sys.argv) != 3 or sys.argv[1] not in {"snapshot", "compare"}:
        return 2
    path = Path(sys.argv[2])
    if not path.is_absolute() or not str(path).startswith("/tmp/podlaz-hosted-synthetic-tun/"):
        raise RuntimeError("private guest snapshot path required")
    if sys.argv[1] == "snapshot":
        data = snapshot()
        descriptor = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
        with os.fdopen(descriptor, "w", encoding="utf-8") as handle:
            json.dump(data, handle, sort_keys=True)
        print("nm-registration-l3-baseline=captured")
        return 0
    if path.is_symlink() or path.stat().st_mode & 0o077:
        raise RuntimeError("unsafe snapshot permissions")
    expected = json.loads(path.read_text(encoding="utf-8"))
    observed = snapshot()
    modified = [key for key, value in expected.items() if observed.get(key) != value]
    if modified:
        print("nm-registration-l3-integrity=changed")
        print("nm-registration-changed-components=" + ",".join(modified))
        return 1
    print("nm-registration-l3-integrity=unchanged")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, ValueError, RuntimeError, subprocess.TimeoutExpired, json.JSONDecodeError):
        print("nm-registration-inspection=failed", file=sys.stderr)
        sys.exit(1)
