# GitHub-hosted E2E qualification design

## Goal

Move the maximum practical Podlaz acceptance surface onto disposable standard GitHub-hosted runners while keeping destructive VPN/network mutation isolated from the Actions runner control plane.

The target is not one monolithic E2E job. The target is a layered qualification model where each job has one failure domain, one class of privileges/secrets, and a fresh disposable environment.

This design preserves existing product semantics and existing scenario intent. It primarily changes where and how existing package/TUN acceptance harnesses execute. Product behavior changes are out of scope unless a real hosted run exposes a product defect that must be fixed separately.

## Current state

The repository already has three useful layers:

1. `ci.yml` runs deterministic secret-free checks, Debian package validation, and a real privileged nftables round trip on `ubuntu-24.04`.
2. `integration.yml` runs installed-package runtime qualification and a trusted real-provider proxy data-plane job on `ubuntu-24.04`.
3. `scripts/e2e/**` contains substantially richer installed-package TUN, rollback, recovery, coexistence, package-restart, and soak scenarios than the current hosted workflows execute.

Historical self-hosted `vpn-e2e` release infrastructure was removed because the runner no longer existed. The release contract intentionally prevents that retired dependency from returning. This design does not restore a persistent self-hosted runner.

The current architecture says destructive real-host E2E runs only on the dedicated runner. This design changes that boundary only after isolation is proven: destructive mutation of the GitHub Actions runner host network remains disallowed, while destructive mutation inside a fresh disposable guest owned by the job becomes an allowed hosted qualification mechanism.

## External constraints

The design relies only on documented GitHub-hosted behavior plus runtime capability probes:

- standard GitHub-hosted runners are free for this public repository;
- a normal Linux job receives a fresh VM that is destroyed after the job;
- standard GitHub-hosted jobs have a six-hour execution limit;
- native `ubuntu-24.04-arm` is available for public repositories;
- general-purpose nested KVM must not be treated as a guaranteed GitHub contract. KVM/QEMU acceleration is an optimization selected only after a runtime capability probe.

Relevant GitHub documentation:

- https://docs.github.com/en/actions/how-tos/write-workflows/choose-where-workflows-run/choose-the-runner-for-a-job
- https://docs.github.com/en/actions/reference/limits
- https://docs.github.com/en/billing/concepts/product-billing/github-actions

## Architecture

### Control plane and mutation boundary

The outer GitHub-hosted runner is the control plane. It owns checkout, package build/download, guest lifecycle, artifact staging, timeout enforcement, and final cleanup.

Destructive full-TUN qualification must not mutate the runner's own default route, resolver, policy rules, or product firewall state. Instead the runner creates a disposable Linux guest with its own network namespace and, for the strongest lifecycle cases, its own kernel.

The intended topology is:

```text
GitHub-hosted Ubuntu VM
|
|-- Actions runner / control plane
|   |-- checkout
|   |-- package artifacts
|   |-- guest lifecycle
|   `-- sanitized result collection
|
`-- disposable test guest
    |-- systemd
    |-- systemd-resolved
    |-- NetworkManager when required by the scenario
    |-- /dev/net/tun
    |-- podlaz + podlazd + packaged Xray
    |-- independent routes/rules/nftables
    `-- Internet through an outer veth/NAT boundary
