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
| `suite` | `cli-contract`, `package-service`, `real-vpn`, `real-vpn-extended`, `all` | Selects the E2E tier. |
| `lifecycle` | boolean | Allows `real-vpn` to run daemon connect/disconnect checks. |
| `tun` | boolean | Allows real VPN suites to apply TUN-mode networking mutations. |
| `crash_tests` | boolean | Allows `real-vpn-extended` to kill supervised core and daemon processes. |
| `stability_minutes` | integer string | Long-running stability probe duration for `real-vpn-extended`. |

The default suite is `cli-contract`. The lifecycle, TUN, crash, and long-running flags default to disabled values so a manual dispatch cannot accidentally change host routes, DNS, nftables, TUN state, or daemon/core process state.

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

## Tier 4: Extended real VPN

Script:

```bash
bash scripts/e2e/real-vpn-extended.sh
```

Required environment secret:

```text
PODLAZ_E2E_PROFILE_URI
```

Optional environment secrets for broader provider/protocol coverage:

```text
PODLAZ_E2E_PROFILE_URI_2
PODLAZ_E2E_PROFILE_URI_3
PODLAZ_E2E_PROFILE_URI_4
PODLAZ_E2E_SUBSCRIPTION_URL
PODLAZ_E2E_EXPECTED_EGRESS_IP
PODLAZ_E2E_EXPECTED_EGRESS_IPV6
```

Optional environment variables:

```text
PODLAZ_E2E_EXPECT_IPV6=observe|blocked|egress
PODLAZ_E2E_PUBLIC_IP_CHECK_URL
PODLAZ_E2E_PUBLIC_IPV6_CHECK_URL
PODLAZ_E2E_DNS_CHECK_HOST
```

Scope:

- imports up to four real profile secrets into isolated user state;
- validates and plans every imported profile for proxy-only and TUN modes;
- builds and installs the local Debian package;
- starts `podlazd.service`;
- optionally exercises a real subscription URL;
- runs proxy-only connect/status/disconnect for every imported profile;
- runs concurrent `status` probes while a connection is active;
- verifies that a second connect is rejected while a connection is active;
- runs idempotent disconnect checks;
- with `tun=true`, runs TUN connect/status/DNS/public-egress/disconnect/recover for every imported profile;
- with `crash_tests=true`, kills the supervised Xray core process and validates status/doctor/disconnect/recover behavior;
- with `crash_tests=true`, kills `podlazd.service`, restarts it, and validates status/recover/disconnect behavior;
- with `stability_minutes > 0`, keeps one profile connected and polls status, DNS, and public egress repeatedly;
- records host snapshots before, during, and after lifecycle operations.

This tier intentionally does not execute destructive host-disruption actions such as `systemctl suspend`, NetworkManager reconnect, or DHCP renew. Those actions can strand a remote CI runner and need a provider-specific host with out-of-band recovery. The extended tier records NetworkManager/systemd/networking diagnostics instead.

## Coverage boundaries

The self-hosted server can cover real package/service, daemon, proxy-only, TUN, DNS, egress, crash, concurrency, multiple-profile, and long-running behavior.

The following items need additional infrastructure or manual orchestration beyond the current single remote host:

| Area | Status |
| --- | --- |
| Suspend/resume | Not automated on the remote runner because `systemctl suspend` can make the runner unreachable without out-of-band wake support. |
| Wi-Fi/network reconnect | Not automated on the current server unless the host actually uses NetworkManager-managed Wi-Fi and has out-of-band recovery. |
| DHCP renewal | Not automated because DHCP renew can change the runner's route/address and strand the job. |
| DNS change while connected | Covered by diagnostics and repeated resolution checks; active resolver mutation is deferred until a safe host wrapper exists. |
| NetworkManager behavior | Diagnostics are captured when `nmcli` is available; destructive reconnect is not automatic. |
| Debian/Ubuntu matrix beyond this host | Requires additional labeled runners, VMs, or a provider image matrix. |
| arm64 package/service install | Requires an arm64 runner or VM. Cross-package build can be added separately, but service install must run on arm64. |
| Polkit GUI/TTY authorization flows | Requires a desktop/TTY test host with an interactive polkit agent. |
| All protocols | Requires secrets for representative profiles; use `PODLAZ_E2E_PROFILE_URI_2` through `_4` for additional providers/protocols. |

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

Extended crash tier, only when `crash_tests=true`:

```text
gha-runner ALL=(root) NOPASSWD: /usr/bin/kill
```

Real VPN lifecycle and TUN tiers need a narrower project-owned sudo wrapper before they become routine gates. Until then, run them manually and review the diagnostics after every run.

## Diagnostics

Each suite writes command stdout/stderr and host diagnostics to `${RUNNER_TEMP}/podlaz-e2e-artifacts`, which the workflow uploads as an artifact.

Diagnostics must stay sanitized. Do not upload full share URIs, subscription URLs, generated Xray configs, private keys, provider tokens, authorization headers, or unredacted credentials.

## Non-goals

- The E2E workflow is not a required PR gate.
- The real VPN suite is not safe for forked pull-request code.
- The lifecycle/TUN/crash tiers are not enabled by default.
- E2E diagnostics are not permanent release evidence; release evidence belongs in the relevant release issue, pull request, or release notes.
