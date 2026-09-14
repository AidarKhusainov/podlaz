# GitHub-hosted E2E qualification design

## Goal

Move the maximum practical Podlaz acceptance surface onto disposable standard GitHub-hosted runners while keeping destructive VPN/network mutation isolated from the Actions runner control plane.

The target is not one monolithic E2E job. The target is a layered qualification model where each job has one failure domain, one privilege/secrets class, and a fresh disposable environment.

This design preserves product semantics and existing scenario intent. It primarily changes where and how existing package/TUN acceptance harnesses execute. Product behavior changes are out of scope unless real hosted execution exposes a product defect that must be fixed separately.

## Current state

The repository already has three useful layers:

1. `ci.yml` runs deterministic secret-free checks, Debian package validation, and a real privileged nftables round trip on `ubuntu-24.04`.
2. `integration.yml` runs installed-package runtime qualification and a trusted real-provider proxy data-plane job on `ubuntu-24.04`.
3. `scripts/e2e/**` contains substantially richer installed-package TUN, rollback, recovery, coexistence, package-restart, reconciliation, and soak scenarios than the current hosted workflows execute.

Historical self-hosted `vpn-e2e` release infrastructure was removed because that runner no longer existed. The release contract intentionally prevents that retired dependency from returning. This design does not restore a persistent self-hosted runner.

The current repository policy allows destructive real-host E2E only on the dedicated runner. That boundary changes only after isolation is proven: destructive Podlaz mutation of the GitHub Actions runner host network remains forbidden, while destructive mutation inside a fresh disposable guest owned by the job becomes an allowed hosted qualification mechanism.

## External constraints

The design relies only on documented GitHub-hosted behavior plus runtime capability probes:

- standard GitHub-hosted runners are free for this public repository;
- each standard Linux job receives a fresh VM that is discarded after the job;
- a standard GitHub-hosted job has a six-hour execution limit;
- public `ubuntu-24.04` and `ubuntu-24.04-arm` runners provide 4 vCPU, 16 GB RAM, and 14 GB SSD;
- general-purpose nested KVM is not treated as a guaranteed GitHub contract; KVM is an optional accelerator selected only after a runtime capability probe;
- QEMU TCG/software emulation remains a valid feasibility fallback, but its runtime must fit the job and disk budgets before becoming a required release gate.

Relevant official documentation:

- https://docs.github.com/en/actions/reference/runners/github-hosted-runners
- https://docs.github.com/en/actions/reference/limits
- https://docs.github.com/en/billing/concepts/product-billing/github-actions
- https://www.freedesktop.org/software/systemd/man/systemd.nspawn.html
- https://qemu.readthedocs.io/en/master/system/
- https://cloud-images.ubuntu.com/releases/noble/

## Architecture

### Control plane and mutation boundary

The outer GitHub-hosted runner is the control plane. It owns checkout, package build/download, disposable-guest lifecycle, timeouts, artifact staging, and final guest destruction.

Destructive full-TUN qualification must not mutate the runner's own default route, resolver configuration, policy-routing semantics, or Podlaz firewall state. The runner may create narrowly scoped test plumbing such as a uniquely named veth, bridge/namespace attachment, or dedicated nftables NAT table when needed to give a guest connectivity. That plumbing is infrastructure-owned, collision-checked, exact-identity-scoped, and removed by the outer controller; it must never be confused with Podlaz product cleanup evidence.

The intended topology is:

```text
GitHub-hosted Ubuntu VM
|
|-- Actions runner / control plane
|   |-- checkout
|   |-- exact package artifacts
|   |-- guest lifecycle
|   |-- narrowly scoped guest-network plumbing
|   `-- sanitized result collection
|
`-- disposable test guest
    |-- systemd
    |-- systemd-resolved
    |-- NetworkManager when required by the scenario
    |-- /dev/net/tun
    |-- podlaz + podlazd + packaged Xray
    |-- independent routes/rules/nftables
    `-- Internet through the outer guest boundary
