#!/usr/bin/env python3
"""Read-only, redacted NM scope and protected packet-path evidence in a guest.

All raw NetworkManager routes, addresses, endpoint details and journal lines
remain inside the disposable guest. Only fixed-class labels and metrics leave it.
"""

from __future__ import annotations

import argparse
import re
import subprocess
import sys
from pathlib import Path

DEFAULT_ROUTE = re.compile(r"\bdst\s*=\s*0\.0\.0\.0/0(?:\s*,|\s*$)")
METRIC = re.compile(r"\bmt\s*=\s*(\d+)")
DEVICE = re.compile(r"\bdev\s+(\S+)")


def command(*args: str, timeout: int = 25) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        args,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        timeout=timeout,
        check=False,
    )


def nm_default_metric(device: str) -> str:
    result = command("nmcli", "-g", "IP4.ROUTE", "device", "show", device)
    if result.returncode:
        return "unavailable"
    routes = [line for line in result.stdout.splitlines() if DEFAULT_ROUTE.search(line)]
    if not routes:
        return "none"
    metrics = [match.group(1) for line in routes if (match := METRIC.search(line))]
    if len(routes) != 1 or len(metrics) != 1:
        return "ambiguous"
    return metrics[0]


def concheck_decisions() -> None:
    result = command(
        "journalctl", "-u", "NetworkManager.service", "--no-pager",
        "-o", "cat", "-n", "2000", timeout=20,
    )
    if result.returncode:
        print("nm-concheck-log=unavailable")
        return
    lines = result.stdout.splitlines()
    matches = sum("no global route configured" in line.lower() for line in lines)
    print(f"nm-concheck-no-global-route-count={matches}")
    print(f"nm-concheck-domain-lines={sum('concheck' in line.lower() for line in lines)}")


def protected_packet_path(uplink: str) -> None:
    route = command("ip", "-4", "route", "get", "198.51.100.1")
    if route.returncode or not (match := DEVICE.search(route.stdout)) or match.group(1) != "podlaz0":
        raise RuntimeError("unmarked IPv4 policy route does not select TUN")

    statistics = Path("/sys/class/net/podlaz0/statistics/tx_packets")
    before = int(statistics.read_text(encoding="ascii").strip())
    tunneled = command(
        "curl", "-4", "--noproxy", "*", "--interface", "if!podlaz0",
        "--connect-timeout", "10", "--max-time", "30",
        "-fsS", "-o", "/dev/null", "https://example.com/", timeout=35,
    )
    if tunneled.returncode:
        raise RuntimeError("TUN-bound HTTPS request failed")
    after = int(statistics.read_text(encoding="ascii").strip())
    if after <= before:
        raise RuntimeError("successful HTTPS generated no TUN transmit packets")

    # A bound, unmarked client must not obtain working direct HTTPS via host0.
    # Failure alone does not attribute the responsible nftables rule.
    direct = command(
        "curl", "-4", "--noproxy", "*", "--interface", f"if!{uplink}",
        "--connect-timeout", "5", "--max-time", "12",
        "-fsS", "-o", "/dev/null", "https://example.com/", timeout=17,
    )
    if direct.returncode == 0:
        raise RuntimeError("unmarked uplink-bound HTTPS unexpectedly succeeded")
    print("nm-packet-path=tun-confirmed")
    print("nm-uplink-direct-https=blocked-or-unreachable")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--uplink", choices=("host0",), required=True)
    args = parser.parse_args()
    for device, label in (("podlaz0", "tun"), (args.uplink, "uplink")):
        print(f"nm-visible-default-route-{label}-metric={nm_default_metric(device)}")
    concheck_decisions()
    protected_packet_path(args.uplink)
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, RuntimeError, ValueError, subprocess.TimeoutExpired) as exc:
        # Never print subprocess stderr/stdout or network inventory.
        print(f"nm-packet-path-error={type(exc).__name__}", file=sys.stderr)
        sys.exit(1)
