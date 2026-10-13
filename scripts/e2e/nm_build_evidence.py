#!/usr/bin/env python3
"""Closed-schema error evidence for the disposable pre-boot NM source build."""
from __future__ import annotations

import re
import sys
from pathlib import Path


def build_error_records(lines: list[str]) -> list[str]:
    """Classify build errors without publishing private compiler/apt messages."""
    patterns = (
        ('stage_dh_auto_configure', r'\b(?:override_)?dh_auto_configure\b'),
        ('stage_dh_auto_build', r'\b(?:override_)?dh_auto_build\b'),
        ('stage_dh_auto_install', r'\b(?:override_)?dh_auto_install\b'),
        ('stage_dh_install', r'\b(?:override_)?dh_install\b'),
        ('stage_dh_missing', r'\b(?:override_)?dh_missing\b'),
        ('stage_dh_auto_test', r'\b(?:override_)?dh_auto_test\b'),
        ('stage_dpkg_shlibdeps', r'\bdpkg-shlibdeps\b'),
        ('meson', r'\bmeson\b'),
        ('unknown_option', r'unknown option|unrecognized.*option'),
        ('locale', r'locale|LC_ALL'),
        ('pkg_config', r'pkg-config|pkgconfig'),
        ('version_mismatch', r'version.*(?:too old|not found|mismatch|not satisfied)'),
        ('ninja', r'\bninja\b'),
        ('introspection', r'\b(?:g-ir-scanner|g-ir-compiler|introspection)\b'),
        ('gtk_doc', r'\b(?:gtkdoc|gtk-doc)[-\w]*\b'),
        ('implicit_declaration', r'implicit declaration|implicit-function-declaration'),
        ('undeclared_identifier', r'undeclared|not declared'),
        ('conflicting_types', r'conflicting types|incompatible pointer'),
        ('format_argument', r'format.*(?:expects|argument)|format-security'),
        ('undefined_reference', r'undefined reference|undefined symbol'),
        ('missing_file', r'no such file|not found|does not exist|cannot (?:open|access|stat)'),
        ('missing_dependency', r'dependency.*(?:not found|not satisfied)|unmet.*dependenc'),
        ('command_failed', r'command.*(?:failed|exited)|returned (?:non-zero|exit)'),
        ('permission_denied', r'permission denied|operation not permitted'),
        ('proc_filesystem', r'/proc\b|\bprocfs\b'),
        ('device_filesystem', r'/dev\b'),
        ('sys_filesystem', r'/sys\b'),
        ('shared_library', r'shared librar'),
        ('compiler_execution', r'cannot (?:run|execute|compile)|unable to execute'),
        ('source_changes', r'unexpected upstream changes|unrepresentable changes'),
        ('linker', r'\b(?:ld|collect2)\b'),
        ('assertion', r'assertion.*failed|assert.*should not be reached'),
        ('killed', r'killed|out of memory|no space left'),
        ('python_exception', r'\b(?:ImportError|ModuleNotFoundError|FileNotFoundError|Traceback)\b'),
        ('symbol_device_get_iface', r'\bnm_device_get_iface\b'),
        ('symbol_device_get_connectivity', r'\bnm_device_get_connectivity_state\b'),
        ('symbol_selector_trace', r'\b_podlaz_research_selector_trace\b'),
    )
    output: list[str] = []
    for line in lines:
        if not re.search(r'\berror\b|\bfatal\b|\bfailed\b|\bexception\b', line, re.I):
            continue
        labels = [label for label, pattern in patterns if re.search(pattern, line, re.I)]
        for filename, label in (('nm-manager.c', 'nm_manager'), ('meson.build', 'meson'), ('debian/rules', 'debian_rules')):
            location = re.search(re.escape(filename) + r':([0-9]{1,6})(?::([0-9]{1,6}))?:', line)
            if location:
                output.append(f'nm-selector-build-detail=source_{label} line_{location[1]} column_{location[2] or 0}')
        output.extend('nm-selector-build-detail=' + label for label in (labels or ['unclassified_error']))
    return list(dict.fromkeys(output))


if __name__ == '__main__':
    try:
        if len(sys.argv) != 2:
            raise ValueError('private build log required')
        path = Path(sys.argv[1])
        if path.is_symlink() or path.stat().st_size > 64 * 1024 * 1024:
            raise ValueError('unsafe or oversized private build log')
        for record in build_error_records(path.read_text(encoding='utf-8', errors='replace').splitlines()):
            print(record)
    except (OSError, ValueError):
        print('nm-selector-build-detail=diagnostic_failed', file=sys.stderr)
        sys.exit(1)
