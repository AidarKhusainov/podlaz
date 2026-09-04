#!/usr/bin/env python3
"""Fail-closed runtime-host identity checks for the installed-package TUN soak."""

from __future__ import annotations

import argparse
import json
import os
import shlex
from pathlib import Path
from typing import Mapping, Sequence

MAX_OS_RELEASE_BYTES = 64 * 1024
REQUIRED_OS_ID = "ubuntu"
REQUIRED_OS_VERSION_ID = "24.04"


def _parse_os_release(text: str) -> dict[str, str]:
    values: dict[str, str] = {}
    for raw in text.splitlines():
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        if "=" not in line:
            raise ValueError("os-release contains a malformed record")
        key, raw_value = line.split("=", 1)
        if not key or not key.replace("_", "").isalnum() or key.upper() != key:
            raise ValueError("os-release contains an invalid key")
        try:
            parsed = shlex.split(raw_value, posix=True)
        except ValueError as exc:
            raise ValueError("os-release contains an invalid value") from exc
        if len(parsed) != 1:
            raise ValueError("os-release contains an ambiguous value")
        if key in values:
            raise ValueError("os-release contains a duplicate key")
        values[key] = parsed[0]
    return values


def verify_runtime_os(path: Path = Path("/etc/os-release")) -> dict[str, str]:
    try:
        data = path.read_bytes()
    except OSError as exc:
        raise ValueError("runtime OS evidence is unavailable") from exc
    if len(data) > MAX_OS_RELEASE_BYTES:
        raise ValueError("runtime OS evidence exceeds the byte limit")
    try:
        values = _parse_os_release(data.decode("utf-8"))
    except UnicodeError as exc:
        raise ValueError("runtime OS evidence is not UTF-8") from exc
    runtime = {
        "id": values.get("ID", ""),
        "version_id": values.get("VERSION_ID", ""),
    }
    if runtime != {"id": REQUIRED_OS_ID, "version_id": REQUIRED_OS_VERSION_ID}:
        raise ValueError("TUN resource acceptance requires Ubuntu 24.04")
    return runtime


def _atomic_write(path: Path, payload: Mapping[str, str]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    temporary = path.with_name(path.name + ".tmp")
    with temporary.open("w", encoding="utf-8") as handle:
        json.dump(dict(payload), handle, sort_keys=True, separators=(",", ":"))
        handle.write("\n")
    os.chmod(temporary, 0o600)
    os.replace(temporary, path)


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="command", required=True)
    verify = subparsers.add_parser("verify-os")
    verify.add_argument("--os-release", type=Path, default=Path("/etc/os-release"))
    verify.add_argument("--output", type=Path, required=True)
    return parser


def main(argv: Sequence[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    try:
        runtime = verify_runtime_os(args.os_release)
        _atomic_write(args.output, runtime)
        return 0
    except ValueError as exc:
        print(f"runtime host verification failed: {exc}", file=os.sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
