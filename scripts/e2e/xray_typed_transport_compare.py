#!/usr/bin/env python3
"""Isolated, disposable Xray client/server transport comparison.

No Podlaz host-network mutations. All test credentials are synthetic and private.
Only protocol/version/outcome labels are printed to CI.
"""
import http.server
import json
import os
import re
from pathlib import Path
import socket
import subprocess
import sys
import tempfile
import threading
import time


def port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


class Handler(http.server.BaseHTTPRequestHandler):
    requests = 0
    replies = 0
    protocol_version = "HTTP/1.1"

    def do_GET(self):
        Handler.requests += 1
        body = b"synthetic transport comparison\n"
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Connection", "close")
        self.end_headers()
        self.wfile.write(body)
        self.wfile.flush()
        Handler.replies += 1

    def log_message(self, *_args):
        pass


def wait_listen(port_number, process, host="127.0.0.1"):
    # Opening an unauthenticated TCP socket into VMess/Trojan can be treated as
    # active probing. Observe LISTEN only; never touch protocol handshake state.
    target = f"{host}:{port_number}"
    for _ in range(100):
        if process.poll() is not None:
            return False
        listing = subprocess.run(["ss", "-H", "-ltn"], capture_output=True, text=True)
        if listing.returncode == 0 and any(
            line.split()[3] == target for line in listing.stdout.splitlines()
            if len(line.split()) >= 4
        ):
            return True
        time.sleep(0.05)
    return False


