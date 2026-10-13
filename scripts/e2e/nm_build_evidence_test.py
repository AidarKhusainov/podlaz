"""Public build diagnostics must classify failures, never echoed arguments."""
import unittest

from nm_build_evidence import build_error_records


class BuildDiagnosticContract(unittest.TestCase):
    def test_echoed_compiler_and_scanner_flags_are_not_errors(self):
        self.assertEqual(build_error_records([
            'cc -Werror=implicit-function-declaration -Werror=format-security -c nm-manager.c',
            'env PKG_CONFIG_PATH=/private.example CC=cc /usr/bin/g-ir-scanner --warn-error --cflags-begin -Werror=format-security',
        ]), [])

    def test_failed_target_is_classified_without_path_disclosure(self):
        for target, kind in (
            ('/private.example/src/libnm-client-impl/NM-1.0.gir', 'introspection'),
            ('/private.example/src/libnm-client-impl/libnm.vapi', 'vala'),
            ('/private.example/src/core/NetworkManager', 'daemon'),
            ('/private.example/src/core/libNetworkManager.a.p/nm-manager.c.o', 'manager_object'),
            ('/private.example/unknown', 'other'),
        ):
            with self.subTest(kind=kind):
                self.assertEqual(build_error_records(['FAILED: ' + target]),
                                 ['nm-selector-build-detail=failed_target_' + kind])

    def test_real_compiler_diagnostic_keeps_location_not_private_values(self):
        records = build_error_records([
            '/private.example/nm-manager.c:3921:5: error: implicit declaration of function nm_device_get_iface [-Werror=implicit-function-declaration]',
            'make[1]: *** [debian/rules:88: override_dh_auto_build] Error 2',
        ])
        self.assertIn('nm-selector-build-detail=source_nm_manager line_3921 column_5', records)
        self.assertIn('nm-selector-build-detail=implicit_declaration', records)
        self.assertIn('nm-selector-build-detail=stage_dh_auto_build', records)
        self.assertNotIn('private.example', '\n'.join(records))

    def test_multiline_build_tool_failures_have_closed_reason_labels(self):
        records = build_error_records([
            'FAILED: private.example/NM-1.0.gir',
            'env CC=cc g-ir-scanner --warn-error -Werror=format-security',
            'Fatal: NM: warnings configured as fatal',
            'UnicodeDecodeError: ascii codec cannot decode byte in private.example',
            'bwrap: Creating new namespace failed: Operation not permitted',
            'ninja: build stopped: subcommand failed.',
        ])
        self.assertIn('nm-selector-build-detail=failed_target_introspection', records)
        self.assertIn('nm-selector-build-detail=fatal_warnings', records)
        self.assertIn('nm-selector-build-detail=encoding_error', records)
        self.assertIn('nm-selector-build-detail=namespace', records)
        self.assertIn('nm-selector-build-detail=permission_denied', records)
        self.assertNotIn('nm-selector-build-detail=format_argument', records)
        self.assertNotIn('private.example', '\n'.join(records))

    def test_non_colon_failure_details_are_retained_without_raw_context(self):
        records = build_error_records([
            'Could not determine the type of constant private.example',
            'ERROR: /proc must be mounted to build private.example',
            'g-ir-scanner: error: Failed to load shared library private.example',
        ])
        self.assertIn('nm-selector-build-detail=constant_type', records)
        self.assertIn('nm-selector-build-detail=proc_filesystem', records)
        self.assertIn('nm-selector-build-detail=shared_library', records)
        self.assertNotIn('private.example', '\n'.join(records))

    def test_unknown_failure_remains_unclassified(self):
        self.assertEqual(build_error_records(['ERROR: arbitrary private.example']),
                         ['nm-selector-build-detail=unclassified_error'])


