#!/usr/bin/env python3
"""Disposable guest-only NetworkManager VPN D-Bus observer.

The caller has already verified an Xray/Podlaz protected data plane. This
adapter does not create a TUN or apply addresses, routes, rules, DNS or
firewall configuration. Deliberately do not emit Ip4Config/Ip6Config: doing
so would delegate network configuration to a second writer.
"""
from __future__ import annotations

from pathlib import Path

import dbus
import dbus.bus
import dbus.service
from dbus.mainloop.glib import DBusGMainLoop
from gi.repository import GLib

SERVICE = "org.freedesktop.NetworkManager.podlazresearch"
IFACE = "org.freedesktop.NetworkManager.VPN.Plugin"
OBJECT = "/org/freedesktop/NetworkManager/VPN/Plugin"
MARKER = Path("/run/podlaz-nm-vpn-observer-invoked")
BUS_MARKER = Path("/run/podlaz-nm-vpn-observer-bus-ready")


class Observer(dbus.service.Object):
    def __init__(self, bus: dbus.SystemBus):
        super().__init__(bus, OBJECT)
        self._state = 1

    @dbus.service.method(IFACE, in_signature="a{sa{sv}}", out_signature="")
    def Connect(self, connection: object) -> None:
        # No connection content is inspected, stored or printed.
        if not Path("/sys/class/net/podlaz0").exists():
            raise dbus.exceptions.DBusException(
                "Xray-owned TUN is missing",
                name="org.freedesktop.NetworkManager.VPN.Error.BadArguments",
            )
        self._state = 3
        self.StateChanged(dbus.UInt32(self._state))
        MARKER.write_text("invoked\n", encoding="ascii")
        # A no-L3 notification is not a request for NetworkManager to own
        # any part of the existing Podlaz transaction.
        self.Config(
            dbus.Dictionary(
                {
                    "tundev": dbus.String("podlaz0"),
                    "has-ip4": dbus.Boolean(False),
                    "has-ip6": dbus.Boolean(False),
                    "can-persist": dbus.Boolean(False),
                },
                signature="sv",
            )
        )
        self._state = 4
        self.StateChanged(dbus.UInt32(self._state))

    @dbus.service.method(IFACE, in_signature="a{sa{sv}}a{sv}", out_signature="")
    def ConnectInteractive(self, connection: object, details: object) -> None:
        self.Connect(connection)

    @dbus.service.method(IFACE, in_signature="a{sa{sv}}", out_signature="s")
    def NeedSecrets(self, settings: object) -> str:
        return ""

    @dbus.service.method(IFACE, in_signature="a{sa{sv}}", out_signature="")
    def NewSecrets(self, connection: object) -> None:
        return None

    @dbus.service.method(IFACE, in_signature="", out_signature="")
    def Disconnect(self) -> None:
        self._state = 6
        self.StateChanged(dbus.UInt32(self._state))

    @dbus.service.method("org.freedesktop.DBus.Properties", in_signature="ss", out_signature="v")
    def Get(self, interface_name: str, property_name: str) -> dbus.UInt32:
        if interface_name != IFACE or property_name != "State":
            raise dbus.exceptions.DBusException(
                "Unknown property", name="org.freedesktop.DBus.Error.UnknownProperty"
            )
        return dbus.UInt32(self._state)

    @dbus.service.signal(IFACE, signature="u")
    def StateChanged(self, state: dbus.UInt32) -> None:
        pass

    @dbus.service.signal(IFACE, signature="a{sv}")
    def Config(self, config: dbus.Dictionary) -> None:
        pass


def main() -> None:
    DBusGMainLoop(set_as_default=True)
    # Keep the exported D-Bus object and its BusName alive for the whole
    # main loop; dropping the last reference can unregister the service.
    bus = dbus.SystemBus()
    reply = bus.request_name(SERVICE, dbus.bus.NAME_FLAG_DO_NOT_QUEUE)
    if reply != dbus.bus.REQUEST_NAME_REPLY_PRIMARY_OWNER:
        raise RuntimeError("VPN observer failed to become primary bus name owner")
    observer = Observer(bus)
    if not bus.name_has_owner(SERVICE):
        raise RuntimeError("VPN observer lost D-Bus name ownership")
    BUS_MARKER.write_text("owned\n", encoding="ascii")
    GLib.MainLoop().run()
    del observer


if __name__ == "__main__":
    main()
