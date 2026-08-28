from __future__ import annotations

import hashlib
import os
from pathlib import Path

from .command import CommandRunner
from .model import AmbiguousState, DebIdentity, PreflightError


class PackageInspector:
    def __init__(self, runner: CommandRunner):
        self.runner = runner

    def inspect(self, path: Path) -> DebIdentity:
        path = path.expanduser().absolute()
        if path.is_symlink() or not path.is_file():
            raise PreflightError(f"package must be a regular non-symlink file: {path}")
        st = path.stat()
        package = self._field(path, "Package")
        version = self._field(path, "Version")
        architecture = self._field(path, "Architecture")
        if package != "podlaz":
            raise PreflightError(f"unexpected package name: {package}")
        native = self.runner.run(("dpkg", "--print-architecture"), timeout=5).require_success("dpkg architecture").stdout.strip()
        if architecture != native:
            raise PreflightError(f"candidate architecture {architecture} does not match host {native}")
        digest = self._sha256(path)
        self._verify_sibling_checksums(path, digest)
        return DebIdentity(path, package, version, architecture, digest, st.st_dev, st.st_ino)

    def compare_lt(self, previous: DebIdentity, candidate: DebIdentity) -> bool:
        result = self.runner.run(("dpkg", "--compare-versions", previous.version, "lt", candidate.version), timeout=5)
        return result.returncode == 0

    def installed_version(self) -> str | None:
        result = self.runner.run(("dpkg-query", "-W", "-f=${Status}\t${Version}\n", "podlaz"), timeout=5)
        if result.returncode != 0:
            return None
        status, _, version = result.stdout.strip().partition("\t")
        if status != "install ok installed" or not version:
            raise AmbiguousState("podlaz package database state is not conclusively installed")
        return version

    def install_exact(self, identity: DebIdentity) -> None:
        current = identity.path.stat()
        if current.st_dev != identity.device or current.st_ino != identity.inode or self._sha256(identity.path) != identity.sha256:
            raise AmbiguousState("supplied package identity changed after preflight")
        self.runner.run(("dpkg", "-i", str(identity.path)), timeout=120).require_success("dpkg -i supplied Podlaz package")
        if self.installed_version() != identity.version:
            raise AmbiguousState("installed package version does not match supplied package")

    def _field(self, path: Path, name: str) -> str:
        result = self.runner.run(("dpkg-deb", "--field", str(path), name), timeout=10).require_success(f"read {name}")
        value = result.stdout.strip()
        if not value:
            raise PreflightError(f"package field {name} is empty")
        return value

    @staticmethod
    def _sha256(path: Path) -> str:
        digest = hashlib.sha256()
        with path.open("rb") as handle:
            for chunk in iter(lambda: handle.read(1024 * 1024), b""):
                digest.update(chunk)
        return digest.hexdigest()

    def _verify_sibling_checksums(self, path: Path, digest: str) -> None:
        sums = path.parent / "SHA256SUMS"
        if not sums.exists():
            return
        if sums.is_symlink() or not sums.is_file():
            raise PreflightError("sibling SHA256SUMS is not a regular file")
        matches = []
        for line in sums.read_text(encoding="utf-8").splitlines():
            fields = line.split(maxsplit=1)
            if len(fields) != 2:
                continue
            filename = fields[1].lstrip("*")
            if filename == path.name:
                matches.append(fields[0])
        if matches and (len(matches) != 1 or matches[0] != digest):
            raise PreflightError("candidate SHA256SUMS record does not match supplied package")
