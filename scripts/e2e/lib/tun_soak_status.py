#!/usr/bin/env python3
"""Classify private `podlaz status` product output for bounded TUN health waits.

The parser follows the public concise status contract. Raw status text stays in
private E2E state and only an allowlisted structural verdict is emitted.
"""

from __future__ import annotations

import argparse
import sys
from pathlib import Path
from typing import Sequence

MAX_STATUS_BYTES = 64 * 1024

VERIFIED = "verified"
RETRY_INITIALIZING = "retry-initializing"
RETRY_REVALIDATING = "retry-revalidating"
TERMINAL_INACTIVE = "terminal-inactive"
COMMAND_ERROR = "command-error"
INVALID_STATUS = "invalid-status"

STATUS_VERDICTS = frozenset(
    {
        VERIFIED,
        RETRY_INITIALIZING,
        RETRY_REVALIDATING,
        TERMINAL_INACTIVE,
        COMMAND_ERROR,
        INVALID_STATUS,
    }
)


def _single_prefixed_value(lines: list[str], prefix: str) -> str | None:
    values = [line[len(prefix) :].strip() for line in lines if line.startswith(prefix)]
    if len(values) != 1 or not values[0]:
        return None
    return values[0]


def classify_status(raw_output: str, *, exit_code: int) -> str:
    """Return one public-safe verdict for one private status observation."""
    if not isinstance(exit_code, int) or not 0 <= exit_code <= 255:
        return INVALID_STATUS
    if exit_code not in (0, 3):
        return COMMAND_ERROR
    if len(raw_output.encode("utf-8", errors="replace")) > MAX_STATUS_BYTES:
        return INVALID_STATUS

    lines = [line.strip() for line in raw_output.splitlines() if line.strip()]
    state = _single_prefixed_value(lines, "Status: ")
    mode = _single_prefixed_value(lines, "Mode: ")
    if state is None:
        return INVALID_STATUS

    if state == "Connected":
        return VERIFIED if exit_code == 0 and mode == "tun" else INVALID_STATUS
    if state == "Connecting":
        return RETRY_INITIALIZING if mode == "tun" else INVALID_STATUS
    if state == "Reconnecting":
        return RETRY_REVALIDATING if mode == "tun" else INVALID_STATUS
    if state == "Disconnected":
        return TERMINAL_INACTIVE if exit_code == 0 else INVALID_STATUS
    return INVALID_STATUS


def _read_bounded_status(path: Path) -> str:
    try:
        raw = path.read_bytes()
    except OSError as exc:
        raise ValueError("private status evidence is unavailable") from exc
    if len(raw) > MAX_STATUS_BYTES:
        return "x" * (MAX_STATUS_BYTES + 1)
    try:
        return raw.decode("utf-8")
    except UnicodeDecodeError:
        return "\ufffd" * (MAX_STATUS_BYTES + 1)


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description=__doc__)
    subparsers = parser.add_subparsers(dest="command", required=True)
    classify = subparsers.add_parser("classify", help="classify one private status observation")
    classify.add_argument("--stdout-file", type=Path, required=True)
    classify.add_argument("--exit-code", type=int, required=True)
    return parser


def main(argv: Sequence[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    try:
        raw_output = _read_bounded_status(args.stdout_file)
        verdict = classify_status(raw_output, exit_code=args.exit_code)
    except ValueError:
        print(INVALID_STATUS)
        return 1
    if verdict not in STATUS_VERDICTS:
        print(INVALID_STATUS)
        return 1
    print(verdict)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
