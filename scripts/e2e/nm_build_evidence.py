#!/usr/bin/env python3
"""Closed-schema error evidence for the disposable pre-boot NM source build."""
from __future__ import annotations

import re
import sys
from pathlib import Path


# A compiler invocation containing --warn-error or -Werror is not a diagnostic.
# Keep the recognized syntax and published vocabulary independent of log input.
TERMINAL_ESCAPE = re.compile(r'\x1b\[[0-?]*[ -/]*[@-~]')
DIAGNOSTIC = re.compile(
    r'(?:^|:\s*)(?:fatal\s+)?(?:error|fatal|failed)\s*:'
    r'|\*\*\*.*\bError [0-9]+'
    r'|^\s*(?:(?:[A-Za-z_]+\.)*(?:[A-Za-z_]*(?:Error|Exception)):|Traceback \(most recent call last\))'
    r'|^\s*(?:Could not|Cannot|Can.t|Unable to)\b'
    r'|\b(?:subcommand failed|Operation not permitted|Permission denied)\b'
    r'|^\s*(?:Aborted|Segmentation fault|Bail out!)\b', re.I,
)


def ninja_targets(manifest: str) -> frozenset[str]:
    """Read only public relative output names from the distro-source build graph.

    No command lines, absolute paths or arbitrary log words are admitted.
    The caller obtains this graph from the freshly unpacked signed NM source,
    before any guest/provider configuration exists.
    """
    targets: set[str] = set()
    for line in manifest.splitlines():
        match = re.fullmatch(r'build ([^:$]+): [^\n]+', line)
        if not match:
            continue
        for target in match[1].split():
            if (re.fullmatch(r'(?:src|data|man|docs|po|examples)/[A-Za-z0-9_+./-]{1,180}', target)
                    and all(part not in ('', '.', '..') for part in target.split('/'))):
                targets.add(target)
    return frozenset(targets)


def load_ninja_targets(root: Path) -> frozenset[str]:
    if not root.is_absolute() or root.is_symlink() or not str(root).endswith('/tmp/nm-selector-source/source'):
        raise ValueError('private distro build source required')
    targets: set[str] = set()
    for manifest in root.glob('*/build.ninja'):
        if manifest.is_symlink() or manifest.stat().st_size > 64 * 1024 * 1024:
            raise ValueError('unsafe build graph')
        targets.update(ninja_targets(manifest.read_text(encoding='utf-8')))
    return frozenset(targets)


def failed_target_record(line: str) -> str | None:
    if not line.startswith('FAILED: '):
        return None
    targets = line.removeprefix('FAILED: ').split()
    kind = 'other'
    for suffix, candidate in (
        ('NM-1.0.gir', 'introspection'),
        ('libnm.vapi', 'vala'),
        ('NetworkManager', 'daemon'),
        ('nm-manager.c.o', 'manager_object'),
    ):
        if any(target == suffix or target.endswith('/' + suffix) for target in targets):
            kind = candidate
            break
    return 'nm-selector-build-detail=failed_target_' + kind


def build_error_records(lines: list[str], allowed_targets: frozenset[str] = frozenset()) -> list[str]:
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
        ('fatal_warnings', r'warnings configured as fatal|warnings.*treated as errors'),
        ('encoding_error', r'Unicode(?:Decode|Encode)Error|codec.*(?:encode|decode)'),
        ('constant_type', r'(?:determine|resolve).*type of constant'),
        ('namespace', r'namespace|\bbwrap\b'),
        ('locale_encoding', r'unsupported locale|locale.*(?:not supported|unknown)|locale\.Error'),
        ('introspection_annotation', r'annotation|not introspectable'),
        ('introspection_symbol', r'unknown namespace|unresolved.*(?:type|symbol)'),
        ('dbus', r'dbus|D-Bus'),
        ('sandbox', r'sandbox|seccomp|uid map|gid map'),
        ('temporary_file', r'temporary file|temporary directory|mkstemp'),
        ('read_only_filesystem', r'read-only file system'),
        ('internal_compiler_error', r'internal compiler error'),
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
    failed_context = False
    for decorated in lines:
        line = TERMINAL_ESCAPE.sub('', decorated)
        if re.match(r'^\s*(?:W:|warning:)', line, re.I):
            continue
        target_record = failed_target_record(line)
        if target_record is not None:
            output.append(target_record)
            failed_context = True
            for target in line.removeprefix('FAILED: ').split():
                if target in allowed_targets:
                    output.append('nm-selector-build-target=' + target)
            continue
        shell_failure = failed_context and re.search(
            r'(?:^|: )(?:(?:cannot|can.t|unable to) (?:open|read|write|execute|access)\b'
            r'|No such file or directory\b|(?:[^ :]+: )?not found$)', line, re.I,
        )
        if not DIAGNOSTIC.search(line) and not shell_failure:
            continue
        labels = [label for label, pattern in patterns if re.search(pattern, line, re.I)]
        for filename, label in (
            ('nm-manager.c', 'nm_manager'), ('nm-device.c', 'nm_device'),
            ('nm-shared-utils.h', 'nm_shared_utils_h'), ('nm-logging.h', 'nm_logging_h'),
            ('nm-logging-fwd.h', 'nm_logging_fwd_h'), ('nm-default.h', 'nm_default_h'),
            ('meson.build', 'meson'), ('debian/rules', 'debian_rules'),
        ):
            location = re.search(re.escape(filename) + r':([0-9]{1,6})(?::([0-9]{1,6}))?:', line)
            if location:
                output.append(f'nm-selector-build-detail=source_{label} line_{location[1]} column_{location[2] or 0}')
        output.extend('nm-selector-build-detail=' + label for label in (labels or ['unclassified_error']))
    return list(dict.fromkeys(output))


if __name__ == '__main__':
    try:
        if len(sys.argv) not in (2, 3):
            raise ValueError('private build log and optional source root required')
        path = Path(sys.argv[1])
        if path.is_symlink() or path.stat().st_size > 64 * 1024 * 1024:
            raise ValueError('unsafe or oversized private build log')
        targets = load_ninja_targets(Path(sys.argv[2])) if len(sys.argv) == 3 else frozenset()
        if len(sys.argv) == 3:
            print(f'nm-selector-build-target-inventory={len(targets)}')
        for record in build_error_records(path.read_text(encoding='utf-8', errors='replace').splitlines(), targets):
            print(record)
    except (OSError, ValueError):
        print('nm-selector-build-detail=diagnostic_failed', file=sys.stderr)
        sys.exit(1)
