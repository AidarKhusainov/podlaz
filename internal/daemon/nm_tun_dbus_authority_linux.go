//go:build linux

package daemon

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	nmManagerPath                 = dbus.ObjectPath("/org/freedesktop/NetworkManager")
	nmManagerInterface            = "org.freedesktop.NetworkManager"
	nmDeviceInterface             = "org.freedesktop.NetworkManager.Device"
	nmActiveInterface             = "org.freedesktop.NetworkManager.Connection.Active"
	nmDeviceTypeTun        uint32 = 16
	nmDeviceStateActivated uint32 = 100
	nmActiveStateActivated uint32 = 2
)

var nmUUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// nmTunDBusAuthority is a future broker's *persisted*, boot-scoped selection.
// A lookalike D-Bus object or matching observed UUID is not cleanup authority.
// No current production path calls this verifier or uses this type for admission.
type nmTunDBusAuthority struct {
	UUID         string
	Name         string
	MTU          uint32
	DevicePath   dbus.ObjectPath
	ActivePath   dbus.ObjectPath
	SettingsPath dbus.ObjectPath
}

type nmTunDBusObservation struct {
	OwnerBefore       string
	OwnerAfter        string
	LookupDevice      dbus.ObjectPath
	DeviceInterface   string
	DeviceIPInterface string
	DeviceMTU         uint32
	DeviceType        uint32
	DeviceState       uint32
	DeviceManaged     bool
	DeviceActive      dbus.ObjectPath
	ActiveUUID        string
	ActiveType        string
	ActiveState       uint32
	ActiveDefaultIPv4 bool
	ActiveDevicePaths []dbus.ObjectPath
	ActiveSettings    dbus.ObjectPath
	ActiveIP4Config   dbus.ObjectPath
}

func validNMObjectPath(path dbus.ObjectPath, prefix string) bool {
	return path.IsValid() && strings.HasPrefix(string(path), prefix) && len(path) > len(prefix)
}

// verifyNMTunDBusAuthority is read-only: no NM activation, mutation, DNS or
// connectivity check. A pass means a *current observation* agrees with trusted
// expected identities, not that FD ownership/generation is atomically bound.
func verifyNMTunDBusAuthority(ctx context.Context, expected nmTunDBusAuthority) error {
	return verifyNMTunDBusAuthorityWith(ctx, expected, observeNMTunDBusAuthority)
}

