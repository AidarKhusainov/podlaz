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

TUN mode requires `iproute2`, `nftables`, and `systemd-resolved` for exact
session-allocated IPv4 address ownership, route and policy-rule management,
firewall, functional scoped resolver verification, rollback, and recovery around
the Xray-owned TUN link. Historical `198.18.0.1/32`, table `51820`, and
priorities `9999`/`10000` are preferred allocation candidates only; package
ownership is the exact allocation persisted for the current Network Session. The
package installs the pinned Xray helper under `/usr/lib/podlaz/xray` as the only
bundled runtime helper.

## Service install behavior

Package installation:

- installs the CLI and daemon binaries;
- installs `plz` as a symlink to the canonical CLI binary;
- installs the pinned Xray helper and its third-party notice file;
- installs shell completion files for both `podlaz` and `plz`;
- installs the polkit action file;
- installs the systemd unit;
- installs the sysusers configuration;
- creates or declares packaged service identities through `systemd-sysusers` when that command is available;
- unmasks, enables, and records `podlazd.service` state through Debian systemd helper tools when available;
- repairs stale Debian helper-state-only enablement only for install-from-Config-Files package state marked by `preinstall` and confirmed by Debian helper state;
- requests `podlazd.service` startup through Debian systemd invocation helper tools when the unit is enabled after helper processing;
- enables polkit authorization for packaged daemon access;
- does not start Xray because of package installation alone;
- does not create TUN devices;
- does not change routes, DNS, nftables, firewall rules, or host resolver files;
- does not install AppStream metadata, desktop entries, or icons.

Ordinary CLI users do not need membership in the `podlaz` group for standard packaged use. The packaged service runs with `PODLAZ_SERVICE=systemd` and `PODLAZ_POLKIT_AUTHORIZATION=required`, keeps the filesystem socket as an internal/admin fallback, and exposes the packaged abstract Unix socket as the normal local IPC path for user-facing CLI commands. Privileged operations are authorized through polkit; when authentication is required, the system polkit agent is responsible for prompting for an admin password.

The internal `podlaz` and `podlaz-xray` system identities still exist for daemon/runtime isolation. The packaged daemon runs as `root:podlaz` with a bounded capability set for networking, child identity transitions, and child process signaling, but ordinary users should not be instructed to join `podlaz` as part of the primary setup path.

Proxy-only Xray is started as the dedicated `podlaz-xray:podlaz-xray` identity without `CAP_NET_ADMIN`. Native TUN Xray is also started as `podlaz-xray:podlaz-xray`, but receives only `CAP_NET_ADMIN` as an ambient capability so pinned Xray can open `/dev/net/tun` and configure the Xray-owned `podlaz0` link. The unit keeps `NoNewPrivileges=yes` and does not rely on file capabilities or setuid helpers.

### Active network-session continuity

Package maintenance is part of the same network-session lifecycle as a daemon restart. Replacing the daemon binary while an active TUN session exists must not require the user to reconnect manually and must not discard exact recovery authority.

The packaged unit therefore distinguishes three lifecycle intents:

- a systemd restart job uses `RestartKillSignal=SIGUSR1`; the daemon preserves current-boot reconnect intent while it performs the normal exact transaction-backed disconnect/rollback before the replacement process starts;
- an explicit service stop uses `KillSignal=SIGTERM`; reconnect intent is removed before teardown, so a later manual service start stays disconnected;
- a reboot is not an implicit reconnect request. Continuation is bound to the current Linux boot ID and is stored only under `/run`, so a normal connection does not cross the reboot boundary.

`KillMode=mixed` is part of this contract: the initial stop/restart signal is delivered to the daemon main process rather than indiscriminately terminating the supervised Xray child before podlazd can order rollback and child shutdown. `TimeoutStopSec` must remain longer than the daemon's bounded rollback/core-stop budget. `RuntimeDirectoryPreserve=yes` prevents systemd from deleting exact transaction evidence after a failed explicit teardown; preserving the runtime directory does not authorize reconnect because explicit stop/disconnect removes continuation first.

The normal continuation file is `/run/podlaz/network-session-continuation.json`. It is daemon-private mode `0600`, atomically replaced, current-boot-only state. It contains the exact `ConnectRequest` required to reproduce the user's active connection and may therefore contain profile credentials or endpoints. It must never be logged or treated as cleanup authority. Route, DNS, nftables, TUN, generated-config, and child cleanup authority continues to come only from exact transaction state.

