#!/usr/bin/env python3
"""Single-writer NM-owned TUN feasibility in a disposable nspawn guest only.

This is NOT a production backend. Positive traffic alone is insufficient:
the pinned Xray must not replace or down NM's exact TUN when it exits.
"""
from __future__ import annotations

import fcntl
import json
import os
import re
import struct
from pathlib import Path
import subprocess
import sys
import time
import uuid

from nm_vpn_l3_scenario_guest import (
    ROOT, TABLE, _is_synthetic_guest, _underlay_evidence, _xray_marked_config,
    _nft_protect, _start_underlay, _verify_paths, _prove_ipv6_no_leak,
    _prove_physical_drop, _natural_full, verify_owned_underlay,
    _numeric_table, ip_json, run,
)
from nm_vpn_underlay_guest import select_underlay

NAME = "podlaz-guest-nm-owned-tun"
CURRENT_STEP = "preflight"


def stage(step: str) -> None:
    global CURRENT_STEP
    CURRENT_STEP = step


def verify_attached_tun(nm_index: int, actual_index: int, running: bool) -> None:
    if type(nm_index) is not int or nm_index <= 0:
        raise ValueError("invalid NM-owned TUN identity")
    if type(actual_index) is not int or actual_index != nm_index:
        raise ValueError("Xray changed the NM-owned TUN identity")
    if not running:
        raise ValueError("Xray not running after TUN attachment")


def verify_nm_survives_xray_exit(active: bool, link_up: bool) -> None:
    if type(active) is not bool or not active:
        raise ValueError("NM TUN deactivated after Xray exit")
    if type(link_up) is not bool or not link_up:
        raise ValueError("NM-owned TUN down after Xray exit")


def _profile_present(profile_id: str, *, active: bool = False) -> bool:
    args = ("nmcli", "-t", "-f", "UUID", "connection", "show")
    if active:
        args += ("--active",)
    rows = run(*args, output=True).splitlines()
    return rows.count(profile_id) == 1


def _link() -> dict:
    links = ip_json("-d", "link", "show", "dev", "podlaz0")
    if len(links) != 1 or links[0].get("ifname") != "podlaz0":
        raise ValueError("NM TUN link identity unavailable")
    if links[0].get("linkinfo", {}).get("info_kind") != "tun":
        raise ValueError("link is not a TUN")
    return links[0]


def _nm_setup(profile_id: str) -> int:
    # All addresses, DNS, routes and link creation belong to NM, never Xray
    # or the Podlaz product daemon. This profile is ephemeral in the guest.
    run("nmcli", "connection", "add", "save", "no", "type", "tun",
        "con-name", NAME, "ifname", "podlaz0",
        "connection.uuid", profile_id, "connection.autoconnect", "no",
        "tun.mode", "tun", "tun.pi", "no",
        "ipv4.method", "manual", "ipv4.addresses", "198.18.0.17/32",
        "ipv4.routes", "0.0.0.0/0", "ipv4.route-metric", "10",
        "ipv4.dns", "1.1.1.1", "ipv4.dns-priority", "-30000",
        "ipv4.dns-search", "~.", "ipv6.method", "disabled")
    if not _profile_present(profile_id):
        raise ValueError("NM ephemeral TUN UUID absent")
    run("nmcli", "-w", "45", "connection", "up", profile_id, timeout=50)
    if not _profile_present(profile_id, active=True):
        raise ValueError("NM TUN activation did not complete")
    link = _link()
    if type(link.get("ifindex")) is not int:
        raise ValueError("NM TUN ifindex absent")
    addresses = ip_json("address", "show", "dev", "podlaz0")
    if len(addresses) != 1 or not any(
        addr.get("family") == "inet" and addr.get("local") == "198.18.0.17"
        for addr in addresses[0].get("addr_info", [])
    ):
        raise ValueError("NM did not configure the exact TUN L3 address")
    return link["ifindex"]


def _cleanup_underlay(plan: object) -> None:
    routes = ip_json("-4", "route", "show", "table", str(plan.table))
    rules = [rule for rule in ip_json("-4", "rule", "show")
             if rule.get("priority") == plan.rule_priority or
             _numeric_table(rule.get("table", rule.get("lookup", -1))) == plan.table]
    rule_ok, default_ok, connected_ok = verify_owned_underlay(plan, routes, rules)
    if rule_ok:
        run("ip", "-4", "rule", "del", "pref", str(plan.rule_priority),
            "fwmark", str(plan.tunnel_mark), "lookup", str(plan.table))
    if default_ok:
        run("ip", "-4", "route", "del", "default",
            "via", plan.default_route[1], "dev", plan.default_route[2],
            "table", str(plan.table))
    if connected_ok:
        run("ip", "-4", "route", "del", plan.connected_route[0],
            "dev", plan.connected_route[1], "table", str(plan.table))
    if ip_json("-4", "route", "show", "table", str(plan.table)):
        raise ValueError("NM TUN research underlay did not clean exactly")


