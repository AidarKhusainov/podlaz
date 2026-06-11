# Debian package contract

This document defines the local Debian package layout, lifecycle behavior, validation targets, and inspection gates for TunWarden.

The package contract is intentionally limited to a locally installable `.deb` artifact. Public apt repository publication and repository signing are separate follow-up work. Tagged GitHub Release publication is owned by [Release workflow](./release.md) and consumes this package contract instead of redefining it.

## Supported validation targets

Initial package validation targets:

- Debian stable on `amd64`.
- Ubuntu LTS on `amd64`.

`arm64` is supported by the packaging script when the Go toolchain and build host can produce the target binary, but `arm64` must not block the first package acceptance gate.

Container validation is acceptable for package metadata, file layout, and basic install/remove checks. Full service lifecycle validation, including `systemctl status tunwardend`, must be done on a VM or host where systemd is PID 1.

## Package toolchain

The package toolchain version contract is owned by `packaging/package-toolchain.env`.

CI, local package validation, and release automation must install the pinned `NFPM_VERSION` from that file instead of using `@latest`. This keeps package generation reproducible and prevents future upstream nFPM releases from changing `.deb` output without a repository change.

## Build contract

Build a local package with:

```bash
bash scripts/build-deb.sh
```

The default build emits:

```text
dist/tunwarden_0.0.0~dev_amd64.deb
```

Override the binary version, Debian package version, and architecture with:

```bash
TUNWARDEN_VERSION=0.1.1 TUNWARDEN_DEB_VERSION=0.1.1 TUNWARDEN_DEB_ARCH=amd64 bash scripts/build-deb.sh
```

`TUNWARDEN_VERSION` controls the upstream application version reported by `tunwarden version`. `TUNWARDEN_DEB_VERSION` controls Debian package metadata and the package filename. When `TUNWARDEN_DEB_VERSION` is unset, the package version defaults to `TUNWARDEN_VERSION`.

Development builds use Debian-compatible `0.0.0~dev`, which sorts before a real `0.0.0` release. Tagged GitHub Release automation maps `vMAJOR.MINOR.PATCH` to binary version `MAJOR.MINOR.PATCH` and Debian package version `MAJOR.MINOR.PATCH`.

Public apt repository packaging and Debian revision policy are intentionally deferred to the apt repository work. Do not pass Debian revisions such as `0.1.1-1` as `TUNWARDEN_DEB_VERSION` until the packaging manifest explicitly models Debian revision separately.

The build requires:

- Go with the project-pinned toolchain from `go.mod`.
- The pinned `nfpm` version from `packaging/package-toolchain.env`.
- `gzip`.
- Debian package tools such as `dpkg-deb` for inspection.
- `man-db` for installed man page validation.

The build script prepares a temporary package root under `dist/package-root`. That directory is build output only and must not be committed. The script also renders a temporary `.nfpm.tunwarden.yaml` config in the repository root and removes it after package generation.

## Installed file layout

The package installs only packaged files under Debian/FHS-appropriate locations:

```text
/usr/bin/tunwarden
/usr/bin/tunwardend
/usr/lib/systemd/system/tunwardend.service
/usr/lib/sysusers.d/tunwarden.conf
/usr/share/man/man1/tunwarden.1.gz
/usr/share/man/man8/tunwardend.8.gz
/usr/share/doc/tunwarden/README.md
/usr/share/doc/tunwarden/LICENSE
/usr/share/doc/tunwarden/copyright
/usr/share/doc/tunwarden/changelog.gz
/usr/share/doc/tunwarden/docs/...
```

The package must not install packaged files under:

- `/usr/local`
- `/run/tunwarden`
- `/var/run/tunwarden`
- user home directories
- generated runtime configuration paths

Generated Xray configs, transaction files, daemon sockets, PID/process state, and other daemon runtime outputs are not packaged files.

## Package metadata

The Debian package metadata is owned by `packaging/nfpm.yaml`.

Initial metadata contract:

- package name: `tunwarden`
- section: `net`
- priority: `optional`
- architecture: `amd64` by default, `arm64` optional
- maintainer: project maintainer metadata from the package manifest
- runtime dependencies: `libc6`, `systemd`, and `ca-certificates`

The package depends on `libc6` because the package intentionally ships dynamically linked, stripped Linux binaries to satisfy Debian package validation. The package depends on `systemd` because the installed service contract uses systemd unit, sysusers, runtime/state directory management, and journald-oriented diagnostics. The package depends on `ca-certificates` because profile/subscription and future core download flows rely on TLS trust roots.

## Service install behavior

Installing the package must not unexpectedly change live host networking.

Package installation:

