# Automated qualification architecture

## Goal

Automate the maximum practical Podlaz qualification surface so routine development and release confidence no longer depend on manually exercising the VPN client on a developer laptop.

The target is not to emulate every laptop, ISP, Wi-Fi chipset, firmware, or physical network. The target is that every defined Podlaz acceptance scenario runs automatically at the strongest practical evidence level, with physical hardware used only where virtualization or network simulation cannot establish the intended invariant.

The qualification system must remain subordinate to the product:

- the test environment adapts to Podlaz;
- Podlaz does not gain CI-specific product behavior, public API, runtime mode, retry policy, authorization bypass, or network semantics;
- product code changes are justified only by a separately demonstrated product defect or an explicitly approved product requirement;
- test-only topology, authorization fixtures, synthetic endpoints, fault injection, and hardware control remain outside production behavior.

This design builds on the evidence gathered by PR #326 (`agent/github-hosted-e2e-qualification`). PR #326 remains a capability spike. It is not the permanent E2E architecture and should stop once the hosted isolation mechanism and one complete installed-package synthetic full-TUN lifecycle are conclusively demonstrated or a specific hosted limitation is established.

## Success criteria

The architecture is successful when:

1. normal product changes receive deterministic package, lifecycle, authorization, TUN, DNS, TLS/HTTPS, recovery, cleanup, and privacy qualification without a developer laptop;
2. destructive networking runs only inside disposable guests or explicitly managed hardware-under-test, never in the GitHub Actions runner control-plane namespace;
3. reboot, suspend/resume, uplink transitions, DHCP/DNS changes, IPv4/IPv6 edge cases, captive-portal-like networks, PMTU/MTU constraints, packet loss, delay, and selected Wi-Fi semantics are automated at an appropriate evidence level;
4. hardware-dependent scenarios can run unattended on a small optional hardware-in-the-loop lab;
5. failures identify one scenario/failure domain instead of being hidden inside one monolithic job;
6. release policy depends only on qualification layers that have accumulated sufficient reliability evidence;
7. temporary qualification machinery does not expand product scope.

## Non-goals

This work does not:

- add VPN protocols, transports, user-facing commands, recovery modes, observability APIs, debug modes, or other product features merely to improve testability;
- restore the retired persistent `self-hosted`/`vpn-e2e` runner dependency;
- require physical hardware for tests that can be proven faithfully in a disposable guest;
- make every topology or expensive scenario a pull-request gate;
- treat mocks, source-string contracts, guest destruction, or test-side repair as substitutes for real product convergence;
- claim coverage of arbitrary vendor firmware, every ISP, every captive portal implementation, or every consumer router.

## Core principles

### Production-shaped execution

Where a scenario is intended to prove installed product behavior, it uses the candidate `.deb`, packaged daemon/service/policy integration, packaged Xray, ordinary-user CLI, and production socket/API/state/network paths.

Test code may provision the environment around Podlaz, but must not bypass the product path under test. In particular:

- ordinary-user scenarios do not gain the private `podlaz` service group merely to avoid the packaged transport/authorization path;
- a temporary polkit fixture may authorize the exact action required by a headless scenario, but must not broaden unrelated actions;
- test-side code must not add, delete, or repair Podlaz-owned routes, rules, DNS, nftables state, TUN devices, transactions, or Network Session state in order to make convergence pass.

### Strongest practical evidence

Each scenario declares what class of evidence it provides. A lower evidence class must never be presented as equivalent to a higher one.

Evidence classes are:

1. **Deterministic contract evidence** — pure/unit/contract behavior with no privileged network mutation.
2. **Namespace/system-guest evidence** — real Linux networking and packaged execution while sharing the outer kernel.
3. **Full-VM evidence** — independent kernel, boot identity, suspend/reboot boundary, and guest device lifecycle.
4. **External-network evidence** — traffic crosses a real public network path to an independently controlled endpoint.
5. **Physical-HIL evidence** — real NIC/firmware/power-management/AP behavior on hardware-under-test.

A test report and its name should make the evidence class clear where ambiguity would matter.

### Exact cleanup before environment destruction

Disposable infrastructure is not cleanup evidence.

Before a guest or hardware test environment is reset, scenarios that mutate Podlaz networking must prove terminal convergence through product-visible and exact owned-resource assertions appropriate to that scenario. This includes the required subset of:

- TUN link absence or tracked Xray termination;
- exact address/route/rule cleanup;
- exact resolved link ownership cleanup;
- exact nftables/Privacy Envelope cleanup;
- transaction cleanup;
- Network Session/current-boot authority cleanup;
- NetworkManager postconditions;
- clean recovery classification;
- restored ordinary connectivity when the scenario expects it.

