#!/usr/bin/env python3
"""Private dual-provider research fixture for native Xray, not a provider parser.

The source is the installed Podlaz-generated synthetic VMess transport.
Produce native provider-owned outbounds/routing and run it back through the
ordinary Podlaz import + full-TUN composer before the NM experiment.
"""
from __future__ import annotations

from copy import deepcopy
import json
import os
from pathlib import Path
import sys

FIRST = "172.31.253.1"  # Dedicated disposable fixture, never a product endpoint.
SECOND = "172.31.253.2"
TARGET_PORT = 28080
FIRST_TAG = "fixture-egress-a"
SECOND_TAG = "fixture-egress-b"


def _eligible_outbound(config: object) -> dict:
    if not isinstance(config, dict):
        raise ValueError("invalid synthetic source")
    outbounds = config.get("outbounds")
    if not isinstance(outbounds, list) or len(outbounds) != 1:
        raise ValueError("expected only the qualified typed fixture")
    item = deepcopy(outbounds[0])
    if not isinstance(item, dict) or item.get("protocol") != "vmess":
        raise ValueError("unexpected synthetic protocol")
    settings = item.get("settings")
    stream = item.get("streamSettings")
    if not isinstance(settings, dict) or not isinstance(stream, dict):
        raise ValueError("invalid original provider")
    if settings.get("address") != FIRST or not isinstance(settings.get("port"), int):
        raise ValueError("unexpected original provider peer")
    if settings["port"] <= 0 or settings["port"] > 65535:
        raise ValueError("invalid synthetic provider port")
    socket_options = stream.get("sockopt")
    if not isinstance(socket_options, dict) or not isinstance(socket_options.get("mark"), int):
        raise ValueError("missing original allocated underlay authority")
    if socket_options["mark"] <= 0 or "dialerProxy" in socket_options:
        raise ValueError("unsupported synthetic provider socket settings")
    del socket_options["mark"]
    if not socket_options:
        stream.pop("sockopt")
    return item


def native_grouped_source(config: object) -> dict:
    first = _eligible_outbound(config)
    second = deepcopy(first)
    first["tag"] = FIRST_TAG
    second["tag"] = SECOND_TAG
    second["settings"]["address"] = SECOND
    return {
        "outbounds": [first, second],
        "routing": {
            "domainStrategy": "AsIs",
            "rules": [{"type": "field", "network": "tcp", "port": str(TARGET_PORT),
                       "outboundTag": SECOND_TAG}],
        },
    }


def verify_composed_grouped(config: object, mark: int) -> int:
    if type(mark) is not int or mark <= 0 or not isinstance(config, dict):
        raise ValueError("invalid grouped ownership authority")
    outbounds = config.get("outbounds")
    if not isinstance(outbounds, list) or len(outbounds) != 2:
        raise ValueError("provider grouped outbounds were flattened")
    peers = []
    for item, tag in zip(outbounds, [FIRST_TAG, SECOND_TAG]):
        if not isinstance(item, dict) or item.get("tag") != tag or item.get("protocol") != "vmess":
            raise ValueError("provider outbound tags/protocol lost")
        settings = item.get("settings")
        stream = item.get("streamSettings")
        if not isinstance(settings, dict) or not isinstance(stream, dict):
            raise ValueError("provider transport structure lost")
        sockopt = stream.get("sockopt")
        if not isinstance(sockopt, dict) or type(sockopt.get("mark")) is not int or sockopt["mark"] != mark:
            raise ValueError("provider underlay mark missing or conflicting")
        peers.append(settings.get("address"))
    if peers != [FIRST, SECOND]:
        raise ValueError("provider peer topology changed")
    port_a = outbounds[0]["settings"].get("port")
    port_b = outbounds[1]["settings"].get("port")
    if type(port_a) is not int or port_a <= 0 or port_a > 65535 or port_a != port_b:
        raise ValueError("provider ports differ")
    routing = config.get("routing")
    expected_rule = {"type": "field", "network": "tcp", "port": str(TARGET_PORT),
                     "outboundTag": SECOND_TAG}
    if not isinstance(routing, dict) or routing.get("rules") != [expected_rule]:
        raise ValueError("provider routing rules not retained")
    return port_a


def prepare(source: Path, destination: Path) -> None:
    if not source.is_file() or source.is_symlink() or source.stat().st_mode & 0o077:
        raise ValueError("unqualified private generated Xray source")
    rendered = native_grouped_source(json.loads(source.read_text(encoding="utf-8")))
    data = json.dumps(rendered, sort_keys=True, separators=(",", ":")).encode()
    fd = os.open(destination, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, "wb") as out:
        out.write(data)


def main() -> None:
    if len(sys.argv) != 4 or sys.argv[1] != "prepare":
        raise SystemExit("usage: grouped-fixture prepare SOURCE DESTINATION")
    prepare(Path(sys.argv[2]), Path(sys.argv[3]))


if __name__ == "__main__":
    main()
