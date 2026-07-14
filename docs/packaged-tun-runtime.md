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

TUN mode uses Xray packet ingestion through its native `tun` inbound. `podlazd` owns the host networking state around that link: route bypass, policy rules, DNS, nftables, transaction files, rollback, and recovery.

## Native TUN privilege contract

The pinned Linux Xray TUN implementation opens `/dev/net/tun`, creates the named TUN interface, sets MTU through netlink, and brings the link up. Linux requires `CAP_NET_ADMIN` for interface configuration, firewall administration, and routing administration.

The packaged contract is therefore explicit:

- `podlazd` runs as `root:podlaz` with `CAP_NET_ADMIN` in the bounding set for podlaz-owned routes, DNS, nftables, and recovery operations;
- proxy-only Xray runs as the dedicated `podlaz-xray:podlaz-xray` identity without extra ambient capabilities;
- native TUN Xray also runs as `podlaz-xray:podlaz-xray`, but receives only `CAP_NET_ADMIN` as an ambient capability so it can create and configure the Xray-owned `podlaz0` link;
- the unit keeps `NoNewPrivileges=yes` and does not rely on file capabilities or a setuid helper;
- VM or self-hosted validation must prove the actual Linux capability handoff with the packaged unit before the PR leaves draft.

## Pinned Xray TUN schema

The pinned Xray release currently accepts only native TUN packet-ingestion settings in the generated inbound: interface `name`, uppercase `MTU`, and `userLevel`. It does not accept `gateway`, `dns`, lowercase `mtu`, `autoSystemRoutingTable`, or `autoOutboundsInterface` in the pinned `v26.3.27` schema. podlazd therefore must not invent unsupported Xray JSON fields.

Current upstream documentation describes a newer public schema with lowercase `mtu`, `gateway`, `dns`, and automatic routing fields. This PR intentionally follows the pinned source version, not the moving documentation page. If the pinned helper is upgraded, the generated JSON tests and this contract must be updated using source evidence for the new tag.

The deliberate contract for this PR is:

- Xray creates and owns the `podlaz0` link lifecycle and packet ingestion;
- Xray applies only the link name and MTU supported by the pinned schema;
- podlazd owns Linux route, policy-rule, DNS, nftables, transaction, rollback, and recovery state;
- podlazd commits only after route lookup, routed TCP, DNS resolution, and DNS-result route verification pass;
- VM or self-hosted validation must provide evidence that this pinned-schema split works on the target Linux hosts before the PR leaves draft.

If a future pinned Xray release adds supported `gateway`/`dns` fields, this contract must be revisited in the issue/PR with Xray schema evidence, generated JSON tests, and VM validation evidence.

## Hard TUN dependencies

Packaged TUN mode relies on these host components and declares them as Debian dependencies:

- `iproute2` for route, policy rule, route verification, and recovery operations around the Xray-owned TUN link;
- `nftables` for firewall and kill-switch state;
- `systemd-resolved` for per-link DNS apply, verify, and rollback;
- `polkitd | policykit-1` for the packaged daemon authorization boundary.

`network-manager` remains optional diagnostics only and is not a hard package dependency.

## Preflight contract

Before active podlaz replacement, controlled handoff cleanup, opening a TUN transaction, or applying host-networking changes, daemon-side connect checks that:

- the Xray helper resolves to an executable;
- packaged helpers under `/usr/lib/podlaz` match the running helper architecture when ELF metadata can be inspected;
- `ip`, `nft`, and `resolvectl` are available in the daemon execution environment;
- the pinned Xray helper accepts a minimal native `tun` inbound config through `xray test -config`;
- the server bypass resolves to a concrete IPv4 route outside `podlaz0`.

The unsupported-Xray preflight uses a temporary, redaction-safe config with only `protocol: tun` and pinned-schema `name`/`MTU`/`userLevel` settings. It does not use profile-derived runtime config and it runs before active replacement, automatic podlaz-owned recovery, handoff mutation, and transaction creation. Unsupported Xray TUN support must not disconnect active podlaz TUN, run recovery, stop an external VPN connection, or leave a transaction artifact.

