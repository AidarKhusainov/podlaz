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
NEXT_IPV4 = "172.31.253.2"
EXTERNAL_DNS_PORT = 53535
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
        self.pinned_path.with_name("nm-l3-next-xray.json").unlink(missing_ok=True)

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

    def write_pinned_generation(self, *, address: str | None = None,
                                output: Path | None = None, allow_changed_dns: bool = False) -> Path:
        """Research-only immutable dial target, NOT an arbitrary profile gateway extractor.

        The source is the one generated VMess outbound from this disposable
        fixture. NM's already-known gateway is *not* derived from an observed
        TCP peer or arbitrary provider JSON.
        """
        if not allow_changed_dns:
            self.assert_single_gateway()
        target = address if address is not None else self.gateway
        if target not in (self.gateway, NEXT_IPV4):
            raise ValueError("unqualified provider target")
        destination = output if output is not None else self.pinned_path
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
        settings["address"] = target
        fd = os.open(destination,
                     os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, "w") as handle:
            json.dump(data, handle)
        return destination

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

    def resolve_external_marked(self, mark: int) -> str:
        """Resolve the exact synthetic domain via a protected marked underlay."""
        transaction = b"\x41\x62"
        question = b"".join(bytes((len(label),)) + label.encode("ascii")
                            for label in HOSTNAME.split(".")) + b"\x00"
        request = (transaction + b"\x01\x00\x00\x01"
                   + b"\x00\x00\x00\x00\x00\x00"
                   + question + b"\x00\x01\x00\x01")
        with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as sock:
            sock.setsockopt(socket.SOL_SOCKET, socket.SO_MARK, mark)
            sock.settimeout(5)
            sock.sendto(request, (NEXT_IPV4, EXTERNAL_DNS_PORT))
            result, source = sock.recvfrom(512)
        if source != (NEXT_IPV4, EXTERNAL_DNS_PORT):
            raise ValueError("unrecognized protected DNS peer")
        if len(result) != len(request) + 16 or result[:2] != transaction:
            raise ValueError("invalid protected DNS transaction")
        if (result[2:4] != b"\x81\x80" or result[4:8] != b"\x00\x01\x00\x01"
                or result[8:12] != b"\x00\x00\x00\x00"
                or result[12:len(request)] != question + b"\x00\x01\x00\x01"):
            raise ValueError("ambiguous protected DNS answer")
        answer = result[len(request):]
        if (answer[:12] != b"\xc0\x0c\x00\x01\x00\x01\x00\x00\x00\x00\x00\x04"):
            raise ValueError("unsupported DNS answer structure")
        resolved = str(ipaddress.IPv4Address(answer[12:]))
        if resolved != NEXT_IPV4:
            raise ValueError("DNS answer does not match real second fixture")
        return resolved

    def verify_actual_peer(self, pid: int, gateway: str | None = None) -> None:
        # Observe Xray's actual remote TCP peer rather than equating the DNS
        # snapshot with the transport. The provider server uses real TCP.
        observed = subprocess.run(("ss", "-H", "-t", "-n", "-p"),
                                  capture_output=True, text=True, check=True, timeout=10).stdout
        peers = [line for line in observed.splitlines()
                 if "pid=" + str(pid) + "," in line and ":" + str(self.port) in line]
        if not peers:
            raise ValueError("no real tracked Xray transport socket to verify")
        wanted = self.gateway if gateway is None else gateway
        if any(wanted + ":" + str(self.port) not in line for line in peers):
            raise ValueError("NM gateway differs from observed Xray transport peer")


@contextmanager
def hostname_fixture(source: Path, gateway: str):
    with HostnameFixture(source, gateway) as fixture:
        yield fixture

class _ExternalDNSHandler(_DNSHandler):
    def handle(self) -> None:
        # Do not forward unexpected queries to the outer runner's resolver.
        packet = self.request[0]
        if b"\x0epodlaz-gateway\x07example\x00" not in packet:
            return
        super().handle()


def _serve_external_dns(address: str, port: int) -> None:
    if address != NEXT_IPV4 or port != EXTERNAL_DNS_PORT:
        raise SystemExit("external DNS requires reserved disposable endpoint")
    with _Server((address, port), _ExternalDNSHandler) as server:
        server.records = ((NEXT_IPV4,), (), False)
        server.serve_forever()


if __name__ == "__main__":
    import sys
    if len(sys.argv) != 4 or sys.argv[1] != "--external-dns":
        raise SystemExit(2)
    _serve_external_dns(sys.argv[2], int(sys.argv[3]))
