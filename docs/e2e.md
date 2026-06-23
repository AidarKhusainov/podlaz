# Self-hosted E2E validation

This document defines the staged self-hosted E2E plan for podlaz.

The E2E workflow is intentionally manual and self-hosted only. It must not run on `pull_request` because the repository is public and the runner can mutate host networking when privileged suites are enabled.

## Runner contract

The runner must be a dedicated Ubuntu 24.04 host with these labels:

```text
self-hosted
linux
x64
vpn-e2e
ubuntu-24.04
```

The host must provide:

- systemd as the service manager;
- `/dev/net/tun`;
- `iproute2`;
- `nftables`;
- `systemd-resolved` and `resolvectl`;
- `journalctl`;
- Go 1.26.4 through the workflow setup step;
- passwordless sudo only for the E2E operations selected by the suite.

## Workflow

Run the workflow from GitHub Actions:

```text
Actions -> E2E -> Run workflow
```

Inputs:

| Input | Values | Purpose |
| --- | --- | --- |
| `suite` | `cli-contract`, `package-service`, `real-vpn`, `all` | Selects the E2E tier. |
| `lifecycle` | boolean | Allows `real-vpn` to run daemon connect/disconnect checks. |
| `tun` | boolean | Allows `real-vpn` lifecycle to apply TUN-mode networking mutations. |

The default suite is `cli-contract`. The lifecycle flags default to `false` so a manual dispatch cannot accidentally change host routes, DNS, nftables, or TUN state.

## Tier 1: CLI contract

Script:

```bash
bash scripts/e2e/cli-contract.sh
```

Scope:

- builds a local `podlaz` binary from the checked-out commit;
- uses isolated `XDG_CONFIG_HOME`, `XDG_STATE_HOME`, and `XDG_CACHE_HOME`;
- exercises help and version behavior;
- exercises shell completion generation for bash, zsh, and fish;
- exercises profile add/import/list/show/validate/delete paths;
- exercises local import detection for Xray JSON, plain URI-list, and Base64 URI-list fixtures;
- exercises subscription add/update/list/show/delete paths with a local `file://` fixture;
- exercises read-only plan output for proxy-only and TUN modes;
- exercises deferred JSON and invalid-argument exit-code gates for daemon-backed commands;
- writes stdout/stderr diagnostics for every command to the workflow artifact directory.

This tier does not require VPN credentials and does not intentionally mutate host networking.

## Tier 2: Package and service

Script:

```bash
bash scripts/e2e/package-service.sh
```

Scope:

- installs the pinned package toolchain from `packaging/package-toolchain.env`;
- builds the local Debian package;
- inspects package metadata and contents;
- installs the package locally;
- checks installed CLI, daemon, completions, systemd unit, and sysusers files;
- starts and stops `podlazd.service` under systemd;
- captures `systemctl status` and journald diagnostics;
- validates same-version reinstall and package removal.

This tier requires passwordless sudo for package and service operations, including `apt`, `systemctl`, and `journalctl`.

## Tier 3: Real VPN

Script:

```bash
bash scripts/e2e/real-vpn.sh
```

Required environment secret:

```text
PODLAZ_E2E_PROFILE_URI
```

Optional environment secrets and variables:

```text
PODLAZ_E2E_EXPECTED_EGRESS_IP
PODLAZ_E2E_PUBLIC_IP_CHECK_URL
PODLAZ_E2E_DNS_CHECK_HOST
```

Default behavior with `lifecycle=false`:

- imports the real E2E profile into isolated user state;
- validates proxy-only and TUN renderability;
- renders proxy-only and TUN plans;
- verifies that TUN planning remains dry-run.

Lifecycle behavior with `lifecycle=true`:

- builds and installs the local Debian package;
- starts `podlazd.service`;
- runs proxy-only connect/status/disconnect through the daemon socket access model;
- captures sanitized systemd, journal, route, rule, DNS, and nftables diagnostics.

TUN behavior with both `lifecycle=true` and `tun=true`:

- runs `connect --mode tun`;
- checks DNS and public egress;
- disconnects;
- runs recovery inspection and explicit cleanup;
- captures sanitized diagnostics.

Only enable `tun=true` on a disposable or dedicated test host. TUN mode is expected to mutate host networking while the transaction is active.

## Suggested sudoers progression

Start with the smallest policy needed for the selected tier.

Smoke/CLI host preflight:

```text
gha-runner ALL=(root) NOPASSWD: /usr/bin/true, /usr/sbin/ip, /usr/sbin/nft
```

Package/service tier:

```text
gha-runner ALL=(root) NOPASSWD: /usr/bin/apt, /usr/bin/systemctl, /usr/bin/journalctl, /usr/bin/env
```

Real VPN lifecycle and TUN tiers need a narrower project-owned sudo wrapper before they become routine gates. Until then, run them manually and review the diagnostics after every run.

## Diagnostics

Each suite writes command stdout/stderr and host diagnostics to `${RUNNER_TEMP}/podlaz-e2e-artifacts`, which the workflow uploads as an artifact.

Diagnostics must stay sanitized. Do not upload full share URIs, subscription URLs, generated Xray configs, private keys, provider tokens, authorization headers, or unredacted credentials.

## Non-goals

- The E2E workflow is not a required PR gate.
- The real VPN suite is not safe for forked pull-request code.
- The lifecycle/TUN tier is not enabled by default.
- E2E diagnostics are not permanent release evidence; release evidence belongs in the relevant release issue, pull request, or release notes.
