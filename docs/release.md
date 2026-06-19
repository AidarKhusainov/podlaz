# Release workflow

This document defines podlaz's GitHub Release automation contract.

The release workflow publishes versioned GitHub Release artifacts only. Public apt repository publication, package repository signing, GPG signing, and key management remain out of scope for this workflow.

## Trigger

A release is produced only from a semantic version tag pushed to the repository.

Required tag format:

```text
vMAJOR.MINOR.PATCH
```

Examples:

```text
v0.1.3
v0.2.0
```

The workflow intentionally has no manual tag input. To release a version, create and push the corresponding Git tag.

## Version mapping

For a tag such as `v0.1.3`:

| Value | Mapping |
| --- | --- |
| Git tag | `v0.1.3` |
| Binary version shown by `podlaz version` | `0.1.3` |
| Release package version | `0.1.3` |
| Debian package, amd64 | `podlaz_0.1.3_linux_amd64.deb` |
| Debian package, arm64 | `podlaz_0.1.3_linux_arm64.deb` |
| Checksums | `SHA256SUMS` |

The workflow passes the release version to the package build script as both `PODLAZ_VERSION` and `PODLAZ_DEB_VERSION` so release artifact names are exactly:

```text
podlaz_<version>_linux_amd64.deb
podlaz_<version>_linux_arm64.deb
SHA256SUMS
```

The workflow also embeds the release commit SHA through `PODLAZ_COMMIT` and a human-readable commit date through `PODLAZ_BUILT`, matching the `podlaz version` output contract.

## Artifacts

The release workflow publishes only:

- `podlaz_<version>_linux_amd64.deb`;
- `podlaz_<version>_linux_arm64.deb`;
- `SHA256SUMS`.

`SHA256SUMS` covers every downloadable artifact produced by the workflow and must contain only the new artifact names:

```text
<sha256>  podlaz_<version>_linux_amd64.deb
<sha256>  podlaz_<version>_linux_arm64.deb
```

The release workflow must not publish legacy `tunwarden_*` artifacts, old-name checksum files, binary aliases, transition packages, AppStream metadata, desktop entries, icons, or signed checksum files.

## Validation gate

Before publication, the workflow runs regular Go checks, vulnerability scanning, package builds for `amd64` and `arm64`, package metadata inspection, package content inspection, package linting, shell completion validation, local `amd64` package installation, version validation, route-table unchanged validation, manual page rendering validation, package removal validation, and checksum generation validation.

Package install validation checks that the package does not start `podlazd` and that the host route table is unchanged by package installation.

Systemd lifecycle assertions that require systemd as PID 1 remain VM or systemd-capable host validation work and are not claimed by the container-backed release workflow.

## Workflow permissions

The workflow declares read-only top-level permissions. Build and validation jobs use read-only repository access. Only the final publication job grants `contents: write`, because GitHub Release creation and asset upload require write access to repository contents.

## Action pinning policy

The workflow uses official GitHub-owned Actions:

- `actions/checkout@v4`
- `actions/setup-go@v5`
- `actions/upload-artifact@v4`
- `actions/download-artifact@v4`

These are tag-pinned rather than SHA-pinned because they are first-party GitHub Actions with stable major-version release channels and a lower supply-chain risk than third-party marketplace actions. Any future third-party Action added to the release path must be pinned to a full-length commit SHA unless the PR explicitly documents why that is not practical.

## Release notes

Generated release notes include:

- the exact Git tag;
- the exact commit SHA;
- the human-readable build date;
- the names of all published artifacts;
- the local Debian package install command;
- the package auto-start policy.

Curated human release notes can be added by editing the GitHub Release after publication or by extending the workflow in a later PR.

## Safety boundary

The release workflow must not:

- create a public apt repository;
- sign repository metadata;
- add broad installer scripts;
- enable or start `podlazd.service` during package installation;
- start a VPN tunnel;
- mutate TUN devices, routes, DNS, nftables, firewall rules, or host resolver files;
- publish `SHA256SUMS.asc`;
- publish GPG signatures;
- publish AppStream metadata, desktop files, or icons.