class BuildDiagnosticFormatContract(unittest.TestCase):
    def test_apt_sandbox_warning_is_not_a_build_error(self):
        self.assertEqual(build_error_records([
            "W: Download is performed unsandboxed as root as file 'private.example.dsc' couldn't be accessed by user '_apt'. - pkgAcquire::Run (13: Permission denied)",
        ]), [])

    def test_colored_compiler_diagnostic_is_not_lost(self):
        plain = 'nm-manager.c:3874:5: error: private.example is undeclared'
        colored = '\x1b[01mnm-manager.c:3874:5:\x1b[m\x1b[K \x1b[01;31merror: \x1b[m\x1b[Kprivate.example is undeclared'
        self.assertEqual(build_error_records([colored]), build_error_records([plain]))
        self.assertIn('nm-selector-build-detail=undeclared_identifier', build_error_records([colored]))

    def test_colored_ninja_target_retains_its_closed_kind(self):
        self.assertEqual(build_error_records(['\x1b[31mFAILED: \x1b[0msrc/core/NetworkManager']),
                         ['nm-selector-build-detail=failed_target_daemon'])

    def test_python_qualified_exception_type_is_not_lost(self):
        records = build_error_records(['locale.Error: unsupported locale setting'])
        self.assertIn('nm-selector-build-detail=locale_encoding', records)

    def test_header_error_keeps_a_closed_source_location(self):
        records = build_error_records([
            '../src/libnm-glib-aux/nm-shared-utils.h:99:4: error: incompatible pointer private.example',
        ])
        self.assertIn('nm-selector-build-detail=source_nm_shared_utils_h line_99 column_4', records)
        self.assertNotIn('private.example', '\n'.join(records))

    def test_whitespace_and_multiple_ninja_outputs_do_not_hide_kind(self):
        records = build_error_records(['FAILED:  src/libnm-client-impl/NM-1.0.gir other.private.example'])
        self.assertIn('nm-selector-build-detail=failed_target_introspection', records)
        self.assertNotIn('private.example', '\n'.join(records))


class BuildTargetInventoryContract(unittest.TestCase):
    def test_only_a_manifest_known_relative_target_can_be_published(self):
        from nm_build_evidence import build_error_records
        records = build_error_records([
            'FAILED: src/libnm-core-impl/nm-core-enum-types.c',
            'FAILED: src/private.example/data.c',
            'FAILED: /private.example/src/libnm-core-impl/nm-core-enum-types.c',
        ], frozenset({'src/libnm-core-impl/nm-core-enum-types.c'}))
        self.assertIn('nm-selector-build-target=src/libnm-core-impl/nm-core-enum-types.c', records)
        self.assertNotIn('private.example', '\n'.join(records))

    def test_manifest_parser_rejects_escaping_or_unbounded_paths(self):
        from nm_build_evidence import ninja_targets
        manifest = '\n'.join([
            'build src/libnm-core-impl/nm-core-enum-types.c: CUSTOM_COMMAND upstream-public-input',
            'build data/NetworkManager.service: CUSTOM_COMMAND public.service.in',
            'build /private.example/secret: CUSTOM_COMMAND',
            'build src/../private.example/secret: CUSTOM_COMMAND',
            'build src/secret$ name: CUSTOM_COMMAND',
            '  command = echo private.example',
        ])
        self.assertEqual(ninja_targets(manifest), frozenset({
            'src/libnm-core-impl/nm-core-enum-types.c', 'data/NetworkManager.service',
        }))

    def test_failed_block_shell_errors_are_not_lost_or_confused_with_configure(self):
        records = build_error_records([
            'Checking for tool: not found',
            'FAILED: data/NetworkManager.service',
            'sed -f public.sed input',
            'sed: cannot read /dev/fd/63: No such file or directory',
            'ninja: build stopped: subcommand failed.',
        ])
        self.assertIn('nm-selector-build-detail=missing_file', records)
        self.assertIn('nm-selector-build-detail=device_filesystem', records)
        self.assertEqual(build_error_records(['Checking for tool: not found']), [])


if __name__ == '__main__':
    unittest.main()
