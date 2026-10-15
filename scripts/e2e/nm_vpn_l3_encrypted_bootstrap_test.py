"""Negative trust/response contracts for guest-only authenticated DNS bootstrap."""
from pathlib import Path
import socket
from types import SimpleNamespace
import unittest
from unittest.mock import patch

from nm_vpn_l3_encrypted_bootstrap import (
    SERVER_IP, SERVER_NAME, PORT, make_query, answer_query, parse_answer,
    parse_https_response, resolve_marked_doh,
)


def https_response(payload: bytes) -> bytes:
    return (b"HTTP/1.0 200 OK\r\nContent-Type: application/dns-message\r\n"
            + b"Content-Length: " + str(len(payload)).encode("ascii")
            + b"\r\nCache-Control: no-store\r\n\r\n" + payload)


class ProtectedBootstrapTest(unittest.TestCase):
    def test_exact_a_answer_and_no_untrusted_resolution_fallback(self):
        query = make_query(b"\x46\x21")
        reply = answer_query(query)
        self.assertEqual(parse_answer(query, reply), SERVER_IP)
        self.assertEqual(parse_https_response(query, https_response(reply)), SERVER_IP)
        for wrong in (reply[:-1], reply[:-4] + socket.inet_aton("172.31.253.1"),
                      b"\x11\x11" + reply[2:], reply + b"\x00"):
            with self.subTest(wrong=wrong[:3]), self.assertRaises(ValueError):
                parse_answer(query, wrong)
        with self.assertRaises(ValueError):
            make_query(b"\x01")
        with self.assertRaises(ValueError):
            answer_query(query[:-1])

    def test_http_requires_exact_success_content_length_and_type(self):
        query = make_query(b"\x55\x66")
        reply = answer_query(query)
        good = https_response(reply)
        for wrong in (
            good.replace(b"200 OK", b"302 Found"),
            good.replace(b"application/dns-message", b"text/plain"),
            good.replace(b"Content-Length: ", b"Content-Length: 1000"),
            good.replace(b"\r\n\r\n", b"\r\nTransfer-Encoding: chunked\r\n\r\n"),
            good + b"\r\n",
            b"HTTP/1.0 200 OK\r\n\r\n" + reply,
        ):
            with self.subTest(wrong=wrong[:40]), self.assertRaises(ValueError):
                parse_https_response(query, wrong)

    def test_so_mark_is_set_before_tls_and_exact_hostname_is_authenticated(self):
        events = []
        class RawSocket:
            def __enter__(self):
                return self
            def __exit__(self, *args):
                return False
            def setsockopt(self, family, option, mark):
                events.append(("mark", family, option, mark))
            def settimeout(self, seconds):
                events.append(("timeout", seconds))
            def connect(self, target):
                events.append(("connect", target))
        class TLSStream:
            def __enter__(self):
                return self
            def __exit__(self, *args):
                return False
            def settimeout(self, seconds):
                pass
            def sendall(self, message):
                self.query = message.partition(b"\r\n\r\n")[2]
                events.append(("sent", message[:45]))
            def recv(self, size):
                if not hasattr(self, "returned"):
                    self.returned = True
                    return https_response(answer_query(self.query))
                return b""
        raw = RawSocket()
        tls = TLSStream()
        class Context:
            def __init__(self):
                self.check_hostname = False
                self.verify_mode = None
            def wrap_socket(self, value, server_hostname):
                self.assertion(value is raw)
                self.assertion(server_hostname == SERVER_NAME)
                self.assertion(self.check_hostname is True)
                self.assertion(self.verify_mode is not None)
                return tls
        context = Context()
        context.assertion = self.assertTrue
        with patch("nm_vpn_l3_encrypted_bootstrap.socket.socket", return_value=raw), patch(
             "nm_vpn_l3_encrypted_bootstrap.ssl.create_default_context",
             return_value=context) as ca:
            self.assertEqual(resolve_marked_doh(20570, ca_file=Path("/private/bootstrap.crt")),
                             SERVER_IP)
        ca.assert_called_once_with(cafile="/private/bootstrap.crt")
        self.assertEqual(events[0], ("mark", socket.SOL_SOCKET, socket.SO_MARK, 20570))
        self.assertIn(("connect", (SERVER_IP, PORT)), events)

    def test_missing_trusted_ca_fails_before_opening_any_socket(self):
        with patch("nm_vpn_l3_encrypted_bootstrap.socket.socket") as sock:
            with self.assertRaises(OSError):
                resolve_marked_doh(19022, ca_file=Path("/missing/required-ca.crt"))
            sock.assert_not_called()


if __name__ == "__main__":
    unittest.main()
