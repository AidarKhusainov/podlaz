# Release workflow

Reference for tagged GitHub Release automation.

## Trigger

A release is produced only from a pushed semantic version tag:

```text
vMAJOR.MINOR.PATCH
```

Examples: `v0.1.3`, `v0.2.0`.

## Artifacts

For tag `v0.1.3`, the workflow publishes a GitHub Release with:

```text
podlaz_0.1.3_linux_amd64.tar.gz
podlaz_0.1.3_linux_arm64.tar.gz
podlaz_0.1.3_linux_amd64.deb
podlaz_0.1.3_linux_arm64.deb
SHA256SUMS
```

The `.tar.gz` archives are standalone Linux binary artifacts containing `podlaz`, `plz`, and `podlazd`. The `.deb` files are installable Debian package artifacts. The same files are also uploaded as workflow artifacts for short-term review of the release run.

`podlaz version`, package metadata, artifact names, release notes, checksums, and GitHub Release assets must all use the same version, release tag, and commit SHA. Release notes must include the exact tag and commit SHA.

## Validation

Before publication, the workflow validates:

- Go formatting, tests, vet, and vulnerability scan;
- package builds for `amd64` and `arm64`;
- package metadata and contents;
- packaged daemon access contract;
- packaged binary architecture for each `.deb` artifact;
- shell completions for `podlaz` and `plz`;
- binary linkage for the host-built `amd64` package root;
- lintian errors for both package architectures;
- local install, same-version reinstall, service availability, route stability, and purge cleanup;
- version output for `podlaz` and `plz`;
- man page rendering;
- checksum contents for all downloadable release artifacts.

Package install validation must confirm that install does not start Xray and does not change host routing. The package may make `podlazd.service` available through Debian helper-managed service enable/start behavior.

## Publication

Only the publication job requests release-writing permissions. It downloads the already validated release artifacts, records artifact provenance through GitHub artifact attestations, and creates or updates the GitHub Release assets for the tag.

The publication job sets `GH_REPO` explicitly so GitHub CLI release commands do not depend on a local checkout or git remote context.

The release workflow does not publish an apt repository and does not sign repository metadata.

## Permissions

Use read-only permissions by default. Only the publication job may request `contents: write`, because GitHub Release creation and asset upload require it. The publication job may also request `attestations: write` and `id-token: write` for artifact provenance.

## Out of scope

- Public apt repository publication.
- Repository signing.
- Starting VPN tunnels.
- Mutating TUN devices, routes, DNS, nftables, firewall rules, or resolver files.
- GUI metadata.
