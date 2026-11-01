#!/usr/bin/env python3
"""Verify the exact UNMODIFIED upstream Linux inherited-FD contract for guests.

No experimental source rewrite and no product Xray version change. A version
switch is a deliberate requalification, not a floating release selection.
"""
from pathlib import Path
import subprocess
import sys

XRAY_COMMIT = "a4a3dff5b20c2faae567b742c172c7b95e75c083"


def verify_source(source: str) -> None:
    # Literal guards are intentionally pinned to one reviewed immutable commit.
    # Their job is fail-closed source drift detection, not arbitrary Go parsing.
    anchors = (
        "tunFd, vnet, tunLink, fdProvided, err := openFromEnv(options.Name)",
        "if fdProvided {",
        "fdStr := platform.NewEnvFlag(platform.TunFdKey).GetValue",
        "unix.IoctlIfreq(fd, unix.TUNGETIFF, ifr)",
        "flags&unix.IFF_TUN == 0",
        "flags&unix.IFF_NO_PI == 0",
        "actualName != expectedName",
        "ownsTun: true",
        "if !t.ownsTun {",
        "if t.ownsTun {",
        "func (t *LinuxTun) Close() error {",
    )
    if any(source.count(anchor) != 1 for anchor in anchors):
        raise ValueError("unqualified upstream inherited-FD ownership contract")
    start = source.split("func (t *LinuxTun) Start() error {", 1)[1]
    if start.find("if !t.ownsTun {") > start.find("netlink.LinkSetUp"):
        raise ValueError("upstream mutates external link on Start")
    close = source.split("func (t *LinuxTun) Close() error {", 1)[1]
    if close.find("if t.ownsTun {") > close.find("netlink.LinkSetDown"):
        raise ValueError("upstream mutates external link on Close")
    create = source.split("func NewTun(options *Config) (Tun, error) {", 1)[1]
    if create.find("if fdProvided {") > create.find("open(options.Name"):
        raise ValueError("upstream creates link despite inherited descriptor")


def main(root: Path) -> None:
    if not (root / ".git").is_dir():
        raise ValueError("not a standalone upstream checkout")
    commit = subprocess.check_output(
        ["git", "-C", str(root), "rev-parse", "HEAD"], text=True).strip()
    if commit != XRAY_COMMIT:
        raise ValueError("unreviewed upstream Xray commit")
    path = root / "proxy" / "tun" / "tun_linux.go"
    if path.is_symlink() or not path.is_file():
        raise ValueError("missing reviewed Linux source")
    verify_source(path.read_text(encoding="utf-8"))


if __name__ == "__main__":
    if len(sys.argv) != 2:
        raise SystemExit("usage: nm_owned_tun_upstream_contract.py XRAY_CHECKOUT")
    main(Path(sys.argv[1]).resolve(strict=True))