func verifyNMTunDBusAuthorityWith(
	ctx context.Context,
	expected nmTunDBusAuthority,
	read func(context.Context, nmTunDBusAuthority) (nmTunDBusObservation, error),
) error {
	if ctx == nil || read == nil ||
		!nmUUIDPattern.MatchString(expected.UUID) ||
		!networkSessionInterfaceRegex.MatchString(expected.Name) ||
		expected.Name == "" || len(expected.Name) >= 16 ||
		expected.MTU == 0 ||
		!validNMObjectPath(expected.DevicePath, "/org/freedesktop/NetworkManager/Devices/") ||
		!validNMObjectPath(expected.ActivePath, "/org/freedesktop/NetworkManager/ActiveConnection/") ||
		!validNMObjectPath(expected.SettingsPath, "/org/freedesktop/NetworkManager/Settings/") {
		return errors.New("incomplete trusted NetworkManager TUN authority")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	found, err := read(ctx, expected)
	if err != nil {
		return fmt.Errorf("inspect NetworkManager TUN authority: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !strings.HasPrefix(found.OwnerBefore, ":") ||
		found.OwnerBefore != found.OwnerAfter ||
		found.LookupDevice != expected.DevicePath ||
		found.DeviceInterface != expected.Name || found.DeviceIPInterface != expected.Name ||
		found.DeviceMTU != expected.MTU || found.DeviceType != nmDeviceTypeTun ||
		found.DeviceState != nmDeviceStateActivated || !found.DeviceManaged ||
		found.DeviceActive != expected.ActivePath ||
		found.ActiveUUID != expected.UUID || found.ActiveType != "tun" ||
		found.ActiveState != nmActiveStateActivated || !found.ActiveDefaultIPv4 ||
		found.ActiveSettings != expected.SettingsPath ||
		!validNMObjectPath(found.ActiveIP4Config, "/org/freedesktop/NetworkManager/IP4Config/") ||
		len(found.ActiveDevicePaths) != 1 ||
		found.ActiveDevicePaths[0] != expected.DevicePath {
		return errors.New("NetworkManager TUN observation differs from trusted active connection")
	}
	return nil
}

// observeNMTunDBusAuthority pins all reads to the same unique NM bus owner.
// The final owner comparison refuses daemon replacement during observation.
// It does not make the multiple D-Bus property reads atomic.
func observeNMTunDBusAuthority(ctx context.Context, expected nmTunDBusAuthority) (nmTunDBusObservation, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := dbus.ConnectSystemBus(dbus.WithContext(ctx))
	if err != nil {
		return nmTunDBusObservation{}, err
	}
	defer conn.Close()
	getOwner := func() (string, error) {
		var name string
		err := conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetNameOwner", 0, networkManagerBusName).Store(&name)
		return name, err
	}
	owner, err := getOwner()
	if err != nil || !strings.HasPrefix(owner, ":") {
		return nmTunDBusObservation{}, errors.New("NetworkManager D-Bus owner unavailable")
	}
	root := conn.Object(owner, nmManagerPath)
	var devicePath dbus.ObjectPath
	if err := root.CallWithContext(ctx, nmManagerInterface+".GetDeviceByIpIface", 0, expected.Name).Store(&devicePath); err != nil {
		return nmTunDBusObservation{}, fmt.Errorf("find NetworkManager IP interface: %w", err)
	}
	if devicePath != expected.DevicePath {
		return nmTunDBusObservation{}, errors.New("NetworkManager IP interface device path changed")
	}

	device := conn.Object(owner, expected.DevicePath)
	active := conn.Object(owner, expected.ActivePath)
	var out nmTunDBusObservation
	out.OwnerBefore = owner
	out.LookupDevice = devicePath
	if out.DeviceInterface, err = readNMTunProperty[string](ctx, device, nmDeviceInterface, "Interface"); err != nil {
		return out, err
	}
	if out.DeviceIPInterface, err = readNMTunProperty[string](ctx, device, nmDeviceInterface, "IpInterface"); err != nil {
		return out, err
	}
	if out.DeviceMTU, err = readNMTunProperty[uint32](ctx, device, nmDeviceInterface, "Mtu"); err != nil {
		return out, err
	}
	if out.DeviceType, err = readNMTunProperty[uint32](ctx, device, nmDeviceInterface, "DeviceType"); err != nil {
		return out, err
	}
	if out.DeviceState, err = readNMTunProperty[uint32](ctx, device, nmDeviceInterface, "State"); err != nil {
		return out, err
	}
	if out.DeviceManaged, err = readNMTunProperty[bool](ctx, device, nmDeviceInterface, "Managed"); err != nil {
		return out, err
	}
	if out.DeviceActive, err = readNMTunProperty[dbus.ObjectPath](ctx, device, nmDeviceInterface, "ActiveConnection"); err != nil {
		return out, err
	}
	if out.ActiveUUID, err = readNMTunProperty[string](ctx, active, nmActiveInterface, "Uuid"); err != nil {
		return out, err
	}
	if out.ActiveType, err = readNMTunProperty[string](ctx, active, nmActiveInterface, "Type"); err != nil {
		return out, err
	}
	if out.ActiveState, err = readNMTunProperty[uint32](ctx, active, nmActiveInterface, "State"); err != nil {
		return out, err
	}
	if out.ActiveDefaultIPv4, err = readNMTunProperty[bool](ctx, active, nmActiveInterface, "Default"); err != nil {
		return out, err
	}
	if out.ActiveSettings, err = readNMTunProperty[dbus.ObjectPath](ctx, active, nmActiveInterface, "Connection"); err != nil {
		return out, err
	}
	if out.ActiveIP4Config, err = readNMTunProperty[dbus.ObjectPath](ctx, active, nmActiveInterface, "Ip4Config"); err != nil {
		return out, err
	}
	if out.ActiveDevicePaths, err = readNMTunProperty[[]dbus.ObjectPath](ctx, active, nmActiveInterface, "Devices"); err != nil {
		return out, err
	}
	out.OwnerAfter, err = getOwner()
	if err != nil {
		return out, errors.New("NetworkManager D-Bus owner disappeared during inspection")
	}
	return out, nil
}

func readNMTunProperty[T any](ctx context.Context, obj dbus.BusObject, iface, name string) (T, error) {
	var zero T
	var variant dbus.Variant
	if err := obj.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, iface, name).Store(&variant); err != nil {
		return zero, fmt.Errorf("read NetworkManager %s property: %w", name, err)
	}
	result, ok := variant.Value().(T)
	if !ok {
		return zero, fmt.Errorf("NetworkManager %s property has invalid D-Bus type", name)
	}
	return result, nil
}
