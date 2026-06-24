# Self-hosted E2E validation

This document defines the one-click self-hosted E2E flow for podlaz.

The E2E workflow is intentionally manual and self-hosted only. It must not run on `pull_request` because the repository is public and the runner can mutate host networking when privileged suites are enabled.

## Normal workflow

Run the complete E2E flow from GitHub Actions:

```text
Actions -> E2E -> Run workflow
```

There are no manual inputs. A normal run executes these jobs in order:

1. `CLI contract e2e`
2. `Package and service e2e`
3. `Maximum server coverage e2e`

This is the default release/acceptance path for the dedicated E2E server.

## Runner contract

The default runner must be a dedicated Ubuntu 24.04 x64 host with these labels:

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
- passwordless sudo for the E2E operations installed by the runner bootstrap flow.

Additional Debian/Ubuntu or arm64 coverage requires separate dedicated runners and a follow-up workflow/matrix. The default workflow intentionally optimizes for a simple, reliable one-button path on the current production-like E2E host.

## Required E2E environment values

Configure the `vpn-e2e` environment once. A normal `E2E` run should not require editing workflow inputs.

At least one real profile secret is required:

```text
PODLAZ_E2E_PROFILE_URI
```

For broader real-provider and protocol coverage, configure any of these optional secrets:

```text
PODLAZ_E2E_PROFILE_URI_2
PODLAZ_E2E_PROFILE_URI_3
PODLAZ_E2E_PROFILE_URI_4
PODLAZ_E2E_PROFILE_URI_LIST
PODLAZ_E2E_SUBSCRIPTION_URL
PODLAZ_E2E_EXPECTED_EGRESS_IP
PODLAZ_E2E_EXPECTED_EGRESS_IPV6
```

`PODLAZ_E2E_PROFILE_URI_LIST` is a newline-delimited secret for additional real profile URIs. Use it to cover more than four representative providers/protocols without changing the workflow file.

Optional environment variables tune the default one-click behavior:

```text
PODLAZ_E2E_ENABLE_TUN=true|false                  # default: true
PODLAZ_E2E_ENABLE_CRASH_TESTS=true|false          # default: true
PODLAZ_E2E_ENABLE_HOST_DISRUPTION=auto|true|false # default: auto
PODLAZ_E2E_STABILITY_MINUTES=5                    # default: 5
PODLAZ_E2E_EXPECT_IPV6=observe|blocked|egress     # default: observe
PODLAZ_E2E_PUBLIC_IP_CHECK_URL
PODLAZ_E2E_PUBLIC_IPV6_CHECK_URL
PODLAZ_E2E_DNS_CHECK_HOST
PODLAZ_E2E_STATUS_CONCURRENCY
PODLAZ_E2E_HOST_WRAPPER_DIR
PODLAZ_E2E_HOST_WRAPPER_TIMEOUT_SECONDS
PODLAZ_E2E_HOST_DISRUPTION_MODE=proxy-only|tun
```

The default is intentionally production-like: TUN checks, crash probes, and a short stability probe are enabled. Host-disruption probes are `auto`: the suite runs safe host-owned wrappers when they exist and records missing wrappers without failing the run. The packaged daemon owns the privileged TUN, route, DNS, and nftables mutations, while Xray and the TUN adapter run under the dedicated `podlaz-xray` child identity.

## Job 1: CLI contract

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

This job does not require VPN credentials and does not intentionally mutate host networking.

## Job 2: Package and service

Script:

```bash
bash scripts/e2e/package-service.sh
```

Scope:

- installs the pinned package toolchain from `packaging/package-toolchain.env`;
- builds the local Debian package for the native runner architecture;
- fails fast if `PODLAZ_DEB_ARCH` does not match the host `dpkg --print-architecture`;
- inspects package metadata and contents;
- installs the package locally;
- checks installed CLI, daemon, completions, systemd unit, and sysusers files;
- starts and stops `podlazd.service` under systemd;
- captures `systemctl status` and journald diagnostics;
- validates same-version reinstall and package removal.

## Job 3: Maximum server coverage

Script:

```bash
bash scripts/e2e/server-coverage.sh
```

Scope:

