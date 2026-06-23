# E2E runner bootstrap

This document defines the automated bootstrap path for the dedicated self-hosted E2E runner described in [Self-hosted E2E validation](./e2e.md).

The bootstrap workflow provisions a clean Linux server into a GitHub Actions self-hosted runner with the labels required by `.github/workflows/e2e.yml`.

## Workflow

Run the workflow from GitHub Actions:

```text
Actions -> Provision E2E runner -> Run workflow
```

Inputs can be provided manually, or left empty when the corresponding `vpn-e2e` environment variables or secrets are configured.

| Input | Fallback | Purpose |
| --- | --- | --- |
| `host` | `E2E_BOOTSTRAP_SSH_HOST` | SSH host or IP address of the clean server. |
| `ssh_user` | `E2E_BOOTSTRAP_SSH_USER`, then `root` | SSH user. The user must be `root` or have passwordless sudo. |
| `ssh_port` | `E2E_BOOTSTRAP_SSH_PORT`, then `22` | SSH port. |
| `allow_insecure_ssh_keyscan` | `false` | Explicit first-use escape hatch when pinned SSH host keys are not configured. |
| `platform_label` | `E2E_RUNNER_PLATFORM_LABEL`, then `ubuntu-24.04` | OS label to validate and attach to the runner. |
| `arch_label` | `E2E_RUNNER_ARCH_LABEL`, then `x64` | CPU architecture label to validate and attach to the runner. |
| `runner_name` | `E2E_RUNNER_NAME`, then `podlaz-<hostname>-vpn-e2e` | GitHub runner name. |
| `runner_user` | `E2E_RUNNER_USER`, then `gha-runner` | Local Linux user that owns the runner service. |
| `runner_home` | `E2E_RUNNER_HOME`, then `/opt/actions-runner/actions-runner` | Runner installation directory. |
| `runner_labels` | `E2E_RUNNER_LABELS`, then derived labels | Comma-separated labels. Empty derives `self-hosted,linux,<arch>,vpn-e2e,<platform>`. |
| `reset_runner` | `true` | Stop and replace an existing runner installation in `runner_home`. |

## Minimal GitHub configuration

The provisioning job uses the `vpn-e2e` environment so bootstrap values can be environment-scoped.

Use GitHub Actions variables for non-sensitive defaults when possible, and secrets for sensitive values. GitHub exposes configuration variables through the `vars` context and secrets through the `secrets` context.

Minimum values for a no-input workflow run:

| Name | Recommended storage | Purpose |
| --- | --- | --- |
| `E2E_BOOTSTRAP_SSH_HOST` | environment variable or secret | SSH host or IP address. |
| `E2E_BOOTSTRAP_SSH_USER` | environment variable or secret | SSH login user. Defaults to `root` when omitted. |
| `E2E_BOOTSTRAP_SSH_PRIVATE_KEY` | environment secret | SSH credential used by the provisioning job to connect to the server. |
| `E2E_BOOTSTRAP_SSH_KNOWN_HOSTS` | environment secret | Pinned SSH known-hosts entry for the target server. |
| `E2E_RUNNER_ADMIN_TOKEN` | environment secret | GitHub token that can create repository self-hosted runner registration tokens. |

Optional values:

| Name | Recommended storage | Purpose |
| --- | --- | --- |
| `E2E_BOOTSTRAP_SSH_PORT` | environment variable or secret | SSH port. Defaults to `22`. |
| `E2E_RUNNER_PLATFORM_LABEL` | environment variable | Default platform label. |
| `E2E_RUNNER_ARCH_LABEL` | environment variable | Default architecture label. |
| `E2E_RUNNER_HOME` | environment variable | Existing or desired runner installation directory. |
| `E2E_RUNNER_USER` | environment variable | Local Linux runner service user. |
| `E2E_RUNNER_NAME` | environment variable | GitHub runner name. |
| `E2E_RUNNER_LABELS` | environment variable | Full comma-separated label override. |

Use a token scoped to this repository with the minimum administration permission needed to create runner registration tokens.

## SSH bootstrap contract

