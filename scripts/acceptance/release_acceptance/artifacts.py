from __future__ import annotations

import json
import os
from pathlib import Path
import re
import tempfile
from typing import Any

from .model import UnsafePath, UserIdentity


_PRIVATE_KEYS = {
    "profile", "profile_id", "endpoint", "server", "ssid", "uuid", "boot_id",
    "transaction_id", "session_id", "public_ip", "interface", "uplink",
}
_TOKEN_PATTERNS = [
    re.compile(r"\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b"),
    re.compile(r"\b[0-9a-fA-F]{8}-[0-9a-fA-F-]{27,}\b"),
    re.compile(r"(?:vless|vmess|trojan|ss)://", re.I),
]


class ArtifactStore:
    def __init__(self, root: Path, user: UserIdentity):
        self.root = root
        self.user = user
        self.private_dir = root / "private"
        self.public_dir = root / "public"

    @classmethod
    def create(cls, root: Path, user: UserIdentity) -> "ArtifactStore":
        root = root.expanduser()
        cls._validate_path(root, user)
        root.mkdir(mode=0o700, parents=True, exist_ok=True)
        os.chown(root, user.uid, user.gid)
        os.chmod(root, 0o700)
        store = cls(root, user)
        for directory in (store.private_dir, store.public_dir):
            directory.mkdir(mode=0o700, exist_ok=True)
            os.chown(directory, user.uid, user.gid)
            os.chmod(directory, 0o700)
        return store

    @staticmethod
    def _validate_path(path: Path, user: UserIdentity) -> None:
        absolute = path.absolute()
        current = Path(absolute.anchor)
        for part in absolute.parts[1:]:
            current /= part
            if not current.exists():
                continue
            st = current.lstat()
            if os.path.islink(current):
                raise UnsafePath(f"artifact path contains symlink: {current}")
            if current == absolute and st.st_uid not in {0, user.uid}:
                raise UnsafePath("artifact directory has foreign ownership")

    def record_command(self, result) -> None:
        payload = {
            "argv": list(result.argv),
            "returncode": result.returncode,
            "stdout": result.stdout[-65536:],
            "stderr": result.stderr[-65536:],
        }
        name = f"command-{len(list(self.private_dir.glob('command-*.json'))):05d}.json"
        self.write_private_json(name, payload)

    def write_private_json(self, name: str, value: Any) -> None:
        self._atomic_write(self.private_dir / name, json.dumps(value, indent=2, sort_keys=True) + "\n", 0o600)

    def write_public_json(self, name: str, value: Any) -> None:
        sanitized = self.sanitize(value)
        encoded = json.dumps(sanitized, indent=2, sort_keys=True) + "\n"
        self._assert_public_safe(encoded)
        self._atomic_write(self.public_dir / name, encoded, 0o600)

    def write_public_text(self, name: str, text: str) -> None:
        self._assert_public_safe(text)
        self._atomic_write(self.public_dir / name, text, 0o600)

    def sanitize(self, value: Any) -> Any:
        if isinstance(value, dict):
            out = {}
            for key, item in value.items():
                lowered = str(key).lower()
                if lowered in _PRIVATE_KEYS or lowered.endswith("_id") or lowered.endswith("_ip"):
                    out[key] = "<redacted>"
                else:
                    out[key] = self.sanitize(item)
            return out
        if isinstance(value, list):
            return [self.sanitize(item) for item in value]
        if isinstance(value, str):
            if any(pattern.search(value) for pattern in _TOKEN_PATTERNS):
                return "<redacted>"
        return value

    @staticmethod
    def _assert_public_safe(encoded: str) -> None:
        lowered = encoded.lower()
        if any(token in lowered for token in ("vless://", "vmess://", "trojan://", "authorization:")):
            raise UnsafePath("public artifact contains secret-shaped material")

    def _atomic_write(self, path: Path, text: str, mode: int) -> None:
        fd, tmp_name = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
        try:
            os.fchmod(fd, mode)
            with os.fdopen(fd, "w", encoding="utf-8") as handle:
                handle.write(text)
                handle.flush()
                os.fsync(handle.fileno())
            os.replace(tmp_name, path)
            os.chown(path, self.user.uid, self.user.gid)
            directory_fd = os.open(path.parent, os.O_RDONLY | os.O_DIRECTORY)
            try:
                os.fsync(directory_fd)
            finally:
                os.close(directory_fd)
        finally:
            try:
                os.unlink(tmp_name)
            except FileNotFoundError:
                pass
