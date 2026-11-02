//go:build linux

package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
)

func validExpectedNMTun() nmTunDBusAuthority {
	return nmTunDBusAuthority{
		UUID:         "abcdef00-0000-4000-8000-000000000002",
		Name:         "podlaz0",
		MTU:          1500,
		DevicePath:   "/org/freedesktop/NetworkManager/Devices/15",
		ActivePath:   "/org/freedesktop/NetworkManager/ActiveConnection/3",
		SettingsPath: "/org/freedesktop/NetworkManager/Settings/8",
	}
}

func validObservedNMTun(expected nmTunDBusAuthority) nmTunDBusObservation {
	return nmTunDBusObservation{
		OwnerBefore: ":1.55", OwnerAfter: ":1.55",
		LookupDevice:    expected.DevicePath,
		DeviceInterface: expected.Name, DeviceIPInterface: expected.Name,
		DeviceMTU: expected.MTU, DeviceType: 16, DeviceState: 100,
		DeviceManaged: true, DeviceActive: expected.ActivePath,
		ActiveUUID: expected.UUID, ActiveType: "tun",
		ActiveState: 2, ActiveDefaultIPv4: true,
		ActiveSettings:    expected.SettingsPath,
		ActiveIP4Config:   "/org/freedesktop/NetworkManager/IP4Config/10",
		ActiveDevicePaths: []dbus.ObjectPath{expected.DevicePath},
	}
}

func TestNMTunDBusAuthorityRejectsIncompleteTrustedInputsBeforeObservation(t *testing.T) {
	ex := validExpectedNMTun()
	tests := []struct {
		name string
		edit func(*nmTunDBusAuthority)
	}{
		{"missing-uuid", func(e *nmTunDBusAuthority) { e.UUID = "" }},
		{"uppercase-uuid", func(e *nmTunDBusAuthority) { e.UUID = strings.ToUpper(e.UUID) }},
		{"invalid-ifname", func(e *nmTunDBusAuthority) { e.Name = "tun/name" }},
		{"oversized-ifname", func(e *nmTunDBusAuthority) { e.Name = strings.Repeat("a", 16) }},
		{"missing-mtu", func(e *nmTunDBusAuthority) { e.MTU = 0 }},
		{"foreign-device-path", func(e *nmTunDBusAuthority) { e.DevicePath = "/org/freedesktop/NetworkManager/Settings/15" }},
		{"root-device-path", func(e *nmTunDBusAuthority) { e.DevicePath = "/" }},
		{"wrong-active-path", func(e *nmTunDBusAuthority) { e.ActivePath = "/org/freedesktop/NetworkManager/Devices/3" }},
		{"missing-settings-path", func(e *nmTunDBusAuthority) { e.SettingsPath = "" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			trusted := ex
			tc.edit(&trusted)
			called := false
			err := verifyNMTunDBusAuthorityWith(context.Background(), trusted, func(context.Context, nmTunDBusAuthority) (nmTunDBusObservation, error) {
				called = true
				return validObservedNMTun(trusted), nil
			})
			if err == nil || called {
				t.Fatalf("invalid authority admitted: err=%v, called=%v", err, called)
			}
		})
	}
}

func TestNMTunDBusAuthorityRejectsStaleForeignAndPartialNMState(t *testing.T) {
	ex := validExpectedNMTun()
	baseline := validObservedNMTun(ex)
	cases := []struct {
		name string
		edit func(*nmTunDBusObservation)
	}{
		{"bus-owner-restarted", func(o *nmTunDBusObservation) { o.OwnerAfter = ":1.56" }},
		{"no-unique-owner", func(o *nmTunDBusObservation) { o.OwnerBefore = "org.freedesktop.NetworkManager" }},
		{"foreign-device", func(o *nmTunDBusObservation) { o.LookupDevice = "/org/freedesktop/NetworkManager/Devices/20" }},
		{"foreign-ip-interface", func(o *nmTunDBusObservation) { o.DeviceIPInterface = "foreign0" }},
		{"foreign-control-interface", func(o *nmTunDBusObservation) { o.DeviceInterface = "foreign0" }},
		{"MTU-drift", func(o *nmTunDBusObservation) { o.DeviceMTU++ }},
		{"tap-device", func(o *nmTunDBusObservation) { o.DeviceType = 16 + 1 }},
		{"unmanaged", func(o *nmTunDBusObservation) { o.DeviceManaged = false }},
		{"activating-device", func(o *nmTunDBusObservation) { o.DeviceState = 70 }},
		{"deactivating-connection", func(o *nmTunDBusObservation) { o.ActiveState = 3 }},
		{"wrong-active-on-device", func(o *nmTunDBusObservation) { o.DeviceActive = "/org/freedesktop/NetworkManager/ActiveConnection/99" }},
		{"uuid-mismatch", func(o *nmTunDBusObservation) { o.ActiveUUID = "00000000-0000-4000-8000-000000000003" }},
		{"wrong-type", func(o *nmTunDBusObservation) { o.ActiveType = "vpn" }},
		{"no-default-route", func(o *nmTunDBusObservation) { o.ActiveDefaultIPv4 = false }},
		{"wrong-settings", func(o *nmTunDBusObservation) { o.ActiveSettings = "/org/freedesktop/NetworkManager/Settings/99" }},
		{"no-ipv4-config", func(o *nmTunDBusObservation) { o.ActiveIP4Config = "/" }},
		{"zero-devices", func(o *nmTunDBusObservation) { o.ActiveDevicePaths = nil }},
		{"two-devices", func(o *nmTunDBusObservation) {
			o.ActiveDevicePaths = append(o.ActiveDevicePaths, "/org/freedesktop/NetworkManager/Devices/20")
		}},
		{"wrong-device", func(o *nmTunDBusObservation) {
			o.ActiveDevicePaths = []dbus.ObjectPath{"/org/freedesktop/NetworkManager/Devices/20"}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := baseline
			tc.edit(&got)
			err := verifyNMTunDBusAuthorityWith(context.Background(), ex, func(context.Context, nmTunDBusAuthority) (nmTunDBusObservation, error) {
				return got, nil
			})
			if err == nil {
				t.Fatal("stale or foreign NetworkManager state must fail closed")
			}
		})
	}
	if err := verifyNMTunDBusAuthorityWith(context.Background(), ex, func(context.Context, nmTunDBusAuthority) (nmTunDBusObservation, error) {
		return baseline, nil
	}); err != nil {
		t.Fatalf("exact observed NM authority rejected: %v", err)
	}
}

func TestNMTunDBusAuthorityPropagatesReaderFailureAndCancellation(t *testing.T) {
	ex := validExpectedNMTun()
	cause := errors.New("NetworkManager unreachable")
	if err := verifyNMTunDBusAuthorityWith(context.Background(), ex, func(context.Context, nmTunDBusAuthority) (nmTunDBusObservation, error) {
		return nmTunDBusObservation{}, cause
	}); !errors.Is(err, cause) {
		t.Fatalf("lost NM D-Bus must be classified, got %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	if err := verifyNMTunDBusAuthorityWith(ctx, ex, func(context.Context, nmTunDBusAuthority) (nmTunDBusObservation, error) {
		called = true
		return validObservedNMTun(ex), nil
	}); !errors.Is(err, context.Canceled) || called {
		t.Fatalf("cancelled admission must not inspect NM: err=%v called=%v", err, called)
	}
}
