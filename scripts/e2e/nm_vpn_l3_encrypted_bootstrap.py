#!/usr/bin/env python3
"""Guest-only authenticated DNS bootstrap over a marked TLS connection.

Never used by product networking. The server and CA live only on disposable
hosted nspawn infrastructure; no public resolver or direct DNS egress.
"""
from __future__ import annotations

import http.server
import ipaddress
import os
from pathlib import Path
import socket
import ssl
import sys
import threading

SERVER_IP = "172.31.253.2"
INITIAL_IP = "172.31.253.1"
SERVER_NAME = "bootstrap.podlaz.example"
VPN_HOSTNAME = "podlaz-gateway.example"
PORT = 53535
CA = Path("/run/podlaz-synthetic-xray/bootstrap-ca.crt")
NAME = b"".join(bytes((len(part),)) + part.encode("ascii")
                for part in VPN_HOSTNAME.split(".")) + b"\x00"
QUESTION = NAME + b"\x00\x01\x00\x01"


def make_query(txid: bytes) -> bytes:
    if len(txid) != 2:
        raise ValueError("invalid DNS transaction identity")
    return txid + b"\x01\x00\x00\x01\x00\x00\x00\x00\x00\x00" + QUESTION


def answer_query(query: bytes, *, address: str = SERVER_IP) -> bytes:
    if (len(query) != 12 + len(QUESTION) or
            query[2:12] != b"\x01\x00\x00\x01\x00\x00\x00\x00\x00\x00" or
            query[12:] != QUESTION):
        raise ValueError("unsupported or malformed bootstrap DNS query")
    if address not in (INITIAL_IP, SERVER_IP):
        raise ValueError("unsupported fixture DNS destination")
    return (query[:2] + b"\x81\x80\x00\x01\x00\x01\x00\x00\x00\x00\x00\x00"
            + QUESTION + b"\xc0\x0c\x00\x01\x00\x01\x00\x00\x00\x00\x00\x04"
            + ipaddress.IPv4Address(address).packed)


def parse_answer(query: bytes, answer: bytes) -> str:
    for address in (INITIAL_IP, SERVER_IP):
        if answer == answer_query(query, address=address):
            return address
    raise ValueError("untrusted, ambiguous or unsupported DNS bootstrap answer")


def parse_https_response(query: bytes, response: bytes) -> str:
    if len(response) > 4096:
        raise ValueError("oversized TLS DNS response")
    headers, sep, data = response.partition(b"\r\n\r\n")
    if not sep or not headers.startswith(b"HTTP/1.0 200 "):
        raise ValueError("protected DNS-over-HTTPS server response failed")
    lengths = []
    content_types = []
    for item in headers.split(b"\r\n")[1:]:
        name, sep, value = item.partition(b":")
        if not sep:
            raise ValueError("invalid DoH response header")
        name = name.strip().lower()
        if name == b"content-length":
            lengths.append(value.strip())
        if name == b"content-type":
            content_types.append(value.strip().lower())
        if name == b"transfer-encoding":
            raise ValueError("chunked bootstrap DNS unsupported")
    if lengths != [str(len(data)).encode("ascii")] or content_types != [b"application/dns-message"]:
        raise ValueError("unqualified DoH response metadata")
    return parse_answer(query, data)


def resolve_marked_doh(mark: int, *, ca_file: Path = CA) -> str:
    """Only fixture's fixed trusted DoH IP, with CA+SNI authentication.

    The egress mark is applied before connect; there is no fallback to the
    system resolver or raw DNS when the protected DoH request fails.
    """
    if type(mark) is not int or mark <= 0:
        raise ValueError("invalid private transport mark")
    query = make_query(os.urandom(2))
    request = (b"POST /dns-query HTTP/1.0\r\nHost: " + SERVER_NAME.encode("ascii")
               + b"\r\nAccept: application/dns-message\r\nContent-Type: application/dns-message\r\n"
               + b"Content-Length: " + str(len(query)).encode("ascii")
               + b"\r\nConnection: close\r\n\r\n" + query)
    context = ssl.create_default_context(cafile=str(ca_file))
    context.check_hostname = True
    context.verify_mode = ssl.CERT_REQUIRED
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as raw:
        raw.setsockopt(socket.SOL_SOCKET, socket.SO_MARK, mark)
        raw.settimeout(8)
        raw.connect((SERVER_IP, PORT))
        with context.wrap_socket(raw, server_hostname=SERVER_NAME) as tls:
            tls.settimeout(8)
            tls.sendall(request)
            chunks = []
            received = 0
            while True:
                chunk = tls.recv(1024)
                if not chunk:
                    break
                received += len(chunk)
                if received > 4096:
                    raise ValueError("oversized protected DNS response")
                chunks.append(chunk)
    return parse_https_response(query, b"".join(chunks))


class _DoHHandler(http.server.BaseHTTPRequestHandler):
    def do_POST(self) -> None:
        if (self.path != "/dns-query" or
                self.headers.get("Content-Type") != "application/dns-message"):
            self.send_error(400)
            return
        size = self.headers.get("Content-Length", "")
        if not size.isdecimal() or int(size) != 12 + len(QUESTION):
            self.send_error(400)
            return
        try:
            question = self.rfile.read(int(size))
            answer = answer_query(question, address=self.server.next_dns_ip())
        except ValueError:
            self.send_error(400)
            return
        self.send_response(200)
        self.send_header("Content-Type", "application/dns-message")
        self.send_header("Content-Length", str(len(answer)))
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        self.wfile.write(answer)

    def log_message(self, _format: str, *args: object) -> None:
        # Never print DNS queries or private guest identifiers.
        pass


class _DoHServer(http.server.ThreadingHTTPServer):
    def __init__(self, address: tuple[str, int]):
        super().__init__(address, _DoHHandler)
        self._query_lock = threading.Lock()
        self._query_count = 0

    def next_dns_ip(self) -> str:
        with self._query_lock:
            self._query_count += 1
            return INITIAL_IP if self._query_count == 1 else SERVER_IP


def serve(ip: str, port: int, certificate: Path, key: Path) -> None:
    if ip != SERVER_IP or port != PORT:
        raise ValueError("DoH fixture must bind only disposable endpoint")
    ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    ctx.load_cert_chain(certfile=str(certificate), keyfile=str(key))
    with _DoHServer((ip, port)) as server:
        server.socket = ctx.wrap_socket(server.socket, server_side=True)
        server.serve_forever()


if __name__ == "__main__":
    if len(sys.argv) != 6 or sys.argv[1] != "--serve":
        raise SystemExit(2)
    serve(sys.argv[2], int(sys.argv[3]), Path(sys.argv[4]), Path(sys.argv[5]))