A lower released package predating this continuation file cannot write it before its first upgrade restart. For that one compatibility boundary, `postinstall` may write `/run/podlaz/legacy-upgrade-continuation`: a mode-`0600` marker containing only the current boot ID. The marker authorizes only an attempt to reconstruct reconnect intent; it grants no network ownership. The new daemon accepts the migration only when there is exactly one recoverable committed TUN transaction, its generated Xray config is exact transaction-owned state under the Podlaz runtime directory, the original server metadata is transaction-backed, and the reconstructed profile regenerates canonically identical Xray JSON. Any ambiguity fails closed: no guessed reconnect or foreign cleanup is allowed, while existing exact transaction evidence remains available for recovery.

Startup recovery for a current-boot continuation happens before the daemon exposes its mutating API. The daemon first converges transaction-backed stale state, reconnects only after recovery reports no failed or ambiguous result, and only then accepts normal requests. A teardown/recovery error is returned as a daemon failure instead of being discarded.

### Boot autostart policy

Boot autostart is a daemon-owned persistent policy, not a systemd `ExecStartPost=`,
separate helper, GUI autostart file, or implicit reuse of ordinary connection
state. The packaged unit keeps its normal `ExecStart=/usr/bin/podlazd` and
`After=network.target` ordering. Fresh boot networking readiness is handled by a
bounded dynamic observation inside the one admitted logical autostart attempt;
the package does not weaken this by adding a second boot launcher.

`StateDirectory=podlaz` and `StateDirectoryMode=0700` provide the private
persistent directory. Enabling autostart writes only
`/var/lib/podlaz/boot-autostart-manifest.json`, atomically and mode `0600`. The
manifest contains a versioned minimal validated connection snapshot plus an
opaque generation and the boot ID in which configuration was written. It does
not contain subscription URLs, profile collections/history, UI metadata, or a
persisted `handoff` policy. The root daemon never scans a user's home directory;
the unprivileged CLI loads the selected user profile and submits the canonical
snapshot through the polkit-protected daemon API.

`podlaz autostart enable` affects a later boot only. A same-boot daemon restart
after enable must remain disconnected unless another current-boot Network Session
already authorizes continuation. `podlaz autostart disable` removes only the
future-boot manifest and never disconnects or cancels an already-admitted
current-boot lifecycle.

The current-boot admission record is volatile
`/run/podlaz/boot-autostart-attempt.json`, private mode `0600`. It pins the exact
admitted request and is the one-logical-attempt/no-retry authority for that boot.
`in_progress` may continue through daemon replacement; `succeeded` and
conclusively `terminal` forbid a second automatic connect until a real next boot.
An explicit disconnect, daemon restart, package upgrade, or later runtime
terminal failure cannot reset that authority.

Terminal completion is committed only after exact owned cleanup and remaining
network verification converge. Persistence failure remains fail-closed through
retained Network Session authority. Product terminal reason is a separate
current-boot user-facing outcome and may be superseded by a newer admitted
explicit lifecycle; the boot attempt itself remains intact as no-retry authority.

## Packaged daemon socket boundary

Packaged installs keep the filesystem daemon socket narrow and expose an abstract Unix socket for the polkit-gated daemon boundary. CLI clients first try the filesystem socket. If that attempt fails with a transport-level permission error, the client retries the packaged abstract socket.

The fallback is not a generic error-masking layer. Daemon responses from the abstract socket are surfaced as-is: authorization denied, authorization unavailable on headless/server systems, malformed JSON, and invalid daemon responses remain distinct from a stopped or unreachable daemon. If the filesystem socket is permission-denied and the abstract socket is unavailable, the CLI must report that the authentication service or packaged polkit IPC path is unavailable instead of reporting only a raw abstract-socket connection failure.

## State ownership and lifecycle

| Category | Location | Owner | Package behavior |
| --- | --- | --- | --- |
| Packaged files | `/usr/bin`, `/usr/lib/podlaz`, `/usr/lib/systemd/system`, `/usr/lib/sysusers.d`, `/usr/share/bash-completion`, `/usr/share/zsh`, `/usr/share/fish`, `/usr/share/polkit-1/actions`, `/usr/share/man`, `/usr/share/doc/podlaz` | Debian package manager | Installed, upgraded, and removed by `dpkg`/`apt`. |
| Daemon runtime state | `/run/podlaz` | `podlazd` through systemd `RuntimeDirectory=` | Volatile; not shipped in the package. Preserved across service replacement so incomplete exact recovery authority is not erased. |
| Current-boot reconnect intent | `/run/podlaz/network-session-continuation.json` | `podlazd` | Private volatile intent; may contain profile credentials/endpoints; never cleanup authority; removed before explicit disconnect/stop and rejected after a boot-ID change. |
| Current-boot boot-autostart attempt | `/run/podlaz/boot-autostart-attempt.json` | `podlazd` | Private volatile pinned request and one-attempt/no-retry authority; rejected after a boot-ID change. |
| Current-boot product terminal outcome | `/run/podlaz/product-terminal-reason.json` | `podlazd` | Private typed reason/supersede state for the latest relevant lifecycle; not autostart authority. |
| Legacy upgrade marker | `/run/podlaz/legacy-upgrade-continuation` | Debian `postinstall`, consumed by `podlazd` | Current-boot compatibility marker only; contains no profile data and grants no cleanup authority. |
| Daemon persistent state | `/var/lib/podlaz` | systemd `StateDirectory=` and daemon | Private persistent policy state; includes `boot-autostart-manifest.json`, not packaged source files. |
| User intent/state | `$XDG_CONFIG_HOME/podlaz`, `$XDG_STATE_HOME/podlaz`, `$XDG_CACHE_HOME/podlaz` | invoking user | Not owned, modified, or removed by package lifecycle scripts. |

