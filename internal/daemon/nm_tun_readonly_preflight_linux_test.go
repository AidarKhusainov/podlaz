//go:build linux

package daemon

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestNMOwnedTunReadOnlyPreflightFencesObservedIdentityDrift(t *testing.T) {
	kernel := testNMFDIdentity()
	nm := validExpectedNMTun()
	cases := []struct {
		name   string
		failAt int
		want   []string
	}{
		{"intact", 0, []string{"fd", "nm", "fd"}},
		{"first-kernel", 1, []string{"fd"}},
		{"nm-owner-changed", 2, []string{"fd", "nm"}},
		{"second-kernel", 3, []string{"fd", "nm", "fd"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls []string
			attempt := 0
			failure := errors.New("authority changed")
			check := func() error {
				attempt++
				if tc.failAt == attempt {
					return failure
				}
				return nil
			}
			err := verifyNMOwnedTunReadOnlyPreflightWith(context.Background(), 9, kernel, nm,
				func(fd int, actual nmTunFDKernelIdentity) error {
					if fd != 9 || actual != kernel {
						t.Fatal("unexpected kernel observation input")
					}
					calls = append(calls, "fd")
					return check()
				},
				func(_ context.Context, actual nmTunDBusAuthority) error {
					if actual != nm {
						t.Fatal("unexpected NetworkManager observation input")
					}
					calls = append(calls, "nm")
					return check()
				})
			if !reflect.DeepEqual(calls, tc.want) {
				t.Fatalf("unexpected observation order: %v, want %v", calls, tc.want)
			}
			if tc.failAt == 0 && err != nil || tc.failAt != 0 && !errors.Is(err, failure) {
				t.Fatalf("unexpected preflight result %v", err)
			}
		})
	}
}

func TestNMOwnedTunReadOnlyPreflightRefusesMismatchedAuthorityWithoutIO(t *testing.T) {
	kernel := testNMFDIdentity()
	nm := validExpectedNMTun()
	cases := []struct {
		name string
		edit func(*nmTunFDKernelIdentity, *nmTunDBusAuthority)
	}{
		{"name", func(_ *nmTunFDKernelIdentity, n *nmTunDBusAuthority) { n.Name = "foreign0" }},
		{"mtu", func(k *nmTunFDKernelIdentity, _ *nmTunDBusAuthority) { k.MTU = 1492 }},
		{"empty", func(k *nmTunFDKernelIdentity, _ *nmTunDBusAuthority) { k.Name = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k, n := kernel, nm
			tc.edit(&k, &n)
			called := false
			err := verifyNMOwnedTunReadOnlyPreflightWith(context.Background(), 9, k, n,
				func(int, nmTunFDKernelIdentity) error { called = true; return nil },
				func(context.Context, nmTunDBusAuthority) error { called = true; return nil })
			if err == nil || called {
				t.Fatalf("contradictory authority must fail before observation: err=%v, called=%v", err, called)
			}
		})
	}
}

func TestNMOwnedTunReadOnlyPreflightRefusesCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	err := verifyNMOwnedTunReadOnlyPreflightWith(ctx, 9, testNMFDIdentity(), validExpectedNMTun(),
		func(int, nmTunFDKernelIdentity) error { called = true; return nil },
		func(context.Context, nmTunDBusAuthority) error { called = true; return nil })
	if !errors.Is(err, context.Canceled) || called {
		t.Fatalf("cancelled context must reject all observations: err=%v called=%v", err, called)
	}
}
