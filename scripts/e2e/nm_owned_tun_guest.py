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
from nm_owned_tun_grouped_guest import (FIRST, SECOND, TARGET_PORT, verify_composed_grouped)
from nm_owned_tun_fd_contract import (
    inherited_fd_environment, verify_tun_fd_identity, verify_xray_config,
)

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
    if Path("/sys/class/net/podlaz0").exists():
        raise ValueError("foreign TUN/link name collision")
    # All addresses, DNS, routes and link creation belong to NM, never Xray
    # or the Podlaz product daemon. This profile is ephemeral in the guest.
    run("nmcli", "connection", "add", "save", "yes", "type", "tun",
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


def _probe_foreign_link_collision() -> None:
    """Do not adopt/replace a conflicting foreign link of the same name."""
    if Path("/sys/class/net/podlaz0").exists():
        raise ValueError("preexisting foreign link cannot be adopted")
    run("ip", "link", "add", "name", "podlaz0", "type", "dummy")
    foreign = ip_json("-d", "link", "show", "dev", "podlaz0")
    if len(foreign) != 1 or foreign[0].get("linkinfo", {}).get("info_kind") != "dummy":
        raise ValueError("collision fixture identity unknown")
    identity = foreign[0].get("ifindex")
    if type(identity) is not int or identity <= 0:
        raise ValueError("collision fixture ifindex unavailable")
    try:
        probe_uuid = str(uuid.uuid4())
        try:
            _nm_setup(probe_uuid)
        except ValueError as exc:
            if "foreign TUN/link name collision" not in str(exc):
                raise
        else:
            raise ValueError("foreign link silently adopted by NM")
        if _profile_present(probe_uuid):
            raise ValueError("foreign link collision left an NM connection")
        after = ip_json("-d", "link", "show", "dev", "podlaz0")
        if (len(after) != 1 or after[0].get("ifindex") != identity
                or after[0].get("linkinfo", {}).get("info_kind") != "dummy"):
            raise ValueError("foreign collision identity not preserved")
    finally:
        # Delete only the exact synthetic link created by THIS test.
        current = ip_json("-d", "link", "show", "dev", "podlaz0")
        if (len(current) != 1 or current[0].get("ifindex") != identity
                or current[0].get("linkinfo", {}).get("info_kind") != "dummy"):
            raise ValueError("refusing cleanup of non-owned collision link")
        run("ip", "link", "del", "dev", "podlaz0")


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
            r"\d+:\s+podlaz0(?:@[^: ]+)?\s*:\s*<([^>]*)>.*",
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
    pre = _link()
    verify_xray_config(config_file, ifname=pre["ifname"], mtu=pre["mtu"])
    fd = os.open("/dev/net/tun", os.O_RDWR | os.O_CLOEXEC | os.O_NOFOLLOW)
    try:
        # Linux amd64 ifreq: IFNAMSIZ=16, flags IFF_TUN|IFF_NO_PI.
        # TUNSETIFF attaches an FD to NM's existing persistent TUN.
        request = bytearray(struct.pack("16sH22x", b"podlaz0", 0x0001 | 0x1000))
        fcntl.ioctl(fd, 0x400454ca, request, True)
        if request[:16].split(b"\x00", 1)[0] != b"podlaz0":
            raise ValueError("external FD attached to another interface")
        inspected = bytearray(40)
        fcntl.ioctl(fd, 0x800454d2, inspected, True)  # TUNGETIFF
        fd_name = inspected[:16].split(bytes([0]), 1)[0].decode("ascii")
        fd_flags = struct.unpack_from("H", inspected, 16)[0]
        verify_tun_fd_identity(
            name=fd_name, flags=fd_flags, expected_name="podlaz0",
            before=pre, after=_link(), expected_index=index,
            expected_mtu=pre["mtu"],
        )
        # Official upstream Linux openFromEnv contract, NOT the old PoC patch.
        environment = inherited_fd_environment(fd)
        environment["PODLAZ_RESEARCH_TUN_IFINDEX"] = str(index)
        environment["PODLAZ_RESEARCH_TUN_MTU"] = str(pre["mtu"])
        return subprocess.Popen(
            (str(candidate), "run", "-c", str(config_file)),
            stdout=output, stderr=errors, pass_fds=(fd,), env=environment,
        )
    finally:
        os.close(fd)


def _assert_nm_owned_link(profile_id: str) -> int:
    if not _profile_present(profile_id, active=True):
        raise ValueError("NM-owned connection not active")
    link = _link()
    if "UP" not in link.get("flags", []) or type(link.get("ifindex")) is not int:
        raise ValueError("NM-owned link not administratively UP")
    addresses = ip_json("address", "show", "dev", "podlaz0")
    if len(addresses) != 1 or not any(
        addr.get("family") == "inet" and addr.get("local") == "198.18.0.17"
        for addr in addresses[0].get("addr_info", [])
    ):
        raise ValueError("NM-owned TUN lost exact L3")
    return link["ifindex"]


def _prove_invalid_fd_fails_closed(
    config_file: Path, profile_id: str, index: int, output: object, errors: object
) -> None:
    """Wrong descriptor must fail rather than falling back to opening a TUN."""
    invalid = os.open("/dev/null", os.O_RDONLY | os.O_CLOEXEC | os.O_NOFOLLOW)
    try:
        environment = inherited_fd_environment(invalid)
        process = subprocess.Popen(
            ("/run/podlaz-synthetic-xray/nm-owned-external-xray",
             "run", "-c", str(config_file)),
            stdout=output, stderr=errors, pass_fds=(invalid,), env=environment,
        )
        try:
            code = process.wait(timeout=20)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=10)
            raise ValueError("Xray did not reject invalid inherited TUN FD")
        if code == 0:
            raise ValueError("Xray accepted non-TUN inherited descriptor")
    finally:
        os.close(invalid)
    if _assert_nm_owned_link(profile_id) != index:
        raise ValueError("invalid FD fallback changed NM identity")

    # A numeric but closed descriptor must also be fatal. Leaving the
    # variable unset would select upstream's self-managed TUN mode and is
    # expressly forbidden by the privileged handoff contract.
    environment = inherited_fd_environment(1 << 29)
    lost = subprocess.Popen(
        ("/run/podlaz-synthetic-xray/nm-owned-external-xray",
         "run", "-c", str(config_file)),
        stdout=output, stderr=errors, env=environment, close_fds=True,
    )
    try:
        code = lost.wait(timeout=20)
    except subprocess.TimeoutExpired:
        lost.kill()
        lost.wait(timeout=10)
        raise ValueError("Xray did not reject missing inherited descriptor")
    if code == 0 or _assert_nm_owned_link(profile_id) != index:
        raise ValueError("lost external TUN FD was not fail-closed")
    print("nm-owned-lost-fd-rejected=pass", flush=True)


