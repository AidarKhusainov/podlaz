#!/usr/bin/env python3
from __future__ import annotations

import sys
from pathlib import Path

MISSING_LINK_MARKER = b'Failed to resolve interface "podlaz0": No such device'
_ALLOWED_STDERR = frozenset(
    {
        MISSING_LINK_MARKER + b"\n",
        MISSING_LINK_MARKER + b"\r\n",
    }
)


def is_exact_missing_link_stderr(raw: bytes) -> bool:
    """Return whether stderr is the exact supported resolvectl protocol result."""
    return raw in _ALLOWED_STDERR


def main(argv: list[str]) -> int:
    if len(argv) != 2:
        print("usage: verify_resolvectl_missing_link.py <stderr-file>", file=sys.stderr)
        return 2

    try:
        raw = Path(argv[1]).read_bytes()
    except OSError as exc:
        print(f"cannot read resolvectl stderr file: {exc}", file=sys.stderr)
        return 2

    return 0 if is_exact_missing_link_stderr(raw) else 1


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
