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


def normalize_host_uplink_route(item: object) -> object:
    """Only normalize this fixture's NM-managed default health-penalty metric."""
    if not isinstance(item, dict):
        return item
    result = dict(item)
    if (
        result.get("dev") == "host0"
        and result.get("dst", "default") == "default"
        and result.get("table", "main") in ("main", 254, "254")
        and result.get("metric") in (100, 20100)
    ):
        result["metric"] = "nm-health-penalty"
    return result


def normalized_ipv4_routes(routes: object) -> list[object]:
    if not isinstance(routes, list):
        raise ValueError("IPv4 route inventory is not an array")
    result = [normalize_host_uplink_route(route) for route in routes]
    return sorted(result, key=lambda item: json.dumps(item, sort_keys=True))


def snapshot() -> dict[str, object]:
    result: dict[str, object] = {}
    for name, args in COMMANDS:
        proc = subprocess.run(args, capture_output=True, text=True, timeout=15, check=False)
        if proc.returncode:
            raise RuntimeError(f"inspection failed for {name}")
        value = proc.stdout
        if name in {"link", "address", "routes", "rules", "firewall"}:
            parsed = json.loads(value)
            result[name] = (strip_runtime_counters(parsed) if name == "firewall" else normalized_ipv4_routes(parsed) if name == "routes" else parsed)
        else:
            result[name] = value
    return result


def connectivity_observation() -> None:
    """Publish only version, D-Bus connectivity enums, and route metrics."""
    import re

    def command(*args: str) -> str:
        result = subprocess.run(args, capture_output=True, text=True, timeout=15, check=True)
        return result.stdout.strip()

    version_raw = command("nmcli", "--version")
    match = re.search(r"\b(\d+\.\d+\.\d+)\b", version_raw)
    if not match:
        raise ValueError("NM version unavailable")
    version = match.group(1)

    def property_uint(path: str, interface: str, field: str) -> int:
        result = command(
            "busctl", "--system", "get-property",
            "org.freedesktop.NetworkManager", path, interface, field,
        )
        if not re.fullmatch(r"u [0-4]", result):
            raise ValueError(f"invalid connectivity enum for {field}")
        return int(result[2:])

    global_value = property_uint(
        "/org/freedesktop/NetworkManager",
        "org.freedesktop.NetworkManager",
        "Connectivity",
    )

    values: dict[str, int] = {}
    for name in ("podlaz0", "host0"):
        path = command("nmcli", "-g", "GENERAL.DBUS-PATH", "device", "show", name)
        if not re.fullmatch(r"/org/freedesktop/NetworkManager/Devices/\d+", path):
            raise ValueError("unexpected NetworkManager device path")
        values[name] = property_uint(path, "org.freedesktop.NetworkManager.Device", "Ip4Connectivity")

    # NetworkManager-visible candidates, not proof of its internal selected
    # best-default-route. No addresses, gateways or probe hosts are logged.
    metrics: dict[str, str] = {}
    for name in ("podlaz0", "host0"):
        routes = command("nmcli", "-g", "IP4.ROUTE", "device", "show", name)
        numbers = sorted({int(x) for x in re.findall(r"\bmt\s*=\s*(\d+)", routes)})
        metrics[name] = ",".join(str(x) for x in numbers) if numbers else "unknown"
    print(
        f"nm-connectivity-evidence=version_{version.replace('.', '_')}"
        f" global_{global_value} tun_ipv4_{values['podlaz0']}"
        f" uplink_ipv4_{values['host0']}"
        f" tun_nm_metrics_{metrics['podlaz0']}"
        f" uplink_nm_metrics_{metrics['host0']}"
    )



def vpn_diagnose() -> None:
    """Classify guest NM journal without exposing sensitive log messages."""
    import re

    result = subprocess.run(
        ("journalctl", "--no-pager", "-o", "cat", "-u", "NetworkManager", "-n", "600"),
        capture_output=True, text=True, timeout=15, check=True,
    )
    journal = result.stdout.lower()
    patterns = (
        ("source_connection_missing", r"could not find source connection"),
        ("vpn_plugin_missing", r"vpn plugin.*(?:missing|not found|not available)"),
        ("vpn_service_start_failed", r"vpn.*(?:failed to start|service start failed|cannot start)"),
        ("vpn_service_timeout", r"vpn.*(?:timeout|timed out)"),
        ("vpn_dbus_denied", r"vpn.*(?:access denied|permission denied)"),
        ("vpn_ip_config_invalid", r"vpn.*(?:invalid ip|ip configuration failed)"),
        ("vpn_connect_request_failed", r"vpn.*(?:connection activation failed|connect request failed)"),
        ("vpn_child_exit", r"vpn.*(?:child.*exited|process.*exited|service.*exited)"),
    )
    labels = [name for name, pattern in patterns if re.search(pattern, journal)]
    print("nm-vpn-journal-service-seen=" + ("yes" if "podlazresearch" in journal else "no"))
    print("nm-vpn-journal-class=" + ("_".join(labels) if labels else "unclassified"))


def main() -> int:
    if len(sys.argv) == 2 and sys.argv[1] == "vpn-diagnose":
        vpn_diagnose()
        return 0
    if len(sys.argv) == 2 and sys.argv[1] == "observe":
        connectivity_observation()
        return 0
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
    except (OSError, ValueError, RuntimeError, subprocess.TimeoutExpired, subprocess.CalledProcessError, json.JSONDecodeError):
        print("nm-registration-inspection=failed", file=sys.stderr)
        sys.exit(1)
