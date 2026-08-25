#!/usr/bin/env python3
"""Private-state fixture helpers for Issue #263 installed-package acceptance.

The helper never renders profile/configuration material. It only changes the
manifest's configured_boot_id to simulate a later boot on a self-hosted runner
that cannot reboot in the middle of a GitHub Actions job, and inspects the
non-secret attempt control fields used by the one-attempt invariant.
"""

from __future__ import annotations

import json
import os
import pathlib
import sys
import tempfile


def fail(message: str) -> "NoReturn":
    raise SystemExit(message)


def load_private_json(path: pathlib.Path) -> dict:
    st = path.lstat()
    if not path.is_file() or os.path.islink(path):
        fail("state path is not a regular file")
    if st.st_mode & 0o777 != 0o600:
        fail("state path is not mode 0600")
    with path.open(encoding="utf-8") as handle:
        value = json.load(handle)
    if not isinstance(value, dict):
        fail("state payload is not an object")
    return value


def atomic_write_private(path: pathlib.Path, value: dict) -> None:
    directory = path.parent
    fd, tmp_name = tempfile.mkstemp(prefix=f".{path.name}-", dir=directory)
    tmp = pathlib.Path(tmp_name)
    try:
        os.fchmod(fd, 0o600)
        payload = (json.dumps(value, indent=2, sort_keys=False) + "\n").encode()
        with os.fdopen(fd, "wb", closefd=True) as handle:
            handle.write(payload)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(tmp, path)
        dir_fd = os.open(directory, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(dir_fd)
        finally:
            os.close(dir_fd)
    finally:
        try:
            tmp.unlink()
        except FileNotFoundError:
            pass


def make_manifest_eligible(path: pathlib.Path, simulated_previous_boot: str) -> None:
    if not simulated_previous_boot.strip():
        fail("simulated previous boot id is empty")
    value = load_private_json(path)
    if value.get("schema_version") != "podlaz.boot-autostart-manifest.v1":
        fail("unexpected boot autostart manifest schema")
    value["configured_boot_id"] = simulated_previous_boot
    atomic_write_private(path, value)


def assert_attempt(path: pathlib.Path, expected_state: str, expected_reason: str | None) -> None:
    value = load_private_json(path)
    if value.get("schema_version") != "podlaz.boot-autostart-attempt.v1":
        fail("unexpected boot autostart attempt schema")
    if value.get("state") != expected_state:
        fail("unexpected boot autostart attempt state")
    actual_reason = value.get("terminal_reason") or ""
    if expected_reason is not None and actual_reason != expected_reason:
        fail("unexpected boot autostart terminal reason")


def attempt_control_fingerprint(path: pathlib.Path) -> None:
    value = load_private_json(path)
    fields = (
        value.get("schema_version") or "",
        value.get("boot_id") or "",
        value.get("manifest_generation") or "",
        value.get("state") or "",
        value.get("terminal_reason") or "",
    )
    if not all(fields[:4]):
        fail("attempt control fields are incomplete")
    print("|".join(fields))


def main(argv: list[str]) -> None:
    if len(argv) < 3:
        fail("usage: issue263_state.py <command> <path> [args]")
    command = argv[1]
    path = pathlib.Path(argv[2])
    if command == "make-manifest-eligible" and len(argv) == 4:
        make_manifest_eligible(path, argv[3])
        return
    if command == "assert-attempt" and len(argv) in {4, 5}:
        reason = argv[4] if len(argv) == 5 else None
        assert_attempt(path, argv[3], reason)
        return
    if command == "attempt-control-fingerprint" and len(argv) == 3:
        attempt_control_fingerprint(path)
        return
    fail("invalid issue263_state.py invocation")


if __name__ == "__main__":
    main(sys.argv)
