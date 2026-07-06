# Debian package contract

This document defines the package-level behavior that CI and reviewers should validate. It is intentionally narrower than a generic Linux packaging guide: it describes what the `podlaz` package owns, what it must not own, and which host/networking side effects are allowed only at runtime.

## Package contents

The package installs only packaged files under Debian/FHS-appropriate locations:

```text
/usr/bin/podlaz
/usr/bin/plz -> /usr/bin/podlaz
/usr/bin/podlazd
/usr/lib/podlaz/xray
/usr/lib/systemd/system/podlazd.service
/usr/lib/sysusers.d/podlaz.conf
/usr/share/bash-completion/completions/podlaz
/usr/share/bash-completion/completions/plz
/usr/share/zsh/vendor-completions/_podlaz
/usr/share/zsh/vendor-completions/_plz
/usr/share/fish/vendor_completions.d/podlaz.fish
/usr/share/fish/vendor_completions.d/plz.fish
/usr/share/polkit-1/actions/io.github.aidarkhusainov.podlaz.policy
/usr/share/man/man1/podlaz.1.gz
/usr/share/man/man8/podlazd.8.gz
/usr/share/doc/podlaz/LICENSE
/usr/share/doc/podlaz/copyright
/usr/share/doc/podlaz/changelog.Debian.gz
/usr/share/doc/podlaz/third-party/xray-LICENSE
/usr/share/doc/podlaz/docs/...
```

The package must not install files under `/usr/local`, `/run`, `/var/run`, `$HOME`, user XDG directories, or runtime/generated state directories.

The package must not install AppStream metadata, desktop files, icons, autostart entries, browser integrations, or unrelated GUI shell integration.

## Package metadata

Initial metadata contract:

- package name: `podlaz`
- section: `net`
- priority: `optional`
- architecture: `amd64` by default, `arm64` supported through `PODLAZ_DEB_ARCH=arm64`
- maintainer: project maintainer metadata from the package manifest
- runtime dependencies: `libc6`, `systemd`, `ca-certificates`, `iproute2`, `nftables`, `systemd-resolved | systemd`, and `polkitd | policykit-1`
- polkit action namespace: `io.github.aidarkhusainov.podlaz.*`

The package depends on `libc6` because it ships dynamically linked Linux binaries. It depends on `systemd` because the installed service contract uses systemd unit, sysusers, runtime/state directory management, and journald-oriented diagnostics. It depends on `ca-certificates` because profile/subscription and packaged Xray validation rely on TLS trust roots.

TUN mode requires `iproute2`, `nftables`, and `systemd-resolved` for route, policy-rule, firewall, resolver, verification, rollback, and recovery operations around the Xray-owned TUN link. The package installs the pinned Xray helper under `/usr/lib/podlaz/xray`; it does not ship `tun2socks`.

## Service install behavior

Package installation:

- installs the CLI and daemon binaries;
- installs `plz` as a symlink to the canonical CLI binary;
- installs the pinned Xray helper and its third-party notice file;
- installs shell completion files for both `podlaz` and `plz`;
- installs the optional polkit action file;
- installs the systemd unit;
- installs the sysusers configuration;
- creates or declares packaged service identities through `systemd-sysusers` when that command is available;
- unmasks, enables, and records `podlazd.service` state through Debian systemd helper tools when available;
- repairs stale Debian helper-state-only enablement only for install-from-Config-Files package state marked by `preinstall` and confirmed by Debian helper state;
- requests `podlazd.service` startup through Debian systemd invocation helper tools when the unit is enabled after helper processing;
- does not start Xray because of package installation alone;
- does not create TUN devices;
- does not change routes, DNS, nftables, firewall rules, or host resolver files;
- does not enable polkit authorization by itself;
- does not install AppStream metadata, desktop entries, or icons.

Users who should access the packaged daemon from the ordinary CLI need one-time membership in the `podlaz` group and a refreshed login/session group set before using daemon-backed commands:

```bash
sudo usermod -aG podlaz "$USER"
newgrp podlaz
podlaz status
podlaz doctor
```

This group-mediated access applies to the daemon socket only. The packaged daemon itself runs as `root:podlaz` with a bounded capability set for networking, child identity transitions, and child process signaling.

Proxy-only Xray is started as the dedicated `podlaz-xray:podlaz-xray` identity without `CAP_NET_ADMIN`. Native TUN Xray is also started as `podlaz-xray:podlaz-xray`, but receives only `CAP_NET_ADMIN` as an ambient capability so pinned Xray can open `/dev/net/tun` and configure the Xray-owned `podlaz0` link. The unit keeps `NoNewPrivileges=yes` and does not rely on file capabilities or setuid helpers.

## Packaged daemon socket boundary

Packaged installs keep the filesystem daemon socket narrow and may expose an abstract Unix socket for the polkit-gated daemon boundary. CLI clients first try the filesystem socket. If that attempt fails with a transport-level permission error, the client retries the packaged abstract socket.

The fallback is not a generic error-masking layer. Daemon responses from the abstract socket are surfaced as-is: authorization denied, authorization unavailable on headless/server systems, malformed JSON, and invalid daemon responses remain distinct from a stopped or unreachable daemon.

## State ownership and lifecycle

| Category | Location | Owner | Package behavior |
| --- | --- | --- | --- |
| Packaged files | `/usr/bin`, `/usr/lib/podlaz`, `/usr/lib/systemd/system`, `/usr/lib/sysusers.d`, `/usr/share/bash-completion`, `/usr/share/zsh`, `/usr/share/fish`, `/usr/share/polkit-1/actions`, `/usr/share/man`, `/usr/share/doc/podlaz` | Debian package manager | Installed, upgraded, and removed by `dpkg`/`apt`. |
| Daemon runtime state | `/run/podlaz` | `podlazd` through systemd `RuntimeDirectory=` | Volatile; not shipped in the package. |
| Daemon persistent state | `/var/lib/podlaz` | systemd `StateDirectory=` and daemon | Reserved for daemon-owned persistent state; not shipped as packaged files. |
| User intent/state | `$XDG_CONFIG_HOME/podlaz`, `$XDG_STATE_HOME/podlaz`, `$XDG_CACHE_HOME/podlaz` | invoking user | Not owned, modified, or removed by package lifecycle scripts. |

## Inspection and validation gates

Package inspection should verify metadata and packaged paths for both supported architectures:

```text
dist/podlaz_0.0.0~dev-1_linux_amd64.deb
dist/podlaz_0.0.0~dev-1_linux_arm64.deb
```

The package gate validates the declarative packaged contract: sysusers identities, service `User=`/`Group=`, `UMask=`, runtime and state directory modes, bounded daemon capabilities, the ambient `CAP_NET_ADMIN` required only for native TUN Xray, packaged Xray helper layout and architecture, static polkit action IDs, absence of broad polkit defaults, `plz` alias and alias completion files, absence of AppStream/metainfo files, Debian helper-based daemon availability hooks, and absence of direct `systemctl start` or `systemctl enable` maintainer-script calls. The maintainer-script regression tests validate the stale helper-state repair contract and the wider raw `systemctl start|enable` guard.
