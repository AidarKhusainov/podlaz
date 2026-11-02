package daemon

import (
	"errors"
	"os"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func testNMFDIdentity() nmTunFDKernelIdentity {
	return nmTunFDKernelIdentity{
		Name: "podlaz0", Index: 42, MTU: 1500,
		NetNSDevice: 4, NetNSInode: 12345,
	}
}

func TestNMOwnedTunFDKernelIdentityRejectsIncompleteAuthorityWithoutInspectingFD(t *testing.T) {
	base := testNMFDIdentity()
	cases := []struct {
		name string
		edit func(*nmTunFDKernelIdentity)
	}{
		{"missing-name", func(v *nmTunFDKernelIdentity) { v.Name = "" }},
		{"oversized-name", func(v *nmTunFDKernelIdentity) { v.Name = strings.Repeat("x", unix.IFNAMSIZ) }},
		{"name-with-NUL", func(v *nmTunFDKernelIdentity) { v.Name = "pod\x00laz0" }},
		{"name-with-slash", func(v *nmTunFDKernelIdentity) { v.Name = "pod/laz" }},
		{"missing-index", func(v *nmTunFDKernelIdentity) { v.Index = 0 }},
		{"missing-MTU", func(v *nmTunFDKernelIdentity) { v.MTU = 0 }},
		{"missing-namespace-device", func(v *nmTunFDKernelIdentity) { v.NetNSDevice = 0 }},
		{"missing-namespace-inode", func(v *nmTunFDKernelIdentity) { v.NetNSInode = 0 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want := base
			tc.edit(&want)
			called := false
			err := verifyNMOwnedTunFDKernelIdentityWith(5, want, func(int) (nmTunFDKernelObservation, error) {
				called = true
				return nmTunFDKernelObservation{}, nil
			})
			if err == nil || called {
				t.Fatalf("incomplete trusted identity must fail before observation: err=%v called=%v", err, called)
			}
		})
	}
}

func TestNMOwnedTunFDKernelIdentityRejectsDriftAndForeignLinks(t *testing.T) {
	expected := testNMFDIdentity()
	valid := nmTunFDKernelObservation{Identity: expected, Flags: unix.IFF_TUN | unix.IFF_NO_PI, LinkUp: true}
	cases := []struct {
		name string
		edit func(*nmTunFDKernelObservation)
		fail bool
	}{
		{"exact-single-queue-TUN", func(*nmTunFDKernelObservation) {}, false},
		{"foreign-interface", func(v *nmTunFDKernelObservation) { v.Identity.Name = "foreign0" }, true},
		{"ifindex-ABA", func(v *nmTunFDKernelObservation) { v.Identity.Index++ }, true},
		{"MTU-drift", func(v *nmTunFDKernelObservation) { v.Identity.MTU-- }, true},
		{"namespace-moved", func(v *nmTunFDKernelObservation) { v.Identity.NetNSInode++ }, true},
		{"namespace-device-drift", func(v *nmTunFDKernelObservation) { v.Identity.NetNSDevice++ }, true},
		{"TAP-not-TUN", func(v *nmTunFDKernelObservation) { v.Flags = unix.IFF_TAP }, true},
		{"multiqueue-not-supported", func(v *nmTunFDKernelObservation) { v.Flags |= unix.IFF_MULTI_QUEUE }, true},
		{"link-administratively-down", func(v *nmTunFDKernelObservation) { v.LinkUp = false }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			observed := valid
			tc.edit(&observed)
			err := verifyNMOwnedTunFDKernelIdentityWith(5, expected, func(fd int) (nmTunFDKernelObservation, error) {
				if fd != 5 {
					t.Fatalf("unexpected FD %d", fd)
				}
				return observed, nil
			})
			if (err != nil) != tc.fail {
				t.Fatalf("identity verification error=%v, expected failure=%v", err, tc.fail)
			}
		})
	}
}

func TestNMOwnedTunFDKernelIdentityFailsClosedOnObservationError(t *testing.T) {
	cause := errors.New("observed link disappeared")
	err := verifyNMOwnedTunFDKernelIdentityWith(7, testNMFDIdentity(), func(int) (nmTunFDKernelObservation, error) {
		return nmTunFDKernelObservation{}, cause
	})
	if !errors.Is(err, cause) {
		t.Fatalf("expected read-only observation error: %v", err)
	}
}

func TestNMOwnedTunFDKernelIdentityRejectsNonTunWithoutClosingFD(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "non-tun-*")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := verifyNMOwnedTunFDKernelIdentity(int(file.Fd()), testNMFDIdentity()); err == nil {
		t.Fatal("ordinary file must not be admitted as a TUN FD")
	}
	if _, err := file.Write([]byte("still open")); err != nil {
		t.Fatalf("read-only admission closed or mutated passed FD: %v", err)
	}
}
