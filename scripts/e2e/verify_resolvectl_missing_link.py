#!/usr/bin/env python3
from __future__ import annotations

import sys
from pathlib import Path

MISSING_LINK_MARKER = b'Failed to resolve interface "podlaz0": No such device'
MISSING_LINK_UBUNTU_MARKER = b'Failed to resolve interface "podlaz0", ignoring: No such device'
_ALLOWED_STDERR = frozenset(
    marker + terminator
    for marker in (MISSING_LINK_MARKER, MISSING_LINK_UBUNTU_MARKER)
    for terminator in (b"\n", b"\r\n")
)
_CAPTURE_EXIT_CODE = "dns-rollback.exit-code"
_CAPTURE_STDOUT = "dns-rollback.stdout"
_CAPTURE_STDERR = "dns-rollback.stderr"


def is_exact_missing_link_stderr(raw: bytes) -> bool:
    """Return whether stderr is the exact supported resolvectl protocol result."""
    return raw in _ALLOWED_STDERR


def is_exact_missing_link_result(
    exit_code_raw: bytes, stdout_raw: bytes, stderr_raw: bytes
) -> bool:
    """Return whether all captured production process-result bytes are exact."""
    return (
        exit_code_raw == b"1\n"
        and stdout_raw == b""
        and is_exact_missing_link_stderr(stderr_raw)
    )


def read_bytes(path: Path, label: str) -> bytes:
    try:
        return path.read_bytes()
    except OSError as exc:
        raise RuntimeError(f"cannot read {label} file: {exc}") from exc


def main(argv: list[str]) -> int:
    if len(argv) != 2:
        print("usage: verify_resolvectl_missing_link.py <stderr-file>", file=sys.stderr)
        return 2

    stderr_path = Path(argv[1])
    try:
        stderr_raw = read_bytes(stderr_path, "resolvectl stderr")
        sibling_exit = stderr_path.with_name(_CAPTURE_EXIT_CODE)
        sibling_stdout = stderr_path.with_name(_CAPTURE_STDOUT)
        capture_mode = (
            stderr_path.name == _CAPTURE_STDERR
            or sibling_exit.exists()
            or sibling_stdout.exists()
        )
        if not capture_mode:
            return 0 if is_exact_missing_link_stderr(stderr_raw) else 1
        exit_code_raw = read_bytes(sibling_exit, "resolvectl exit-code capture")
        stdout_raw = read_bytes(sibling_stdout, "resolvectl stdout capture")
    except RuntimeError as exc:
        print(str(exc), file=sys.stderr)
        return 2

    return (
        0
        if is_exact_missing_link_result(exit_code_raw, stdout_raw, stderr_raw)
        else 1
    )


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
