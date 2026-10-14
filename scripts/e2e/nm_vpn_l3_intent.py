#!/usr/bin/env python3
"""Load strictly private, disposable guest-only NM VPN activation intent."""
from __future__ import annotations

from dataclasses import dataclass
import json
import os
from pathlib import Path
import stat

from nm_vpn_l3_contract import GuestVPNConfig, config_dicts

# A temporary single-activation permit. The guest orchestrator creates it
# only after verifying the original active NM profile before daemon restart.
REAPPLY_MARKER = Path("/run/podlaz-nm-l3/retained-ip4-reapply")


def retained_ipv4_reapply_authorized(path: Path = REAPPLY_MARKER, *,
                                     expected_uid: int = 0) -> bool:
    """Authorize *inspection* of a retained NM IPv4 assignment, not deletion."""
    try:
        fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC)
    except FileNotFoundError:
        return False
    with os.fdopen(fd, "rb") as stream:
        meta = os.fstat(stream.fileno())
        if (not stat.S_ISREG(meta.st_mode) or
                stat.S_IMODE(meta.st_mode) != 0o600 or
                meta.st_uid != expected_uid or meta.st_size != 0):
            raise ValueError("invalid private NM reapply permit")
    return True


@dataclass(frozen=True)
class GuestPaths:
    container: Path = Path("/run/systemd/container")
    intent: Path = Path("/run/podlaz-nm-l3/intent.json")
    legacy_session: Path = Path("/run/podlaz/network-session-continuation.json")


def _distinct_pairs(pairs: list[tuple[str, object]]) -> dict[str, object]:
    result: dict[str, object] = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("ambiguous guest intent")
        result[key] = value
    return result


def load_guest_intent(paths: GuestPaths = GuestPaths(), *,
                      expected_uid: int = 0) -> tuple[GuestVPNConfig, int]:
    """Fail closed before D-Bus activation; no hostname/secret is logged."""
    if paths.container.is_symlink() or not paths.container.is_file() or paths.container.read_text(encoding="ascii").strip() != "systemd-nspawn":
        raise ValueError("disposable systemd-nspawn guest required")
    if paths.legacy_session.exists() or paths.legacy_session.is_symlink():
        raise ValueError("legacy Podlaz L3 session owns the host")
    if paths.intent.is_symlink():
        raise ValueError("symlinked guest intent")
    try:
        fd = os.open(paths.intent, os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC)
    except OSError as exc:
        raise ValueError("guest intent unavailable") from exc
    with os.fdopen(fd, "rb") as handle:
        st = os.fstat(handle.fileno())
        if not stat.S_ISREG(st.st_mode) or stat.S_IMODE(st.st_mode) != 0o600 or st.st_uid != expected_uid or st.st_size > 2048:
            raise ValueError("unsafe guest intent ownership")
        data = handle.read(2049)
    if len(data) > 2048:
        raise ValueError("oversized guest intent")
    try:
        payload = json.loads(data, object_pairs_hook=_distinct_pairs)
    except (ValueError, UnicodeError) as exc:
        raise ValueError("invalid guest intent") from exc
    if not isinstance(payload, dict) or set(payload) != {"schema", "owner", "tun_ifindex", "tun_name", "external_gateway", "tun_address", "dns"}:
        raise ValueError("unsupported guest intent keys")
    if payload["schema"] != "podlaz.nm-l3-research.v1" or payload["owner"] != "podlaz-guest-nm-l3":
        raise ValueError("unexpected guest intent authority")
    index = payload["tun_ifindex"]
    if type(index) is not int or index <= 0:
        raise ValueError("invalid guest TUN identity")
    if not isinstance(payload["dns"], list) or any(not isinstance(x, str) for x in payload["dns"]):
        raise ValueError("invalid guest DNS type")
    config = GuestVPNConfig(payload["tun_name"], payload["external_gateway"],
                            payload["tun_address"], tuple(payload["dns"]))
    config_dicts(config)
    return config, index
