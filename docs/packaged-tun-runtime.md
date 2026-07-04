# Packaged TUN runtime helpers

The Debian package is responsible for making the current TUN architecture self-contained for end users. A clean package install must not require users to install Xray or tun2socks manually before running TUN mode.

## Bundled helpers

Packaged helper inputs are pinned in `packaging/runtime-helpers.env`.

The package build installs:

```text
/usr/lib/podlaz/xray
/usr/lib/podlaz/tun2socks
/usr/share/doc/podlaz/third-party/xray-LICENSE
/usr/share/doc/podlaz/third-party/tun2socks-LICENSE
```

Xray release archives are downloaded from the pinned release asset names and verified with pinned SHA-256 checksums before packaging. tun2socks is built from the pinned Go module version during package build, with the module download verified by Go's module checksum mechanism.

The packaged systemd unit points the daemon at the bundled helpers with absolute paths:

```ini
Environment=PODLAZ_XRAY_PATH=/usr/lib/podlaz/xray
Environment=PODLAZ_TUN2SOCKS_PATH=/usr/lib/podlaz/tun2socks
```

## Hard TUN dependencies

Packaged TUN mode relies on these host components and declares them as Debian dependencies:

- `iproute2` for TUN device, route, policy rule, route verification, and recovery operations;
- `nftables` for firewall and kill-switch state;
- `systemd-resolved` for per-link DNS apply, verify, and rollback;
- `polkitd | policykit-1` for the packaged daemon authorization boundary.

`network-manager` remains optional diagnostics only and is not a hard package dependency.

## Preflight contract

Before applying a TUN transaction, daemon-side connect checks that:

- the Xray helper resolves to an executable;
- the TUN adapter helper resolves to an executable;
- packaged helpers under `/usr/lib/podlaz` match the running helper architecture when ELF metadata can be inspected;
- `ip`, `nft`, and `resolvectl` are available in the daemon execution environment.

Missing or non-executable helpers and missing hard TUN commands are setup/runtime-unavailable failures. They must fail before any TUN device, route, DNS, or nftables mutation starts.

User-facing CLI output must not present these failures as a daemon crash or raw internal server error. It must state that TUN mode cannot start and that no network changes were applied.

## CI gates

Package validation checks the helper files, executable bits, architecture, service environment, declared dependencies, and third-party notice files for both `amd64` and `arm64` packages.

Installed package smoke checks verify that the helper files and notices are present after install and reinstall, and that they are removed on purge together with other packaged files.
