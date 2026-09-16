# Automated qualification architecture

## Goal

Automate the maximum practical Podlaz qualification surface so routine development and release acceptance no longer require manually exercising the VPN client on a developer laptop.

The target is not to emulate every laptop, ISP, Wi-Fi chipset, firmware, or consumer router. The target is that every accepted Podlaz invariant is either:

- exercised automatically at the strongest practical evidence level; or
- explicitly recorded as an uncovered hardware/platform risk rather than converted into a manual release procedure.

The qualification system remains subordinate to the product:

- the test environment adapts to Podlaz;
- Podlaz does not gain CI-specific product behavior, public API, runtime mode, retry policy, authorization bypass, or network semantics;
- product code changes are justified only by a separately demonstrated product defect or an explicitly approved product requirement;
- test-only topology, authorization fixtures, synthetic endpoints, fault injection, and hardware control remain outside production behavior.

PR #326 (`agent/github-hosted-e2e-qualification`) remains a capability spike. It is not the permanent E2E architecture and should stop once the hosted isolation mechanism and one complete installed-package synthetic full-TUN lifecycle are conclusively demonstrated, or a specific hosted limitation is established.

## Success criteria

The architecture is successful when:

1. normal changes receive automated package, authorization, TUN, DNS/TLS/HTTPS, cleanup, recovery, privacy, and lifecycle qualification without a developer laptop;
2. destructive networking runs only inside disposable guests or explicitly managed hardware-under-test, never in the GitHub Actions runner control-plane namespace;
3. reboot, suspend/resume, uplink changes, adverse network conditions, relevant IPv4/IPv6 behavior, and supported Wi-Fi lifecycle semantics are automated where they protect an existing product invariant;
4. trusted real-provider qualification remains available separately from synthetic qualification;
5. hardware-dependent scenarios can run unattended through optional physical HIL when virtualization cannot establish the intended invariant;
6. each failure has a clear scenario and failure domain;
7. release policy depends only on qualification that has accumulated sufficient reliability evidence;
8. routine and release acceptance require no manual scenario execution.

## Non-goals

This work does not:

- add VPN protocols, transports, commands, recovery modes, observability APIs, debug modes, or other product features merely to improve testability;
- restore the retired persistent `self-hosted`/`vpn-e2e` runner dependency;
- require physical hardware for behavior that can be proven faithfully in a disposable guest;
- make every topology or expensive scenario a pull-request gate;
- treat mocks, source-string contracts, guest destruction, or test-side repair as substitutes for product convergence;
- promise coverage of arbitrary vendor firmware, every ISP, every captive portal, or every consumer router.

## Core principles

### Production-shaped execution

Installed-product scenarios use the exact candidate `.deb`, packaged daemon/service/policy integration, packaged Xray, ordinary-user CLI, and production socket/API/state/network paths.

Test code may provision the environment around Podlaz, but must not bypass the product path under test. In particular:

- ordinary-user scenarios do not gain the private `podlaz` group merely to avoid the packaged transport/authorization path;
- a temporary headless polkit fixture may authorize the exact action required by a scenario, but must not broaden unrelated actions;
- test-side code must not add, delete, or repair Podlaz-owned routes, rules, DNS, nftables state, TUN devices, transactions, or Network Session state to make convergence pass.

### Exact candidate and release provenance

Every installed-product scenario proves that the running package/runtime matches the candidate under test.

Release qualification should build once and qualify the exact artifact digest that will be published. Rebuilding equivalent-looking packages independently for different release checks is weaker evidence and should be avoided where the release workflow can pass the same artifact forward.

### Strongest practical evidence

Evidence environments are ordered by what they can prove, not by perceived test importance:

1. **Deterministic CI** — pure/unit/contract/package behavior without destructive VPN mutation.
2. **System guest** — real Linux networking and packaged execution in an isolated namespace while sharing the outer kernel.
3. **Full VM** — independent kernel, boot identity, suspend/reboot boundary, and virtual device lifecycle.
4. **External network** — traffic crosses a real public network path to an independently controlled or trusted provider endpoint.
5. **Physical HIL** — real NIC/firmware/power-management/AP behavior on hardware-under-test.

A lower evidence class is not reported as equivalent to a higher one.

### Exact cleanup before environment destruction

Disposable infrastructure is not cleanup evidence.

Before a guest or hardware environment is reset, destructive scenarios prove the required subset of exact terminal state:

