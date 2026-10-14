#!/usr/bin/env python3
"""Disposable-NM VPN D-Bus L3 adapter; NEVER package for production.

Activation is deliberately blocked outside a dedicated nspawn guest and on
an interface already configured by Podlaz or another L3 writer. This service
cannot itself guarantee the Privacy Envelope: the enclosing isolated guest
scenario must qualify and arm it before NM's activation request is allowed.
"""
from __future__ import annotations

import json
import os
import subprocess

import dbus
import dbus.bus
import dbus.service
from dbus.mainloop.glib import DBusGMainLoop
from gi.repository import GLib

from nm_vpn_l3_contract import emit_nm_ipv4_config
from nm_vpn_l3_intent import load_guest_intent
from nm_vpn_l3_runtime_guard import validate_xray_tun

SERVICE = "org.freedesktop.NetworkManager.podlazl3research"
IFACE = "org.freedesktop.NetworkManager.VPN.Plugin"
OBJECT = "/org/freedesktop/NetworkManager/VPN/Plugin"


def _read_kernel_inventory() -> tuple[object, object]:
    """Read-only kernel inspection; never infer ownership from an address."""
    def query(*args: str) -> object:
        result = subprocess.run(("ip", "-j", *args), check=True,
                                capture_output=True, text=True, timeout=5)
        return json.loads(result.stdout)
    return query("-d", "link", "show", "dev", "podlaz0"), query("address", "show", "dev", "podlaz0")


class GuestPlugin(dbus.service.Object):
    def __init__(self, bus: dbus.SystemBus) -> None:
        super().__init__(bus, OBJECT)
        self._state = 1

    @dbus.service.method(IFACE, in_signature="a{sa{sv}}", out_signature="")
    def Connect(self, connection: object) -> None:
        # Input is ignored intentionally. The root-owned bounded intent is
        # the sole configuration source in this throwaway experiment.
        try:
            if os.geteuid() != 0:
                raise ValueError("root-only isolated guest adapter")
            config, ifindex = load_guest_intent()
            links, addresses = _read_kernel_inventory()
            validate_xray_tun(ifindex, links, addresses)
        except (ValueError, OSError, subprocess.SubprocessError, json.JSONDecodeError) as exc:
            # Do not forward sensitive guest state or network values to D-Bus.
            raise dbus.exceptions.DBusException(
                "Guest NM L3 authority or TUN preflight failed",
                name="org.freedesktop.NetworkManager.VPN.Error.BadArguments",
            ) from exc
        self._state = 3
        self.StateChanged(dbus.UInt32(self._state))
        emit_nm_ipv4_config(config, dbus,
                            lambda name, value: getattr(self, name)(value))
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
                "Unknown property", name="org.freedesktop.DBus.Error.UnknownProperty")
        return dbus.UInt32(self._state)

    @dbus.service.signal(IFACE, signature="u")
    def StateChanged(self, state: dbus.UInt32) -> None:
        pass

    @dbus.service.signal(IFACE, signature="a{sv}")
    def Config(self, config: dbus.Dictionary) -> None:
        pass

    @dbus.service.signal(IFACE, signature="a{sv}")
    def Ip4Config(self, config: dbus.Dictionary) -> None:
        pass


def main() -> None:
    # Validate the environment before registering the D-Bus name; Connect()
    # revalidates to protect against state changes between setup and call.
    if os.geteuid() != 0:
        raise SystemExit("root-only guest adapter")
    load_guest_intent()
    DBusGMainLoop(set_as_default=True)
    bus = dbus.SystemBus()
    name_reply = bus.request_name(SERVICE, dbus.bus.NAME_FLAG_DO_NOT_QUEUE)
    if name_reply != dbus.bus.REQUEST_NAME_REPLY_PRIMARY_OWNER:
        raise SystemExit("guest-only NM VPN service name occupied")
    plugin = GuestPlugin(bus)
    GLib.MainLoop().run()
    del plugin


if __name__ == "__main__":
    main()
