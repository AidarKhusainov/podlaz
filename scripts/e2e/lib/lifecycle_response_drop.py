#!/usr/bin/env python3
"""Drop an installed CLI lifecycle response only after the daemon replies 200.

This is a disposable-guest, one-shot test relay. It has no production consumer,
does not log request or response material, and runs with the invoking user's UID.
"""

import http.client
import os
import socket
import socketserver
import sys
from http.server import BaseHTTPRequestHandler
from pathlib import Path

MAX_REQUEST = 64 * 1024
MAX_RESPONSE = 256 * 1024


class UnixConnection(http.client.HTTPConnection):
    def __init__(self, path: str):
        super().__init__("podlazd", timeout=180)
        self.socket_path = path

    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(self.timeout)
        self.sock.connect(self.socket_path)


class RelayServer(socketserver.UnixStreamServer):
    timeout = 180
    completed = False
    failed = False


class RelayHandler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, _format, *_args):
        # Request bodies carry private profile and provider metadata.
        pass

    def do_GET(self):
        self.relay()

    def do_POST(self):
        self.relay()

    def relay(self):
        if self.path not in ("/v1/status", "/v1/connect"):
            self.send_error(404)
            return
        if (self.command, self.path) not in (
            ("GET", "/v1/status"),
            ("POST", "/v1/connect"),
        ):
            self.send_error(405)
            return

        length = self.headers.get("Content-Length", "0")
        if not length.isdigit() or int(length) > MAX_REQUEST:
            self.send_error(413)
            return
        body = self.rfile.read(int(length)) if int(length) else None
        upstream = UnixConnection(self.server.backend_path)
        try:
            upstream.request(
                self.command,
                self.path,
                body=body,
                headers={"Content-Type": "application/json"} if body else {},
            )
            response = upstream.getresponse()
            payload = response.read(MAX_RESPONSE + 1)
            if len(payload) > MAX_RESPONSE:
                raise ValueError("daemon lifecycle response exceeded test bound")
            if self.command == "POST" and response.status == 200:
                marker = self.server.committed_path
                fd = os.open(str(marker), os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
                with os.fdopen(fd, "w", encoding="utf-8") as output:
                    output.write("committed\n")
                self.server.completed = True
                self.close_connection = True
                try:
                    self.connection.shutdown(socket.SHUT_RDWR)
                except OSError:
                    pass
                return

            self.send_response(response.status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(payload)))
            self.send_header("Connection", "close")
            self.end_headers()
            self.wfile.write(payload)
            self.close_connection = True
            if self.command == "POST":
                self.server.failed = True
                self.server.completed = True
        finally:
            upstream.close()


def main():
    if len(sys.argv) != 4:
        raise SystemExit("usage: lifecycle_response_drop.py SOCKET DAEMON_SOCKET MARKER")
    listener_path, backend_path, marker_path = map(Path, sys.argv[1:])
    if listener_path.exists() or listener_path.is_symlink() or marker_path.exists():
        raise SystemExit("test socket or marker already exists")
    with RelayServer(str(listener_path), RelayHandler) as server:
        server.backend_path = str(backend_path)
        server.committed_path = marker_path
        os.chmod(listener_path, 0o600)
        try:
            for _ in range(6):
                server.handle_request()
                if server.completed:
                    break
            if not server.completed or server.failed:
                raise SystemExit("daemon did not commit through fault relay")
        finally:
            listener_path.unlink(missing_ok=True)


if __name__ == "__main__":
    main()
