# Self-hosted E2E

Manual real-host validation. This workflow is not a pull-request gate because it
uses a self-hosted runner and may exercise privileged networking.

## Run

```text
Actions -> E2E -> Run workflow
```

Default job order:

1. CLI contract
2. Package and service
3. Proxy data-plane
4. Maximum server coverage

## Runner

Required labels:

```text
self-hosted
linux
x64
vpn-e2e
ubuntu-24.04
```

Required host capabilities:

- systemd;
- `/dev/net/tun`;
- `iproute2`, `nftables`, `resolvectl`, `journalctl`;
- Go from the workflow setup step;
- passwordless sudo for the repository E2E scripts.

Additional OS/architecture coverage requires additional dedicated runners or VMs.

## Secrets and variables

Required:

```text
PODLAZ_E2E_PROFILE_URI
```

Optional:

```text
PODLAZ_E2E_PROFILE_URI_LIST
PODLAZ_E2E_SUBSCRIPTION_URL
PODLAZ_E2E_EXPECTED_EGRESS_IP
PODLAZ_E2E_EXPECTED_EGRESS_IPV6
PODLAZ_E2E_ENABLE_TUN=true|false
PODLAZ_E2E_ENABLE_CRASH_TESTS=true|false
PODLAZ_E2E_ENABLE_HOST_DISRUPTION=auto|true|false
PODLAZ_E2E_STABILITY_MINUTES=5
PODLAZ_E2E_RELIABILITY_CYCLES=100
PODLAZ_E2E_EXPECT_IPV6=observe|blocked|egress
PODLAZ_E2E_HOST_WRAPPER_DIR
```

## Scripts

| Job | Script | Scope |
| --- | --- | --- |
| CLI contract | `scripts/e2e/cli-contract.sh` | Build local CLI, isolated user state, command/flag/JSON/error checks. |
| Package and service | `scripts/e2e/package-service.sh` | Build package, install, service availability, reinstall, purge cleanup. |
| Proxy data-plane | `scripts/e2e/data-plane.sh` | Real proxy-only connect, egress, listener scope, cleanup. |
| Maximum server coverage | `scripts/e2e/server-coverage.sh` | Real-provider proxy/TUN, crash probes, concurrency, snapshots, optional host wrappers. |

## Host wrappers

Host-disruption tests may run only root-owned wrappers from
`PODLAZ_E2E_HOST_WRAPPER_DIR`, defaulting to:

```text
/usr/local/libexec/podlaz-e2e
```

Supported wrapper names:

```text
suspend-resume
network-reconnect
dhcp-renew
dns-change
polkit-gui-auth
polkit-tty-auth
```

With `PODLAZ_E2E_ENABLE_HOST_DISRUPTION=auto`, missing wrappers are skipped and
recorded. With `true`, missing wrappers fail the run. With `false`, wrappers are
not used.

## Diagnostics

Artifacts are written under `${RUNNER_TEMP}/podlaz-e2e-artifacts` and uploaded by
the workflow. Diagnostics must stay sanitized and must not include raw profile
URIs, subscription URLs, generated runtime configs, credentials, or tokens.

## Non-goals

- Not a fork PR gate.
- Not a replacement for unit tests.
- Not permanent release evidence; keep evidence in issues, PRs, or release notes.
