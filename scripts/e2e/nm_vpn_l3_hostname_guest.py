#!/usr/bin/env python3
"""Disposable-guest hostname DNS experiment, never product gateway authority.

The loopback DNS fixture intercepts only one RFC-reserved example hostname and
forwards unrelated queries to the existing systemd-resolved stub. It cannot
prove that a DNS snapshot equals a subsequent Xray dial.
"""
from __future__ import annotations

from contextlib import contextmanager
import ipaddress
import json
import os
from pathlib import Path
import socket
import socketserver
import subprocess
import threading

HOSTNAME = "podlaz-gateway.example"
DNS_ADDR = "127.0.0.55"
DNS_UPSTREAM = "127.0.0.53"
OTHER_IPV4 = "172.31.254.1"
OTHER_IPV6 = "2001:db8:460::1"
RESOLV = Path("/etc/resolv.conf")


class _DNSHandler(socketserver.BaseRequestHandler):
    def handle(self) -> None:
        packet, sock = self.request
        if len(packet) < 17 or packet[2] & 0x80:
            return
        pos = 12
        labels = []
        while pos < len(packet) and packet[pos]:
            n = packet[pos]
            if n > 63 or pos + 1 + n >= len(packet):
                return
            try:
                labels.append(packet[pos + 1:pos + 1 + n].decode("ascii").lower())
            except UnicodeDecodeError:
                return
            pos += 1 + n
        if pos + 5 > len(packet):
            return
        end = pos + 5
        qtype = int.from_bytes(packet[pos + 1:pos + 3], "big")
        qclass = int.from_bytes(packet[pos + 3:pos + 5], "big")
        if ".".join(labels) != HOSTNAME or qclass != 1:
            with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as relay:
                relay.settimeout(3)
                try:
                    relay.sendto(packet, (DNS_UPSTREAM, 53))
                    reply = relay.recv(4096)
                except socket.timeout:
                    return
            sock.sendto(reply, self.client_address)
            return
        records = self.server.records
        ips = records[0] if qtype == 1 else records[1] if qtype == 28 else ()
        answers = b""
        for ip in ips:
            address = ipaddress.ip_address(ip)
            answers += (b"\xc0\x0c" + qtype.to_bytes(2, "big") + b"\x00\x01"
                        + b"\x00\x00\x00\x00" + len(address.packed).to_bytes(2, "big")
                        + address.packed)
        response_code = 3 if records[2] else 0
        header = (packet[:2] + bytes((0x81, 0x80 | response_code))
                  + b"\x00\x01" + len(ips).to_bytes(2, "big")
                  + b"\x00\x00\x00\x00")
        sock.sendto(header + packet[12:end] + answers, self.client_address)


class _Server(socketserver.ThreadingUDPServer):
    allow_reuse_address = False
    daemon_threads = True


