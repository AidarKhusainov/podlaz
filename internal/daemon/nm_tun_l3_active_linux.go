//go:build linux

package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"reflect"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

const nmIP4ConfigInterface = "org.freedesktop.NetworkManager.IP4Config"
const nmDeviceTunInterface = "org.freedesktop.NetworkManager.Device.Tun"

// nmL3ActiveObservation is read-only D-Bus evidence. In particular the
// observed device path is NOT authority for adoption, cleanup, or FD handoff.
type nmL3ActiveObservation struct {
	OwnerBefore string
	OwnerAfter string
	UUID string
	ConnectionType string
	SettingsPath dbus.ObjectPath
	DevicePath dbus.ObjectPath
	LookupDevice dbus.ObjectPath
	DeviceActive dbus.ObjectPath
	Interface string
	IPInterface string
	MTU uint32
	DeviceType uint32
	DeviceState uint32
	DeviceManaged bool
	TunMode string
	TunNoPI bool
	TunVnetHdr bool
	TunMultiQueue bool
	TunOwner int64
	TunGroup int64
	ActiveState uint32
	Default4 bool
	Default6 bool
	IP4Path dbus.ObjectPath
	Addresses []map[string]dbus.Variant
	Routes []map[string]dbus.Variant
	Nameservers []map[string]dbus.Variant
	Searches []string
	DNSPriority int32
}

// verifyActiveL3 checks actual NM-owned L3 under an already-armed, real
// Privacy Envelope. A successful result is NOT VPN connectivity, global FULL,
// a generation admit capability, or license to delete the observed link.
func (s nmL3Stager) verifyActiveL3(ctx context.Context) error {
	return s.verifyActiveL3With(ctx, observeNML3Active, verifyNML3Barrier)
}

