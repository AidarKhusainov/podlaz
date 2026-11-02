//go:build linux

package daemon

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"reflect"
	"testing"

	"github.com/godbus/dbus/v5"
)

func testNML3ActivatingIntent(t *testing.T) (nmL3Stager, nmL3Intent) {
	t.Helper()
	dir := t.TempDir()
	session := protectedNMTestSession(t, dir)
	stager := newNML3Stager(dir, fixedBootID("boot-a"))
	intent := nmL3Intent{
		Schema: nmL3IntentSchema,
		Owner: networkSessionStateOwner,
		BootID: "boot-a",
		SessionID: session.SessionID,
		UUID: "abcdef00-0000-4000-8000-000000000002",
		Interface: "podlaz0",
		IPv4: "198.18.0.17/32",
		DNS: "192.0.2.53",
		MTU: 1500,
		Metric: 10,
		Mark: 101,
		Stage: "activating",
		SettingsPath: "/org/freedesktop/NetworkManager/Settings/7",
		ActivePath: "/org/freedesktop/NetworkManager/ActiveConnection/12",
	}
	if err := stager.persist(intent); err != nil {
		t.Fatal(err)
	}
	return stager, intent
}

func testNML3ValidActiveObservation(i nmL3Intent) nmL3ActiveObservation {
	prefix, _ := netip.ParsePrefix(i.IPv4)
	v := dbus.MakeVariant
	return nmL3ActiveObservation{
		OwnerBefore: ":1.17",
		OwnerAfter: ":1.17",
		UUID: i.UUID,
		ConnectionType: "tun",
		SettingsPath: i.SettingsPath,
		DevicePath: "/org/freedesktop/NetworkManager/Devices/25",
		LookupDevice: "/org/freedesktop/NetworkManager/Devices/25",
		DeviceActive: i.ActivePath,
		Interface: i.Interface,
		IPInterface: i.Interface,
		MTU: i.MTU,
		DeviceType: nmDeviceTypeTun,
		DeviceState: nmDeviceStateActivated,
		DeviceManaged: true,
		TunMode: "tun",
		TunNoPI: true,
		TunOwner: 0,
		TunGroup: 0,
		ActiveState: nmActiveStateActivated,
		Default4: true,
		IP4Path: "/org/freedesktop/NetworkManager/IP4Config/10",
		Addresses: []map[string]dbus.Variant{{"address": v(prefix.Addr().String()), "prefix": v(uint32(prefix.Bits()))}},
		Routes: []map[string]dbus.Variant{
			{"dest": v("198.18.0.17"), "prefix": v(uint32(32)), "metric": v(i.Metric)},
			{"dest": v("0.0.0.0"), "prefix": v(uint32(0)), "metric": v(i.Metric)},
		},
		Nameservers: []map[string]dbus.Variant{{"address": v(i.DNS)}},
		Searches: []string{"~."},
		DNSPriority: -30000,
	}
}

func TestNML3ActiveEvidenceRequiresExactNMIdentityRouteAndDNS(t *testing.T) {
	_, intent := testNML3ActivatingIntent(t)
	positive := testNML3ValidActiveObservation(intent)
	if err := verifyNML3ActiveEvidence(intent, positive); err != nil {
		t.Fatalf("valid NM L3 observation rejected: %v", err)
	}
	cases := []struct {
		name string
		edit func(*nmL3ActiveObservation)
	}{
		{"NM-restarted", func(v *nmL3ActiveObservation) { v.OwnerAfter = ":1.19" }},
		{"unowned-NM", func(v *nmL3ActiveObservation) { v.OwnerBefore = "org.freedesktop.NetworkManager" }},
		{"foreign-UUID", func(v *nmL3ActiveObservation) { v.UUID = "abcabc00-0000-4000-8000-000000000002" }},
		{"foreign-settings", func(v *nmL3ActiveObservation) { v.SettingsPath = "/org/freedesktop/NetworkManager/Settings/8" }},
		{"wrong-connection-type", func(v *nmL3ActiveObservation) { v.ConnectionType = "vpn" }},
		{"foreign-device", func(v *nmL3ActiveObservation) { v.LookupDevice = "/org/freedesktop/NetworkManager/Devices/24" }},
		{"foreign-active", func(v *nmL3ActiveObservation) { v.DeviceActive = "/org/freedesktop/NetworkManager/ActiveConnection/13" }},
		{"foreign-interface", func(v *nmL3ActiveObservation) { v.IPInterface = "foreign0" }},
		{"unknown-MTU", func(v *nmL3ActiveObservation) { v.MTU = 1400 }},
		{"foreign-type", func(v *nmL3ActiveObservation) { v.DeviceType = 13 }},
		{"unmanaged", func(v *nmL3ActiveObservation) { v.DeviceManaged = false }},
		{"activating", func(v *nmL3ActiveObservation) { v.ActiveState = 1 }},
		{"device-down", func(v *nmL3ActiveObservation) { v.DeviceState = 70 }},
		{"TAP", func(v *nmL3ActiveObservation) { v.TunMode = "tap" }},
		{"packet-info", func(v *nmL3ActiveObservation) { v.TunNoPI = false }},
		{"multiqueue", func(v *nmL3ActiveObservation) { v.TunMultiQueue = true }},
		{"vnet-header", func(v *nmL3ActiveObservation) { v.TunVnetHdr = true }},
		{"foreign-owner", func(v *nmL3ActiveObservation) { v.TunOwner = 1000 }},
		{"foreign-group", func(v *nmL3ActiveObservation) { v.TunGroup = -1 }},
		{"not-default", func(v *nmL3ActiveObservation) { v.Default4 = false }},
		{"unexpected-ipv6-default", func(v *nmL3ActiveObservation) { v.Default6 = true }},
		{"foreign-ipconfig", func(v *nmL3ActiveObservation) { v.IP4Path = "/" }},
		{"wrong-ip", func(v *nmL3ActiveObservation) { v.Addresses[0] = map[string]dbus.Variant{"address": dbus.MakeVariant("198.18.0.22"), "prefix": dbus.MakeVariant(uint32(32))} }},
		{"additional-address", func(v *nmL3ActiveObservation) { v.Addresses = append(v.Addresses, v.Addresses[0]) }},
		{"missing-route", func(v *nmL3ActiveObservation) { v.Routes = v.Routes[:1] }},
		{"duplicate-default", func(v *nmL3ActiveObservation) { v.Routes = append(v.Routes, v.Routes[1]) }},
		{"foreign-default-metric", func(v *nmL3ActiveObservation) { v.Routes[1] = map[string]dbus.Variant{"dest": dbus.MakeVariant("0.0.0.0"), "prefix": dbus.MakeVariant(uint32(0)), "metric": dbus.MakeVariant(uint32(22))} }},
		{"foreign-default-gateway", func(v *nmL3ActiveObservation) { v.Routes[1] = map[string]dbus.Variant{"dest": dbus.MakeVariant("0.0.0.0"), "prefix": dbus.MakeVariant(uint32(0)), "metric": dbus.MakeVariant(uint32(10)), "next-hop": dbus.MakeVariant("192.0.2.1")} }},
		{"wrong-DNS", func(v *nmL3ActiveObservation) { v.Nameservers[0] = map[string]dbus.Variant{"address": dbus.MakeVariant("192.0.2.99")} }},
		{"extra-DNS", func(v *nmL3ActiveObservation) { v.Nameservers = append(v.Nameservers, v.Nameservers[0]) }},
		{"wrong-search-domain", func(v *nmL3ActiveObservation) { v.Searches[0] = "example.org" }},
		{"DNS-priority", func(v *nmL3ActiveObservation) { v.DNSPriority = -200 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			obs := testNML3ValidActiveObservation(intent)
			tc.edit(&obs)
			if err := verifyNML3ActiveEvidence(intent, obs); err == nil {
				t.Fatalf("unexpectedly accepted stale or unsafe NM state: %s", tc.name)
			}
		})
	}
}

