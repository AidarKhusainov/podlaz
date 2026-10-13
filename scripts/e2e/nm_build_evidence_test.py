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


if __name__ == '__main__':
    unittest.main()