The workflow connects to `${ssh_user}@${host}` with `E2E_BOOTSTRAP_SSH_PRIVATE_KEY` before it can install anything.

That means the server must already accept the matching public key for the selected SSH user. For a clean server, add the public key through the provider's cloud-init, SSH-key, rescue console, or image-init mechanism before running the workflow.

The selected SSH user must be able to run:

```bash
sudo -n true
```

A failure like this means the first SSH hop did not authenticate:

```text
Permission denied (publickey,password).
scp: Connection closed
```

Common causes:

- the SSH credential in `E2E_BOOTSTRAP_SSH_PRIVATE_KEY` does not match the public key installed on the server;
- the public key is installed for a different user than `ssh_user`;
- the provider image disables SSH key authentication for that user;
- the server requires an interactive sudo password.

The workflow prints the bootstrap SSH public key fingerprint before the SSH preflight so the key can be compared with the provider-side key.

## SSH host-key verification

Configure `E2E_BOOTSTRAP_SSH_KNOWN_HOSTS` with the server's pinned SSH host key before provisioning. This prevents the bootstrap job from trusting a host key learned over the same network path that receives the runner registration token.

For a one-off first-use bootstrap on a trusted network, the workflow has an explicit `allow_insecure_ssh_keyscan=true` input. This is intentionally opt-in and should not be used as the routine path.

## Server contract

The remote bootstrap script supports this host matrix:

| Platform label | OS |
| --- | --- |
| `ubuntu-24.04` | Ubuntu 24.04 |
| `ubuntu-22.04` | Ubuntu 22.04 |
| `debian-12` | Debian 12 |
| `debian-13` | Debian 13 |

Supported architecture labels:

| Architecture label | Machine architecture |
| --- | --- |
| `x64` | `x86_64` |
| `arm64` | `aarch64` or `arm64` |

The host must provide:

- systemd as PID 1;
- `/dev/net/tun`;
- an SSH user with passwordless sudo.

The script installs host packages required by the existing E2E suites, creates the runner service user, creates the `podlaz` access group used by socket-access tests, installs the E2E sudoers policy, downloads the latest GitHub Actions runner release, configures it against this repository, and starts it as a systemd service.

## Existing runner safety

`reset_runner=true` replaces the runner installed in `runner_home`.

The script refuses to reset a parent directory when it detects an existing runner below that directory. For example, if the current runner is installed in:

```text
/opt/actions-runner/actions-runner
```

run provisioning with:

```text
runner_home=/opt/actions-runner/actions-runner
```

or use a clean server. Do not point `runner_home` at `/opt/actions-runner` in that case.

## Sudo policy installed by bootstrap

The workflow installs `/etc/sudoers.d/podlaz-e2e-runner` for the runner user.

The policy allows root execution of the commands used by the current E2E scripts:

```text
/usr/bin/true
/usr/bin/apt
/usr/bin/systemctl
/usr/bin/journalctl
/usr/sbin/ip
/usr/sbin/nft
/usr/bin/kill
/usr/bin/env
/usr/bin/timeout
```

It also allows the supported host-disruption wrapper paths under `/usr/local/libexec/podlaz-e2e` and allows the runner user to execute `/usr/bin/env` with the `podlaz` run-as group so daemon socket access can be tested without making the runner permanently privileged beyond the selected command path.

## Recommended first run

After provisioning a clean server:

1. Run `Provision E2E runner` with the target host or with the configured default values.
2. Wait for the runner to appear online in repository settings.
3. Run `E2E` with `suite=cli-contract`.
4. Run `E2E` with `suite=package-service`.
5. Add the real VPN E2E values to the `vpn-e2e` environment.
6. Run `real-vpn` first with `lifecycle=false` and `tun=false`.
7. Enable `lifecycle=true`, then enable `tun=true` only on a disposable host.

## Safety boundary

The provisioning workflow must stay `workflow_dispatch` only. It must not run on `pull_request`, because it creates infrastructure capable of running privileged networking tests.

The E2E workflow itself remains manual and self-hosted only. The bootstrap flow only removes the manual server preparation step; it does not make privileged VPN lifecycle or TUN suites safe as automatic PR gates.
