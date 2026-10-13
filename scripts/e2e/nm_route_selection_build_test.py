"""Contracts for the isolated distro-source build, without apt or networking."""
import subprocess
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).with_name('nm_route_selection_guest.sh')


class BuildSourceContract(unittest.TestCase):
    def generate(self, source: str) -> subprocess.CompletedProcess[str]:
        script = SCRIPT.read_text(encoding='utf-8')
        start = script.index('# BEGIN BUILD SOURCE INPUT\n') + len('# BEGIN BUILD SOURCE INPUT\n')
        end = script.index('# END BUILD SOURCE INPUT', start)
        return subprocess.run(
            ['bash', '-c', script[start:end]], input=source,
            capture_output=True, text=True, timeout=5, check=False,
        )

    def test_add_only_build_sources_from_known_archive(self):
        for suite in ('noble', 'resolute'):
            got = self.generate(f'deb http://archive.ubuntu.com/ubuntu {suite} main\n')
            self.assertEqual(got.returncode, 0, got.stderr)
            self.assertEqual(got.stdout,
                f'deb-src http://archive.ubuntu.com/ubuntu {suite} main\n'
                f'deb http://archive.ubuntu.com/ubuntu {suite} universe\n')

    def test_unexpected_archive_suite_or_ambiguous_input_fails_closed(self):
        valid = 'deb http://archive.ubuntu.com/ubuntu noble main\n'
        for line in (
            'deb http://private.example noble main\n',
            'deb http://archive.ubuntu.com/ubuntu unknown main\n',
            '', valid + valid,
        ):
            with self.subTest(source=line):
                self.assertNotEqual(self.generate(line).returncode, 0)

    def test_progress_filter_rejects_private_suffixes(self):
        script = SCRIPT.read_text(encoding='utf-8')
        start = script.index('report_progress() {')
        end = script.index('report_failure() {', start)
        with tempfile.TemporaryDirectory() as directory:
            log = Path(directory) / 'build.log'
            log.write_text(
                'nm-selector-build-progress=compile\n'
                'nm-selector-build-progress=compile private.example\n'
                'private.example\nnm-selector-build-exit=17\n', encoding='utf-8',
            )
            result = subprocess.run(
                ['bash', '-c', 'build_log="$1"; ' + script[start:end] + '\nreport_progress',
                 'test', str(log)], capture_output=True, text=True, timeout=5, check=True,
            )
        self.assertEqual(result.stdout,
            'nm-selector-build-progress=compile\nnm-selector-build-exit=17\n')
        self.assertEqual(result.stderr, '')


if __name__ == '__main__':
    unittest.main()
