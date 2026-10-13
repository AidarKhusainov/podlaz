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

class CompilerEvidenceContract(unittest.TestCase):
    def test_compiler_context_preserves_only_closed_diagnostic_atoms(self):
        from nm_build_evidence import build_error_records
        lines = [
            'secret.example/203.0.113.7 nm-manager.c:3921:5: error: implicit declaration of function nm_device_get_iface [-Werror=implicit-function-declaration]',
            'make[1]: *** [debian/rules:88: override_dh_auto_build] Error 2',
        ]
        records = build_error_records(lines)
        self.assertIn('nm-selector-build-detail=source_nm_manager line_3921 column_5', records)
        self.assertIn('nm-selector-build-detail=implicit_declaration', records)
        self.assertIn('nm-selector-build-detail=symbol_device_get_iface', records)
        self.assertIn('nm-selector-build-detail=stage_dh_auto_build', records)
        self.assertNotIn('secret', '\n'.join(records))
        self.assertNotIn('203.0.113.7', '\n'.join(records))

    def test_build_environment_diagnostic_does_not_echo_values(self):
        from nm_build_evidence import build_error_records
        records = build_error_records([
            'ERROR: /proc must be mounted to build private.example',
            'g-ir-scanner: error: Failed to load shared library private.example',
        ])
        self.assertIn('nm-selector-build-detail=proc_filesystem', records)
        self.assertIn('nm-selector-build-detail=introspection', records)
        self.assertIn('nm-selector-build-detail=shared_library', records)
        self.assertNotIn('private.example', '\n'.join(records))

    def test_unknown_error_and_non_error_do_not_become_false_diagnosis(self):
        from nm_build_evidence import build_error_records
        self.assertEqual(build_error_records(['ERROR: arbitrary private.example']),
                         ['nm-selector-build-detail=unclassified_error'])
        self.assertEqual(build_error_records(['compiling nm-manager.c']), [])


if __name__ == '__main__':
    unittest.main()