def inspect_nm_tun_netlink_events(events: str) -> int:
    """Reject administrative DOWN, deletion or ambiguous identity changes.

    iproute2's `state DOWN` is *operstate*, not the administrative IFF_UP
    flag. Closing the last TUN packet FD may remove carrier even while the
    independent NetworkManager owner keeps IFF_UP. That event is not
    authorization to change the link's administrative state or L3.
    The input is strictly private and bounded; no raw event is exported.
    """
    if not isinstance(events, str) or len(events) > 65536:
        raise ValueError("invalid bounded netlink observation")
    carrier_loss = 0
    for line in events.splitlines():
        if not line.strip() or line.lstrip().startswith(("link/", "    ")):
            continue
        if "Deleted " in line:
            raise ValueError("NM-owned TUN deleted during Xray lifecycle")
        match = re.fullmatch(
            r"\\d+:\\s+podlaz0(?:@[^: ]+)?\\s*:\\s*<([^>]*)>.*",
            line.strip(),
        )
        if match is None:
            raise ValueError("ambiguous NM-owned TUN netlink event")
        flags = match.group(1).split(",")
        if "UP" not in flags:
            raise ValueError("NM-owned TUN administrative IFF_UP cleared")
        if "state DOWN" in line:
            carrier_loss += 1
    return carrier_loss


def start_externally_managed_xray(
    config_file: Path, index: int, output: object, errors: object
) -> subprocess.Popen:
    """Hand the exact existing NM TUN packet descriptor to a guest-only Xray.

    Open the TUN as root in the nspawn process, verify the same link identity,
    and transfer only this FD to the Xray child. Neither side may invent a
    substitute link or change the NetworkManager-managed interface settings.
    """
    candidate = Path("/run/podlaz-synthetic-xray/nm-owned-external-xray")
    if not candidate.is_file() or candidate.is_symlink() or not os.access(candidate, os.X_OK):
        raise ValueError("missing isolated external-TUN Xray test binary")
    if type(index) is not int or index <= 0 or _link().get("ifindex") != index:
        raise ValueError("stale NM-owned TUN identity before FD handoff")
    fd = os.open("/dev/net/tun", os.O_RDWR | os.O_CLOEXEC | os.O_NOFOLLOW)
    try:
        # Linux amd64 ifreq: IFNAMSIZ=16, flags IFF_TUN|IFF_NO_PI.
        # TUNSETIFF attaches an FD to NM's existing persistent TUN.
        request = bytearray(struct.pack("16sH22x", b"podlaz0", 0x0001 | 0x1000))
        fcntl.ioctl(fd, 0x400454ca, request, True)
        if request[:16].split(b"\x00", 1)[0] != b"podlaz0":
            raise ValueError("external FD attached to another interface")
        if _link().get("ifindex") != index:
            raise ValueError("NM-owned TUN changed during FD handoff")
        environment = os.environ.copy()
        environment["PODLAZ_NM_GUEST_TUN_FD"] = str(fd)
        environment["PODLAZ_NM_GUEST_TUN_IFINDEX"] = str(index)
        return subprocess.Popen(
            (str(candidate), "run", "-c", str(config_file)),
            stdout=output, stderr=errors, pass_fds=(fd,), env=environment,
        )
    finally:
        os.close(fd)