Infrastructure destruction is only the final safety net after product cleanup has been evaluated.

### One failure domain per scenario

Permanent destructive qualification is split into small scenario jobs. A job should answer one principal question, such as normal TUN lifecycle, NetworkManager reconciliation, package replacement, boot continuation, or suspend/resume recovery.

Shared bootstrap and evidence mechanics may be consolidated only when multiple permanent scenarios have the same semantics. Scenario-specific predicates and cleanup remain local when their meaning differs.

### Evidence before gating

A new qualification layer starts as informational/manual-dispatch or scheduled evidence. It becomes a pull-request or release gate only after enough successful runs show that:

- the topology is reproducible;
- failures are diagnostically useful;
- infrastructure noise is acceptably low;
- runtime/cost fits the intended cadence;
- the scenario is checking product behavior rather than properties of the harness itself.

## Architecture overview

The permanent model is layered rather than one large workflow.

```text
                     source change
                          |
          +---------------+---------------+
          |                               |
   deterministic CI                package candidate
          |                               |
          +---------------+---------------+
                          |
                  hosted qualification
                          |
          +---------------+-------------------+
          |                                   |
   system-guest TUN                     full-VM lifecycle
          |                                   |
   synthetic topology              boot/reboot/suspend/device
          |                                   |
          +----------------+------------------+
                           |
                  scheduled deep matrix
                           |
             external-network qualification
                           |
                  optional physical HIL
```

The layers complement rather than replace one another. A full-VM test does not remove the need for fast deterministic tests; physical HIL does not replace deterministic fault injection.

## Layer 1: deterministic CI

### Purpose

Protect fast, reproducible behavior and packaging contracts on every relevant pull request.

### Scope

Keep the current deterministic surface, including the applicable repository checks, Go tests, race-sensitive tests, vet/vulnerability checks, CLI/API/state contracts, package build/validation, service/package lifecycle checks, and non-destructive privileged kernel contracts already proven safe on the hosted runner.

### Constraints

- secret-free;
- no Podlaz full-TUN mutation of the Actions runner network namespace;
- short enough to remain the primary merge feedback loop;
- failures are treated as code/package failures, not environmental capability failures.

## Layer 2: hosted synthetic system-guest qualification

### Purpose

Provide the main real VPN acceptance signal without a persistent runner or provider secret.

### Environment

A fresh standard GitHub-hosted Ubuntu job acts only as the outer controller. A disposable system guest, initially the mechanism proven by #326, owns destructive Podlaz networking.

The outer controller may create exact infrastructure-owned connectivity plumbing for the guest. It must not run Podlaz TUN in its own network namespace.

### Canonical synthetic lifecycle

The first permanent scenario is deliberately narrow:

1. build the exact candidate `.deb` once;
2. create the disposable guest;
3. establish guest NetworkManager/systemd-resolved Internet connectivity;
4. install the exact candidate and prove runtime provenance;
5. prove the ordinary-user packaged boundary;
6. start one job-local synthetic Xray-compatible endpoint outside the client guest TUN;
7. import/validate the synthetic profile through public product interfaces;
8. connect through the ordinary-user production transport/authorization path;
9. require verified-active typed product state;
10. prove the expected TUN/address/routes/rules/resolved/nftables authority;
11. prove real system DNS and IPv4 TCP/TLS/HTTPS while active;
12. run `doctor --tun` and retain bounded classifications rather than coercing topology-dependent results into success;
13. disconnect normally;
14. prove exact terminal cleanup and NetworkManager postconditions;
15. require clean recovery output;
16. prove ordinary guest connectivity is restored;
17. prove outer-runner health/cleanup;
18. destroy the guest.

This scenario must remain small. Additional protocols or transports are added only when they protect a distinct product compatibility contract.

### Pull-request cadence

After the mechanism has accumulated stable evidence, the canonical synthetic lifecycle is a candidate for ordinary pull-request qualification because it is secret-free and directly exercises the installed product.

If runtime remains too expensive for every pull request, it may run only when relevant package/networking paths change plus on scheduled full coverage. The selection mechanism must fail safely and never silently omit a required release qualification.

## Layer 3: deterministic network and lifecycle fault matrix

### Purpose

Replace repetitive manual laptop/network testing with reproducible software-controlled adverse conditions.

### Topology controls