def verify_live_nm_restart(
    before_pid: str, after_pid: str, exact_index: int, actual_index: int,
    exact_mtu: int, actual_mtu: int, xray_running: bool,
) -> None:
    if not xray_running:
        raise ValueError("Xray exited during live NM restart")
    if (not before_pid.isdecimal() or int(before_pid) <= 0
            or not after_pid.isdecimal() or int(after_pid) <= 0
            or before_pid == after_pid):
        raise ValueError("NetworkManager PID not replaced")
    if (type(actual_index) is not int or actual_index != exact_index
            or type(actual_mtu) is not int or actual_mtu != exact_mtu):
        raise ValueError("live NM restart replaced the attached TUN identity or MTU")


def _exercise_live_nm_restart(
    xray: subprocess.Popen, profile_id: str, index: int, mark: int,
) -> None:
    """Restart NetworkManager while Xray still owns its inherited packet FD.

    A recreated device under the same name is NOT a valid reattachment; no
    second FD may be opened during this phase, and the barrier stays armed.
    """
    stage("live-nm-restart")
    if xray.poll() is not None or _assert_nm_owned_link(profile_id) != index:
        raise ValueError("live NM restart requires original active Xray/TUN")
    before_mtu = _link().get("mtu")
    if type(before_mtu) is not int or before_mtu <= 0:
        raise ValueError("unknown pre-restart NM TUN MTU")
    before_pid = run("systemctl", "show", "-P", "MainPID",
                     "NetworkManager.service", output=True).strip()
    monitor_path = ROOT / "nm-owned-live-nm-link-events.private"
    with (monitor_path.open("w") as events,
          (ROOT / "nm-owned-live-nm-monitor.stderr").open("w") as monitor_errors):
        monitor = subprocess.Popen(
            ("ip", "monitor", "link", "dev", "podlaz0"),
            stdout=events, stderr=monitor_errors)
        try:
            time.sleep(0.5)
            if monitor.poll() is not None:
                raise ValueError("live NM netlink observer unavailable")
            run("systemctl", "restart", "NetworkManager", timeout=50)
            # Permit bounded NM service initialization, never link recreation.
            for _ in range(80):
                if xray.poll() is not None:
                    raise ValueError("Xray exited while NM restarted")
                current = _link()
                if (current.get("ifindex") != index
                        or current.get("mtu") != before_mtu
                        or "UP" not in current.get("flags", [])):
                    raise ValueError("live NM restart replaced the exact TUN")
                if _profile_present(profile_id, active=True):
                    break
                time.sleep(0.5)
            else:
                raise ValueError("NM did not retain the original active UUID")
            time.sleep(0.5)
        finally:
            if monitor.poll() is None:
                monitor.terminate()
                monitor.wait(timeout=5)
    inspect_nm_tun_netlink_events(monitor_path.read_text(encoding="utf-8"))
    after_pid = run("systemctl", "show", "-P", "MainPID",
                    "NetworkManager.service", output=True).strip()
    current = _link()
    verify_live_nm_restart(before_pid, after_pid, index, current.get("ifindex"),
                           before_mtu, current.get("mtu"), xray.poll() is None)
    if _assert_nm_owned_link(profile_id) != index:
        raise ValueError("live NM restart lost exact L3/UUID")
    _verify_paths(mark)
    _prove_physical_drop()
    _prove_ipv6_no_leak()
    # Fresh natural connectivity period after the real NM daemon replacement.
    _natural_full(xray)
    print("nm-owned-live-nm-restart=pass", flush=True)