def run_guest(config_file: Path) -> None:
    _is_synthetic_guest()
    if run("nmcli", "-t", "-f", "NAME", "connection", "show",
           output=True).splitlines().count(NAME):
        raise ValueError("NM profile name collision")
    plan = select_underlay(_underlay_evidence(), 10)
    _xray_marked_config(config_file, plan.tunnel_mark)
    profile_id = str(uuid.uuid4())
    xray = None
    barrier = False
    underlay = False
    profile = False
    with (ROOT / "nm-owned-xray.stdout").open("w") as output, \
         (ROOT / "nm-owned-xray.stderr").open("w") as errors:
        try:
            stage("barrier-armed")
            _nft_protect(plan.tunnel_mark)
            barrier = True
            stage("underlay-allocated")
            _start_underlay(plan)
            underlay = True
            stage("nm-created")
            profile = True
            nm_index = _nm_setup(profile_id)
            print("nm-owned-link-and-l3=pass", flush=True)
            stage("xray-attaching")
            xray = start_externally_managed_xray(
                config_file, nm_index, output, errors)
            # This first gate tests real pinned-Xray TUNSETIFF attachment,
            # not a hypothetical external-fd API.
            for _ in range(75):
                if xray.poll() is not None:
                    raise ValueError("packaged Xray rejected NM TUN attachment")
                verify_attached_tun(nm_index, _link()["ifindex"], True)
                if _link().get("flags") and "UP" in _link()["flags"]:
                    break
                time.sleep(0.2)
            else:
                raise ValueError("Xray did not bring the NM-owned TUN up")
            print("nm-owned-xray-attached=pass", flush=True)
            stage("real-traffic")
            _verify_paths(plan.tunnel_mark)
            _prove_physical_drop()
            _prove_ipv6_no_leak()
            print("nm-owned-protected-traffic=pass", flush=True)
            stage("natural-global-full")
            _natural_full(xray)
            print("nm-owned-natural-full=pass", flush=True)
            stage("xray-release")
            # Catch transient link-down transitions, not only NM's later
            # possibly recovered UP state. This monitor is read-only; all
            # observed kernel inventory remains inside the disposable guest.
            monitor_path = ROOT / "nm-owned-link-events.private"
            with monitor_path.open("w") as events, \
                 (ROOT / "nm-owned-link-monitor.stderr").open("w") as monitor_errors:
                monitor = subprocess.Popen(
                    ("ip", "monitor", "link", "dev", "podlaz0"),
                    stdout=events, stderr=monitor_errors)
                try:
                    time.sleep(0.5)
                    if monitor.poll() is not None:
                        raise ValueError("netlink lifecycle observer unavailable")
                    xray.terminate()
                    xray.wait(timeout=12)
                    time.sleep(1)
                finally:
                    if monitor.poll() is None:
                        monitor.terminate()
                        monitor.wait(timeout=5)
            observed = monitor_path.read_text(encoding="utf-8")
            try:
                carrier_loss = inspect_nm_tun_netlink_events(observed)
            except ValueError:
                print("nm-owned-admin-lifecycle-violation=observed", flush=True)
                raise
            if carrier_loss:
                # Only the closed category may be exported; raw private
                # link flags/addresses and netlink payload never leave guest.
                print("nm-owned-carrier-only-down=observed", flush=True)
            # IFF_UP remained set in every event, but a post-stop identity,
            # L3 and NM active-profile check must still complete.
            state = _profile_present(profile_id, active=True)
            link = _link()
            if link["ifindex"] != nm_index:
                raise ValueError("NM TUN ifindex replaced during Xray exit")
            verify_nm_survives_xray_exit(state, "UP" in link.get("flags", []))
            after_addresses = ip_json("address", "show", "dev", "podlaz0")
            if len(after_addresses) != 1 or not any(
                entry.get("family") == "inet" and entry.get("local") == "198.18.0.17"
                for entry in after_addresses[0].get("addr_info", [])
            ):
                raise ValueError("NM-owned TUN L3 changed after Xray exit")
            _prove_physical_drop()
            _prove_ipv6_no_leak()
            print("nm-owned-nm-post-xray-active=pass", flush=True)
        except Exception:
            print("nm-owned-failure-step=" + CURRENT_STEP, flush=True)
            raise
        finally:
            stage("cleanup-xray")
            if xray is not None and xray.poll() is None:
                xray.terminate()
                xray.wait(timeout=12)
            stage("cleanup-nm")
            if profile:
                if _profile_present(profile_id, active=True):
                    run("nmcli", "-w", "25", "connection", "down",
                        profile_id, timeout=30)
                if _profile_present(profile_id):
                    run("nmcli", "connection", "delete", profile_id)
            if underlay:
                stage("cleanup-underlay")
                _cleanup_underlay(plan)
            if barrier:
                stage("cleanup-barrier")
                if _profile_present(profile_id, active=True):
                    raise ValueError("NM remains active; barrier retained")
                run("nft", "delete", "table", "inet", TABLE)


def main() -> None:
    if len(sys.argv) != 2:
        raise SystemExit("usage: nm_owned_tun_guest.py GENERATED_CONFIG")
    try:
        run_guest(Path(sys.argv[1]))
    except (OSError, RuntimeError, ValueError, subprocess.SubprocessError,
            KeyError, TypeError, json.JSONDecodeError):
        print("nm-owned-result=fail", flush=True)
        raise SystemExit(1)
    print("nm-owned-result=pass", flush=True)


if __name__ == "__main__":
    main()