Use guest-local or infrastructure-owned Linux networking mechanisms rather than product-specific hooks. Applicable mechanisms include network namespaces, veth/tap devices, NetworkManager, DHCP/DNS fixtures, nftables, `tc netem`, and full-VM virtual devices.

### Scenario families

The permanent matrix may include, after each family has a concrete product invariant:

- uplink down/up;
- DHCP renewal and address change;
- gateway replacement;
- DNS server replacement or temporary DNS failure;
- UDP/53 blocked while TCP/53 remains available;
- TCP/53 blocked;
- broader UDP restriction where meaningful to the supported data plane;
- packet delay, jitter, loss, duplication, and reorder;
- constrained MTU and topology-aware PMTU cases;
- IPv4-only;
- IPv6-only where the product contract supports meaningful operation;
- dual-stack with unusable or blackholed IPv6;
- IPv6 route/leak conditions;
- captive-portal-like pre-authentication network followed by restored Internet;
- NetworkManager connection replacement/revalidation;
- Xray crash/restart;
- daemon restart/crash;
- interrupted replacement/rollback/recovery;
- stale TUN/resolver/network state where existing product recovery semantics define an expected result.

### Captive portal model

The automated captive-portal scenario models network semantics, not a particular hotel login UI. For example:

- DHCP and local DNS are available;
- unrestricted Internet is unavailable or redirected according to the selected fixture;
- VPN connection behavior is observed without test-side repair;
- the fixture transitions to normal Internet availability;
- Podlaz convergence and leak protection are then verified.

The test must not claim coverage of arbitrary captive portal implementations.

### Fault-injection discipline

Faults are injected into infrastructure or externally controlled processes. A fault must not mutate authority-bearing Podlaz state directly unless the scenario explicitly models storage corruption and the product contract defines behavior for it.

Each scenario has a bounded trigger, expected lifecycle transition, and terminal convergence assertion. Arbitrary sleeps are replaced by condition-based waits where observable state exists.

## Layer 4: full-VM qualification

### Purpose

Prove semantics weakened by a shared kernel or namespace-only system guest.

### Baseline environment

Use an official Ubuntu image with checksum verification, a sparse disposable overlay, bounded logs, and loopback-only host control/SSH forwarding. KVM is an optional accelerator; software emulation remains an evidence path only while it fits the resource/time budget.

### Scenario families

Full-VM qualification is appropriate for:

- real boot and boot-ID change;
- boot-autostart and boot-scoped authority;
- package replacement/upgrade across service restarts;
- reboot during or after recovery-relevant state;
- suspend/resume where the guest/kernel/device lifecycle adds evidence;
- virtual NIC detach/attach;
- interface replacement or link identity change;
- transitions between independently modelled uplinks;
- tests where a shared host kernel would weaken the claimed result.

### Suspend/resume

Suspend/resume qualification must prove an actual guest suspend state rather than merely stopping Podlaz processes. The environment controls suspend/wakeup, while the product is observed through normal lifecycle/status/network interfaces before and after resume.

Expected product behavior is taken from existing architecture/CLI semantics. The test does not invent reconnect behavior solely because a VM makes a particular transition convenient.

## Layer 5: automated Wi-Fi semantics

### Purpose

Cover Wi-Fi-related network lifecycle behavior without requiring a human-operated laptop for every run.

### Simulated Wi-Fi

Where supported by the hosted/full-VM environment, Linux virtual Wi-Fi facilities such as `mac80211_hwsim` plus normal `hostapd`/NetworkManager components may provide reproducible Wi-Fi semantics.

Useful scenarios include:

- association to one AP;
- AP disappearance;
- reassociation to another AP;
- same-SSID/different-BSSID transitions;
- network identity/gateway/DHCP changes after roaming;
- loss/restoration of Internet during the transition;
- Podlaz revalidation, protection, and final convergence.

This evidence is classified as simulated Wi-Fi. It does not claim to prove a vendor Wi-Fi driver or firmware implementation.

### Relationship to physical HIL

Simulated Wi-Fi should cover product-level NetworkManager/uplink behavior broadly and deterministically. Physical HIL is reserved for questions where real radio/NIC/firmware/power-management behavior is the subject of the evidence.

## Layer 6: external-network qualification

### Purpose

Add a small amount of real public-network diversity without using a developer laptop or requiring a persistent VPN provider account for every test.

### Model

Scheduled or pre-release jobs may create or use short-lived independently controlled Internet endpoints in one or more regions/providers. Podlaz still runs in the disposable client guest; external nodes provide observation/endpoint services only.

Possible evidence includes:

- real public IPv4 reachability;
- real public IPv6 reachability where available;
- DNS/TLS/HTTPS over a non-local synthetic path;
- PMTU behavior across a real routed network;
- observed egress or source identity where deterministic and privacy-safe;
- comparison of provider/region path behavior without embedding user data.

### Cost and provider policy

This layer is optional for normal pull requests. It should prefer free allowances or low-cost ephemeral resources and enforce explicit runtime/resource budgets.

The architecture is provider-neutral. No single cloud provider becomes a permanent product dependency.

Credentials for infrastructure provisioning are restricted to trusted scheduled/manual/release contexts and never exposed to untrusted pull-request code.

## Layer 7: physical hardware-in-the-loop

### Purpose

Eliminate the remaining manual hardware smoke tests for scenarios that genuinely depend on physical devices.

### Scope boundary

HIL is planned now but implemented only after the hosted/system-guest/full-VM layers are stable. It must not become a prerequisite for finishing the hosted qualification work.

Physical HIL is appropriate for:

- real Wi-Fi NIC/driver/firmware behavior;
- real AP roaming behavior not adequately represented by simulation;
- hardware suspend/resume and ACPI/firmware interaction;
- real Ethernet/Wi-Fi device transition where driver/device behavior matters;
- selected long-running release qualification on one or more representative Linux systems.

### Suggested topology

A minimal lab consists of:

- one Linux device-under-test running the candidate Podlaz package;
- one separate controller that remains reachable when the DUT network is intentionally broken;
- one or two independently controllable APs, preferably devices running an automation-friendly firmware such as OpenWrt;
- independent power/reset/wakeup control where required;
- a management path distinct from the network path being tested.

The controller may use an HIL framework or simple explicit tooling, but the DUT must be disposable/recoverable without manual intervention.

### No return to the retired runner dependency

The hardware lab is not the old persistent self-hosted release runner under another name.

Initially it is an optional/scheduled evidence source. GitHub-hosted deterministic and guest qualification remain independently usable when the lab is offline.

Only after sustained reliability evidence may selected HIL scenarios become release requirements, and even then the release policy must distinguish `product failed` from `lab unavailable`.

## Cadence model

A default target cadence is:

| Layer | Default cadence | Initial gating role |
| --- | --- | --- |
| Deterministic CI | every PR | required |
| Canonical hosted synthetic TUN | every relevant PR or every PR if runtime permits | informational until stable, then required |
| Network/fault matrix | scheduled + targeted PRs | informational initially |
| Full-VM boot/reboot/suspend | scheduled + relevant PRs | informational initially |
| Simulated Wi-Fi | scheduled + relevant networking PRs | informational initially |
| External-network qualification | scheduled/pre-release | informational |
| Physical HIL | scheduled/pre-release | informational until proven reliable |

No scenario becomes a gate merely because it exists.

## Failure classification

The qualification system should distinguish at least:

- `product_failure` — the candidate violated a defined product invariant;
- `fixture_failure` — guest/topology/test fixture failed before valid product evidence could be obtained;
- `capability_unavailable` — the runner/environment cannot provide an optional capability;
- `infrastructure_unavailable` — external service/HIL/control plane could not be used;
- `diagnostic_unknown` — a topology-dependent diagnostic ran but could not establish a supported classification.

Required scenarios fail closed when product evidence cannot be established. However, an infrastructure outage must not be mislabeled as a product regression.

Normalized public reports expose classifications and bounded safe metadata, not raw profiles, provider material, private endpoint data, authority-bearing state, or unredacted journals.

## Test-integrity rules

A permanent E2E scenario is valid only when all relevant rules below hold:

- candidate package provenance is exact;
- the product is exercised through the intended public/packaged interface;
- the user/authorization identity matches the scenario contract;
- fixture actions are clearly separated from Podlaz-owned mutations;
- no test process repairs Podlaz-owned state between the action and assertion;
- active success means verified product state plus required real traffic evidence, not merely process/link presence;
- cleanup success is established before guest reset/destruction;
- optional/topology-dependent diagnostic outcomes are not silently promoted to PASS;
- a source-contract test may protect harness structure but is not counted as runtime E2E evidence;
- a retry is allowed only for a demonstrated transient/environmental condition and must not hide deterministic product failure.

## Artifact and privacy model

Private working state may include raw logs needed for diagnosis while a trusted job is running, but it remains in restricted temporary storage and is deleted before normal completion.

Public artifacts contain only bounded normalized evidence or explicitly redaction-scanned diagnostics. Generated synthetic credentials are ephemeral and never printed. Real-provider or infrastructure credentials never enter public artifacts.

