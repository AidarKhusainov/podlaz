#!/usr/bin/env python3
"""Negative root-only Unix broker admission probe in a disposable guest."""

import os
from pathlib import Path
import socket
import stat


def main() -> None:
    root = Path("/run/podlaz-nm-fd")
    owner = root.stat()
    if owner.st_uid != 0 or stat.S_IMODE(owner.st_mode) != 0o700:
        raise RuntimeError("broker runtime directory does not enforce root-only access")
    with socket.socket(socket.AF_UNIX, socket.SOCK_SEQPACKET) as peer:
        try:
            peer.connect(str(root / "broker.sock"))
        except PermissionError:
            print("nm-broker-ordinary-peer=denied")
            return
    raise RuntimeError("ordinary user connected to root-only NM FD broker")


if __name__ == "__main__":
    if os.geteuid() == 0:
        raise SystemExit("NM broker negative probe must run unprivileged")
    main()