- tracked Xray/TUN lifecycle convergence;
- exact address/route/rule cleanup;
- exact resolved ownership cleanup;
- exact nftables/Privacy Envelope cleanup;
- transaction and Network Session authority cleanup;
- NetworkManager postconditions;
- clean recovery classification;
- restored ordinary connectivity when expected;
- preservation of foreign network state.

Guest destruction is only a final infrastructure safety net.

### Fail-closed and negative behavior matter

Permanent qualification must prove negative safety properties as well as successful connectivity. Where the product contract requires them, scenarios explicitly cover:

- ordinary direct egress remaining blocked while protection is intentionally armed;
- no unprotected gap during replacement/recovery transitions;
- foreign routes/rules/DNS/firewall state remaining unchanged;
- ambiguous cleanup remaining fail-closed rather than becoming broad deletion authority.

### Thin workflow, scenario-owned lifecycle

GitHub workflow YAML owns checkout, build/artifact transfer, environment provisioning, bounded invocation, and artifact publication.

The executable scenario owns its internal lifecycle and assertions. Permanent workflows must not become a second runtime implementation that polls guest-private files, repairs staging, or mutates product state in parallel with the scenario.

Shared E2E helpers are introduced only for mechanics with at least two real consumers and compatible semantics.

### Evidence before gating

A new scenario starts informational, scheduled, or manual-dispatch unless an existing release contract already requires equivalent evidence. It becomes a merge/release gate only after sufficient runs show that the topology is reproducible, diagnostically useful, acceptably stable, and fits the intended runtime/cost budget.

## Qualification model

The architecture separates **where evidence is obtained** from **which product invariant is being tested**.

A scenario chooses the cheapest evidence environment strong enough to prove its invariant. Moving a scenario to a stronger environment is justified only when the weaker one materially reduces evidence.

### Acceptance scenario families

The permanent qualification surface is organized around existing or explicitly approved product invariants, not around CI features.

#### 1. Package and ordinary-user boundary

Covers exact package/runtime provenance, service lifecycle, filesystem/abstract socket behavior, polkit classifications, packaged Xray supervision, install/reinstall/purge semantics, and architecture-specific package execution.

#### 2. Canonical synthetic full-TUN lifecycle

The primary secret-free real VPN acceptance scenario:

1. create an isolated client guest with NetworkManager/systemd-resolved and Internet access;
2. install and verify the exact candidate;
3. start one job-local synthetic Xray-compatible endpoint outside the client TUN;
4. import/validate through public product interfaces;
5. connect as an ordinary user through the production authorization/transport path;
6. require verified-active typed product state;
7. prove exact TUN/address/routes/rules/resolved/nftables authority;
8. prove real system DNS and IPv4 TCP/TLS/HTTPS while active;
9. run `doctor --tun` and retain bounded topology-aware classifications;
10. disconnect normally;
11. prove exact cleanup, NetworkManager postconditions, clean recovery, restored guest connectivity, and preserved foreign state;
12. prove outer-runner health before destroying the guest.

Additional protocols/transports are added only when they protect a distinct supported compatibility contract.

#### 3. Privacy, recovery, and fault behavior

Migrates existing rollback, terminal recovery, Privacy Envelope, coexistence, reconciliation, stale-link/resolver, daemon/Xray restart/crash, and related acceptance semantics into isolated scenarios.

Infrastructure-controlled faults may include bounded uplink, DHCP/DNS, routing, IPv4/IPv6, MTU/PMTU, captive-network, and `tc netem` conditions only when each maps to a concrete Podlaz invariant. The matrix is not expanded merely because a fault injector supports another mode.

#### 4. Boot, reboot, suspend, and device lifecycle

Uses a full VM when an independent kernel or real boot identity materially strengthens evidence. Covers applicable boot-autostart/continuation, real reboot and boot-ID change, suspend/resume, and virtual NIC/uplink replacement semantics.

Suspend/resume must prove an actual guest suspend state, not merely stopped Podlaz processes.

#### 5. Historical package upgrade and continuation

Qualifies explicitly supported historical compatibility boundaries, not every past release. The scenario starts with a pinned historical package/runtime state, replaces it with the exact candidate, and proves service/runtime provenance, continuation/recovery semantics, privacy behavior, foreign-state preservation, and terminal convergence.

#### 6. Trusted real-provider qualification

Synthetic qualification does not replace provider compatibility evidence.

