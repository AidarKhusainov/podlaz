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
import dbus.service
from dbus.mainloop.glib import DBusGMainLoop
from gi.repository import GLib

SERVICE = "org.freedesktop.NetworkManager.podlazresearch"
IFACE = "org.freedesktop.NetworkManager.VPN.Plugin"
OBJECT = "/org/freedesktop/NetworkManager/VPN/Plugin"
MARKER = Path("/run/podlaz-nm-vpn-observer-invoked")


class Observer(dbus.service.Object):
    def __init__(self, bus: dbus.SystemBus):
        self._name = dbus.service.BusName(SERVICE, bus=bus)
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
    Observer(dbus.SystemBus())
    GLib.MainLoop().run()


if __name__ == "__main__":
    main()