func (s nmL3Stager) verifyActiveL3With(
	ctx context.Context,
	observe func(context.Context, nmL3Intent) (nmL3ActiveObservation, error),
	verifyBarrier func(context.Context, networkSessionProtection) error,
) error {
	if ctx == nil || observe == nil || verifyBarrier == nil {
		return errors.New("NM L3 verification requires a context and exact observers")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	expected, exists, err := s.loadIntent()
	if err != nil {
		return fmt.Errorf("read exact NM L3 intent: %w", err)
	}
	if !exists || expected.Stage != "activating" {
		return errors.New("NM L3 has no completed activation request")
	}
	session, err := s.readExactSessionWithoutMutation()
	if err != nil {
		return err
	}
	if !nmL3SessionProtected(session, expected) {
		return errors.New("NM L3 lacks exact armed marked Network Session")
	}
	if err := verifyBarrier(ctx, *session.Protection); err != nil {
		return fmt.Errorf("NM L3 Privacy Envelope inspection: %w", err)
	}
	observed, err := observe(ctx, expected)
	if err != nil {
		return fmt.Errorf("read NM L3 active evidence: %w", err)
	}
	if err := verifyNML3ActiveEvidence(expected, observed); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := verifyBarrier(ctx, *session.Protection); err != nil {
		return fmt.Errorf("NM L3 Privacy Envelope changed during inspection: %w", err)
	}
	after, exists, err := s.loadIntent()
	if err != nil {
		return err
	}
	if !exists || !reflect.DeepEqual(after, expected) {
		return errors.New("NM L3 durable intent changed during inspection")
	}
	afterSession, err := s.readExactSessionWithoutMutation()
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(afterSession, session) {
		return errors.New("NM L3 Network Session changed during inspection")
	}
	return ctx.Err()
}

func nmL3SessionProtected(session networkSessionState, intent nmL3Intent) bool {
	p := session.Protection
	if session.BootID != intent.BootID || session.SessionID != intent.SessionID ||
		session.Intent != networkSessionIntentResume || session.Request.Mode != "tun" || session.Replacement != nil ||
		p == nil || p.State != networkSessionProtectionArmed ||
		p.CompositionVersion != privacyEnvelopeMarkedCompositionVersion ||
		p.TunInterface != intent.Interface || len(p.PreviousEgressMarks) != 0 {
		return false
	}
	for _, mark := range p.EgressMarks {
		if mark == intent.Mark {
			return true
		}
	}
	return false
}

func verifyNML3ActiveEvidence(expected nmL3Intent, observed nmL3ActiveObservation) error {
	if !strings.HasPrefix(observed.OwnerBefore, ":") || observed.OwnerBefore != observed.OwnerAfter ||
		observed.UUID != expected.UUID || observed.ConnectionType != "tun" || observed.SettingsPath != expected.SettingsPath ||
		!validNMObjectPath(observed.DevicePath, "/org/freedesktop/NetworkManager/Devices/") || observed.DevicePath != observed.LookupDevice ||
		observed.DeviceActive != expected.ActivePath ||
		observed.Interface != expected.Interface || observed.IPInterface != expected.Interface ||
		observed.MTU != expected.MTU || observed.DeviceType != nmDeviceTypeTun ||
		observed.DeviceState != nmDeviceStateActivated || !observed.DeviceManaged ||
		observed.TunMode != "tun" || !observed.TunNoPI || observed.TunVnetHdr ||
		observed.TunMultiQueue || observed.TunOwner != 0 || observed.TunGroup != 0 ||
		observed.ActiveState != nmActiveStateActivated || !observed.Default4 || observed.Default6 ||
		!validNMObjectPath(observed.IP4Path, "/org/freedesktop/NetworkManager/IP4Config/") ||
		observed.DNSPriority != -30000 ||
		len(observed.Searches) != 1 || observed.Searches[0] != "~." {
		return errors.New("active NetworkManager TUN identity or L3 ownership mismatch")
	}
	prefix, err := netip.ParsePrefix(expected.IPv4)
	if err != nil {
		return errors.New("invalid durable NM IPv4 allocation")
	}
	if len(observed.Addresses) != 1 ||
		!nmL3DictString(observed.Addresses[0], "address", prefix.Addr().String()) ||
		!nmL3DictUint32(observed.Addresses[0], "prefix", uint32(prefix.Bits())) {
		return errors.New("active NetworkManager IPv4 address differs from durable intent")
	}
	defaults := 0
	for _, route := range observed.Routes {
		dest, destOK := nmL3VariantString(route, "dest")
		mask, maskOK := nmL3VariantUint32(route, "prefix")
		if !destOK || !maskOK {
			return errors.New("NetworkManager returned malformed route evidence")
		}
		if mask == 0 {
			defaults++
			if dest != "0.0.0.0" || !nmL3DictUint32(route, "metric", expected.Metric) {
				return errors.New("NetworkManager default route differs from durable intent")
			}
			if nextHop, exists := route["next-hop"]; exists {
				value, ok := nextHop.Value().(string)
				if !ok || (value != "" && value != "0.0.0.0") {
					return errors.New("NetworkManager default route has unexpected gateway")
				}
			}
		}
	}
	if defaults != 1 || len(observed.Nameservers) != 1 ||
		!nmL3DictString(observed.Nameservers[0], "address", expected.DNS) {
		return errors.New("NetworkManager default route or resolver differs from durable intent")
	}
	return nil
}

func nmL3VariantString(data map[string]dbus.Variant, name string) (string, bool) {
	v, exists := data[name]
	if !exists {
		return "", false
	}
	value, ok := v.Value().(string)
	return value, ok
}

func nmL3VariantUint32(data map[string]dbus.Variant, name string) (uint32, bool) {
	v, exists := data[name]
	if !exists {
		return 0, false
	}
	value, ok := v.Value().(uint32)
	return value, ok
}

func nmL3DictString(data map[string]dbus.Variant, name, expected string) bool {
	v, ok := nmL3VariantString(data, name)
	return ok && v == expected
}

func nmL3DictUint32(data map[string]dbus.Variant, name string, expected uint32) bool {
	v, ok := nmL3VariantUint32(data, name)
	return ok && v == expected
}

// observeNML3Active pins each read to the same unique NM bus owner and
// rechecks that owner afterwards. Property reads are not atomic; concurrent
// mutation is additionally guarded by repeated durable/envelope observations.
func observeNML3Active(ctx context.Context, expected nmL3Intent) (nmL3ActiveObservation, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := dbus.ConnectSystemBus(dbus.WithContext(ctx))
	if err != nil {
		return nmL3ActiveObservation{}, err
	}
	defer conn.Close()
	getOwner := func() (string, error) {
		var owner string
		err := conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetNameOwner", 0, networkManagerBusName).Store(&owner)
		return owner, err
	}
	owner, err := getOwner()
	if err != nil || !strings.HasPrefix(owner, ":") {
		return nmL3ActiveObservation{}, errors.New("NM bus owner is unavailable")
	}
	active := conn.Object(owner, expected.ActivePath)
	var obs nmL3ActiveObservation
	obs.OwnerBefore = owner
	if obs.UUID, err = readNMTunProperty[string](ctx, active, nmActiveInterface, "Uuid"); err != nil {
		return obs, err
	}
	if obs.ConnectionType, err = readNMTunProperty[string](ctx, active, nmActiveInterface, "Type"); err != nil {
		return obs, err
	}
	if obs.SettingsPath, err = readNMTunProperty[dbus.ObjectPath](ctx, active, nmActiveInterface, "Connection"); err != nil {
		return obs, err
	}
	if obs.ActiveState, err = readNMTunProperty[uint32](ctx, active, nmActiveInterface, "State"); err != nil {
		return obs, err
	}
	if obs.Default4, err = readNMTunProperty[bool](ctx, active, nmActiveInterface, "Default"); err != nil {
		return obs, err
	}
	if obs.Default6, err = readNMTunProperty[bool](ctx, active, nmActiveInterface, "Default6"); err != nil {
		return obs, err
	}
	var devices []dbus.ObjectPath
	if devices, err = readNMTunProperty[[]dbus.ObjectPath](ctx, active, nmActiveInterface, "Devices"); err != nil {
		return obs, err
	}
	if len(devices) != 1 || !validNMObjectPath(devices[0], "/org/freedesktop/NetworkManager/Devices/") {
		return obs, errors.New("NM connection has no unique TUN device")
	}
	obs.DevicePath = devices[0]
	if obs.IP4Path, err = readNMTunProperty[dbus.ObjectPath](ctx, active, nmActiveInterface, "Ip4Config"); err != nil {
		return obs, err
	}
	root := conn.Object(owner, nmManagerPath)
	if err := root.CallWithContext(ctx, nmManagerInterface+".GetDeviceByIpIface", 0, expected.Interface).Store(&obs.LookupDevice); err != nil {
		return obs, err
	}
	device := conn.Object(owner, obs.DevicePath)
	if obs.Interface, err = readNMTunProperty[string](ctx, device, nmDeviceInterface, "Interface"); err != nil {
		return obs, err
	}
	if obs.IPInterface, err = readNMTunProperty[string](ctx, device, nmDeviceInterface, "IpInterface"); err != nil {
		return obs, err
	}
	if obs.MTU, err = readNMTunProperty[uint32](ctx, device, nmDeviceInterface, "Mtu"); err != nil {
		return obs, err
	}
	if obs.DeviceType, err = readNMTunProperty[uint32](ctx, device, nmDeviceInterface, "DeviceType"); err != nil {
		return obs, err
	}
	if obs.DeviceState, err = readNMTunProperty[uint32](ctx, device, nmDeviceInterface, "State"); err != nil {
		return obs, err
	}
	if obs.DeviceManaged, err = readNMTunProperty[bool](ctx, device, nmDeviceInterface, "Managed"); err != nil {
		return obs, err
	}
	if obs.DeviceActive, err = readNMTunProperty[dbus.ObjectPath](ctx, device, nmDeviceInterface, "ActiveConnection"); err != nil {
		return obs, err
	}
	tun := conn.Object(owner, obs.DevicePath)
	if obs.TunMode, err = readNMTunProperty[string](ctx, tun, nmDeviceTunInterface, "Mode"); err != nil {
		return obs, err
	}
	if obs.TunNoPI, err = readNMTunProperty[bool](ctx, tun, nmDeviceTunInterface, "NoPi"); err != nil {
		return obs, err
	}
	if obs.TunVnetHdr, err = readNMTunProperty[bool](ctx, tun, nmDeviceTunInterface, "VnetHdr"); err != nil {
		return obs, err
	}
	if obs.TunMultiQueue, err = readNMTunProperty[bool](ctx, tun, nmDeviceTunInterface, "MultiQueue"); err != nil {
		return obs, err
	}
	if obs.TunOwner, err = readNMTunProperty[int64](ctx, tun, nmDeviceTunInterface, "Owner"); err != nil {
		return obs, err
	}
	if obs.TunGroup, err = readNMTunProperty[int64](ctx, tun, nmDeviceTunInterface, "Group"); err != nil {
		return obs, err
	}
	ip4 := conn.Object(owner, obs.IP4Path)
	if obs.Addresses, err = readNMTunProperty[[]map[string]dbus.Variant](ctx, ip4, nmIP4ConfigInterface, "AddressData"); err != nil {
		return obs, err
	}
	if obs.Routes, err = readNMTunProperty[[]map[string]dbus.Variant](ctx, ip4, nmIP4ConfigInterface, "RouteData"); err != nil {
		return obs, err
	}
	if obs.Nameservers, err = readNMTunProperty[[]map[string]dbus.Variant](ctx, ip4, nmIP4ConfigInterface, "NameserverData"); err != nil {
		return obs, err
	}
	if obs.Searches, err = readNMTunProperty[[]string](ctx, ip4, nmIP4ConfigInterface, "Searches"); err != nil {
		return obs, err
	}
	if obs.DNSPriority, err = readNMTunProperty[int32](ctx, ip4, nmIP4ConfigInterface, "DnsPriority"); err != nil {
		return obs, err
	}
	obs.OwnerAfter, err = getOwner()
	if err != nil {
		return obs, errors.New("NM bus owner disappeared during L3 inspection")
	}
	return obs, nil
}