For non-interactive connect policies, the daemon automatically runs controlled recovery when the refreshed host snapshot contains unambiguous podlaz-owned stale TUN, route, policy-rule, nftables, or transaction state. It then recollects the snapshot and continues only when that owned state is gone. Foreign VPN interfaces, foreign route-only DNS owners, ambiguous resources, and failed or incomplete recovery remain blockers. `--handoff=ask` stays mutation-free.

The generated profile runtime config path is recorded in transaction rollback metadata after the transaction is opened and before the daemon starts Xray with the generated config. If the daemon is interrupted after generated config write, recovery knows how to remove the generated config.

Missing or non-executable helpers, missing hard TUN commands, unsupported Xray TUN config, and unavailable server bypass are setup/runtime-unavailable failures. They must fail before route, DNS, nftables, firewall, handoff, active-podlaz replacement, or transaction mutation starts.

User-facing CLI output must not present these failures as a daemon crash or raw internal server error. It must state that TUN mode cannot start and that no network changes were applied.

## Transaction and rollback contract

A failed TUN connect is allowed to start Xray before host network apply because the pinned native Xray TUN implementation owns packet ingestion and creates the `podlaz0` link. Xray start alone is not a committed VPN connection. The connection is committed only after host network apply, host network verify, connectivity verify, and active-state commit all succeed.

The transaction's structured `desired_plan` is durably written before the daemon starts Xray or mutates host networking. Normal in-process rollback continues to use recorded applied steps. If the daemon stops abruptly before an applied rollback category is persisted, daemon recovery may reconstruct only the missing category from `desired_plan`; every reconstructed entry must still pass the fixed podlaz ownership, interface, route-table, policy-priority, DNS-backend, and nftables target guards before mutation.

When a failed connect successfully rolls back every podlaz-owned mutation it applied, the terminal transaction file is removed. If a terminal `rolled_back` or `committed` transaction file is found later, TUN handoff preflight treats it as non-blocking. Transaction files in cleanup-required states such as `planned`, `applying`, `applied`, `verifying`, `rolling_back`, or `failed` continue to block connect until automatic or explicit daemon-owned recovery completes. Invalid or unreadable transaction files are blockers because their ownership and cleanup state cannot be proven safely.

Rollback order for failed or disconnected native TUN sessions is:

1. remove podlaz-owned nftables/firewall state;
2. revert podlaz-owned systemd-resolved per-link DNS state;
3. remove podlaz-owned routes and policy rules created by the transaction;
4. stop the Xray child process when it was started by the transaction;
5. remove generated runtime config;
6. remove the terminal transaction file only after rollback succeeds.

## DNS verification contract

Before applying DNS, podlaz runs a scoped `resolvectl revert podlaz0` to discard stale podlaz-owned per-link state. An already-missing link, including the exact `No such device` response, is idempotent. Podlaz never restarts `systemd-resolved` automatically and never edits `/etc/resolv.conf`.

`systemd-resolved` can expose recently applied per-link settings with a delay. DNS verification therefore polls for up to roughly two seconds before failing on transient missing target link, planned DNS server, route-only `~.` domain, or `+DefaultRoute`. `Current Scopes` is derived lookup state and is not an apply-success requirement. A foreign route-only DNS owner remains an immediate hard failure.

Rollback uses `resolvectl revert <link>` for the podlaz-owned link. The exact missing-device response is successful idempotent cleanup in runtime rollback, stale cleanup, doctor inspection, and transaction recovery. Other unexpected errors remain failures.

## Diagnostics and logs

Daemon connect failures are logged as sanitized phase summaries. The log line includes the requested mode, failure phase, transaction id when a transaction exists, rollback status, and broad classification. It intentionally does not include raw command output, profile servers, share URIs, private keys, tokens, private domains, or private IP addresses.

User-facing errors remain actionable and may describe the safe next command, but daemon logs must use structured, low-cardinality fields rather than raw diagnostic text.

## CI gates

Package validation checks the Xray helper file, executable bit, architecture, service environment, declared dependencies, third-party notice file, and absence of obsolete TUN helper artifacts for both `amd64` and `arm64` packages.

Installed package smoke checks verify that the helper file and notice are present after install and reinstall, and that they are removed on purge together with other packaged files.
