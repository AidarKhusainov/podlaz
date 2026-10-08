#!/usr/bin/env python3
"""Isolated, disposable Xray client/server transport comparison.

No Podlaz host-network mutations. All test credentials are synthetic and private.
Only protocol/version/outcome labels are printed to CI.
"""
import http.server
import json
import os
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
    protocol_version = "HTTP/1.1"

    def do_GET(self):
        body = b"synthetic transport comparison\n"
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Connection", "close")
        self.end_headers()
        self.wfile.write(body)
        self.wfile.flush()

    def log_message(self, *_args):
        pass


def wait_listen(port_number, process):
    for _ in range(100):
        if process.poll() is not None:
            return False
        try:
            with socket.create_connection(("127.0.0.1", port_number), 0.1):
                return True
        except OSError:
            time.sleep(0.05)
    return False


def test(binary, protocol, root):
    from uuid import uuid4

    password = "synthetic-" + uuid4().hex
    endpoint, socks, backend = port(), port(), port()
    cert = root / "server.crt"
    key = root / "server.key"
    if protocol == "trojan":
        subprocess.run(
            ["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes",
             "-sha256", "-days", "1", "-keyout", str(key), "-out", str(cert),
             "-subj", "/CN=trojan.example.com",
             "-addext", "subjectAltName=DNS:trojan.example.com",
             "-addext", "basicConstraints=critical,CA:TRUE"],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=True,
        )
        inbound_settings = {"users": [{"password": password}]}
        outbound_settings = {"address": "127.0.0.1", "port": endpoint, "password": password}
        server_stream = {"network": "raw", "security": "tls", "tlsSettings": {
            "certificates": [{"certificateFile": str(cert), "keyFile": str(key)}]}}
        client_stream = {"network": "raw", "security": "tls", "tlsSettings": {
            "serverName": "trojan.example.com",
            "certificates": [{"usage": "verify", "certificateFile": str(cert)}]}}
    else:
        identifier = str(uuid4())
        inbound_settings = {"users": [{"id": identifier, "level": 0}]}
        outbound_settings = {"address": "127.0.0.1", "port": endpoint,
                             "id": identifier, "security": "auto"}
        server_stream = client_stream = {"network": "raw", "security": "none"}

    server_config = {"log": {"loglevel": "warning"},
                     "inbounds": [{"listen": "127.0.0.1", "port": endpoint,
                                   "protocol": protocol, "settings": inbound_settings,
                                   "streamSettings": server_stream}],
                     "outbounds": [{"protocol": "freedom", "settings": {}}]}
    client_config = {"log": {"loglevel": "warning"},
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
            if not wait_listen(endpoint, server_proc) or not wait_listen(socks, client_proc):
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
        for protocol in ("vmess", "trojan"):
            root = Path(tmp) / protocol
            root.mkdir(mode=0o700)
            test(binary, protocol, root)
    return 0  # Comparison reports are diagnostic, not the permanent acceptance gate.


if __name__ == "__main__":
    raise SystemExit(main())
