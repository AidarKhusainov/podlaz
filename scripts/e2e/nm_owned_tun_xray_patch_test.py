"""Contract tests for the exact-version guest-only Xray FD patch."""
import unittest

from nm_owned_tun_xray_patch import patch_linux_tun


SOURCE = """//go:build linux && !android
package tun
import (
    "github.com/vishvananga/netlink"
)
type LinuxTun struct {
    tunFd int
    tunLink netlink.Link
    options TunOptions
}
func NewTun(options TunOptions) (Tun, error) {
    tunFd, err := open(options.Name)
    if err != nil { return nil, err }
}
func (t *LinuxTun) Start() error {
    err := netlink.LinkSetUp(t.tunLink)
    return err
}
func (t *LinuxTun) Close() error {
    _ = netlink.LinkSetDown(t.tunLink)
    return nil
}
""".replace('    "github.com/vishvananga/netlink"', '\t"github.com/vishvananga/netlink"').replace(
    '    tunLink netlink.Link\n    options TunOptions',
    '\ttunLink netlink.Link\n\toptions TunOptions'
).replace('    tunFd, err := open(options.Name)', '\ttunFd, err := open(options.Name)').replace(
    '    err := netlink.LinkSetUp(t.tunLink)',
    '\terr := netlink.LinkSetUp(t.tunLink)'
).replace('    _ = netlink.LinkSetDown(t.tunLink)',
    '\t_ = netlink.LinkSetDown(t.tunLink)'
)


class ExternalTunPatchTest(unittest.TestCase):
    def test_explicit_inherited_fd_never_changes_external_link(self):
        patched = patch_linux_tun(SOURCE)
        self.assertIn("PODLAZ_NM_GUEST_TUN_FD", patched)
        self.assertIn("PODLAZ_NM_GUEST_TUN_IFINDEX", patched)
        self.assertIn("unix.TUNGETIFF", patched)
        self.assertIn("attrs.Index != index", patched)
        self.assertIn("attrs.MTU != int(options.MTU)", patched)
        self.assertIn("if t.external {\n\t\treturn nil", patched)
        self.assertIn("if !t.external {\n\t\t_ = netlink.LinkSetDown", patched)
        self.assertIn("tunFd, err := open(options.Name)", patched)

    def test_source_drift_rejected(self):
        with self.assertRaisesRegex(ValueError, "contract changed"):
            patch_linux_tun(SOURCE.replace("LinkSetDown", "LinkSetUp"))

    def test_patch_not_repeatable(self):
        with self.assertRaisesRegex(ValueError, "contract changed"):
            patch_linux_tun(patch_linux_tun(SOURCE))


if __name__ == "__main__":
    unittest.main()