```

A broken guest default route, resolver, nftables policy, TUN, daemon, Xray process, or package lifecycle must not prevent the outer Actions runner from collecting bounded diagnostics and terminating the job.

Outer-runner health is an explicit invariant: the capability proof records the pre-test control-plane network baseline and proves after each destructive guest phase that outer GitHub/HTTPS connectivity still works and no unexpected outer route/rule/resolver/firewall mutation remains.

### Guest classes

Two guest classes are required because they prove different things.

#### Namespace/system guest

Use a lightweight isolated system guest for scenarios that require real Linux networking primitives but do not require an independent kernel or real reboot boundary.

It must provide, when the scenario requires them:

- systemd as the guest service manager;
- systemd-resolved;
- NetworkManager owning a guest uplink for desktop-network scenarios;
- `/dev/net/tun`;
- real route and policy-rule mutation;
- real nftables mutation;
- real Internet egress;
- package install/reinstall/purge;
- daemon and Xray process lifecycle;
- ordinary-user, socket, group, and polkit behavior rather than root-only shortcuts.

The capability spike may use `systemd-nspawn` or an equivalent namespace-backed system guest. The permanent mechanism is selected from actual Actions evidence and must not replace production interfaces with mocks merely to fit CI.

#### Full VM guest

Use an Ubuntu 24.04 QEMU guest for semantics that require an independent kernel, real boot identity, or stronger failure isolation:

- actual guest reboot and changed boot ID;
- boot-autostart behavior across a real reboot;
- package replacement in an independent init/kernel environment;
- exact historical-package-to-candidate qualification when the test intentionally leaves the guest network broken or recovery-required;
- lifecycle cases where sharing the outer kernel would materially weaken evidence.

The VM uses an official Ubuntu 24.04 image whose checksum is verified before boot. Use a sparse copy-on-write disk/overlay and bounded logs so the complete job remains inside the standard runner's 14 GB SSD budget. KVM presence and actual usability are reported separately; KVM absence must not be represented as a failed product test if QEMU/TCG can still prove the required semantics. Conversely, an unusably slow or disk-exhausting TCG path cannot silently become a required release gate.

## Qualification depth

Hosted qualification must preserve the depth of existing Podlaz diagnostics and acceptance rather than reducing “VPN works” to `tun0 exists` or one HTTP request.

### Active data plane

Where the topology can exercise the corresponding production path, active TUN evidence includes:

- exact installed package/runtime provenance;
- real `podlaz0` lifecycle and exact address identity;
- exact routes and policy rules;
- VPN server bypass routing;
- exact systemd-resolved link ownership/composition;
- exact nftables and Privacy Envelope composition;
- verified-active typed product status;
- system DNS through the active VPN path;
- IPv4 TCP/443, TLS, and HTTPS through the VPN;
- `doctor --tun` execution against the real active session, retaining its DNS UDP/TCP, positive resolution, `.invalid` NXDOMAIN integrity, TLS/HTTPS, independent DoH, IPv6, and guarded PMTU classifications as product evidence rather than replacing them with test-only probes;
- expected synthetic or real-provider egress identity where deterministic.

IPv6 is not silently assumed to be a supported tunneled data plane. The hosted topology must exercise and assert the product's actual IPv6 contract: absent/unusable states remain explicit, and any available IPv6 route that bypasses the protected path must be detected as a leak rather than ignored.

MTU/PMTU evidence remains diagnostic and topology-aware. The CI harness must not manufacture a passing MTU claim by disabling the product probe; where the guest topology cannot reproduce a meaningful PMTU path, the limitation is recorded rather than converted into success.

### Fail-closed privacy behavior

The hosted destructive matrix must prove negative behavior as well as successful connectivity:

- direct ordinary egress remains blocked while a Network Session Privacy Envelope intentionally protects an interrupted/degraded replacement or recovery;
- foreign firewall/routing/DNS state remains unchanged;
- a terminal teardown restores ordinary networking only after exact Podlaz-owned data-plane and protection cleanup converges;
- destroying the disposable guest is never accepted as proof that Podlaz cleanup succeeded.

### NetworkManager and uplink behavior

A desktop-like guest class includes NetworkManager as the uplink owner. Hosted coverage must preserve the existing acceptance semantics for:

- the physical/virtual uplink remaining the expected active NetworkManager connection;
- Podlaz's Xray-created TUN not being left published as an active external NetworkManager connection after convergence;
- bounded uplink down/up or DHCP-style churn driving normal revalidation/reconciliation;
- route replacement and resolved convergence without test-side Podlaz repair.

Physical Wi-Fi roaming remains outside hosted CI, but the product-level uplink-change semantics are not omitted merely because the guest uses virtual Ethernet.

### Authorization and ordinary-user boundary

Installed-package hosted qualification preserves the existing privilege contract:

- the CLI test identity is not root;
- it does not gain permanent membership in the private `podlaz` service group merely to make tests pass;
- filesystem/abstract daemon socket behavior remains intentional;
- polkit authorization and authorization-unavailable/denied classifications remain exercised;
- any headless CI polkit rule is narrowly scoped to the exact required actions, is guest-local, and is removed after the scenario.

### Journals, diagnostics, and privacy

Guest orchestration does not weaken existing evidence/privacy rules:

- raw profile/provider material, endpoint data, generated runtime config, private host addresses, and authority-bearing state remain private;
- raw Xray stdout/stderr is not reintroduced into journald as a testing shortcut;
- daemon/core log acceptance remains executable under the supported ordinary-user boundary;
- public artifacts contain only normalized, bounded, redaction-scanned evidence;
- private guest diagnostics are deleted after any public-safe projection needed by the workflow is produced.

## Qualification layers

### Layer 1: fast deterministic CI

Existing `ci.yml` remains the pull-request merge foundation:

- workflow/shell/repository guards;
- Go formatting/tests/race-sensitive coverage/vet;
- vulnerability scan;
- CLI contracts;
- Debian amd64/arm64 build and package validation;
- install/reinstall/purge service validation;
- real-kernel nftables ownership round trip.

This layer remains secret-free and does not depend on an external VPN provider.

### Layer 2: synthetic hosted TUN qualification

Add a secret-free synthetic VPN topology for ordinary pull requests or another appropriately frequent secret-free path.

The job creates:

1. an isolated Podlaz client guest;
2. a job-local Xray-compatible endpoint reachable outside the client guest's TUN;
3. ephemeral test credentials generated for that run;
4. a deterministic target or Internet path needed to prove the data plane.

The client guest installs the candidate `.deb` and exercises real `podlaz connect --mode tun` behavior.

Required proof includes the active-data-plane, privilege, cleanup, foreign-state, and privacy evidence above, plus clean `recover --json` after convergence.

The permanent synthetic endpoint covers one canonical supported protocol/transport first. Additional protocols/transports are added only when they protect a distinct compatibility contract rather than multiplying CI runtime without new evidence.

### Layer 3: trusted real-provider qualification

Keep the existing trusted `vpn-e2e` Environment boundary and split provider qualification into separate proxy and TUN jobs.

`real-provider-proxy` keeps the current proxy-only signal.

`real-provider-tun` installs the exact candidate inside an isolated client guest and requires verified active TUN state, real system DNS/TCP/TLS/HTTPS, optional expected egress identity, cleanup/recovery, and privacy-safe artifacts.

The split makes failures diagnosable: proxy PASS with TUN FAIL points primarily at host-network/TUN lifecycle rather than provider reachability in general.

Provider secrets remain unavailable to untrusted pull-request code.

### Layer 4: destructive lifecycle and recovery matrix

Run existing installed-package scenarios as separate jobs or a small matrix, not one catch-all invocation. Permanent boundaries remain invariant-oriented, for example:

- normal TUN lifecycle and convergence;
- network-resource coexistence and foreign-state preservation;
- Privacy Envelope lifecycle and leak prevention;
- fault-injected rollback;
- terminal recovery;
- daemon/Xray restart and crash recovery;
- package lifecycle/replacement;
- NetworkManager/uplink reconciliation;
- stale-link/resolver convergence;
- boot continuation.

Reuse existing `scripts/e2e/**` scenario bodies when their semantics already match the desired qualification. Shared guest/bootstrap mechanics belong in `scripts/e2e/lib/**` only after at least two real scenarios need the same mechanics.

Each destructive job gets a fresh guest so residue from one failed scenario cannot contaminate another.

### Layer 5: historical package upgrade/recovery qualification

The existing exact `v0.2.40 -> candidate` package-restart harness is a primary migration acceptance case.

Run it inside the strongest available disposable guest class, preferably the full VM guest, with the pinned historical package digest and candidate package provenance checks intact.

Required evidence includes the existing DNS/HTTPS, package/runtime identity, Network Session/replay evidence, foreign-state preservation, recovery classification, privacy behavior, and final convergence assertions.

### Layer 6: boot/reboot qualification

Use a full VM guest where a real reboot adds semantic evidence.

Required cases include:

- boot-autostart off/on behavior across a real guest reboot;
- actual boot-ID change and boot-scoped authority behavior;
- no same-boot retry after terminal automatic-connect outcome;
- correct continuation priority relative to fresh boot autostart;
- clean explicit disconnect/stop behavior followed by reboot/start as applicable.

Deterministic unit/contract coverage remains; VM coverage complements rather than replaces it.

### Layer 7: resource soak

Run `tun-resource-soak.sh` on a schedule and by manual dispatch, not on every pull request.

The existing three-hour default measurement window fits inside GitHub's six-hour job limit, but setup, warm-up, cleanup, redaction scanning, and upload must retain explicit safety margin.

The soak guest is disposable. If the current trusted-host fingerprint model assumes an administrator-provisioned persistent physical host, a guest-specific trust baseline may be generated only from a known-clean immutable guest image/topology before product mutation. The test must not bless arbitrary post-mutation live state as its own trust anchor.

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

Use native `ubuntu-24.04-arm` to qualify the maximum proven native surface:

- package installation/runtime;
- CLI/service/package lifecycle;
- proxy data plane where appropriate;
- isolated TUN/network qualification when the selected guest mechanism works natively.

Do not require nested hardware virtualization on ARM unless the GitHub runner actually exposes and supports it. Full-VM parity may remain amd64-only until the capability is stable and justified.

## Capability proof before permanent wiring

The first implementation milestone is a bounded Actions capability workflow. It is a spike and is not a release gate.

The spike includes the smallest throwaway synthetic Xray endpoint needed to prove that an installed Podlaz package can complete a real full-TUN lifecycle inside the candidate guest. That endpoint is feasibility scaffolding only; it is not the permanent Layer 2 harness. After the guest mechanism is selected from real Actions evidence, the next milestone promotes the proven topology into reusable behavior-oriented synthetic qualification.

The capability workflow records pass/fail/unavailable evidence for:

- outer-runner baseline and post-test control-plane health;
- `/dev/net/tun` availability and functional TUN creation;
- network namespace creation;
- veth/NAT guest Internet access without altering the outer default route or resolver;
- independent guest route/policy-rule mutation;
- independent guest nftables mutation;
- system guest boot with systemd;
- systemd-resolved operation in the guest;
- NetworkManager operation and active uplink identity in the guest;
- candidate package install and exact service/runtime provenance;
- ordinary-user/socket/polkit acceptance in the guest;
- guest Internet access before Podlaz connect;
- Podlaz proxy-only lifecycle in the guest;
- Podlaz full-TUN lifecycle against the throwaway synthetic endpoint;
- active system DNS and IPv4 HTTPS/TLS through TUN;
- active `doctor --tun` execution and bounded classification capture, including IPv6/leak and PMTU outcomes;
- NetworkManager postcondition after TUN disconnect;
- clean `recover --json` and exact owned-resource absence before guest destruction;
- public-artifact privacy/redaction boundary;
- QEMU availability;
- runner free-disk budget before/after VM preparation;
- `/dev/kvm` presence and actual usability, reported separately from QEMU software-emulation viability;
- checksum-verified official Ubuntu 24.04 VM image boot;
- SSH/control access to the VM through loopback-only host forwarding;
- full guest reboot with a changed boot ID;
- final outer-runner connectivity and infrastructure-plumbing cleanup.

The spike does not attempt the full permanent destructive matrix. Its purpose is to prove the two isolation mechanisms and one complete installed-package full-TUN path cheaply enough to choose permanent infrastructure from evidence rather than assumptions.

Required capabilities fail the spike explicitly. Optional accelerators and topology-dependent diagnostics are classified separately instead of being silently turned into passes or product failures.

## Workflow placement and cadence

Avoid adding many top-level workflows without need. Prefer the existing workflow ownership model after the spike:

- `ci.yml`: deterministic PR/master merge gate and secret-free synthetic qualification when runtime is suitable for PRs;
- `integration.yml`: trusted/master/manual real-provider and destructive integration, plus scheduled soak if concurrency semantics remain clear;
- `release.yml`: exact-tag qualification of the same built artifacts, reusing the same scenario entrypoints instead of release-only duplicates.

The temporary capability workflow may exist as its own draft-PR workflow because it is a feasibility instrument. It is removed or folded into the permanent structure after the capability decision.

If permanent VM/destructive orchestration makes `integration.yml` unreadable or creates conflicting trigger/concurrency semantics, one dedicated invariant-oriented hosted-E2E workflow is acceptable. It must replace complexity rather than duplicate equivalent jobs.

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

## Failure isolation and time/resource bounds

Every external command that can hang remains bounded by the scenario's existing timeout model or an equally strict replacement.

One scenario failure must not prevent the outer runner from staging bounded diagnostics. The outer controller is responsible for guest termination even when product cleanup inside the guest fails.

Guest destruction is not product success. Product cleanup assertions complete before guest destruction.

No permanent hosted job approaches the six-hour ceiling. The soak job is the only intentionally multi-hour job. VM images, sparse overlays, package build caches, private diagnostics, and artifacts are budgeted against the standard 14 GB SSD; disk exhaustion is a capability/infrastructure failure, never a product PASS.

## Secrets, logs, and artifacts

Existing private-output and redaction boundaries remain authoritative.

Additional guest orchestration ensures:

- profile/provider credentials never appear in public command logs or artifact names;
- raw guest journals, generated configs, host/provider addresses, and authority-bearing state stay in private temporary storage;
- only normalized/sanitized evidence is copied to public artifact staging;
- artifact upload remains gated by cleanup/redaction checks where existing scenarios require them;
- untrusted pull-request code cannot access real-provider Environment secrets;
- synthetic qualification uses ephemeral generated credentials and contains no long-lived secrets.

## Repository policy update

Only after capability evidence proves the isolation boundary, update canonical E2E policy so safety is expressed by mutation ownership rather than historical runner type:

- destructive Podlaz mutation of the outer GitHub Actions runner network is forbidden;
- exact, bounded infrastructure plumbing for disposable guests is allowed and is separately owned/cleaned by the harness;
- destructive Podlaz mutation is allowed inside a fresh disposable isolated guest whose lifecycle is controlled by the job;
- scenarios requiring an independent boot/kernel boundary use the full VM guest;
- physical/hardware-only acceptance remains outside hosted CI.

The contract preventing the retired persistent self-hosted runner dependency from returning remains valid.

## Explicitly out of scope

GitHub-hosted qualification is not represented as proof of hardware-specific laptop behavior such as:

- physical Wi-Fi driver/firmware behavior;
- real Wi-Fi roaming between access points;
- physical suspend/resume, lid, battery, or ACPI behavior;
- quirks of a specific user's router, NIC, kernel build, or laptop firmware.

Virtual uplink down/up, DHCP-style churn, daemon restart, guest reboot, and product revalidation remain in scope because they exercise product semantics independent of physical Wi-Fi hardware.

## Implementation sequence

1. Add the capability workflow, minimal guest/bootstrap code, and a throwaway synthetic Xray endpoint sufficient to prove full-TUN feasibility.
2. Run it on `ubuntu-24.04` and capture actual Actions evidence.
3. Review evidence and select the simplest guest mechanism that proves the required production interfaces.
4. Promote the proven synthetic topology into reusable secret-free Xray/TUN qualification.
5. Migrate existing destructive installed-package harnesses into isolated hosted jobs by invariant.
6. Add trusted real-provider TUN qualification.
7. Add full-VM reboot and exact historical package migration qualification.
8. Add native ARM64 installed/TUN coverage where capability permits.
9. Move the existing soak to scheduled disposable hosted execution.
10. Tighten release publication dependencies only after corresponding hosted jobs demonstrate stable evidence on master/manual runs.
11. Update canonical architecture/policy wording and remove temporary spec/plan artifacts before final merge-ready repository verification.

## Acceptance criteria

The work is complete when:

1. Pull requests have a secret-free real full-TUN data-plane check using the installed candidate package and real Linux TUN/network primitives.
2. Active hosted TUN evidence includes the product's relevant DNS/TCP/TLS/HTTPS/DoH/IPv6/PMTU diagnostics instead of a shallow link-only check.
3. A guest can lose routes, DNS, nftables correctness, Xray, or `podlazd` without losing outer Actions control or diagnostic collection.
4. Privacy Envelope/fail-closed scenarios prove blocked direct egress during protected degraded/recovery windows and ordinary connectivity only after terminal convergence.
5. NetworkManager/uplink churn, ordinary-user/polkit/socket, journal/privacy, and foreign-state-preservation contracts remain covered rather than being bypassed for CI convenience.
6. Existing coexistence, rollback, terminal recovery, restart, reconciliation, and package-lifecycle scenarios run on disposable GitHub-hosted infrastructure or have a concrete documented technical reason they cannot.
7. Real-provider proxy and TUN qualification are separate trusted jobs.
8. Exact `v0.2.40 -> candidate` package-restart acceptance runs on a disposable hosted guest and preserves its exact provenance semantics.
9. At least one full-VM job proves a real guest reboot and boot-ID change, then exercises Podlaz boot/recovery semantics.
10. Native ARM64 package/runtime qualification exists and TUN coverage is enabled to the maximum proven runner capability without assuming unsupported nested virtualization.
11. The three-hour resource soak can run on schedule inside the six-hour hosted limit with safe artifact handling.
12. Release publication depends only on reproducible GitHub-hosted qualification plus exact build artifacts; no persistent self-hosted runner is required.
13. Destructive Podlaz host-network mutation never targets the outer GitHub Actions runner namespace.
14. VM/image/caches fit a bounded standard-runner disk budget and verified image provenance; disk exhaustion cannot be mistaken for product evidence.
15. Hardware-specific gaps are explicitly bounded rather than represented as covered.