- imports numbered profile secrets and every non-empty line from `PODLAZ_E2E_PROFILE_URI_LIST` into isolated user state;
- validates and plans every imported profile for proxy-only and TUN modes;
- builds and installs the local Debian package for the native runner architecture;
- starts `podlazd.service`;
- optionally exercises a real subscription URL;
- runs proxy-only connect/status/DNS/disconnect/idempotent-disconnect for every imported profile;
- runs concurrent status probes, overlapping second-connect rejection, and disconnect during active status polling;
- runs TUN connect/status/DNS/public-egress/IPv6 observation/disconnect/recover for every imported profile when TUN is enabled;
- kills supervised Xray and `podlazd.service`, then validates status/doctor/recover/disconnect behavior when crash tests are enabled;
- runs host-provided wrappers for suspend/resume, network reconnect, DHCP renewal, DNS mutation, and polkit GUI/TTY authorization probes when wrappers are available and host disruption is enabled or auto-detected;
- keeps one profile connected and polls status, DNS, and public egress repeatedly when `PODLAZ_E2E_STABILITY_MINUTES > 0`;
- records host snapshots before, during, and after lifecycle, crash, host-disruption, and cleanup operations.

## Host-disruption wrappers

The workflow never runs raw `systemctl suspend`, NetworkManager reconnect, DHCP renew, resolver mutation, or interactive polkit commands directly. `server-coverage.sh` only runs root-owned host wrappers from `PODLAZ_E2E_HOST_WRAPPER_DIR`, defaulting to:

```text
/usr/local/libexec/podlaz-e2e
```

Supported wrapper names are fixed:

```text
suspend-resume
network-reconnect
dhcp-renew
dns-change
polkit-gui-auth
polkit-tty-auth
```

Each wrapper is responsible for host-specific safety, out-of-band recovery, and restoring any temporary state it changes. The E2E script provides these environment values to wrappers:

```text
PODLAZ_E2E_DNS_CHECK_HOST
PODLAZ_E2E_ACTIVE_MODE
```

With `PODLAZ_E2E_ENABLE_HOST_DISRUPTION=auto`, missing wrappers are recorded in diagnostics and skipped. With `true`, enabling host-disruption tests with no supported wrappers is a failure because it would otherwise create a false positive. With `false`, wrappers are never executed.

## Coverage boundaries

The self-hosted server can cover real package/service, daemon, proxy-only, TUN, DNS, egress, crash, concurrency, multiple-profile, multiple-provider/subscription, host wrapper, and long-running behavior.

The following items require explicit host support beyond the default single remote host:

| Area | Status |
| --- | --- |
| Suspend/resume | Covered only when the host provides a safe `suspend-resume` wrapper and out-of-band recovery. |
| Wi-Fi/network reconnect | Covered only when the host provides a safe `network-reconnect` wrapper for its NetworkManager/device topology. |
| DHCP renewal | Covered only when the host provides a safe `dhcp-renew` wrapper. |
| DNS change while connected | Covered by regular diagnostics/resolution checks; active resolver mutation requires the `dns-change` wrapper. |
| NetworkManager behavior | Diagnostics are captured when `nmcli` is available; destructive reconnect requires a wrapper. |
| Debian/Ubuntu matrix beyond this host | Requires additional dedicated runners or VMs. |
| arm64 package/service install | Requires a native arm64 self-hosted runner or VM. |
| Polkit GUI/TTY authorization flows | Covered only when the host provides `polkit-gui-auth` or `polkit-tty-auth` wrappers with an appropriate agent/session. |
| All protocols | Requires representative profile secrets; use numbered profile secrets plus `PODLAZ_E2E_PROFILE_URI_LIST` for additional providers/protocols. |

## Diagnostics

Each job writes command stdout/stderr and host diagnostics to `${RUNNER_TEMP}/podlaz-e2e-artifacts`, which the workflow uploads as artifacts.

Diagnostics must stay sanitized. Do not upload full share URIs, subscription URLs, generated Xray configs, private keys, provider tokens, authorization headers, or unredacted credentials.

## Non-goals

- The E2E workflow is not a required PR gate.
- The real VPN suite is not safe for forked pull-request code.
- Host-disruption wrappers are host-owned safety mechanisms, not generic commands embedded in the repository.
- E2E diagnostics are not permanent release evidence; release evidence belongs in the relevant release issue, pull request, or release notes.
