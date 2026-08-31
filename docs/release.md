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

Pull-request CI and tagged release validation must invoke the same canonical `scripts/ci/validate-package-install.sh` script exactly once. `PODLAZ_VALIDATE_SERVICE=1` must be bound directly to that command invocation; an unrelated workflow or step environment declaration does not satisfy the parity contract. Release validation may supply release-specific version metadata, but it must not introduce a stricter package installation or service-availability contract that was not already exercised before merge. `scripts/ci/validate-package-workflow-contract.sh` enforces this parity.

The tagged workflow remains responsible for rebuilding immutable artifacts from the tag and checking their release metadata, checksums, attestations, and publication behavior. Those release-only checks may still fail after tagging, but package installation, service availability, CLI access, and purge behavior must already have passed the pull-request gate.

For manually dispatched self-hosted TUN coverage and issue-specific package scenarios, see `docs/e2e.md`. The laptop harness below complements those disposable-runner workflows with maintainer-laptop upgrade, suspend/NetworkManager, long-soak, and real reboot evidence; it does not replace CI or claim a release qualification until its own mandatory phases complete.

## Maintainer laptop release qualification

For a release candidate that needs real-host package, TUN, lifecycle, privacy, resource, suspend, NetworkManager, and boot evidence, use the standalone Bash harness against the already-built native Debian package:

```bash
sudo ./scripts/acceptance/release-laptop.sh ./podlaz_<candidate>_linux_<arch>.deb --profile <profile-id>
```

The operator laptop does not need a Podlaz source checkout beyond the single copied script itself, and the harness does not require Python. The candidate package, any explicitly supplied lower package, and the selected profile are treated as immutable run inputs after preflight.

If no strictly lower Podlaz release is already installed, provide the exact lower release explicitly:

```bash
sudo ./scripts/acceptance/release-laptop.sh ./podlaz_<candidate>_linux_<arch>.deb \
  --previous-deb ./podlaz_<previous>_linux_<arch>.deb \
  --profile <profile-id>
```

Candidate/lower package metadata, architecture, checksum/identity, Debian version ordering, artifact path safety, profile validity, required tools/capabilities, fixture availability, clean starting network boundary, and release-boundary eligibility are checked before disruptive acceptance mutation. In particular, when the candidate is already installed and no strictly lower release or `--previous-deb` is available, the run fails before creating a checkpoint.

A full qualification deliberately crosses three real user-controlled reboot boundaries. After each requested reboot, continue the same durable run with:

```bash
sudo ./scripts/acceptance/release-laptop.sh --resume
```

Reboot-wait checkpoints are intentional pauses. They are not automatically aborted by another ordinary invocation; an ordinary invocation reports that `--resume` is required. `--resume` also refuses to continue a reboot phase until the persisted `boot_id` has actually changed.

For a normal non-reboot interruption, running the original command again is useful: replay-safe persisted phases are resumed from live observation plus the mutation ledger; an exactly cleanable failed run is retired and a fresh run starts only after the new inputs have been checked; ambiguous ownership fails closed without broad cleanup.

To explicitly discard a prior run with exact cleanup and then start a new run, use:

```bash
sudo ./scripts/acceptance/release-laptop.sh ./podlaz_<candidate>_linux_<arch>.deb --restart --profile <profile-id>
```

`--restart` is not a broad reset. It uses the same exact-owned cleanup rules as failure recovery and starts the new run only if cleanup converges conclusively.

To abandon a persisted run without starting a replacement, use:

```bash
sudo ./scripts/acceptance/release-laptop.sh --abort
```

The harness is intentionally disruptive. It may install only explicitly supplied Podlaz `.deb` files with `dpkg -i`, create exact documentation-safe coexistence fixtures, restart/kill the Podlaz daemon for lifecycle scenarios, perform a controlled NetworkManager reconnect when applicable, suspend the local laptop with bounded RTC wake, and ask the operator to reboot. It never runs a Podlaz source build, never downloads a previous release, never installs or repairs dependencies, never performs broad route/rule/nftables cleanup, never automatically reboots, and never invokes broad recovery to manufacture a pass.

