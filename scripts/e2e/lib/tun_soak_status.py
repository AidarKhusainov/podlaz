#!/usr/bin/env python3
"""Classify private `podlaz status` output for bounded TUN health waits.

The parser emits one allowlisted structural verdict only. Raw status text stays in
private E2E state and is never copied to public artifacts or workflow output.
"""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path
from typing import Sequence

MAX_STATUS_BYTES = 64 * 1024

VERIFIED = "verified"
RETRY_REVALIDATING = "retry-revalidating"
RETRY_DEGRADED = "retry-degraded"
TERMINAL_CLEANUP_REQUIRED = "terminal-cleanup-required"
TERMINAL_INACTIVE = "terminal-inactive"
COMMAND_ERROR = "command-error"
INVALID_STATUS = "invalid-status"

STATUS_VERDICTS = frozenset(
    {
        VERIFIED,
        RETRY_REVALIDATING,
        RETRY_DEGRADED,
        TERMINAL_CLEANUP_REQUIRED,
        TERMINAL_INACTIVE,
        COMMAND_ERROR,
        INVALID_STATUS,
    }
)

_HEALTH_CLASSIFICATIONS = frozenset(
    {
        "uplink_revalidating",
        "uplink_changed",
        "uplink_fingerprint_unavailable",
        "ownership_invalid",
        "owned_state_invalid",
        "connectivity_failed",
        "revalidation_timeout",
        "revalidation_interrupted",
    }
)

_ACTIVE_CONNECTION = re.compile(
    r"active \((revalidating|degraded|cleanup-required): ([a-z0-9_]+)\)"
)
_GENERATION = re.compile(r"[1-9][0-9]*")


def _single_prefixed_value(lines: list[str], prefix: str) -> str | None:
    values = [line[len(prefix) :].strip() for line in lines if line.startswith(prefix)]
    if len(values) != 1 or not values[0]:
        return None
    return values[0]


def _single_tun_attribute(parts: list[str], prefix: str) -> str | None:
    values = [part[len(prefix) :].strip() for part in parts if part.startswith(prefix)]
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
    connection = _single_prefixed_value(lines, "Connection: ")
    tun = _single_prefixed_value(lines, "TUN: ")
    if connection is None or tun is None:
        return INVALID_STATUS

    parts = [part.strip() for part in tun.split(";") if part.strip()]
    health = _single_tun_attribute(parts, "current health=")
    generation = _single_tun_attribute(parts, "network generation=")
    classification = _single_tun_attribute(parts, "classification=")

    if connection == "inactive":
        if exit_code == 0 and health is None and generation is None and classification is None:
            return TERMINAL_INACTIVE
        return INVALID_STATUS

    if health is None or generation is None or _GENERATION.fullmatch(generation) is None:
        return INVALID_STATUS

    if health == "verified":
        if exit_code == 0 and connection == "active" and classification is None:
            return VERIFIED
        return INVALID_STATUS

    match = _ACTIVE_CONNECTION.fullmatch(connection)
    if match is None:
        return INVALID_STATUS
    connection_state, connection_classification = match.groups()
    if connection_state != health:
        return INVALID_STATUS
    if classification != connection_classification or classification not in _HEALTH_CLASSIFICATIONS:
        return INVALID_STATUS
    if exit_code != 3:
        return INVALID_STATUS

    if health == "revalidating":
        return RETRY_REVALIDATING
    if health == "degraded":
        return RETRY_DEGRADED
    if health == "cleanup-required":
        return TERMINAL_CLEANUP_REQUIRED
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
