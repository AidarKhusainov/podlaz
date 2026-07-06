# Packaged TUN runtime helper

The Debian package is responsible for making the current TUN architecture self-contained for end users. A clean package install must not require users to install Xray manually before running TUN mode.

## Bundled helper

Packaged helper inputs are pinned in `packaging/runtime-helpers.env`.

The package build installs:

```text
/usr/lib/podlaz/xray
/usr/share/doc/podlaz/third-party/xray-LICENSE
```

Xray release archives are downloaded from the pinned release asset names and verified with pinned SHA-256 checksums before packaging.

The packaged systemd unit points the daemon at the bundled helper with an absolute path:

```ini
Environment=PODLAZ_XRAY_PATH=/usr/lib/podlaz/xray
```

TUN mode no longer ships or starts a separate `tun2socks` helper. Xray owns packet ingestion through its native `tun` inbound. `podlazd` still owns the host networking state around that link: route bypass, policy rules, DNS, nftables, transaction files, rollback, and recovery.

## Hard TUN dependencies

Packaged TUN mode relies on these host components and declares them as Debian dependencies:

- `iproute2` for route, policy rule, route verification, and recovery operations around the Xray-owned TUN link;
- `nftables` for firewall and kill-switch state;
- `systemd-resolved` for per-link DNS apply, verify, and rollback;
- `polkitd | policykit-1` for the packaged daemon authorization boundary.

`network-manager` remains optional diagnostics only and is not a hard package dependency.

## Preflight contract

Before applying host-networking changes for a TUN transaction, daemon-side connect checks that:

- the Xray helper resolves to an executable;
- packaged helpers under `/usr/lib/podlaz` match the running helper architecture when ELF metadata can be inspected;
- `ip`, `nft`, and `resolvectl` are available in the daemon execution environment;
- the pinned Xray helper accepts the generated native `tun` inbound config through `xray test -config`.

The generated config path is recorded in transaction rollback metadata before the preflight writes it. If the daemon is interrupted after the preflight write, recovery knows how to remove the generated config.

Missing or non-executable helpers, missing hard TUN commands, and unsupported Xray TUN config are setup/runtime-unavailable failures. They must fail before route, DNS, or nftables mutation starts.

User-facing CLI output must not present these failures as a daemon crash or raw internal server error. It must state that TUN mode cannot start and that no network changes were applied.

## CI gates

Package validation checks the Xray helper file, executable bit, architecture, service environment, declared dependencies, and third-party notice file for both `amd64` and `arm64` packages.

Installed package smoke checks verify that the helper file and notice are present after install and reinstall, and that they are removed on purge together with other packaged files.
