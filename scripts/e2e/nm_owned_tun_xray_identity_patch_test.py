"""Source drift and negative inherited-FD identity extension tests."""
import unittest

from nm_owned_tun_xray_identity_patch import extend_linux_fd


def source_fixture() -> str:
    # Small fixed-shape source fixture; actual upstream hash & exact source
    # contract are checked independently in the CI builder.
    return '''package tun
import (
    "net/netip"
    "os/exec"
)
func NewTun(options *Config) (Tun, error) {
    tunFd, vnet, tunLink, fdProvided, err := openFromEnv(options.Name)
    if fdProvided {
        return nil, nil
    }
    open(options.Name)
    ownsTun: true
}
func openFromEnv(expectedName string) (int, int, netlink.Link, bool, error) {
    fdStr := platform.NewEnvFlag(platform.TunFdKey).GetValue
    unix.IoctlIfreq(fd, unix.TUNGETIFF, ifr)
    flags&unix.IFF_TUN == 0
    flags&unix.IFF_NO_PI == 0
    actualName != expectedName
    tunLink, err := netlink.LinkByName(actualName)
    if err != nil {
        return -1, 0, nil, true, err
    }

    if err = unix.SetNonblock(fd, true); err != nil {
    }
}
func (t *LinuxTun) Start() error {
    if !t.ownsTun {
        return nil
    }
    netlink.LinkSetUp(t.tunLink)
}
func (t *LinuxTun) Close() error {
    if t.ownsTun {
        netlink.LinkSetDown(t.tunLink)
    }
}
'''.replace('    ', '\t')


class InProcessExternalTunTest(unittest.TestCase):
    def test_checks_exact_in_process_identity_before_open(self):
        # Compare semantics directly, not a binary built from an unreviewed
        # commit. Source-based tests are not sufficient for guest admission.
        patched = extend_linux_fd(source_fixture())
        self.assertIn('os.Getenv("PODLAZ_RESEARCH_TUN_IFINDEX")', patched)
        self.assertIn('os.Getenv("PODLAZ_RESEARCH_TUN_MTU")', patched)
        self.assertIn('attrs.Index != index', patched)
        self.assertIn('attrs.MTU != mtu', patched)
        self.assertIn('attrs.Flags&net.FlagUp == 0', patched)
        self.assertIn('unix.TUNGETIFF, ifr', patched)
        self.assertLess(patched.index('attrs.Index != index'),
                        patched.index('unix.SetNonblock(fd, true)'))
        self.assertIn('if !t.ownsTun', patched)
        self.assertIn('if t.ownsTun', patched)

    def test_mutated_upstream_rejected(self):
        with self.assertRaisesRegex(ValueError, "source drift|upstream"):
            extend_linux_fd(source_fixture().replace(
                'netlink.LinkByName(actualName)', 'netlink.LinkByIndex(2)'))
        with self.assertRaises(ValueError):
            extend_linux_fd(extend_linux_fd(source_fixture()))


if __name__ == "__main__":
    unittest.main()