```

A broken guest default route, resolver, nftables policy, TUN, daemon, or package lifecycle must not prevent the outer job from collecting diagnostics and terminating cleanly.

### Guest classes

Two guest classes are required because they prove different things.

#### Namespace/system guest

Use a lightweight isolated system guest for scenarios that require real Linux networking primitives but do not require an independent kernel or a real reboot boundary.

It must provide, when the scenario requires them:

- systemd as the service manager for the guest;
- systemd-resolved;
- optional NetworkManager owning the guest uplink;
- `/dev/net/tun`;
- real route/policy-rule mutation;
- real nftables mutation;
- real Internet egress;
- package install/reinstall/purge;
- daemon and Xray process lifecycle.

The implementation may use systemd-nspawn or an equivalent namespace-backed system guest. The exact mechanism is chosen by the capability spike and must not weaken the tested production interfaces merely to fit CI.

#### Full VM guest

Use an Ubuntu 24.04 QEMU guest for semantics that require an independent kernel/boot identity or stronger host isolation:

- actual guest reboot and new boot ID;
- boot-autostart behavior across a real reboot;
- package replacement across an independent init/kernel environment;
- daemon/systemd restart semantics where VM-level isolation materially increases confidence;
- exact historical-package-to-candidate qualification when the scenario intentionally leaves the guest network in a broken or recovery-required state.

At runtime, prefer hardware acceleration only when a capability probe proves it usable. Otherwise use a supported software-emulation fallback or mark the VM capability unavailable according to the workflow's explicit policy. Absence of acceleration must never be silently converted into a passing reduced test.

## Qualification layers

### Layer 1: fast deterministic CI

Existing `ci.yml` remains the pull-request merge foundation:

- workflow/shell/repository guards;
- Go formatting/tests/race-sensitive coverage/vet;
- vulnerability scan;
- CLI contracts;
- Debian amd64/arm64 build and static/package validation;
- install/reinstall/purge service validation;
- real-kernel nftables ownership round trip.

This layer stays secret-free and does not depend on an external VPN provider.

### Layer 2: synthetic hosted TUN qualification

Add a secret-free synthetic VPN topology that runs on ordinary pull requests or another appropriately frequent trusted-free path.

The test job creates:

1. an isolated Podlaz client guest;
2. a job-local Xray-compatible test endpoint reachable outside the client guest TUN;
3. deterministic test credentials generated for that run;
4. an external HTTP(S)/egress target or equivalent job-local/Internet target needed to prove the data plane.

The client guest installs the candidate package and exercises real `podlaz connect --mode tun` behavior.

Required proof includes:

- package/runtime provenance;
- successful profile import/validation/plan;
- real `podlaz0` lifecycle;
- exact TUN address, routes, rules, resolver state, and nftables composition expected by the product;
- verified-active product state;
- system DNS through the active TUN path;
- IPv4 TCP/TLS/HTTPS through the active TUN path;
- expected synthetic endpoint/egress behavior;
- clean disconnect;
- exact owned-resource removal;
- foreign sentinel preservation;
- clean `recover --json` after convergence.

This layer is the primary PR-time proof that Podlaz works as a VPN client rather than only as a planner/proxy.

The synthetic endpoint should cover one canonical protocol first. Additional supported protocol/transport coverage is added only when it protects a distinct compatibility contract rather than multiplying runtime without new evidence.

### Layer 3: trusted real-provider qualification

Keep the existing trusted `vpn-e2e` Environment boundary and split real-provider qualification into separate proxy and TUN jobs.

`real-provider-proxy` keeps the current proxy-only data-plane signal.

`real-provider-tun` runs the exact installed candidate inside an isolated client guest and requires:

- verified active TUN state;
- real system DNS;
- real IPv4 TCP/TLS/HTTPS;
- optional expected egress IP when configured;
- clean disconnect and recovery;
- private handling of profile/provider material;
- sanitized public artifacts only after successful cleanup and redaction scanning.

The split makes failures diagnosable: a passing proxy job with a failing TUN job points at host-network/TUN lifecycle rather than provider reachability in general.

Provider secrets remain unavailable to untrusted pull-request code.

### Layer 4: destructive lifecycle and recovery matrix

Run existing installed-package scenarios as separate jobs or a small matrix, not one catch-all script invocation.

The permanent job/scenario boundaries should be based on invariants such as:

- normal TUN lifecycle and convergence;
- network-resource coexistence and foreign-state preservation;
- Privacy Envelope lifecycle;
- fault-injected rollback;
- terminal recovery;
- daemon/Xray restart and crash recovery;
- package lifecycle/replacement;
- network reconciliation;
- stale-link/resolver convergence;
- boot continuation.

Reuse the existing `scripts/e2e/**` scenario bodies where their semantics already match the desired qualification. Add shared guest/bootstrap mechanics under `scripts/e2e/lib/**` only when at least two scenarios need the same mechanics.

Each destructive job gets a fresh guest so residue from one failed scenario cannot contaminate another.

### Layer 5: historical package upgrade/recovery qualification

The existing exact `v0.2.40 -> candidate` package-restart harness is a primary migration acceptance case.

Run it inside the strongest available disposable guest class, preferably the full VM guest, with the existing pinned historical package digest and candidate package provenance checks intact.

Required evidence includes the harness's existing DNS/HTTPS, package/runtime identity, Network Session/replay evidence, foreign-state preservation, recovery classification, and final convergence assertions.

This replaces the historical dependence on a manually maintained target host without weakening the exact historical boundary.

### Layer 6: boot/reboot qualification

Use a full VM guest to replace simulated reboot-only coverage where a real reboot adds semantic evidence.

Required cases include:

- boot-autostart off/on behavior across a real guest reboot;
- new boot-ID scoping;
- no same-boot retry after terminal automatic-connect outcome;
- correct continuation priority relative to fresh boot autostart;
- clean explicit disconnect/stop behavior followed by reboot/start as applicable.

Do not remove deterministic unit/contract coverage when adding the VM test; the VM layer complements it.

### Layer 7: resource soak

Run `tun-resource-soak.sh` on a schedule and by manual dispatch, not on every pull request.

The existing default three-hour measurement window fits within GitHub's six-hour standard hosted job limit, but setup, teardown, and artifact handling must retain sufficient margin below the platform limit.

The soak guest must be disposable. If the current trusted-host fingerprint model assumes an administrator-provisioned persistent physical host, introduce a guest-specific trusted baseline generated from a known-clean immutable guest image/topology only if that preserves the purpose of the fingerprint. The test must not bless arbitrary post-mutation live state as its own trust anchor.

## Architecture matrix

### amd64

`ubuntu-24.04` is the primary complete qualification architecture. It receives:

- all fast CI;
- synthetic TUN;
- trusted real-provider TUN;
- destructive guest scenarios;
- full-VM boot/reboot and historical upgrade qualification;
- scheduled soak.

### arm64

Use native `ubuntu-24.04-arm` to qualify what can run directly and safely there:

- native package installation/runtime;
- CLI/service/package lifecycle;
- proxy data plane where appropriate;
- isolated TUN/network qualification when the required guest mechanism works natively.

Do not require nested hardware virtualization on ARM unless GitHub documents/proves the capability for the runner used. Full-VM parity can remain amd64-only until the capability is stable and justified.

## Capability proof before permanent wiring

The first implementation milestone is a bounded Actions capability workflow. It is intentionally a spike and does not become a release gate until its findings are reviewed.

The spike includes the smallest throwaway synthetic Xray endpoint needed to prove that an installed Podlaz package can complete a real full-TUN lifecycle inside the candidate guest. That endpoint is feasibility scaffolding only; it is not the permanent Layer 2 harness. After the guest mechanism is selected from real Actions evidence, the next milestone promotes the proven topology into reusable, behavior-oriented synthetic qualification.

The capability workflow must record pass/fail for:

- `/dev/net/tun` availability and functional TUN creation;
- network namespace creation;
- veth/NAT Internet access from an isolated namespace;
- independent route/policy-rule mutation;
- independent nftables mutation;
- system guest boot with systemd;
- systemd-resolved operation in the guest;
- NetworkManager operation in the guest;
- package install/service start in the guest;
- guest Internet access after setup;
- Podlaz proxy-only lifecycle in the guest;
- Podlaz full-TUN lifecycle against the throwaway synthetic endpoint;
- active system DNS and HTTPS through TUN;
- guest cleanup while the outer runner retains Internet/GitHub connectivity;
- QEMU availability;
- `/dev/kvm` presence and actual usability, reported separately from QEMU software-emulation viability;
- full Ubuntu guest boot;
- full guest reboot with changed boot ID.

The capability workflow must fail explicitly when a capability required for the next permanent layer is absent. Optional accelerators are reported separately from required semantics.

## Workflow placement and cadence

Avoid adding many top-level workflows without need. Prefer the existing workflow ownership model:

- `ci.yml`: deterministic PR/master merge gate and secret-free synthetic qualification when runtime remains appropriate for PRs;
- `integration.yml`: trusted/master/manual real-provider and destructive integration, plus scheduled soak if concurrency semantics remain clear;
- `release.yml`: exact-tag qualification of the same built artifacts, reusing the same scenario entrypoints instead of release-only duplicates.

If capability proof shows that VM/destructive orchestration makes `integration.yml` unreadable or creates conflicting trigger/concurrency semantics, one dedicated invariant-oriented hosted-E2E workflow is acceptable. It must replace complexity rather than duplicate equivalent jobs.

Suggested cadence:

```text
pull_request:
  fast deterministic CI
  synthetic TUN qualification

master push:
  fast CI
  installed runtime
  synthetic TUN
  trusted real-provider proxy/TUN
  selected destructive recovery matrix

schedule:
  long resource soak
  optional extended destructive matrix

release tag:
  exact artifact build
  native amd64/arm64 installed qualification
  real-provider proxy/TUN
  destructive recovery/package migration
  boot/reboot VM qualification
  publish only after required qualification succeeds
```

## Failure isolation and time bounds

Every external command that can hang must remain bounded by the scenario's existing timeout model or an equally strict replacement.

One scenario failure must not prevent outer-runner diagnostics from being staged. The outer control plane is responsible for guest termination even when product cleanup inside the guest fails.

Guest cleanup is not product success. Product cleanup assertions must complete before the outer guest is destroyed; destroying the guest cannot be used to turn incomplete Podlaz convergence into a pass.

No single permanent hosted job should approach GitHub's six-hour platform ceiling. The soak job is the only intentionally multi-hour job and must preserve explicit setup/cleanup margin.

## Secrets, logs, and artifacts

Existing private-output and redaction boundaries remain authoritative.

Additional hosted guest orchestration must ensure:

- profile/provider credentials never appear in command lines or public artifact names when avoidable;
- raw guest journals, generated configs, host/provider addresses, and authority-bearing state stay in private temporary storage;
- only normalized/sanitized evidence is copied to the outer public artifact staging directory;
- artifact upload remains gated by cleanup/redaction checks where the existing scenarios require them;
- untrusted pull-request code cannot access the real-provider Environment secrets.

Synthetic qualification uses ephemeral generated test credentials so it can remain secret-free.

## Repository policy update

After the capability proof succeeds, update the canonical E2E architecture wording so the safety rule is expressed by mutation boundary rather than by historical runner type:

- destructive mutation of the GitHub Actions runner host network is forbidden;
- destructive mutation is allowed inside a fresh disposable isolated guest whose lifecycle is controlled by the job;
- scenarios that specifically require a real independent boot/kernel boundary use the full VM guest;
- physical/hardware-only acceptance remains outside hosted CI.

The workflow/release contract that prevents the retired persistent self-hosted runner dependency from returning remains valid.

## Explicitly out of scope

GitHub-hosted qualification is not treated as proof of hardware-specific laptop behavior such as:

- physical Wi-Fi driver/firmware behavior;
- real Wi-Fi roaming between access points;
- physical suspend/resume, lid, battery, or ACPI behavior;
- quirks of a specific user's router, NIC, kernel build, or laptop firmware.

Those remain optional manual/hardware acceptance. Their absence does not justify weakening the hosted networking/lifecycle tests.

## Implementation sequence

1. Add the capability workflow, minimal guest/bootstrap helpers, and a throwaway synthetic Xray endpoint sufficient to prove full-TUN feasibility.
2. Run it on `ubuntu-24.04` and capture actual capability evidence.
3. Review results and select the simplest guest mechanism that proves the required production interfaces.
4. Promote the proven synthetic topology into reusable secret-free Xray/TUN qualification.
5. Migrate existing destructive installed-package harnesses into isolated hosted jobs by invariant.
6. Add trusted real-provider TUN qualification.
7. Add full-VM reboot and exact historical package migration qualification.
8. Add native ARM64 installed/TUN coverage where capability permits.
9. Move the existing soak to scheduled disposable hosted execution.
10. Tighten release publication dependencies only after the corresponding hosted jobs have demonstrated stable evidence on master/manual runs.
11. Update canonical architecture/policy wording and remove the temporary spec/plan artifacts before final merge-ready repository verification.

## Acceptance criteria

The work is complete when:

1. Pull requests have a secret-free real full-TUN data-plane check using the installed candidate package and real Linux TUN/network primitives.
2. A guest can lose routes, DNS, nftables correctness, Xray, or `podlazd` without losing outer Actions control or diagnostic collection.
3. Existing coexistence, Privacy Envelope, rollback, terminal recovery, restart, and package-lifecycle scenarios run on disposable GitHub-hosted infrastructure or have a documented technical reason they cannot.
4. Real-provider proxy and TUN qualification are separate trusted jobs.
5. Exact `v0.2.40 -> candidate` package-restart acceptance runs on a disposable hosted guest and preserves its existing exact provenance semantics.
6. At least one full-VM job proves a real guest reboot and boot-ID change, then exercises Podlaz boot/recovery semantics.
7. Native ARM64 package/runtime qualification exists and TUN coverage is enabled to the maximum proven runner capability without assuming unsupported nested virtualization.
8. The three-hour resource soak can run on schedule inside the six-hour hosted limit with safe artifact handling.
9. Release publication depends only on reproducible GitHub-hosted qualification plus exact build artifacts; no persistent self-hosted runner is required.
10. Destructive host-network mutation never targets the outer GitHub Actions runner namespace.
11. Hardware-specific gaps are explicitly bounded rather than being represented as covered.