def _exercise_crash_restart(
    xray: subprocess.Popen, config_file: Path, profile_id: str,
    index: int, mark: int, output: object, errors: object,
) -> tuple[subprocess.Popen, int]:
    """Deliberate Xray SIGKILL and NM daemon restart under an armed barrier."""
    stage("xray-crash")
    xray.kill()
    if xray.wait(timeout=12) != -9:
        raise ValueError("Xray SIGKILL did not terminate with signal")
    if _assert_nm_owned_link(profile_id) != index:
        raise ValueError("NM TUN replaced on Xray crash")
    _prove_physical_drop()
    _prove_ipv6_no_leak()
    stage("nm-restart")
    # Saved ONLY in the disposable guest and explicitly removed on teardown.
    before_pid = run(
        "systemctl", "show", "--property=MainPID", "--value",
        "NetworkManager.service", output=True).strip()
    if not before_pid.isdecimal() or int(before_pid) <= 0:
        raise ValueError("NM PID unavailable before restart")
    run("systemctl", "restart", "NetworkManager", timeout=50)
    after_pid = run(
        "systemctl", "show", "--property=MainPID", "--value",
        "NetworkManager.service", output=True).strip()
    if (not after_pid.isdecimal() or int(after_pid) <= 0
            or after_pid == before_pid):
        raise ValueError("NM daemon PID was not replaced")
    if not _profile_present(profile_id):
        raise ValueError("NM-owned UUID lost after NM restart")
    if not _profile_present(profile_id, active=True):
        # Explicit reactivation is not evidence for automatic product recovery.
        run("nmcli", "-w", "45", "connection", "up", profile_id, timeout=50)
    next_index = _assert_nm_owned_link(profile_id)
    _prove_physical_drop()
    _prove_ipv6_no_leak()
    stage("xray-restart")
    restarted = start_externally_managed_xray(
        config_file, next_index, output, errors)
    try:
        for _ in range(75):
            if restarted.poll() is not None:
                raise ValueError("Xray refused reattached NM TUN")
            if _assert_nm_owned_link(profile_id) == next_index:
                break
            time.sleep(0.2)
        else:
            raise ValueError("NM TUN generation not restored")
        _verify_paths(mark)
        _prove_physical_drop()
        _prove_ipv6_no_leak()
    except Exception:
        restarted.kill()
        restarted.wait(timeout=12)
        raise
    print("nm-owned-fault-recovery=pass", flush=True)
    return restarted, next_index