class HostnameFixture:
    def __init__(self, source: Path, gateway: str):
        self.source = source
        self.gateway = gateway
        self.server = None
        self.thread = None
        self.previous_link = None
        self.changed_resolver = False
        self.config_path = source.with_name("nm-l3-hostname-xray.json")
        self.pinned_path = source.with_name("nm-l3-pinned-xray.json")

    def __enter__(self) -> "HostnameFixture":
        if os.geteuid() != 0 or Path("/run/systemd/container").read_text().strip() != "systemd-nspawn":
            raise ValueError("only isolated root guest may run hostname fixture")
        if self.gateway != "172.31.253.1" or RESOLV.is_symlink() is False:
            raise ValueError("unexpected hostname-fixture network authority")
        self.previous_link = os.readlink(RESOLV)
        if self.previous_link != "/run/systemd/resolve/stub-resolv.conf":
            raise ValueError("unexpected system resolver identity")
        data = json.loads(self.source.read_text())
        outbounds = data.get("outbounds")
        if not isinstance(outbounds, list) or len(outbounds) != 1:
            raise ValueError("fixture requires one known typed outbound")
        outbound = outbounds[0]
        if (outbound.get("protocol") != "vmess" or
                outbound.get("settings", {}).get("address") != self.gateway or
                not isinstance(outbound.get("settings", {}).get("port"), int)):
            raise ValueError("fixture cannot bind generated provider address")
        outbound["settings"]["address"] = HOSTNAME
        # Do not change TLS/REALITY settings, marks, or resolution strategy.
        if "domainStrategy" in outbound.get("streamSettings", {}).get("sockopt", {}):
            raise ValueError("fixture requires unchanged AsIs resolution")
        fd = os.open(self.config_path, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, "w") as handle:
            json.dump(data, handle)
        self.port = outbound["settings"]["port"]
        try:
            self.server = _Server((DNS_ADDR, 53), _DNSHandler)
            self.server.records = ((self.gateway,), (), False)
            self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
            self.thread.start()
            RESOLV.unlink()
            RESOLV.write_text("nameserver " + DNS_ADDR + "\noptions timeout:1 attempts:1\n", encoding="ascii")
            self.changed_resolver = True
            self.assert_single_gateway()
            self.write_pinned_generation()
            return self
        except BaseException:
            self.__exit__(None, None, None)
            raise

    def __exit__(self, *_args: object) -> None:
        if self.changed_resolver:
            if RESOLV.is_symlink():
                raise ValueError("resolver identity unexpectedly changed")
            RESOLV.unlink()
            RESOLV.symlink_to(self.previous_link)
            # A temporary foreign resolv.conf is read by systemd-resolved.
            # Recreate the resolver after restoring the managed symlink;
            # the unchanged guest baseline comparator verifies the result.
            subprocess.run(("systemctl", "restart", "systemd-resolved.service"),
                           capture_output=True, text=True, check=True, timeout=30)
        if self.server:
            self.server.shutdown()
            self.server.server_close()
            self.thread.join(timeout=5)
        self.config_path.unlink(missing_ok=True)
        self.pinned_path.unlink(missing_ok=True)

    def answer(self, ipv4: tuple[str, ...], ipv6: tuple[str, ...] = (),
               *, fail: bool = False) -> None:
        self.server.records = (ipv4, ipv6, fail)

    def assert_single_gateway(self) -> None:
        # This is deliberately only a *necessary* admission check. DNS itself
        # is not authority for what the independent Xray dialer will select.
        try:
            addresses = {result[4][0] for result in
                         socket.getaddrinfo(HOSTNAME, self.port, family=socket.AF_UNSPEC,
                                            type=socket.SOCK_STREAM)}
        except socket.gaierror as exc:
            raise ValueError("hostname DNS resolution unavailable") from exc
        if addresses != {self.gateway}:
            raise ValueError("hostname DNS is ambiguous or has changed")

    def write_pinned_generation(self) -> None:
        """Research-only immutable dial target, NOT an arbitrary profile gateway extractor.

        The source is the one generated VMess outbound from this disposable
        fixture. NM's already-known gateway is *not* derived from an observed
        TCP peer or arbitrary provider JSON.
        """
        self.assert_single_gateway()
        data = json.loads(self.config_path.read_text())
        outbounds = data.get("outbounds")
        if not isinstance(outbounds, list) or len(outbounds) != 1:
            raise ValueError("unsupported multi-outbound generation")
        outbound = outbounds[0]
        settings = outbound.get("settings")
        stream = outbound.get("streamSettings")
        if (outbound.get("protocol") != "vmess" or not isinstance(settings, dict)
                or settings.get("address") != HOSTNAME or not isinstance(stream, dict)):
            raise ValueError("unsupported outbound generation")
        security = stream.get("security", "none")
        if security == "tls":
            tls = stream.get("tlsSettings")
            if not isinstance(tls, dict):
                raise ValueError("unqualified TLS server identity")
            name = tls.get("serverName")
            if name is None or name == "":
                tls["serverName"] = HOSTNAME
            elif not isinstance(name, str):
                raise ValueError("unqualified TLS server identity")
        elif security != "none":
            # REALITY/native/grouped configs require their own explicit
            # identity and routing qualification; never silently rewrite.
            raise ValueError("unsupported transport security for pinning")
        settings["address"] = self.gateway
        fd = os.open(self.pinned_path,
                     os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, "w") as handle:
            json.dump(data, handle)

    def install_tcp_syn_counter(self, table: str, mark: int) -> None:
        """Count actual marked provider TCP SYNs, not generic traffic."""
        subprocess.run(("nft", "add", "rule", "inet", table, "egress",
                        "meta", "mark", str(mark), "ip", "daddr", self.gateway,
                        "tcp", "dport", str(self.port), "tcp", "flags",
                        "&", "0x12", "==", "0x02",
                        "counter", "comment", "podlaz_nm_l3_provider_syn"),
                       capture_output=True, text=True, check=True, timeout=10)

    def tcp_syn_packets(self, table: str) -> int:
        result = subprocess.run(("nft", "-j", "list", "chain", "inet", table, "egress"),
                                capture_output=True, text=True, check=True, timeout=10)
        payload = json.loads(result.stdout)
        if not isinstance(payload, dict) or not isinstance(payload.get("nftables"), list):
            raise ValueError("missing real transport SYN counter inventory")
        matched = []
        for item in payload["nftables"]:
            rule = item.get("rule") if isinstance(item, dict) else None
            if not isinstance(rule, dict):
                continue
            if (rule.get("family"), rule.get("table"), rule.get("chain"),
                    rule.get("comment")) != ("inet", table, "egress",
                                             "podlaz_nm_l3_provider_syn"):
                continue
            expr = rule.get("expr")
            if not isinstance(expr, list):
                raise ValueError("unqualified real provider SYN rule")
            counts = [element["counter"].get("packets") for element in expr
                      if isinstance(element, dict) and isinstance(element.get("counter"), dict)]
            if len(counts) != 1 or type(counts[0]) is not int or counts[0] < 0:
                raise ValueError("invalid real provider SYN counter")
            matched.append(counts[0])
        if len(matched) != 1:
            raise ValueError("ambiguous real provider SYN counter")
        return matched[0]

    def qualify_negative_answers(self) -> None:
        # A changed singleton, multi-A, A+AAAA and NXDOMAIN must not be
        # admitted as the original immutable NM Config.gateway.
        for ipv4, ipv6, fail in (
            ((OTHER_IPV4,), (), False),
            ((self.gateway, OTHER_IPV4), (), False),
            ((self.gateway,), (OTHER_IPV6,), False),
            ((), (), True),
        ):
            self.answer(ipv4, ipv6, fail=fail)
            try:
                self.assert_single_gateway()
            except ValueError:
                pass
            else:
                raise ValueError("unproven DNS answer admitted as pinned VPN gateway")
        self.answer((self.gateway,))
        self.assert_single_gateway()

    def verify_actual_peer(self, pid: int) -> None:
        # Observe Xray's actual remote TCP peer rather than equating the DNS
        # snapshot with the transport. The provider server uses real TCP.
        observed = subprocess.run(("ss", "-H", "-t", "-n", "-p"),
                                  capture_output=True, text=True, check=True, timeout=10).stdout
        peers = [line for line in observed.splitlines()
                 if "pid=" + str(pid) + "," in line and ":" + str(self.port) in line]
        if not peers:
            raise ValueError("no real tracked Xray transport socket to verify")
        if any(self.gateway + ":" + str(self.port) not in line for line in peers):
            raise ValueError("NM gateway differs from observed Xray transport peer")


@contextmanager
def hostname_fixture(source: Path, gateway: str):
    with HostnameFixture(source, gateway) as fixture:
        yield fixture