func TestNML3ActiveInspectionChecksPrivacyBeforeAndAfterNM(t *testing.T) {
	stager, intent := testNML3ActivatingIntent(t)
	sequence := []string{}
	obs := testNML3ValidActiveObservation(intent)
	err := stager.verifyActiveL3With(context.Background(),
		func(_ context.Context, actual nmL3Intent) (nmL3ActiveObservation, error) {
			sequence = append(sequence, "nm")
			if !reflect.DeepEqual(actual, intent) {
				t.Fatal("observer received wrong durable NM authority")
			}
			return obs, nil
		},
		func(context.Context, networkSessionProtection) error {
			sequence = append(sequence, "privacy")
			return nil
		})
	if err != nil || !reflect.DeepEqual(sequence, []string{"privacy", "nm", "privacy"}) {
		t.Fatalf("wrong NM verification ordering: %v, err=%v", sequence, err)
	}
}

func TestNML3ActiveInspectionRejectsPrivacyLossOrDurableDrift(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*testing.T, nmL3Stager, nmL3Intent)
	}{
		{"corrupt-intent", func(t *testing.T, s nmL3Stager, i nmL3Intent) {
			i.Stage = "planned"
			i.ActivePath = ""
			i.SettingsPath = ""
			if err := s.persist(i); err != nil {
				t.Fatal(err)
			}
		}},
		{"session-drift", func(t *testing.T, s nmL3Stager, _ nmL3Intent) {
			store := newNetworkSessionStateStore(s.session.runtimeDir, fixedBootID("boot-a"))
			if _, _, err := store.Update(func(state *networkSessionState) error {
				state.RecoveryEpoch++
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stager, intent := testNML3ActivatingIntent(t)
			obs := testNML3ValidActiveObservation(intent)
			barrierCount := 0
			err := stager.verifyActiveL3With(context.Background(),
				func(context.Context, nmL3Intent) (nmL3ActiveObservation, error) {
					tc.edit(t, stager, intent)
					return obs, nil
				},
				func(context.Context, networkSessionProtection) error {
					barrierCount++
					return nil
				})
			if err == nil || barrierCount != 2 {
				t.Fatalf("durable authority drift admitted: err=%v barriers=%d", err, barrierCount)
			}
		})
	}
}

func TestNML3ActiveInspectionRefusesUnprotectedOrIncompleteSession(t *testing.T) {
	stager, intent := testNML3ActivatingIntent(t)
	if err := os.Remove(stager.path()); err != nil {
		t.Fatal(err)
	}
	called := false
	if err := stager.verifyActiveL3With(context.Background(),
		func(context.Context, nmL3Intent) (nmL3ActiveObservation, error) {
			called = true
			return testNML3ValidActiveObservation(intent), nil
		},
		func(context.Context, networkSessionProtection) error {
			called = true
			return nil
		}); err == nil || called {
		t.Fatalf("missing NM intent admitted: err=%v called=%v", err, called)
	}
	stager, intent = testNML3ActivatingIntent(t)
	fail := errors.New("nftables Privacy Envelope absent")
	called = false
	err := stager.verifyActiveL3With(context.Background(),
		func(context.Context, nmL3Intent) (nmL3ActiveObservation, error) {
			called = true
			return testNML3ValidActiveObservation(intent), nil
		},
		func(context.Context, networkSessionProtection) error { return fail })
	if !errors.Is(err, fail) || called {
		t.Fatalf("unprotected NM L3 observation admitted: err=%v called=%v", err, called)
	}
}