def _prove_child_in_process_identity_rejection(
    config_file: Path, profile_id: str, index: int,
    output: object, errors: object,
) -> None:
    """Actual patched Xray must fail before packet startup for wrong owner IDs."""
    for dimension in ("ifindex", "mtu"):
        if _assert_nm_owned_link(profile_id) != index:
            raise ValueError("NM identity lost before child collision probe")
        original = _link()
        fd = os.open("/dev/net/tun", os.O_RDWR | os.O_CLOEXEC | os.O_NOFOLLOW)
        try:
            request = bytearray(struct.pack("16sH22x", b"podlaz0", 0x0001 | 0x1000))
            fcntl.ioctl(fd, 0x400454ca, request, True)
            if request[:16].split(bytes([0]), 1)[0] != b"podlaz0":
                raise ValueError("identity test FD not attached to NM TUN")
            environment = inherited_fd_environment(fd)
            environment["PODLAZ_RESEARCH_TUN_IFINDEX"] = str(
                index + (1 if dimension == "ifindex" else 0))
            environment["PODLAZ_RESEARCH_TUN_MTU"] = str(
                original["mtu"] + (1 if dimension == "mtu" else 0))
            child = subprocess.Popen(
                ("/run/podlaz-synthetic-xray/nm-owned-external-xray",
                 "run", "-c", str(config_file)),
                stdout=output, stderr=errors, pass_fds=(fd,), env=environment,
            )
            try:
                status = child.wait(timeout=25)
            except subprocess.TimeoutExpired:
                child.kill()
                child.wait(timeout=12)
                raise ValueError("Xray accepted mismatched in-process identity")
            if status == 0:
                raise ValueError("Xray accepted wrong expected TUN identity")
        finally:
            os.close(fd)
        if _assert_nm_owned_link(profile_id) != index or _link()["mtu"] != original["mtu"]:
            raise ValueError("in-process identity rejection damaged NM link")
        _prove_physical_drop()
        _prove_ipv6_no_leak()
    print("nm-owned-child-identity-rejected=pass", flush=True)


def _grouped_peer_counters(mark: int, transport_port: int) -> None:
    """Count exact first and second provider transport peer packets under the barrier."""
    for address, label in ((FIRST, "nm_owned_peer_a"), (SECOND, "nm_owned_peer_b")):
        run("nft", "add", "rule", "inet", TABLE, "egress",
            "ip", "daddr", address, "tcp", "dport", str(transport_port),
            "meta", "mark", str(mark), "counter", "comment", label)


def _qualified_peer_packets() -> tuple[bool, bool]:
    snapshot = json.loads(run("nft", "-j", "list", "chain", "inet", TABLE, "egress",
                              output=True))
    if not isinstance(snapshot, dict) or not isinstance(snapshot.get("nftables"), list):
        raise ValueError("missing protected egress counter inventory")
    counted: dict[str, int] = {}
    for entry in snapshot["nftables"]:
        if not isinstance(entry, dict) or not isinstance(entry.get("rule"), dict):
            continue
        expressions = entry["rule"].get("expr")
        if not isinstance(expressions, list):
            raise ValueError("malformed exact peer counter")
        # nft -j exposes a rule's comment on the rule object, NOT in expr.
        name = entry["rule"].get("comment")
        if name not in ("nm_owned_peer_a", "nm_owned_peer_b"):
            continue
        counters = [expr["counter"].get("packets") for expr in expressions
                    if isinstance(expr, dict) and isinstance(expr.get("counter"), dict)]
        if name in counted or len(counters) != 1 or type(counters[0]) is not int:
            raise ValueError("ambiguous exact peer counter")
        counted[name] = counters[0]
    if set(counted) != {"nm_owned_peer_a", "nm_owned_peer_b"}:
        raise ValueError("missing exact independent peer counters")
    return counted["nm_owned_peer_a"] > 0, counted["nm_owned_peer_b"] > 0


