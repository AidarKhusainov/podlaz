from __future__ import annotations

import base64
import hashlib
import os
from pathlib import Path
import tempfile

from .model import AmbiguousState


BOOT_MANIFEST = Path("/var/lib/podlaz/boot-autostart-manifest.json")


def capture_boot_manifest() -> dict:
    if not BOOT_MANIFEST.exists():
        return {"enabled": False}
    if BOOT_MANIFEST.is_symlink() or not BOOT_MANIFEST.is_file():
        raise AmbiguousState("boot autostart manifest is not a regular file")
    st = BOOT_MANIFEST.stat()
    data = BOOT_MANIFEST.read_bytes()
    if len(data) > 64 * 1024:
        raise AmbiguousState("boot autostart manifest is unexpectedly large")
    return {
        "enabled": True,
        "mode": st.st_mode & 0o777,
        "uid": st.st_uid,
        "gid": st.st_gid,
        "sha256": hashlib.sha256(data).hexdigest(),
        "payload_b64": base64.b64encode(data).decode("ascii"),
    }


def restore_boot_manifest(snapshot: dict) -> None:
    if not snapshot.get("enabled"):
        if BOOT_MANIFEST.exists():
            raise AmbiguousState("autostart manifest unexpectedly exists after restoring disabled policy")
        return
    try:
        data = base64.b64decode(snapshot["payload_b64"], validate=True)
    except Exception as error:
        raise AmbiguousState("recorded autostart manifest payload is invalid") from error
    if hashlib.sha256(data).hexdigest() != snapshot.get("sha256"):
        raise AmbiguousState("recorded autostart manifest checksum mismatch")
    BOOT_MANIFEST.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    fd, tmp_name = tempfile.mkstemp(prefix=".boot-autostart-manifest.", dir=BOOT_MANIFEST.parent)
    try:
        os.fchmod(fd, int(snapshot["mode"]))
        os.fchown(fd, int(snapshot["uid"]), int(snapshot["gid"]))
        with os.fdopen(fd, "wb") as handle:
            handle.write(data)
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(tmp_name, BOOT_MANIFEST)
        directory_fd = os.open(BOOT_MANIFEST.parent, os.O_RDONLY | os.O_DIRECTORY)
        try:
            os.fsync(directory_fd)
        finally:
            os.close(directory_fd)
    finally:
        try:
            os.unlink(tmp_name)
        except FileNotFoundError:
            pass
    current = BOOT_MANIFEST.read_bytes()
    if hashlib.sha256(current).hexdigest() != snapshot["sha256"]:
        raise AmbiguousState("restored autostart manifest does not match original")
