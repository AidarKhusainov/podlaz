"""The pre-boot NM build needs private procfs, not the runner's mount/PID view."""
import json
import os
import re
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).with_name('nm_route_selection_guest.sh')
PROBE = "set -o pipefail; printf 'expected\\n' | grep -Fx -f <(printf 'expected\\n')"


def build_function() -> str:
    source = SCRIPT.read_text(encoding='utf-8')
    start = source.index('run_build_guest() {')
    end = source.index('\n}\n', start) + 3
    return source[start:end]


def minimal_root(root: Path) -> None:
    """Copy only the two public executables and their dynamic loader libraries."""
    for name in ('bash', 'grep'):
        executable = shutil.which(name)
        if executable is None:
            raise RuntimeError('build preflight executable missing')
        executable = Path(executable).resolve()
        target = root / 'bin' / name
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(executable, target)
        libraries = subprocess.run(
            ['ldd', str(executable)], capture_output=True, text=True, check=True, timeout=5,
        )
        for library in re.findall(r'(?:=>\s*)?(/[^\s()]+)', libraries.stdout):
            target = root / library.lstrip('/')
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(library, target)
    (root / 'proc').mkdir()
    (root / 'dev').mkdir()
    (root / 'dev' / 'fd').symlink_to('/proc/self/fd')


class BuildNamespaceContract(unittest.TestCase):
    def test_namespace_is_scoped_to_guest_and_propagates_failure(self):
        # Exercise the real wrapper with an argv-recording privilege boundary.
        fake_sudo = '''sudo() {
            python3 -c 'import json, sys; print(json.dumps(sys.argv[1:])); sys.exit(37)' "$@"
        }
        '''
        result = subprocess.run(
            ['bash', '-c', 'ROOT="$1"; ' + fake_sudo + build_function() + '\nrun_build_guest',
             'test', '/tmp/disposable guest'],
            input='private.example\n', capture_output=True, text=True, timeout=5, check=False,
        )
        self.assertEqual(result.returncode, 37)
        self.assertEqual(json.loads(result.stdout), [
            '-n', 'unshare', '--mount', '--pid', '--fork', '--kill-child',
            '--mount-proc=/tmp/disposable guest/proc', '--',
            'chroot', '/tmp/disposable guest', '/bin/bash', '-s',
        ])
        self.assertEqual(result.stderr, '')
        self.assertNotIn('private.example', result.stdout)

    def test_build_uses_wrapper_and_checks_process_substitution_before_apt(self):
        source = SCRIPT.read_text(encoding='utf-8')
        self.assertIn('if ! run_build_guest >"${build_log}" 2>&1', source)
        guest = source.split("<<'GUEST'\n", 1)[1].split('\nGUEST\n', 1)[0]
        self.assertLess(guest.index('test -d /proc/self/fd'), guest.index('apt-get update'))
        self.assertIn("grep -Fx -f <(printf 'expected\\n')", guest)
        self.assertNotIn('mount --bind /proc', source)
        self.assertIn("grep -qx 'exit 101'", source)

    @unittest.skipUnless(os.environ.get('GITHUB_ACTIONS') == 'true',
                         'real mount/PID namespace proof runs on disposable hosted infrastructure')
    def test_process_substitution_fails_without_proc_and_passes_in_private_namespace(self):
        # No network mutation or real service: a minimal two-command chroot.
        with tempfile.TemporaryDirectory(prefix='nm-build-environment-') as directory:
            root = Path(directory)
            minimal_root(root)
            failed = subprocess.run(
                ['sudo', '-n', 'chroot', directory, '/bin/bash', '-c', PROBE],
                capture_output=True, text=True, timeout=10, check=False,
            )
            self.assertEqual(failed.returncode, 2)
            self.assertIn('/dev/fd/', failed.stderr)
            before = Path('/proc/self/mountinfo').read_bytes()
            command = 'ROOT="$1"; ' + build_function() + '\nrun_build_guest'
            succeeded = subprocess.run(
                ['bash', '-c', command, 'test', directory], input=PROBE,
                capture_output=True, text=True, timeout=15, check=False,
            )
            self.assertEqual(succeeded.returncode, 0, 'private procfs preflight failed')
            self.assertEqual(succeeded.stdout, 'expected\n')
            self.assertEqual(succeeded.stderr, '')
            # Failure must also destroy its namespace and leave no guest mount.
            rejected = subprocess.run(
                ['bash', '-c', command, 'test', directory], input='exit 37\n',
                capture_output=True, text=True, timeout=15, check=False,
            )
            self.assertEqual(rejected.returncode, 37)
            self.assertEqual(Path('/proc/self/mountinfo').read_bytes(), before)
            self.assertFalse(os.path.ismount(root / 'proc'))
            self.assertEqual(list((root / 'proc').iterdir()), [])


if __name__ == '__main__':
    unittest.main()
