package daemon

import (
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// nmTunFDKernelIdentity is trusted, boot-scoped authority from the future NM
// session broker. It must never be reconstructed from a matching observed link.
// NetworkManager's connection UUID and session generation require independent
// durable/D-Bus proof; this value establishes neither of those facts.
type nmTunFDKernelIdentity struct {
	Name        string
	Index       int
	MTU         int
	NetNSDevice uint64
	NetNSInode  uint64
}

type nmTunFDKernelObservation struct {
	Identity nmTunFDKernelIdentity
	Flags    uint16
	LinkUp   bool
}

// verifyNMOwnedTunFDKernelIdentity performs a read-only kernel identity check.
// This is defense in depth before/after supervised handoff, not atomic FD-to-
// ifindex binding, proof of NM ownership, or revocation of copied descriptors.
// The caller must enforce generation fencing and confinement independently.
func verifyNMOwnedTunFDKernelIdentity(fd int, expected nmTunFDKernelIdentity) error {
	return verifyNMOwnedTunFDKernelIdentityWith(fd, expected, observeNMOwnedTunFDKernelIdentity)
}

func verifyNMOwnedTunFDKernelIdentityWith(fd int, expected nmTunFDKernelIdentity, observe func(int) (nmTunFDKernelObservation, error)) error {
	if fd < 3 || observe == nil || expected.Index <= 0 || expected.MTU <= 0 ||
		expected.NetNSDevice == 0 || expected.NetNSInode == 0 || expected.Name == "" {
		return errors.New("NM TUN FD admission has incomplete kernel authority")
	}
	if !networkSessionInterfaceRegex.MatchString(expected.Name) {
		return errors.New("NM TUN FD admission has invalid interface identity")
	}
	if _, err := unix.NewIfreq(expected.Name); err != nil {
		return errors.New("NM TUN FD admission has invalid interface identity")
	}
	for _, b := range []byte(expected.Name) {
		if b == 0 || b == '/' {
			return errors.New("NM TUN FD admission has invalid interface identity")
		}
	}
	actual, err := observe(fd)
	if err != nil {
		return fmt.Errorf("inspect inherited NM TUN FD: %w", err)
	}
	if actual.Flags&(unix.IFF_TUN|unix.IFF_NO_PI) != unix.IFF_TUN|unix.IFF_NO_PI ||
		actual.Flags&(unix.IFF_TAP|unix.IFF_MULTI_QUEUE|unix.IFF_VNET_HDR) != 0 || !actual.LinkUp ||
		actual.Identity != expected {
		return errors.New("inherited NM TUN FD does not match exact kernel identity")
	}
	return nil
}

func observeNMOwnedTunFDKernelIdentity(fd int) (nmTunFDKernelObservation, error) {
	if fd < 0 {
		return nmTunFDKernelObservation{}, errors.New("invalid TUN file descriptor")
	}
	ifr, err := unix.NewIfreq("")
	if err != nil {
		return nmTunFDKernelObservation{}, err
	}
	if err := unix.IoctlIfreq(fd, unix.TUNGETIFF, ifr); err != nil {
		return nmTunFDKernelObservation{}, fmt.Errorf("TUNGETIFF: %w", err)
	}
	name := ifr.Name()
	if name == "" {
		return nmTunFDKernelObservation{}, errors.New("TUNGETIFF returned no device identity")
	}
	link, err := netlink.LinkByName(name)
	if err != nil {
		return nmTunFDKernelObservation{}, fmt.Errorf("read TUN netlink identity: %w", err)
	}
	attrs := link.Attrs()
	if attrs == nil {
		return nmTunFDKernelObservation{}, errors.New("TUN netlink attributes unavailable")
	}
	if attrs.Name != name || attrs.Index <= 0 {
		return nmTunFDKernelObservation{}, errors.New("TUN netlink name/index changed during inspection")
	}
	// TUNGETDEVNETNS (_IO('T', 227)) returns a namespace FD for the
	// actual TUN device, not the original socket or the querying process.
	// It requires CAP_NET_ADMIN, hence only a privileged broker may call it.
	const tunGetDeviceNetNS = 0x54e3
	nsFD, err := unix.IoctlGetInt(fd, tunGetDeviceNetNS)
	if err != nil {
		return nmTunFDKernelObservation{}, fmt.Errorf("read TUN device namespace: %w", err)
	}
	defer unix.Close(nsFD)
	var deviceNS unix.Stat_t
	if err := unix.Fstat(nsFD, &deviceNS); err != nil {
		return nmTunFDKernelObservation{}, fmt.Errorf("stat TUN device namespace: %w", err)
	}
	stat, err := os.Stat("/proc/self/ns/net")
	if err != nil {
		return nmTunFDKernelObservation{}, fmt.Errorf("read broker network namespace: %w", err)
	}
	ns, ok := stat.Sys().(*syscall.Stat_t)
	if !ok {
		return nmTunFDKernelObservation{}, errors.New("broker network namespace inode unavailable")
	}
	if deviceNS.Ino != ns.Ino || uint64(deviceNS.Dev) != uint64(ns.Dev) {
		return nmTunFDKernelObservation{}, errors.New("TUN device moved outside broker network namespace")
	}
	return nmTunFDKernelObservation{
		Identity: nmTunFDKernelIdentity{
			Name: name, Index: attrs.Index, MTU: attrs.MTU,
			NetNSDevice: uint64(ns.Dev), NetNSInode: ns.Ino,
		},
		Flags:  ifr.Uint16(),
		LinkUp: attrs.Flags&net.FlagUp != 0,
	}, nil
}
