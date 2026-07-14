# Release workflow

Reference for tagged GitHub Release automation.

## Trigger

A release is produced from a pushed semantic version tag:

```text
vMAJOR.MINOR.PATCH
```

Examples: `v0.1.3`, `v0.2.0`.

Creating a GitHub Release manually for a new tag is supported only when the workflow-created assets are not already attached to that release. The workflow still runs from the tag push event and completes the GitHub Release publication.

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

The service availability smoke requires the systemd unit to be enabled and active, the packaged daemon socket to exist, and both `podlaz status` and `plz status` to report `Daemon: running`. Status exit code `3` is accepted only for this smoke because it is the documented diagnostic result for stale or incomplete ambient host state; exit codes other than `0` and `3`, or missing daemon-running output, still fail release validation.

Pull-request CI and tagged release validation must invoke the same canonical `scripts/ci/validate-package-install.sh` script with `PODLAZ_VALIDATE_SERVICE=1`. Release validation may supply release-specific version metadata, but it must not introduce a stricter package installation or service-availability contract that was not already exercised before merge. `scripts/ci/validate-package-workflow-contract.sh` enforces this parity.

The tagged workflow remains responsible for rebuilding immutable artifacts from the tag and checking their release metadata, checksums, attestations, and publication behavior. Those release-only checks may still fail after tagging, but package installation, service availability, CLI access, and purge behavior must already have passed the pull-request gate.

## Publication

The workflow treats published release assets as immutable. It never uploads with `--clobber` and never replaces an already attached expected artifact.

If no GitHub Release exists for the tag, the publication job creates it and uploads the expected assets.

If a GitHub Release already exists for the tag without any expected workflow assets, the publication job updates the title and notes, then uploads the expected assets. This supports the GitHub UI flow where creating a release for a new tag also pushes the tag that starts the workflow.

If all expected workflow assets already exist, the publication job downloads them, verifies their `SHA256SUMS`, compares the existing `SHA256SUMS` with the newly built checksum file, updates title and notes, and exits successfully only when they match.

If only some expected workflow assets exist, or if the existing checksum differs from the newly built checksum file, publication fails. Remove the incomplete assets manually before retrying or cut a new tag for a corrected published release.

The attestation job downloads the already validated release artifacts and records artifact provenance through GitHub artifact attestations. The publication job then downloads the same artifacts and creates or completes the GitHub Release for the tag.

The publication job sets `GH_REPO` explicitly so GitHub CLI release commands do not depend on a local checkout or git remote context.

The release workflow does not publish an apt repository and does not sign repository metadata.

## Permissions

Use read-only permissions by default. The artifact attestation job requests only `contents: read`, `attestations: write`, and `id-token: write`. The publication job requests only `contents: write`, because GitHub Release creation, release editing, and asset upload require it.

## Out of scope

- Public apt repository publication.
- Repository signing.
- Starting VPN tunnels.
- Mutating TUN devices, routes, DNS, nftables, firewall rules, or resolver files.
- GUI metadata.