If a failure requires richer forensic evidence, the preferred progression is:

1. add the smallest privacy-safe discriminator needed to separate hypotheses;
2. run it once;
3. remove or simplify temporary forensic instrumentation after the root cause is known;
4. keep only diagnostics that protect a recurring failure mode or durable invariant.

The workflow must not accumulate indefinite diagnostic sidecars around a solved spike.

## Product-change policy discovered by E2E

When automated qualification exposes unexpected behavior, the investigation first determines whether the failure belongs to:

- the product;
- the test fixture/topology;
- the hosted/runtime environment;
- an unsupported product contract.

A product modification is allowed only after a product defect is demonstrated independently of test convenience. The fix then follows normal TDD and compatibility rules and should be reviewable separately from test-environment adaptations where practical.

No production behavior is changed solely to make a hosted runner, VM, simulated AP, or HIL controller easier to operate.

## Relationship to PR #326

PR #326 is retained as a capability investigation, not promoted wholesale into permanent architecture.

Before the spike is considered complete:

- stop adding broad diagnostic instrumentation;
- resolve the current synthetic full-TUN blocker through a minimal root-cause experiment;
- preserve ordinary-user production transport/authorization semantics;
- make the primary capability harness own its guest staging rather than depending on workflow-side repair;
- obtain one conclusive full-TUN result or document a specific hosted limitation;
- retain the already established system-guest/QEMU capability evidence without repeatedly re-investigating it.

After the spike:

- temporary forensic watchers/classifiers that do not protect a durable invariant are removed;
- permanent qualification is implemented from the proven topology in smaller behavior-oriented scenarios;
- the spike workflow itself is not automatically made a release gate.

## Delivery sequence

Implementation should be decomposed into independent work items rather than one long-lived feature branch.

### Phase 0: finish capability proof

Finish only the bounded remaining work in #326. Do not add the permanent fault matrix or HIL scope to that PR.

### Phase 1: canonical hosted synthetic TUN

Create the small permanent system-guest lifecycle scenario and stabilize it as the primary real hosted VPN signal.

### Phase 2: network fault/topology matrix

Add deterministic adverse-network scenarios only when each maps to an existing product invariant.

### Phase 3: full-VM lifecycle

Promote the proven QEMU mechanism into boot/reboot/package/continuation scenarios, then add suspend/resume and virtual-device lifecycle where the evidence justifies it.

### Phase 4: simulated Wi-Fi

Add automated AP/roaming semantics if the selected environment supports faithful Linux Wi-Fi simulation without disproportionate harness complexity.

### Phase 5: external-network qualification

Add bounded scheduled real-Internet diversity only where it gives evidence not already established by local synthetic topology.

### Phase 6: physical HIL

Build the smallest unattended physical lab that closes the remaining hardware-specific evidence gaps.

### Phase 7: release-policy integration

After collecting stability/runtime/flakiness evidence, decide which scenarios become merge gates, pre-release gates, scheduled diagnostics, or remain informational.

## Issue decomposition

After this design is approved, create one umbrella issue describing the automation goal and separate implementation issues roughly along these boundaries:

1. finish #326 capability proof;
2. permanent hosted synthetic full-TUN qualification;
3. deterministic network fault/topology matrix;
4. full-VM boot/reboot/suspend/device qualification;
5. simulated Wi-Fi qualification;
6. external-network qualification;
7. physical HIL lab;
8. release-policy integration after stability evidence.

The issues should reference this design while it is active instead of duplicating its complete technical prose.

## Permanent repository impact

This design file is temporary planning material under `docs/superpowers/**`.

When the architecture is implemented and stabilized:

- durable E2E ownership/isolation/evidence invariants are folded concisely into the canonical E2E section of `ARCHITECTURE.md`;
- public user behavior remains in `docs/cli.md` only when product behavior actually changes;
- executable setup and assertions remain in `scripts/**` and `.github/workflows/**` rather than being duplicated as permanent prose;
- temporary spec/plan files are removed before final repository-structure completion as required by repository policy.

## Completion definition

The overall initiative is complete when the repository has an automated, layered qualification path that covers all defined software/network/lifecycle acceptance scenarios without a developer-operated laptop, and any remaining unautomated scenario is explicitly documented as a hardware capability gap with either an HIL plan or a deliberate acceptance decision.

The stronger long-term target is that release acceptance requires no manual interaction: humans review evidence and make release decisions, while scenario execution itself is automated.