- installs the CLI and daemon binaries;
- installs the systemd unit;
- installs the sysusers configuration;
- creates the package service identities through `systemd-sysusers` when that command is available;
- records systemd unit state through Debian systemd helper tools when available;
- does not start `tunwardend.service`;
- does not enable `tunwardend.service`;
- does not start Xray;
- does not create TUN devices;
- does not change routes, DNS, nftables, firewall rules, or host resolver files.

The user must explicitly start the service on a systemd host:

```bash
sudo systemctl start tunwardend
```

The user must explicitly enable the service if they want boot-time startup:

```bash
sudo systemctl enable tunwardend
```

The packaged unit uses the packaged daemon path:

```ini
ExecStart=/usr/bin/tunwardend
```

## State ownership and lifecycle

The package lifecycle distinguishes these state categories.

| Category | Location | Owner | Package behavior |
| --- | --- | --- | --- |
| Packaged files | `/usr/bin`, `/usr/lib/systemd/system`, `/usr/lib/sysusers.d`, `/usr/share/man`, `/usr/share/doc/tunwarden` | Debian package manager | Installed, upgraded, and removed by `dpkg`/`apt`. |
| Daemon runtime state | `/run/tunwarden` | `tunwardend` through systemd `RuntimeDirectory=` | Volatile; created when the service starts; not shipped in the package. |
| Generated runtime configs | `/run/tunwarden/generated` | `tunwardend` | Runtime output only; not shipped in the package and not persistent source of truth. |
| Daemon persistent state | `/var/lib/tunwarden` | systemd `StateDirectory=` and daemon | Reserved for daemon-owned persistent state; not shipped as packaged files. |
| User intent/state | `$XDG_CONFIG_HOME/tunwarden`, `$XDG_STATE_HOME/tunwarden`, `$XDG_CACHE_HOME/tunwarden` | invoking user | Not owned, modified, or removed by package lifecycle scripts. |

### Fresh install

A fresh install places packaged files, registers the unit, and creates sysusers identities when possible. It must not start or enable the daemon and must not alter VPN/networking state.

### Reinstall and upgrade

A same-version reinstall or package upgrade replaces packaged files. Existing daemon runtime state and `/var/lib/tunwarden` persistent daemon state are not packaged files and are not deleted by package unpack. If the service is running during an upgrade, Debian package replacement semantics apply to files on disk; full live service upgrade/restart policy is deferred until release automation and service lifecycle hardening explicitly define it.

The CI package gate validates the practical same-version reinstall path with:

```bash
sudo apt install -y --reinstall ./dist/tunwarden_0.0.0~dev_amd64.deb
```

### Remove

Package removal stops `tunwardend.service` when Debian systemd helper tools are available and then removes packaged files through the package manager. It does not remove user-owned XDG profile/subscription state. It does not remove `/var/lib/tunwarden` persistent daemon state; that state is treated as administrator-visible application state.

### Purge

If purge is requested, maintainer hooks ask Debian systemd helper tools to purge recorded unit state when available. User-owned XDG state is still not removed. Dedicated service users created by sysusers are not manually deleted by TunWarden maintainer scripts.

## Inspection and validation gates

Package inspection:

```bash
dpkg-deb --info dist/tunwarden_0.0.0~dev_amd64.deb
dpkg-deb --contents dist/tunwarden_0.0.0~dev_amd64.deb
```

Local install validation:

```bash
sudo apt install ./dist/tunwarden_0.0.0~dev_amd64.deb
dpkg -L tunwarden
tunwarden version
man -l /usr/share/man/man1/tunwarden.1.gz >/dev/null
man -l /usr/share/man/man8/tunwardend.8.gz >/dev/null
systemctl status tunwardend
```

Removal validation:

```bash
sudo apt remove tunwarden
dpkg -L tunwarden
```

`dpkg -L tunwarden` should report that the package is not installed after removal. No packaged files should remain. Runtime, persistent, and user state may remain only according to the lifecycle table above.

Use `lintian` where practical:

```bash
lintian dist/tunwarden_0.0.0~dev_amd64.deb
```

A package PR must document whether `lintian` was clean or list every relevant warning with justification.

## Container versus VM validation

Container validation can check:

- package metadata;
- package contents;
- local install/remove mechanics;
- absence of `/usr/local`, `/run`, `/var/run`, user home, and generated runtime config packaged paths;
- binary and man page presence.

Container validation is not sufficient for:

- service status behavior when systemd is not PID 1;
- daemon startup under the packaged unit;
- journald integration;
- service runtime directory creation through systemd;
- any real networking lifecycle.

Those checks require a VM or systemd-capable host.