def test(binary, protocol, root):
    from uuid import uuid4

    password = "synthetic-" + uuid4().hex
    endpoint, socks, backend = port(), port(), port()
    endpoint_host = "127.0.0.2"
    cert = root / "server.crt"
    key = root / "server.key"
    if protocol == "shadowsocks":
        inbound_settings = {"method": "aes-128-gcm", "password": password, "network": "tcp,udp"}
        outbound_settings = {"address": endpoint_host, "port": endpoint,
                             "method": "aes-128-gcm", "password": password}
        server_stream = client_stream = {"network": "raw", "security": "none"}
    elif protocol == "trojan":
        subprocess.run(
            ["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes",
             "-sha256", "-days", "1", "-keyout", str(key), "-out", str(cert),
             "-subj", "/CN=trojan.example.com",
             "-addext", "subjectAltName=DNS:trojan.example.com",
             "-addext", "basicConstraints=critical,CA:TRUE"],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=True,
        )
        inbound_settings = {"users": [{"password": password}]}
        outbound_settings = {"address": endpoint_host, "port": endpoint, "password": password}
        server_stream = {"network": "raw", "security": "tls", "tlsSettings": {
            "certificates": [{"certificateFile": str(cert), "keyFile": str(key)}]}}
        client_stream = {"network": "raw", "security": "tls", "tlsSettings": {
            "serverName": "trojan.example.com",
            "certificates": [{"usage": "verify", "certificateFile": str(cert)}]}}
    else:
        identifier = str(uuid4())
        inbound_settings = {"users": [{"id": identifier, "level": 0}]}
        outbound_settings = {"address": endpoint_host, "port": endpoint,
                             "id": identifier, "security": "auto"}
        server_stream = client_stream = {"network": "raw", "security": "none"}

    Handler.requests = Handler.replies = 0
    server_config = {"log": {"loglevel": "debug"},
                     "inbounds": [{"listen": endpoint_host, "port": endpoint,
                                   "protocol": protocol, "settings": inbound_settings,
                                   "streamSettings": server_stream}],
                     "outbounds": [{"protocol": "freedom", "settings": {}}]}
    client_config = {"log": {"loglevel": "debug"},
                     "inbounds": [{"listen": "127.0.0.1", "port": socks,
                                   "protocol": "socks", "settings": {"auth": "noauth"}}],
                     "outbounds": [{"protocol": protocol, "settings": outbound_settings,
                                    "streamSettings": client_stream}]}
    server_path, client_path = root / "server.json", root / "client.json"
    server_path.write_text(json.dumps(server_config))
    client_path.write_text(json.dumps(client_config))
    for path in (server_path, client_path):
        path.chmod(0o600)
        check = subprocess.run([binary, "run", "-test", "-config", str(path)],
                               stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        if check.returncode != 0:
            print(f"{protocol}.config=fail")
            return False

    httpd = http.server.ThreadingHTTPServer(("127.0.0.1", backend), Handler)
    thread = threading.Thread(target=httpd.serve_forever, daemon=True)
    thread.start()
    with open(root / "server.log", "w") as server_log, open(root / "client.log", "w") as client_log:
        server_proc = subprocess.Popen([binary, "run", "-config", str(server_path)],
                                       stdout=server_log, stderr=subprocess.STDOUT)
        client_proc = subprocess.Popen([binary, "run", "-config", str(client_path)],
                                       stdout=client_log, stderr=subprocess.STDOUT)
        try:
            if not wait_listen(endpoint, server_proc, endpoint_host) or not wait_listen(socks, client_proc):
                print(f"{protocol}.listeners=fail")
                return False
            url = f"http://127.0.0.1:{backend}/"
            command = ["curl", "--fail", "--silent", "--show-error", "--max-time", "10",
                       "--noproxy", "", "--socks5-hostname", f"127.0.0.1:{socks}", url]
            result = subprocess.run(command, capture_output=True, timeout=13)
            success = result.returncode == 0 and result.stdout == b"synthetic transport comparison\n"
            print(f"{protocol}.data_plane={'pass' if success else 'fail'}")
            if not success:
                print(f"{protocol}.curl_exit={result.returncode}")
                print(f"{protocol}.backend_get={Handler.requests}")
                print(f"{protocol}.backend_reply_written={Handler.replies}")
                for role in ("server", "client"):
                    source = (root / (role + ".log")).read_text(errors="replace").lower()
                    for label, token in (
                        ("context_cancel", "context canceled"),
                        ("connection_end", "connection ends"),
                        ("failed", "failed"),
                        ("eof", "eof"),
                        ("inbound", "inbound"),
                        ("outbound", "outbound"),
                    ):
                        print(f"{protocol}.{role}.{label}={source.count(token)}")
                    diagnostic_lines = []
                    for raw in source.splitlines():
                        if not any(word in raw for word in ("failed", "connection ends", "rejected", "error")):
                            continue
                        message = re.sub(r"[0-9a-f]{8}-[0-9a-f-]{27,}", "<uuid>", raw)
                        message = re.sub(r"[0-9a-f]{18,}", "<token>", message)
                        message = re.sub(r"(?:[0-9]{1,3}\\.){3}[0-9]{1,3}(?::[0-9]+)?", "<ip>", message)
                        message = re.sub(r"/[^ \\t>]+", "<path>", message)
                        diagnostic_lines.append(message[-260:])
                    for message in diagnostic_lines[:5]:
                        print(f"{protocol}.{role}.diagnostic={message}")
            return success
        finally:
            for proc in (client_proc, server_proc):
                proc.terminate()
            for proc in (client_proc, server_proc):
                try:
                    proc.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    proc.kill()
                    proc.wait()
            httpd.shutdown()


def main():
    if len(sys.argv) != 2:
        return 2
    binary = str(Path(sys.argv[1]).resolve())
    with tempfile.TemporaryDirectory(prefix="podlaz-xray-compare-") as tmp:
        os.chmod(tmp, 0o700)
        for protocol in ("shadowsocks", "vmess", "trojan"):
            root = Path(tmp) / protocol
            root.mkdir(mode=0o700)
            test(binary, protocol, root)
    return 0  # Comparison reports are diagnostic, not the permanent acceptance gate.


if __name__ == "__main__":
    raise SystemExit(main())