Trusted scheduled/pre-release/release contexts may exercise the existing real-provider boundary with secrets unavailable to untrusted pull-request code. Proxy and TUN evidence remain distinguishable so a provider-reachability failure and a host-network/TUN failure do not collapse into one signal.

Real-provider TUN qualification uses an isolated client guest and requires the same production-shaped authorization, active traffic, cleanup, recovery, privacy, and artifact rules as synthetic TUN where applicable.

#### 7. Wi-Fi lifecycle semantics

Product-level NetworkManager/uplink behavior may be covered with reproducible Linux Wi-Fi simulation when the selected environment supports it faithfully. Candidate scenarios include AP loss/reassociation, same-SSID/different-BSSID transition, and resulting gateway/DHCP/Internet changes.

Simulation is reported as simulated Wi-Fi evidence and does not claim vendor driver/firmware coverage. Real radio/NIC/firmware behavior belongs to physical HIL.

#### 8. Resource soak

Existing resource-soak intent remains part of automated qualification. Soak runs on a scheduled/manual cadence with an explicit runtime budget and measures durable resource/lifecycle behavior without becoming an ordinary PR gate.

#### 9. External-network diversity

Where local synthetic topology cannot establish the intended invariant, scheduled/pre-release jobs may use short-lived independently controlled Internet endpoints for bounded IPv4/IPv6, TLS/DNS, PMTU, or route-path evidence.

This layer is provider-neutral and optional for normal pull requests.

#### 10. Physical hardware-in-the-loop

Physical HIL closes only evidence gaps that genuinely depend on hardware, such as real Wi-Fi NIC/driver/firmware behavior, hardware suspend/resume, AP roaming not represented adequately by simulation, or real Ethernet/Wi-Fi device transitions.

A minimal lab has a separate controller, a recoverable Linux DUT, independently controllable AP/network equipment where needed, and a management path that remains reachable when the DUT data path is intentionally broken.

HIL starts as optional/scheduled evidence and must distinguish `product_failure` from `lab unavailable`. It is not a return to the retired mandatory self-hosted runner model.

## Acceptance inventory and migration traceability

Before permanent migration is declared complete, the repository must maintain a temporary migration inventory covering the current acceptance surface.

For every existing E2E/release-laptop invariant, record at least:

- protected invariant;
- current executable/manual evidence;
- target scenario family;
- target evidence environment;
- intended cadence/gating role;
- migration status;
- whether the old harness is retained, superseded, or intentionally removed.

This prevents the initiative from declaring success by simply omitting an existing acceptance requirement.

The current `release-laptop.sh` is specifically treated as a migration source. Its required upgrade, soak, Wi-Fi reconnect, suspend/resume, reboot, privacy, recovery, and related release evidence may stop being a manual release surface only after each required invariant has automated replacement evidence or an explicit decision removes that product requirement.

## Architecture coverage

### amd64

`amd64` is the primary deepest qualification architecture and receives the maximum proven system-guest, full-VM, recovery/upgrade, and scheduled deep coverage.

### arm64

`arm64` receives the maximum native qualification supported by the current product and GitHub-hosted environment: package/runtime/service coverage and networking/TUN coverage where the chosen isolation mechanism is available.

Missing nested/full-VM capability on ARM is an environment limitation, not a product failure. This architecture matrix does not expand the set of supported product platforms.

## Cadence and cost policy

Core qualification is **free-first**: use standard GitHub-hosted infrastructure for the maximum practical deterministic, system-guest, and VM surface.

Paid or externally provisioned infrastructure is introduced only when free hosted infrastructure cannot establish a useful invariant. Such infrastructure must be optional outside its trusted cadence, ephemeral where practical, automatically torn down, and bounded by an explicit cost/runtime budget before it becomes a recurring workflow dependency.

A default cadence is:

| Scenario class | Default cadence | Initial gating role |
| --- | --- | --- |
| Deterministic CI | every PR | required |
| Canonical synthetic TUN | every relevant PR or every PR if runtime permits | informational until stable, then candidate for required |
| Privacy/recovery/fault scenarios | targeted PRs + scheduled | informational initially |
| Full-VM lifecycle / historical upgrade | relevant PRs + scheduled | informational initially |
| Simulated Wi-Fi | relevant networking PRs + scheduled | informational initially |
| Real-provider | trusted scheduled/pre-release/release | preserve existing required release evidence where applicable |
| Resource soak | scheduled/manual | informational |
| External-network diversity | scheduled/pre-release | informational |
| Physical HIL | scheduled/pre-release | informational until proven reliable |

