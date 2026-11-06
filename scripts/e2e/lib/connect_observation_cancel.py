#!/usr/bin/env python3
"""Owned guest relay: lose the client AND cancel the backend after Query starts."""
import os
from pathlib import Path
import socket
import sys
import time
from http.server import BaseHTTPRequestHandler
from lifecycle_response_drop import RelayServer, UnixConnection, MAX_REQUEST, MAX_RESPONSE


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_args):
        pass

    def do_GET(self):
        upstream = UnixConnection('@podlazd')
        try:
            upstream.request('GET', '/v1/status')
            response = upstream.getresponse()
            payload = response.read(MAX_RESPONSE)
            self.send_response(response.status)
            self.send_header('Content-Length', str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)
        finally:
            upstream.close()

    def do_POST(self):
        size = int(self.headers['Content-Length'])
        assert self.path == '/v1/connect' and 0 < size <= MAX_REQUEST
        upstream = UnixConnection('@podlazd')
        try:
            upstream.request('POST', self.path, self.rfile.read(size), {'Content-Type': 'application/json'})
            deadline = time.monotonic() + 30
            while not Path(sys.argv[2]).exists():
                if time.monotonic() >= deadline:
                    raise TimeoutError('query boundary not reached')
                time.sleep(0.02)
            upstream.sock.shutdown(socket.SHUT_RDWR)
            self.connection.shutdown(socket.SHUT_RDWR)
            self.close_connection = True
            self.server.completed = True
        finally:
            upstream.close()


if __name__ == '__main__':
    with RelayServer(sys.argv[1], Handler) as server:
        os.chmod(sys.argv[1], 0o600)
        for _ in range(6):
            server.handle_request()
            if server.completed:
                break
        assert server.completed