Each disruptive scenario persists an explicit boundary and scenario state (`pending`, `prepared`, `running`, `verifying`, `passed`, or `failed`). Mutation authority is write-ahead persisted and reconciled against live state before any retry. A mutation is not repeated merely because the previous shell process disappeared; if completion cannot be proved from live state and persisted ownership, the harness fails closed instead of guessing.

Status waits use semantic JSON state rather than the human-readable `tun` presentation string. Active TUN requires compatible `connection`, `mode`, TUN-health, committed transaction, and non-contradictory cleanup/terminal evidence. `revalidating` is treated as bounded progress toward `verified`; already-satisfied state succeeds immediately; terminal or incompatible state fails without waiting for the entire timeout.

### Automatic failure recovery

After a checkpoint exists, an unexpected run failure normally enters one guarded failure finalizer. Before cleanup it captures a private failure bundle containing the checkpoint snapshot, complete command transcript, last status payload, `doctor --tun`, `systemctl status`, journal entries scoped to the current run start time, package state, mutation ledger, continuation/transaction/boot-attempt state, and network observations (`ip`, nftables, and resolved state).

`SIGINT` and `SIGTERM` use the same guarded path when the checkpoint is in a normal mutable phase. Intentional reboot-wait phases and retained ambiguous cleanup states are not mutated merely because a signal arrives.

Cleanup order is deliberate: an active acceptance session that can be proved to belong to the run is disconnected first; then exact harness-owned fixtures, NetworkManager state, systemd hooks, autostart state, synthetic profiles, and package authority are reconciled. Package installation/restoration is performed only when live package state proves it is required. Finally the harness proves an inactive Podlaz boundary and ordinary DNS, TCP, HTTPS, default-route, and direct-egress connectivity.

If exact cleanup and ordinary connectivity are proven, the failure outcome is:

```text
FAILED_CLEAN
```

The failed run's checkpoint is then removed; the private diagnostics remain in the run artifact directory.

If cleanup or ownership is ambiguous, the outcome is:

```text
FAIL_CLEANUP_FAILED
```

The checkpoint is retained as recovery authority. The harness does not perform broad cleanup in this state. Inspect the private failure bundle and resolve the ownership ambiguity before attempting an explicit abort/restart.

### Result meanings

Normal qualification results are strict:

- `QUALIFIED_PASS`: the canonical 60-minute soak, lower-release active upgrade boundary, mandatory candidate lifecycle/privacy/terminal cases, coexistence proof, and all three real reboot phases completed without a product or cleanup failure;
- `PARTIAL_PASS`: useful validation completed without an observed product failure, but user-requested skips, a shortened soak, omitted reboot phases, or missing mandatory full-qualification coverage prevent a complete release qualification;
- `FAIL`: a completed qualification could not satisfy the required positive product/evidence contract;
- `FAILED_CLEAN`: an unexpected post-checkpoint failure occurred, private diagnostics were captured, and automatic exact cleanup/restoration was conclusively verified;
- `FAIL_CLEANUP_FAILED`: an unexpected failure occurred and exact cleanup/restoration could not be proved, so the checkpoint was deliberately retained.

The candidate remains installed after successful finalization and after clean failure/abort recovery when recorded package authority requires the candidate. The harness never restores a package merely because a `.deb` path exists; it observes live package state and recorded ownership first.

Evidence is written under the original user's state tree by default. Private command/host evidence is kept under the run's private directory. Shareable `summary.txt`, `report.json`, and `requirements-observation.json` are structurally generated from sanitized outcome/state data and must not contain real user IPs, domains, endpoints, SSIDs, profile IDs, credentials, or subscription data. The harness does not upload artifacts automatically.

The laptop run itself is manual release evidence; source-level or CI validation of the harness implementation is separate from executing the disruptive qualification workflow.

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
- Starting VPN tunnels outside explicit release-laptop qualification.
- Mutating TUN devices, routes, DNS, nftables, firewall rules, or resolver files outside explicit release-laptop qualification.
- GUI metadata.
