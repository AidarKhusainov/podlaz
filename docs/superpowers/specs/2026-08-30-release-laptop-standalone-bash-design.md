# Standalone Bash release-laptop acceptance harness design

## Goal

Replace the current Bash entrypoint plus Python orchestration package with one downloadable, self-contained `scripts/acceptance/release-laptop.sh` that requires no repository checkout and no Python runtime modules.

## Operator contract

The operator downloads exactly one executable file and supplies already-built Podlaz Debian packages:

```bash
sudo ./release-laptop.sh ./podlaz_<candidate>_linux_<arch>.deb --profile <profile-id>
```

When no strictly lower Podlaz release is already installed, the exact previous package is supplied explicitly:

```bash
sudo ./release-laptop.sh ./podlaz_<candidate>_linux_<arch>.deb \
  --previous-deb ./podlaz_<previous>_linux_<arch>.deb \
  --profile <profile-id>
```

The same file resumes or abandons a durable run:

```bash
sudo ./release-laptop.sh --resume
sudo ./release-laptop.sh --abort
```

No source checkout, Python package, virtual environment, or generated helper file is required.

## Architecture

The harness is pure Bash and uses `jq` as the JSON parser/transformer. It shells out only to explicit host tools needed by the qualification workflow: `dpkg`, `dpkg-deb`, `sha256sum`, `stat`, `systemctl`, `curl`, `ip`, `nft`, `resolvectl`, `nmcli`, `rtcwake`, `ps`, `awk`, `sed`, `grep`, `find`, `mktemp`, and the installed Podlaz CLI.

All orchestration lives in one file, grouped into shell-function sections with stable prefixes:

- `ra_cli_*`: argument parsing and operator UX;
- `ra_state_*`: crash-atomic checkpoint persistence and mutation ledger;
- `ra_pkg_*`: exact Debian package inspection/install/identity validation;
- `ra_product_*`: Podlaz CLI/daemon operations;
- `ra_privacy_*`: protected/ordinary network evidence;
- `ra_fixture_*`: documentation-safe coexistence fixtures and exact cleanup;
- `ra_lifecycle_*`: restart, kill, stop/start, reinstall, rollback interruption;
- `ra_soak_*`: timed resource/privacy/doctor observations plus optional Wi-Fi and suspend events;
- `ra_reboot_*`: three user-controlled reboot phases and terminal synthetic-profile recovery;
- `ra_report_*`: private evidence plus sanitized summary/report output;
- `ra_run_*`: new/resume/abort state machine.

Functions exchange scalar values through stdout and structured values through JSON strings/files. No `eval` is allowed. Arrays are Bash arrays, and persisted structured state is manipulated only through `jq`.

## Durable state and crash safety

The checkpoint remains under the original non-root user's state tree. Writes use a same-directory temporary file, restrictive mode, `mv`, and directory sync where available. The checkpoint records candidate identity, run configuration, phase, previous `boot_id`, scenario outcomes, private evidence paths, and a mutation ledger.

Mutation state remains:

`acquiring -> acquired -> releasing -> released`

Write-ahead authority is persisted before each host mutation. `--resume` and `--abort` reconcile only exact recorded authority. Ambiguous live state fails closed and retains the checkpoint.

Dynamic identities, including the synthetic terminal profile, use write-ahead baselines before creation so a crash between creation and ID persistence can be reconciled safely.

## Package safety

Only explicitly supplied `.deb` files may be installed. The harness never invokes `apt`/`apt-get`, never repairs dependencies, never builds Podlaz, and never downloads a previous package.

Candidate and previous package authority is bound to path, package name, Debian version, architecture, SHA-256, device, and inode. Package setup is resumable around interrupted `dpkg -i` operations and accepts only exact candidate/previous installed versions.

## Network fixture safety

Fixture identities use documentation/reserved address ranges and fixed harness-owned route tables, priorities, nftables table names, TUN names, and dummy DNS links. Acquisition is write-ahead.

Cleanup first observes all potentially occupied identities. It deletes only components that exactly match the recorded fixture. Foreign routes, rules, nftables objects, link kinds, or other ambiguous state abort cleanup without broad flushes. Both interrupted acquisition (`acquiring`) and interrupted cleanup (`releasing`) are resumable.

## Qualification behavior

The standalone Bash implementation preserves the approved release-laptop scope:

- lower-release active TUN -> candidate upgrade continuity;
- protected privacy proof and ordinary-network restoration;
- graceful daemon restart;
- unexpected daemon main-process kill;
- explicit stop/start with no unwanted reconnect;
- same-candidate reinstall;
- durable rollback interruption;
- pre-connect and active-session coexistence/collision fixtures;
- resource attribution and canonical 60-minute soak;
- optional NetworkManager reconnect and bounded RTC suspend/resume when host capability exists;
- established-session terminal failure convergence;
- three real user-controlled reboot phases with `--resume`;
- successful autostart, explicit-disconnect same-boot no-retry, terminal autostart failure, terminal same-boot no-retry;
- exact original autostart restoration;
- `QUALIFIED_PASS`, `PARTIAL_PASS`, or `FAIL` result semantics;
- private evidence separated from sanitized shareable reports.

The harness never reboots automatically.

## Validation strategy

Repository tests must prove the distribution contract directly:

1. `release-laptop.sh` contains no Python invocation or dependency on `release_acceptance` modules.
2. No runtime `.py` files remain under `scripts/acceptance/`.
3. `bash -n` and shellcheck validate the standalone file.
4. A shell regression suite runs the script in an isolated fake-host environment by prepending deterministic fake commands to `PATH` and overriding the state/evidence roots through test-only environment variables accepted only when `RELEASE_ACCEPTANCE_TEST_MODE=1`.
5. Regression cases cover package identity, write-ahead mutation transitions, exact/foreign fixture recovery, NetworkManager restoration, terminal-profile recovery, resume boundaries, abort cleanup, and qualification evaluation.
6. Existing repository Go/vet/govuln/CLI/package CI remains green.

The disruptive real-laptop workflow is still separate operator evidence and is not run in GitHub Actions.
