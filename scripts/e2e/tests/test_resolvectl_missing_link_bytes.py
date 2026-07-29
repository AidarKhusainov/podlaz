from __future__ import annotations

import importlib.util
import tempfile
import unittest
from pathlib import Path

HELPER = Path(__file__).resolve().parents[1] / "verify_resolvectl_missing_link.py"
SCRIPT = Path(__file__).resolve().parents[1] / "tun-package-convergence.sh"
MARKER = b'Failed to resolve interface "podlaz0": No such device'


def load_helper():
    spec = importlib.util.spec_from_file_location("verify_resolvectl_missing_link", HELPER)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"cannot load {HELPER}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class ResolvectlMissingLinkBytesTests(unittest.TestCase):
    def test_accepts_only_one_lf_or_one_crlf(self) -> None:
        helper = load_helper()
        self.assertTrue(helper.is_exact_missing_link_stderr(MARKER + b"\n"))
        self.assertTrue(helper.is_exact_missing_link_stderr(MARKER + b"\r\n"))

    def test_rejects_incompatible_raw_stderr(self) -> None:
        helper = load_helper()
        rejected = {
            "extra blank line": MARKER + b"\n\n",
            "embedded newline": b'Failed to resolve\n interface "podlaz0": No such device\n',
            "unterminated": MARKER,
            "multiple LF terminators": MARKER + b"\n\n\n",
            "multiple CRLF terminators": MARKER + b"\r\n\r\n",
            "mixed terminators": MARKER + b"\r\n\n",
        }
        for name, raw in rejected.items():
            with self.subTest(name=name):
                self.assertFalse(helper.is_exact_missing_link_stderr(raw))

    def test_accepts_only_exact_complete_capture(self) -> None:
        helper = load_helper()
        self.assertTrue(
            helper.is_exact_missing_link_result(b"1\n", b"", MARKER + b"\n")
        )
        self.assertTrue(
            helper.is_exact_missing_link_result(b"1\n", b"", MARKER + b"\r\n")
        )
        rejected = {
            "unterminated exit code": (b"1", b"", MARKER + b"\n"),
            "extra exit-code line": (b"1\n2\n", b"", MARKER + b"\n"),
            "non-empty stdout": (b"1\n", b" ", MARKER + b"\n"),
            "wrong stderr": (b"1\n", b"", MARKER + b"\n\n"),
        }
        for name, result in rejected.items():
            with self.subTest(name=name):
                self.assertFalse(helper.is_exact_missing_link_result(*result))

    def test_cli_capture_mode_reads_all_three_files_byte_exactly(self) -> None:
        helper = load_helper()
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            exit_code = root / "dns-rollback.exit-code"
            stdout = root / "dns-rollback.stdout"
            stderr = root / "dns-rollback.stderr"
            exit_code.write_bytes(b"1\n")
            stdout.write_bytes(b"")
            stderr.write_bytes(MARKER + b"\n")
            self.assertEqual(helper.main([str(HELPER), str(stderr)]), 0)

            exit_code.write_bytes(b"1\n2\n")
            self.assertEqual(helper.main([str(HELPER), str(stderr)]), 1)

    def test_package_probe_uses_byte_exact_helper_without_tr_normalization(self) -> None:
        text = SCRIPT.read_text(encoding="utf-8")
        start = text.index("run_missing_link_probe() {")
        end = text.index("\n}\n\nsetup_isolated_xdg", start)
        body = text[start:end]
        self.assertIn("verify_resolvectl_missing_link.py", body)
        self.assertIn('"${DNS_ROLLBACK_STDERR}"', body)
        self.assertNotIn("tr -d '\\r\\n'", body)


if __name__ == "__main__":
    unittest.main()