## Inspection and validation gates

Package inspection should verify metadata and packaged paths for both supported architectures:

```text
dist/podlaz_0.0.0~dev-1_linux_amd64.deb
dist/podlaz_0.0.0~dev-1_linux_arm64.deb
```

The dedicated Ubuntu 24.04 package-convergence gate additionally verifies that
Xray creates `podlaz0` without a product-selected OS address, podlazd assigns the
exact persisted session address before functional DNS verification, and normal
disconnect removes only the session-owned state. The issue #260 coexistence step
then deliberately occupies historical `198.18.0.1/32`, table `51820`, and
priorities `9999`/`10000` with synthetic unrelated state, requires the installed
candidate to allocate different exact identities, verifies the protected data
plane, and proves that the unrelated baseline survives both connect and
disconnect before the E2E fixture explicitly removes itself. Immediate reconnect
must succeed without restarting `podlazd` or `systemd-resolved`.

The network-session continuity acceptance gate must additionally exercise a real installed-package lifecycle: connect an installed lower released package in TUN mode, install the candidate package without issuing a second CLI connect, and verify that the session returns active. The same candidate package must survive `systemctl restart podlazd` and an unexpected daemon death through systemd automatic restart, while explicit service stop followed by start remains disconnected. Forced teardown interruption must prove that surviving Podlaz-owned host state still has exact transaction recovery authority rather than a heuristic marker. Routine acceptance must not repair the host with manual `ip`, `resolvectl`, `nft`, or `recover --execute` commands.

Issue #263 adds an installed-package acceptance on the same dedicated runner. It
covers autostart disabled, enable-next-boot gating, one fresh boot connect,
daemon restart while connected, package upgrade while connected, explicit
disconnect followed by same-boot restart, a terminal autostart failure, and
`terminal_no_same_boot_retry`. The runner cannot reboot in the middle of one
Actions job, so the fixture changes only the private manifest boot fence while
the daemon is inactive; all actual startup/connect/cleanup behavior still runs
through the installed `podlazd`. The script is
`scripts/e2e/issue263-package-acceptance.sh` and must not repair networking with
manual `ip`, `resolvectl`, `nft`, NetworkManager, or `recover --execute` calls.

The package gate validates the declarative packaged contract: sysusers identities, service `User=`/`Group=`, `UMask=`, runtime and state directory modes, required packaged polkit authorization environment, bounded daemon capabilities, the ambient `CAP_NET_ADMIN` required only for native TUN Xray, packaged Xray helper layout and architecture, static polkit action IDs including the dedicated autostart-configuration action, absence of broad polkit defaults, `plz` alias and alias completion files, autostart completion/man-page coverage, absence of AppStream/metainfo files, Debian helper-based daemon availability hooks, restart/stop signal ordering, current-boot upgrade marker permissions, absence of obsolete TUN helper artifacts, and absence of direct `systemctl start` or `systemctl enable` maintainer-script calls. The maintainer-script regression tests validate the stale helper-state repair contract and the wider raw `systemctl start|enable` guard.

Pull-request CI and tagged release builds must run the same `scripts/ci/validate-package-install.sh` install/reinstall/purge validator with packaged service validation enabled. The pull-request gate must therefore exercise service enablement, service activity, daemon socket creation, `podlaz`/`plz` daemon status access, and the same purge cleanup required during release. A second reduced package-install validator is not allowed because it can let release-only failures escape the merge gate. The deterministic non-root Issue #263 contract is exposed separately as `scripts/ci/issue263-contract.sh`; it checks service/docs/E2E wiring while the real host lifecycle remains in the manually dispatched package-convergence workflow.
