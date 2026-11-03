#!/usr/bin/env python3
"""Negative Unix broker admission probes inside a disposable installed guest."""

import array
import os
from pathlib import Path
import socket
import stat
import sys


SOCKET = Path("/run/podlaz-nm-fd/broker.sock")


def verify_socket_directory() -> None:
    owner = SOCKET.parent.stat()
    if owner.st_uid != 0 or stat.S_IMODE(owner.st_mode) != 0o700:
        raise RuntimeError("broker runtime directory does not enforce root-only access")


def ordinary_user_denied() -> None:
    if os.geteuid() == 0:
        raise RuntimeError("ordinary-user denial probe must not run as root")
    verify_socket_directory()
    with socket.socket(socket.AF_UNIX, socket.SOCK_SEQPACKET) as peer:
        peer.settimeout(5)
        try:
            peer.connect(str(SOCKET))
        except PermissionError:
            print("nm-broker-ordinary-peer=denied")
            return
    raise RuntimeError("ordinary user connected to root-only NM FD broker")


def invalid_root_request_denied() -> None:
    if os.geteuid() != 0:
        raise RuntimeError("invalid root-request probe requires the guest root")
    verify_socket_directory()
    descriptor = os.open("/dev/null", os.O_RDONLY | os.O_CLOEXEC)
    try:
        with socket.socket(socket.AF_UNIX, socket.SOCK_SEQPACKET) as peer:
            peer.settimeout(5)
            peer.connect(str(SOCKET))
            sent = peer.sendmsg(
                [b"{}"],
                [(socket.SOL_SOCKET, socket.SCM_RIGHTS,
                  array.array("i", [descriptor]))],
            )
            if sent != 2:
                raise RuntimeError("SCM_RIGHTS negative request was incomplete")
            try:
                reply = peer.recv(64)
            except (ConnectionResetError, BrokenPipeError):
                reply = b""
            if reply:
                raise RuntimeError("broker acknowledged invalid NM authority")
    finally:
        os.close(descriptor)
    print("nm-broker-invalid-root-authority=denied")


def main() -> None:
    if len(sys.argv) == 1:
        ordinary_user_denied()
    elif sys.argv[1:] == ["reject-invalid-root-authority"]:
        invalid_root_request_denied()
    else:
        raise SystemExit("invalid private broker probe")


if __name__ == "__main__":
    main()