def run_guest(config_file: Path, *, grouped: bool = False) -> None:
    _is_synthetic_guest()
    if run("nmcli", "-t", "-f", "NAME", "connection", "show",
           output=True).splitlines().count(NAME):
        raise ValueError("NM profile name collision")
    plan = select_underlay(_underlay_evidence(), 10)
    _xray_marked_config(config_file, plan.tunnel_mark)
    transport_port = None
    if grouped:
        if config_file.is_symlink() or not config_file.is_file() or config_file.stat().st_mode & 0o077:
            raise ValueError("unqualified private grouped generated Xray config")
        transport_port = verify_composed_grouped(
            json.loads(config_file.read_text(encoding="utf-8")), plan.tunnel_mark)
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
            if grouped:
                _grouped_peer_counters(plan.tunnel_mark, transport_port)
            stage("underlay-allocated")
            _start_underlay(plan)
            underlay = True
            stage("foreign-link-collision")
            _probe_foreign_link_collision()
            print("nm-owned-foreign-link-collision=pass", flush=True)
            stage("nm-created")
            profile = True
            nm_index = _nm_setup(profile_id)
            print("nm-owned-link-and-l3=pass", flush=True)
            stage("fd-negative")
            _prove_invalid_fd_fails_closed(config_file, profile_id, nm_index, output, errors)
            print("nm-owned-invalid-fd-rejected=pass", flush=True)
            stage("identity-collision")
            try:
                start_externally_managed_xray(
                    config_file, nm_index + 1, output, errors)
            except ValueError as exc:
                if "stale NM-owned TUN identity" not in str(exc):
                    raise
            else:
                raise ValueError("stale TUN ifindex collision accepted")
            if _assert_nm_owned_link(profile_id) != nm_index:
                raise ValueError("identity collision changed NM link")
            print("nm-owned-identity-collision-rejected=pass", flush=True)
            stage("child-identity-collision")
            _prove_child_in_process_identity_rejection(
                config_file, profile_id, nm_index, output, errors)
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
            if grouped:
                stage("grouped-independent-outbounds")
                # This request enters NM's TUN as ordinary traffic. Xray's
                # provider-authored port routing must select secondary peer B.
                # There is no direct-uplink exception for the target.
                run("curl", "-4", "--noproxy", "*", "-fsS",
                    "--max-time", "25", "-o", "/dev/null",
                    f"http://{SECOND}:{TARGET_PORT}/", timeout=30)
                if _qualified_peer_packets() != (True, True):
                    raise ValueError("two native provider peers did not dial independently")
                _prove_physical_drop()
                _prove_ipv6_no_leak()
                print("nm-owned-grouped-outbounds=pass", flush=True)
            stage("natural-global-full")
            _natural_full(xray)
            print("nm-owned-natural-full=pass", flush=True)
            _exercise_live_nm_restart(xray, profile_id, nm_index, plan.tunnel_mark)
            xray, nm_index = _exercise_crash_restart(
                xray, config_file, profile_id, nm_index,
                plan.tunnel_mark, output, errors)
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
    if len(sys.argv) != 3 or sys.argv[2] not in ("typed", "grouped"):
        raise SystemExit("usage: nm_owned_tun_guest.py GENERATED_CONFIG typed|grouped")
    try:
        run_guest(Path(sys.argv[1]), grouped=sys.argv[2] == "grouped")
    except (OSError, RuntimeError, ValueError, subprocess.SubprocessError,
            KeyError, TypeError, json.JSONDecodeError):
        print("nm-owned-result=fail", flush=True)
        raise SystemExit(1)
    print("nm-owned-result=pass", flush=True)


if __name__ == "__main__":
    main()
