#!/usr/bin/env python3
"""Apply a *guest-only* external TUN FD prototype to exact Xray v26.3.27.

Never patch or replace the Podlaz package runtime. Caller pins the upstream
Git commit independently before invoking this source-level experiment.
"""
from __future__ import annotations

import argparse
from pathlib import Path
import subprocess


def _replace_once(source: str, old: str, new: str) -> str:
    if source.count(old) != 1:
        raise ValueError("pinned upstream Xray TUN source contract changed")
    return source.replace(old, new, 1)


def patch_linux_tun(source: str) -> str:
    source = _replace_once(
        source,
        'import (\n\t"github.com/vishvananda/netlink"',
        'import (\n\t"fmt"\n\t"net"\n\t"os"\n\t"strconv"\n\n\t"github.com/vishvananda/netlink"',
    )
    source = _replace_once(
        source,
        '\ttunLink netlink.Link\n\toptions TunOptions\n',
        '\ttunLink netlink.Link\n\toptions TunOptions\n\texternal bool // experimental inherited FD: no device/link mutations\n',
    )
    source = _replace_once(
        source,
        'func NewTun(options TunOptions) (Tun, error) {\n\ttunFd, err := open(options.Name)',
        '''func NewTun(options TunOptions) (Tun, error) {
	if value, exists := os.LookupEnv("PODLAZ_NM_GUEST_TUN_FD"); exists {
		// This mode is a guest-only upstream feasibility experiment.
		// There is no interface creation, MTU change, link up or link down.
		fd, err := strconv.Atoi(value)
		if err != nil || fd < 3 {
			return nil, fmt.Errorf("invalid inherited external TUN descriptor")
		}
		index, err := strconv.Atoi(os.Getenv("PODLAZ_NM_GUEST_TUN_IFINDEX"))
		if err != nil || index <= 0 {
			return nil, fmt.Errorf("invalid exact external TUN ifindex")
		}
		ifr, err := unix.NewIfreq(options.Name)
		if err != nil {
			return nil, err
		}
		if err = unix.IoctlIfreq(fd, unix.TUNGETIFF, ifr); err != nil {
			return nil, fmt.Errorf("external TUN descriptor inspection: %w", err)
		}
		const required = unix.IFF_TUN | unix.IFF_NO_PI
		if ifr.Name() != options.Name || ifr.Uint16() & required != required {
			return nil, fmt.Errorf("external TUN name or type mismatch")
		}
		link, err := netlink.LinkByName(options.Name)
		if err != nil {
			return nil, err
		}
		attrs := link.Attrs()
		if attrs == nil || attrs.Index != index || attrs.MTU != int(options.MTU) || attrs.Flags & net.FlagUp == 0 {
			return nil, fmt.Errorf("external TUN identity, MTU or link state mismatch")
		}
		if err := unix.SetNonblock(fd, true); err != nil {
			return nil, err
		}
		return &LinuxTun{tunFd: fd, tunLink: link, options: options, external: true}, nil
	}
	tunFd, err := open(options.Name)''',
    )
    source = _replace_once(
        source,
        'func (t *LinuxTun) Start() error {\n\terr := netlink.LinkSetUp(t.tunLink)',
        'func (t *LinuxTun) Start() error {\n\tif t.external {\n\t\treturn nil\n\t}\n\terr := netlink.LinkSetUp(t.tunLink)',
    )
    source = _replace_once(
        source,
        'func (t *LinuxTun) Close() error {\n\t_ = netlink.LinkSetDown(t.tunLink)',
        'func (t *LinuxTun) Close() error {\n\tif !t.external {\n\t\t_ = netlink.LinkSetDown(t.tunLink)\n\t}',
    )
    return source


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("xray_source_root", type=Path)
    args = parser.parse_args()
    root = args.xray_source_root.resolve(strict=True)
    if not (root / ".git").is_dir() or (root / "go.mod").is_symlink():
        raise SystemExit("not a pinned standalone Xray checkout")
    result = subprocess.run(
        ["git", "-C", str(root), "rev-parse", "HEAD"],
        check=True, capture_output=True, text=True,
    )
    if result.stdout.strip() != "d2758a023cd7f4174a5a5fa4ff66e487d4342ba0":
        raise SystemExit("unexpected Xray source commit")
    path = root / "proxy" / "tun" / "tun_linux.go"
    if path.is_symlink() or not path.is_file():
        raise SystemExit("missing ordinary Linux TUN implementation")
    before = path.read_text(encoding="utf-8")
    after = patch_linux_tun(before)
    path.write_text(after, encoding="utf-8")


if __name__ == "__main__":
    main()
