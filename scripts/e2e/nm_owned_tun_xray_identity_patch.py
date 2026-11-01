#!/usr/bin/env python3
"""Pinned, guest-only Linux Xray inherited-TUN-FD admission extension.

This is not an upstream API, not a production patch, and does NOT make the
name-to-ifindex lookup atomic against concurrent privileged renames. The
in-process checks are a defense in depth and require a persistent firewall
barrier and an exact-owned NetworkManager guardian.
"""
from __future__ import annotations

import argparse
from pathlib import Path
import subprocess

from nm_owned_tun_upstream_contract import XRAY_COMMIT, verify_source


def replace_once(source: str, old: str, new: str) -> str:
    if source.count(old) != 1:
        raise ValueError("upstream Linux TUN identity extension source drift")
    return source.replace(old, new, 1)


def extend_linux_fd(source: str) -> str:
    verify_source(source)
    source = replace_once(
        source, '\t"net/netip"\n\t"os/exec"',
        '\t"net/netip"\n\t"os"\n\t"os/exec"',
    )
    old = '''\ttunLink, err := netlink.LinkByName(actualName)
\tif err != nil {
\t\treturn -1, 0, nil, true, err
\t}

\tif err = unix.SetNonblock(fd, true); err != nil {'''
    new = '''\ttunLink, err := netlink.LinkByName(actualName)
\tif err != nil {
\t\treturn -1, 0, nil, true, err
\t}

\t// RESEARCH EXTENSION: require explicit, exact owner identity in Xray
\t// before accepting an inherited descriptor. TUNGETIFF names a device,
\t// but Linux does not expose atomic FD-to-ifindex binding here.
\tindexRaw := os.Getenv("PODLAZ_RESEARCH_TUN_IFINDEX")
\tmtuRaw := os.Getenv("PODLAZ_RESEARCH_TUN_MTU")
\tindex, indexErr := strconv.Atoi(indexRaw)
\tmtu, mtuErr := strconv.Atoi(mtuRaw)
\tif indexErr != nil || index <= 0 || mtuErr != nil || mtu <= 0 {
\t\treturn -1, 0, nil, true, errors.New("external TUN expected ifindex/MTU missing or invalid")
\t}
\tattrs := tunLink.Attrs()
\tif attrs == nil || attrs.Name != actualName || attrs.Index != index ||
\t\tattrs.MTU != mtu || attrs.Flags&net.FlagUp == 0 {
\t\treturn -1, 0, nil, true, errors.New("external TUN expected identity/MTU/UP mismatch")
\t}
\tif flags&unix.IFF_VNET_HDR != 0 || flags&unix.IFF_MULTI_QUEUE != 0 {
\t\treturn -1, 0, nil, true, errors.New("unqualified external TUN packet mode")
\t}
\t// Bound, but do not claim to eliminate, the privileged rename race.
\tif err = unix.IoctlIfreq(fd, unix.TUNGETIFF, ifr); err != nil ||
\t\tifr.Name() != actualName {
\t\treturn -1, 0, nil, true, errors.New("external TUN descriptor changed during admission").Base(err)
\t}

\tif err = unix.SetNonblock(fd, true); err != nil {'''
    return replace_once(source, old, new)


def main(root: Path) -> None:
    if not (root / ".git").is_dir():
        raise ValueError("not an isolated upstream checkout")
    rev = subprocess.check_output(
        ("git", "-C", str(root), "rev-parse", "HEAD"), text=True,
    ).strip()
    if rev != XRAY_COMMIT:
        raise ValueError("upstream Xray revision is not pinned")
    path = root / "proxy" / "tun" / "tun_linux.go"
    if path.is_symlink() or not path.is_file():
        raise ValueError("unexpected upstream TUN source file")
    path.write_text(extend_linux_fd(path.read_text(encoding="utf-8")),
                    encoding="utf-8")


if __name__ == "__main__":
    cli = argparse.ArgumentParser()
    cli.add_argument("xray_root", type=Path)
    args = cli.parse_args()
    main(args.xray_root.resolve(strict=True))
