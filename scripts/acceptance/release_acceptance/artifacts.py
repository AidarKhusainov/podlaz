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
        root = root.expanduser().absolute()
        cls._create_safe_tree(root, user)
        store = cls(root, user)
        for directory in (store.private_dir, store.public_dir):
            cls._create_child(directory, user)
        return store

    @classmethod
    def _create_safe_tree(cls, root: Path, user: UserIdentity) -> None:
        home = user.home.resolve(strict=True)
        current = Path(root.anchor)
        missing: list[Path] = []
        inside_home = False
        for part in root.parts[1:]:
            current /= part
            if current == home:
                inside_home = True
            if current.exists():
                st = current.lstat()
                if current.is_symlink() or not current.is_dir():
                    raise UnsafePath(f"artifact path has unsafe component: {current}")
                if inside_home and current != home:
                    if st.st_uid != user.uid:
                        raise UnsafePath(f"artifact path component is not user-owned: {current}")
                    if st.st_mode & 0o022:
                        raise UnsafePath(f"artifact path component is group/world writable: {current}")
                if current == root:
                    if st.st_uid != user.uid:
                        raise UnsafePath("existing artifact root must be owned by original user")
                    if st.st_mode & 0o077:
                        raise UnsafePath("existing artifact root permissions must be 0700")
                continue
            missing.append(current)
        if not missing:
            return
        parent = missing[0].parent
        parent_st = parent.stat()
        if parent_st.st_uid not in {0, user.uid}:
            raise UnsafePath("artifact parent has foreign ownership")
        if parent_st.st_mode & 0o022:
            raise UnsafePath("artifact parent is group/world writable")
        for directory in missing:
            directory.mkdir(mode=0o700)
            os.chown(directory, user.uid, user.gid)
            os.chmod(directory, 0o700)

    @staticmethod
    def _create_child(directory: Path, user: UserIdentity) -> None:
        if directory.exists():
            if directory.is_symlink() or not directory.is_dir():
                raise UnsafePath(f"artifact child is not a regular directory: {directory}")
            st = directory.stat()
            if st.st_uid != user.uid or st.st_mode & 0o077:
                raise UnsafePath(f"artifact child has unsafe ownership/mode: {directory}")
            return
        directory.mkdir(mode=0o700)
        os.chown(directory, user.uid, user.gid)
        os.chmod(directory, 0o700)

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
        self._atomic_write(
            self.private_dir / name,
            json.dumps(value, indent=2, sort_keys=True) + "\n",
            0o600,
        )

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
        if isinstance(value, str) and any(pattern.search(value) for pattern in _TOKEN_PATTERNS):
            return "<redacted>"
        return value

    @staticmethod
    def _assert_public_safe(encoded: str) -> None:
        lowered = encoded.lower()
        if any(
            token in lowered
            for token in ("vless://", "vmess://", "trojan://", "authorization:")
        ):
            raise UnsafePath("public artifact contains secret-shaped material")

    def _atomic_write(self, path: Path, text: str, mode: int) -> None:
        if path.parent.is_symlink() or path.parent.stat().st_uid != self.user.uid:
            raise UnsafePath("artifact destination parent is unsafe")
        if path.exists():
            if path.is_symlink() or not path.is_file():
                raise UnsafePath("refuse to replace non-regular artifact")
            if path.stat().st_uid != self.user.uid:
                raise UnsafePath("refuse to replace foreign-owned artifact")
        fd, tmp_name = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
        try:
            os.fchmod(fd, mode)
            os.fchown(fd, self.user.uid, self.user.gid)
            with os.fdopen(fd, "w", encoding="utf-8") as handle:
                handle.write(text)
                handle.flush()
                os.fsync(handle.fileno())
            os.replace(tmp_name, path)
            os.chown(path, self.user.uid, self.user.gid)
            os.chmod(path, mode)
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