No new scenario becomes a gate merely because it exists.

## Trust and secret boundary

Secret-free destructive guest qualification may run for ordinary pull requests because Podlaz never mutates the outer runner network namespace.

Real-provider credentials, cloud-provisioning credentials, and HIL control credentials are available only to trusted refs/events/environments. Untrusted pull-request code must never receive them.

## Failure and evidence classification

The qualification system distinguishes at least:

- `product_failure` — the candidate violated a defined product invariant;
- `fixture_failure` — guest/topology/test fixture failed before valid product evidence could be obtained;
- `capability_unavailable` — the runner/environment cannot provide an optional capability;
- `infrastructure_unavailable` — external service/HIL/control plane could not be used;
- `diagnostic_unknown` — a topology-dependent diagnostic executed but could not establish a supported classification.

Required scenarios fail closed when required product evidence cannot be established, but an infrastructure outage is not mislabeled as a product regression.

Public artifacts contain bounded normalized evidence or explicitly redaction-scanned diagnostics. Generated synthetic credentials are ephemeral and never printed; real-provider/infrastructure credentials never enter public artifacts.

When richer failure evidence is needed, add the smallest privacy-safe discriminator that separates hypotheses, run it, then remove or simplify temporary forensic instrumentation after the root cause is known. Permanent workflows must not accumulate diagnostic sidecars around solved spike failures.

## Product-change policy discovered by E2E

When qualification exposes unexpected behavior, first classify the failure as product, fixture/topology, hosted/runtime environment, or unsupported contract.

A product change is allowed only after a product defect is demonstrated independently of test convenience. The fix then follows normal TDD/compatibility rules and should be reviewable separately from test-environment adaptations where practical.

No production behavior changes solely to make a hosted runner, VM, simulated AP, provider fixture, or HIL controller easier to operate.

## Relationship to PR #326

PR #326 remains a bounded capability investigation and is not promoted wholesale into permanent architecture.

Before the spike is considered complete:

- stop adding broad diagnostic instrumentation;
- resolve the current synthetic full-TUN blocker through a minimal root-cause experiment;
- preserve ordinary-user production transport/authorization semantics;
- make the primary capability harness own its guest staging instead of depending on workflow-side repair;
- obtain one conclusive full-TUN result or document a specific hosted limitation;
- retain already established system-guest/QEMU capability evidence without repeatedly re-investigating it.

After the spike, remove temporary forensic watchers/classifiers that do not protect a durable invariant and implement permanent qualification as smaller behavior-oriented scenarios.

## Delivery sequence

Implementation should use separate bounded work items rather than another long-lived all-in-one feature branch:

1. finish #326 capability proof;
2. inventory current E2E and `release-laptop` acceptance requirements;
3. implement the canonical hosted synthetic full-TUN scenario;
4. migrate privacy/recovery/fault scenarios onto isolated hosted environments;
5. promote QEMU into reboot/suspend/device and supported historical-upgrade qualification;
6. add simulated Wi-Fi and scheduled soak where they close inventory gaps;
7. preserve trusted real-provider qualification and add external-network diversity only where it adds distinct evidence;
8. add the smallest physical HIL lab only for remaining hardware-specific gaps;
9. integrate stable scenarios into release policy after reliability/runtime evidence exists.

## Permanent repository impact

This design file is temporary planning material under `docs/superpowers/**`.

When the architecture is implemented and stabilized:

- durable E2E isolation/evidence/ownership invariants are folded concisely into `ARCHITECTURE.md`;
- public user behavior changes `docs/cli.md` only when product behavior actually changes;
- executable setup/assertions remain in `scripts/**` and `.github/workflows/**`;
- temporary design/plan and migration inventory artifacts are removed before final repository-structure completion.

## Completion definition

The initiative is complete when:

- the acceptance inventory has no unexplained manual or legacy gaps;
- all accepted routine/release scenarios execute automatically at their declared evidence level;
- release acceptance does not require a person to run Podlaz scenarios on a laptop or other DUT;
- remaining unsupported hardware/platform combinations are recorded as explicit coverage risks rather than manual release steps;
- humans review evidence, investigate failures, and make release decisions, but do not perform the acceptance procedure itself.
