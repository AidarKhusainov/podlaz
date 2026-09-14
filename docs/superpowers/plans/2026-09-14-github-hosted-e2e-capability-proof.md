# GitHub-hosted E2E Capability Proof Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prove, on a standard `ubuntu-24.04` GitHub-hosted runner, that Podlaz can run one real installed-package full-TUN lifecycle inside an isolated system guest while the outer Actions control plane remains healthy, and separately prove whether a checksum-verified Ubuntu 24.04 QEMU guest can boot and reboot with KVM and/or TCG.

**Architecture:** The Actions runner is an outer controller only. A `systemd-nspawn`-class system guest owns the destructive TUN/network/package lifecycle; an outer job-local Xray server provides a throwaway synthetic VPN endpoint. A separate QEMU phase proves an independent-kernel boot/reboot boundary. The spike emits only normalized capability evidence and does not change product semantics, canonical E2E policy, permanent release gates, or real-provider secret handling.

**Tech Stack:** GitHub Actions `ubuntu-24.04`, Bash, Go contract tests, `systemd-nspawn`/Linux network namespaces, systemd-resolved, NetworkManager, nftables/iproute2, packaged Xray v26.3.27, QEMU, Ubuntu 24.04 official cloud image, cloud-init NoCloud, OpenSSH.

**Spec:** `docs/superpowers/specs/2026-09-14-github-hosted-e2e-qualification-design.md`

## Global Constraints

- Treat this as a feasibility spike. Do not change Podlaz CLI/API/state/network semantics to make the environment pass.
- Never run `podlaz connect --mode tun` in the outer GitHub runner network namespace.
- Outer-host networking changes are limited to exact, collision-checked disposable-guest plumbing. Save and restore any global knob such as IPv4 forwarding. Never flush or normalize unrelated routes, rules, resolver state, nftables state, or NetworkManager state.
- Guest destruction is infrastructure cleanup, not product cleanup evidence. The guest must prove clean disconnect/recovery and exact owned-resource absence before it is destroyed.
- Keep the spike secret-free. Do not reference the `vpn-e2e` Environment or real-provider secrets.
- Generate synthetic credentials at runtime and keep the synthetic Xray server config/profile URI below private `E2E_TMP_ROOT`; never print them.
- Public artifacts contain only a normalized capability report with bounded enum/boolean/hash-safe fields. Raw journals, configs, addresses, routes, rules, and process details remain private and are deleted before upload.
- Keep guest/bootstrap mechanics local to the capability script. Do not create a new shared `scripts/e2e/lib/**` abstraction until at least two permanent scenarios consume the same semantics.
- Do not update `ARCHITECTURE.md` or `AGENTS.md` until the capability run proves the new mutation boundary.
- Do not add a release dependency or make this spike a merge/release gate. Permanent wiring is a later plan based on actual Actions evidence.
- Use only official upstream documentation for systemd-nspawn, QEMU, Ubuntu cloud images, cloud-init, and Xray configuration. Verify the Ubuntu image checksum before boot.
- Respect the standard hosted runner resource envelope: 4 vCPU, 16 GB RAM, 14 GB SSD, six-hour hard job limit. The spike itself targets a much smaller workflow timeout and uses sparse QEMU storage.
- Preserve the existing repository privacy, exact-ownership, rollback, authorization, and artifact-publication contracts.

---

## Task 1: Add RED capability contracts before orchestration

**Files:**
- Create: `scripts/e2e/hosted_e2e_capability_contract_test.go`
- Expected later: `.github/workflows/hosted-e2e-capability.yml`
- Expected later: `scripts/e2e/hosted-e2e-capability.sh`

**Interfaces:**
- Workflow name: `Hosted E2E Capability`
- Main entrypoint: `bash scripts/e2e/hosted-e2e-capability.sh <candidate.deb>`
- Normalized report: `${E2E_ARTIFACT_DIR}/hosted-e2e-capability.txt`
- Source-only gate for deterministic helper tests: `PODLAZ_E2E_CAPABILITY_SOURCE_ONLY=true`

- [ ] Add `TestHostedE2ECapabilityWorkflowContract` that requires a standard `ubuntu-24.04` runner, `contents: read`, pinned external actions, `persist-credentials: false`, a bounded job timeout, no `environment:` block, and no `${{ secrets.* }}` references.
- [ ] Add `TestHostedE2ECapabilityScriptContract` that requires source-only mode, private temp usage, a top-level EXIT cleanup path, exact candidate `.deb` validation, and the major evidence families: outer host, TUN/netns, system guest, package/runtime, ordinary-user authorization, synthetic full-TUN, doctor/IPv6/PMTU, NetworkManager postcondition, QEMU/KVM/TCG, image checksum, reboot/boot-id, disk budget, and final outer-host health.
- [ ] Add a test that invokes `bash -n scripts/e2e/hosted-e2e-capability.sh` once the script exists.
- [ ] Add negative contract assertions forbidding `PODLAZ_E2E_PROFILE_URI`, `PODLAZ_E2E_PROFILE_URI_LIST`, `PODLAZ_E2E_EXPECTED_EGRESS_IP`, `environment: vpn-e2e`, `self-hosted`, and direct `podlaz connect --mode tun` in workflow YAML.
- [ ] Run `go test ./scripts/e2e -run '^TestHostedE2ECapability' -count=1` and verify RED because the workflow/script do not exist yet.
- [ ] Do not weaken the test after seeing RED; the implementation in later tasks must satisfy it.

**Commit after GREEN for Tasks 1-2 together:** `test: define hosted e2e capability contract`

## Task 2: Implement normalized evidence and outer-runner safety boundary

**Files:**
- Create: `scripts/e2e/hosted-e2e-capability.sh`
- Modify: `scripts/e2e/hosted_e2e_capability_contract_test.go`

**Interfaces:**
- `record_capability <key> <pass|fail|unavailable|observed>`
- `capture_outer_baseline`
- `assert_outer_control_plane_healthy <phase>`
- `cleanup_outer_plumbing`
- `teardown_all`

- [ ] Extend the Go contract with a deterministic source-only test that sources the script into a temporary directory, writes duplicate/invalid evidence keys/states, and requires fail-closed rejection rather than malformed public output.
- [ ] Run the focused Go test and verify RED because the script/helper behavior is absent.
- [ ] Implement strict normalized evidence writing. Keys are `[A-Za-z0-9_.-]+`; states are a closed enum; report lines contain no raw command output.
- [ ] Create private directories with `0700` and private files with `0600`, reusing `scripts/e2e/lib/e2e.sh`, `evidence.sh`, and `private_command.sh` only where their existing semantics already match.
- [ ] Capture a private pre-test outer baseline for the default IPv4 route, policy rules, resolver mode/state needed for comparison, IPv4 forwarding value, and absence of all capability-owned interface/nftables identities.
- [ ] Add a bounded outer HTTPS control-plane probe before destructive guest phases and after cleanup. The public report records only success/failure.
- [ ] Implement one EXIT trap that always terminates Xray/QEMU/nspawn children, removes exact capability-owned links/tables/temp state, restores saved IPv4 forwarding if changed, and then rechecks the outer route/rule/resolver/control-plane baseline.
- [ ] Make cleanup idempotent and fail closed: an infrastructure cleanup failure converts an otherwise successful spike to failure.
- [ ] Run `go test ./scripts/e2e -run '^TestHostedE2ECapability' -count=1` and `bash -n scripts/e2e/hosted-e2e-capability.sh`; verify GREEN for the helper/contract subset implemented so far.

**Commit:** `test: define hosted e2e capability contract`

## Task 3: Prove kernel primitives and boot a NetworkManager-owned system guest

**Files:**
- Modify: `scripts/e2e/hosted-e2e-capability.sh`
- Modify: `scripts/e2e/hosted_e2e_capability_contract_test.go`

**Interfaces:**
- `probe_hosted_kernel_primitives`
- `prepare_system_guest`
- `start_system_guest`
- `guest_exec ...`
- `stop_system_guest`

- [ ] Add contract assertions requiring a functional `/dev/net/tun` test, private netns test, veth/system-guest boundary, systemd, systemd-resolved, NetworkManager, and exact cleanup identities. Verify the focused test is RED before implementation.
- [ ] Probe `/dev/net/tun` by creating and deleting a throwaway TUN interface in an isolated test namespace; existence of the device node alone is not sufficient.
- [ ] Probe `ip netns`/private namespace creation and independent route/rule/nftables mutation without touching the outer default route or resolver.
- [ ] Build a minimal Ubuntu 24.04 (`noble`) rootfs using the Ubuntu archive and install only packages needed for production-shaped Podlaz execution: systemd, systemd-resolved, NetworkManager, polkit, ca-certificates, iproute2, nftables, curl, Python, sudo, and package/runtime inspection tools.
- [ ] Create a dedicated guest user for ordinary-user acceptance. It must not be a permanent member of the `podlaz` service group. Any passwordless sudo capability is fixture-only and must not be used by the Podlaz client calls that prove the product authorization boundary.
- [ ] Boot the rootfs with `systemd-nspawn --boot` (or the equivalent exact nspawn form supported by Ubuntu 24.04), a private network namespace, a veth uplink, and explicit access to `/dev/net/tun`/required network capabilities.
- [ ] Give the guest a collision-checked RFC-reserved test subnet. If Internet access requires host forwarding/masquerade, use uniquely named infrastructure-owned nftables state, save/restore `net.ipv4.ip_forward`, and never reuse Podlaz table names.
- [ ] Make NetworkManager own the guest uplink and systemd-resolved provide guest resolution. Record the active connection/device identity privately so later TUN postconditions can compare against it.
- [ ] Prove guest direct DNS/HTTPS before Podlaz connect, systemd PID 1, resolved active, NetworkManager active, and `/dev/net/tun` usable from the guest.
- [ ] Stop the system guest and prove the outer runner still has control-plane connectivity and all capability-owned network plumbing can be removed exactly.
- [ ] Run focused contract/syntax tests locally through repository CI mechanics and verify GREEN.

**Commit:** `test: probe hosted system guest networking`

## Task 4: Install the exact candidate and prove ordinary-user/package boundaries

**Files:**
- Modify: `scripts/e2e/hosted-e2e-capability.sh`
- Modify: `scripts/e2e/hosted_e2e_capability_contract_test.go`
- Reuse unchanged: `scripts/e2e/lib/package_provenance.sh`
- Reuse unchanged where compatible: `scripts/e2e/installed-user-lifecycle-acceptance.sh`

**Interfaces:**
- `install_candidate_in_guest <deb>`
- `assert_guest_package_provenance`
- `run_guest_ordinary_user_acceptance`

- [ ] Add a contract requiring native `.deb` architecture validation, installed file/hash/runtime provenance, active `podlazd.service`, ordinary-user/no-`podlaz`-group proof, socket permission proof, polkit classification coverage, and packaged Xray supervision. Verify RED first.
- [ ] Validate the argument is one regular non-symlink native amd64 `podlaz` package before mounting/copying it into the guest.
- [ ] Install the exact candidate package using apt/dpkg inside the guest; do not rebuild Podlaz inside the guest.
- [ ] Reuse package provenance assertions to prove installed `podlaz`, `podlazd`, packaged Xray, package version, source commit, running daemon hash, and running daemon inode match the candidate.
- [ ] Run the existing ordinary-user installed-package acceptance if its fixture assumptions hold in nspawn. If a nspawn-specific fixture adaptation is necessary, keep it in the capability script and preserve the exact product assertions: non-root client, no permanent `podlaz` group membership, intended filesystem/abstract socket behavior, polkit unavailable/denied/allowed classification, proxy-only connect/disconnect, supervised Xray crash convergence, and clean recovery.
- [ ] Do not add a global or permanent CI authorization bypass. Any temporary polkit rule required for later TUN connect is guest-local, action-specific, and installed only after the ordinary-user contract has already been proven without it.
- [ ] Run the focused Go contract and relevant existing `installed_user_lifecycle_acceptance_test.go` tests; verify GREEN.

**Commit:** `test: qualify installed package inside hosted guest`

## Task 5: Prove real full-TUN with a throwaway synthetic Xray endpoint

**Files:**
- Modify: `scripts/e2e/hosted-e2e-capability.sh`
- Modify: `scripts/e2e/hosted_e2e_capability_contract_test.go`
- Reuse unchanged: `scripts/e2e/lib/daemon_status_semantics.py`
- Reuse unchanged: `scripts/e2e/lib/recovery_json.sh`
- Reuse unchanged where applicable: `scripts/e2e/lib/tun_package_assertions.sh`

**Interfaces:**
- `start_synthetic_xray_endpoint <candidate.deb>`
- `install_tun_ci_authorization`
- `run_synthetic_tun_lifecycle`
- `assert_guest_tun_clean`
- `stop_synthetic_xray_endpoint`

- [ ] Before implementing the synthetic server config, verify Xray v26.3.27 configuration syntax against official Xray documentation matching the packaged runtime. Use one minimal supported canonical protocol/transport; do not introduce a new product dependency.
- [ ] Add contract assertions for candidate-packaged Xray extraction, private generated credentials/config, endpoint binding only to the capability guest boundary, typed verified-active status, system DNS, IPv4 HTTPS/TLS, `doctor --tun`, NetworkManager postcondition, clean disconnect, clean `recover --json`, and exact cleanup. Verify RED first.
- [ ] Extract `/usr/lib/podlaz/xray` from the candidate package on the outer runner and run it as a non-root job-local server on the guest-boundary address with a high unprivileged port.
- [ ] Generate an ephemeral VLESS (or another single minimal supported protocol chosen from official/runtime evidence) identity and keep both server config and client URI private. Never print them to Actions logs or public artifacts.
- [ ] Inside the guest, import the synthetic profile and prove proxy-only planning/validation as a cheap preflight.
- [ ] Install a temporary headless guest-local polkit allow rule limited to the exact TUN connect/disconnect actions required by this phase, then prove it does not broaden unrelated actions.
- [ ] Execute `podlaz connect --mode tun` as the ordinary guest user. Wait on the existing typed daemon status semantics until verified-active or a bounded terminal state; do not accept presentation-only `Status: Connected` as sufficient evidence.
- [ ] While active, prove package/runtime identity still matches the candidate, exact TUN/address/routes/rules/resolved/nftables authority is present, NetworkManager still owns the expected uplink, and the product TUN is not accidentally adopted as a foreign active NetworkManager connection.
- [ ] Run bounded system DNS plus IPv4 TLS/HTTPS through the active TUN path. Where configured topology permits deterministic egress identity, assert it; otherwise record only the stronger route/transport evidence without inventing an IP expectation.
- [ ] Run `podlaz doctor --tun` while the session is active and privately retain its bounded classification output. Require the command/probes to execute; record DNS UDP/TCP, positive resolution, NXDOMAIN integrity, TCP/443, TLS/HTTPS, DoH, IPv6/leak state, and PMTU classifications without coercing topology-dependent WARN/UNKNOWN into PASS.
- [ ] Disconnect normally, wait for typed clean-inactive status, assert exact Podlaz-owned TUN/routes/rules/resolved/nftables/config/transaction/session state is gone as required, assert NetworkManager/uplink postconditions, and require clean `recover --json`.
- [ ] Remove the temporary polkit rule and synthetic endpoint, then prove direct guest DNS/HTTPS is restored before destroying the guest.
- [ ] Run focused contract tests and the existing recovery/status helper tests; verify GREEN.

**Commit:** `test: prove hosted synthetic full tun lifecycle`

## Task 6: Prove independent Ubuntu VM boot/reboot with KVM/TCG classification

**Files:**
- Modify: `scripts/e2e/hosted-e2e-capability.sh`
- Modify: `scripts/e2e/hosted_e2e_capability_contract_test.go`

**Interfaces:**
- `probe_qemu_accelerators`
- `prepare_qemu_image`
- `start_qemu_guest`
- `wait_qemu_ssh`
- `reboot_qemu_guest`
- `stop_qemu_guest`

- [ ] Add contract assertions requiring official Ubuntu release image URL, SHA-256 verification, sparse qcow2 overlay, loopback-only SSH forwarding, separate `kvm_present`/`kvm_usable`/`tcg_usable` evidence, disk-budget evidence, bounded boot waits, and real boot-ID change. Verify RED first.
- [ ] Record free disk before VM preparation and remove the nspawn rootfs/private package-build residue no longer needed before downloading the VM image.
- [ ] Download the official Ubuntu 24.04 amd64 released cloud image and matching `SHA256SUMS` from `cloud-images.ubuntu.com`; verify the image hash before QEMU sees it.
- [ ] Create a sparse qcow2 overlay rather than expanding/copying the base image. Create ephemeral SSH credentials and a NoCloud seed with key-only login and no long-lived password/secret.
- [ ] Probe `/dev/kvm` presence and a real QEMU KVM initialization separately. Record absence/unusable as accelerator capability, not as a Podlaz failure.
- [ ] Start the VM using KVM when proved usable; otherwise use QEMU TCG if it can boot within the spike timeout. Use user-mode networking with SSH forwarding bound to `127.0.0.1` only.
- [ ] SSH into the guest, prove Ubuntu 24.04 identity and capture `/proc/sys/kernel/random/boot_id`.
- [ ] Reboot from inside the guest, wait for SSH to disappear and return under a bounded deadline, then require the new boot ID to differ from the original.
- [ ] Stop QEMU, remove overlay/seed/private keys, record free disk after cleanup, and require sufficient margin so the spike never relies on disk exhaustion behavior.
- [ ] Run focused contract/syntax tests and verify GREEN.

**Commit:** `test: probe hosted qemu reboot boundary`

## Task 7: Wire the temporary capability workflow and privacy-safe artifact

**Files:**
- Create: `.github/workflows/hosted-e2e-capability.yml`
- Modify: `scripts/e2e/hosted_e2e_capability_contract_test.go`
- Modify: `scripts/e2e/hosted-e2e-capability.sh`

**Interfaces:**
- Triggers: `pull_request` and `workflow_dispatch`
- Runner: `ubuntu-24.04`
- Permissions: `contents: read`
- Public artifact: exact `hosted-e2e-capability.txt`

- [ ] Add/strengthen the workflow contract first and run it RED while the workflow is absent/incomplete.
- [ ] Create the temporary workflow with pinned `actions/checkout` and `actions/setup-go`, `persist-credentials: false`, no secrets/environment, and an explicit timeout comfortably below six hours (target <= 120 minutes for the spike).
- [ ] Install only host packages required by the spike: shell/network tools already relied on by repository E2E plus `debootstrap`, `systemd-container`, QEMU/qemu-utils, cloud-image tooling, OpenSSH client, and other exact packages proven necessary by Ubuntu 24.04.
- [ ] Build one native amd64 candidate package using the repository-pinned Go/nFPM toolchain and pass that exact `.deb` into the capability script. Avoid building unrelated architectures in this temporary job.
- [ ] Set `E2E_TMP_ROOT` and `E2E_ARTIFACT_DIR` below `${RUNNER_TEMP}`. Remove stale directories before the run.
- [ ] Run the focused Go contract before the privileged spike so malformed workflow/script changes fail cheaply.
- [ ] Execute the capability script. Its report is progressive so a failing capability still leaves normalized evidence, but workflow success requires every capability classified as required by the script to satisfy the spike policy.
- [ ] In an `always()` step, run a strict report validator that rejects unexpected files, duplicate/missing keys, raw paths/addresses/URIs, or unbounded values. The validator may live in the capability script under a `validate-report` subcommand to avoid creating a second one-off artifact scanner.
- [ ] Delete the entire private temp root before upload. Upload only `${E2E_ARTIFACT_DIR}/hosted-e2e-capability.txt` with short retention.
- [ ] Run `bash scripts/ci/workflow-lint.sh`, `go test ./scripts/e2e -run '^TestHostedE2ECapability' -count=1`, and `bash scripts/ci/repository-structure.sh` (active/draft mode); verify GREEN.

**Commit:** `ci: add hosted e2e capability spike`

## Task 8: Run the spike in a draft PR and classify real Actions evidence

**Files:**
- Modify only if evidence requires spike/infrastructure corrections: `.github/workflows/hosted-e2e-capability.yml`, `scripts/e2e/hosted-e2e-capability.sh`, `scripts/e2e/hosted_e2e_capability_contract_test.go`
- Do not modify product networking code in this task.

**Interfaces:**
- Draft PR from `agent/github-hosted-e2e-qualification` to `master`
- Required evidence source: GitHub Actions job result + normalized capability artifact

- [ ] Open/keep the PR in draft state so transient `docs/superpowers/**` artifacts are valid under repository policy.
- [ ] Let `pull_request` run the ordinary CI plus `Hosted E2E Capability` on the exact branch head.
- [ ] Inspect the workflow jobs/logs and normalized artifact. Do not infer a capability from script intent; require actual Actions evidence.
- [ ] If a capability fails because the spike scaffolding is wrong, fix only the infrastructure/test harness, add or strengthen the regression contract that would have caught the failure, and rerun on the new exact head.
- [ ] If a required capability is genuinely unavailable on standard hosted runners, preserve that negative result explicitly; do not add a product workaround or silently downgrade it to PASS.
- [ ] Record the final decision in the draft PR body: chosen system-guest mechanism, KVM availability, TCG viability, measured disk/time headroom, full-TUN result, authorization result, NetworkManager result, doctor/IPv6/PMTU observations, and any bounded technical gaps.
- [ ] Use the evidence to decide the next permanent plan. At minimum the next plan must cover reusable secret-free synthetic TUN qualification; destructive matrix/QEMU release gates are split further if the evidence shows different infrastructure needs.

**Commit:** only if evidence-driven spike fixes are required; each fix uses an invariant-oriented message.

## Task 9: Verification checkpoint before the permanent hosted-E2E plan

**Files:**
- No new product files expected.
- Keep spec/plan transient while the PR is draft.

- [ ] Require the latest exact-head ordinary PR CI to pass: workflow/shell lint, repository structure active check, Go core/race/vet, govulncheck, CLI contracts, real nftables contract, Debian package validation/install/reinstall/purge.
- [ ] Require the latest exact-head `Hosted E2E Capability` run to complete with a normalized artifact and no private-temp upload.
- [ ] Review the diff for outer-host mutation scope, exact cleanup, timeouts, privacy, generated credentials, pinned actions/toolchain usage, disk bounds, and absence of real-provider secrets.
- [ ] Do **not** run `repository-structure.sh --final` yet: the approved active design and plan are intentionally transient `docs/superpowers/**` artifacts and the PR remains draft.
- [ ] Stop this plan after the capability decision. Write the next implementation plan from observed runner evidence instead of extending this plan speculatively.

## Plan self-review

- [x] Scope is intentionally limited to the capability proof; permanent synthetic TUN, destructive matrix, real-provider TUN, ARM64, soak, release gating, and canonical policy updates are later plans.
- [x] Every planned public artifact is normalized; raw guest/provider/network state remains private.
- [x] Outer-runner mutation is bounded to disposable-guest plumbing and exact restoration; Podlaz itself never receives authority over the outer runner network.
- [x] The plan includes real package/runtime provenance, ordinary-user authorization, NetworkManager, systemd-resolved, full TUN, DNS/TLS/HTTPS, `doctor --tun`, IPv6/leak/PMTU classification, clean recovery, QEMU boot/reboot, and disk/time budgets.
- [x] KVM is an optional accelerator, not an assumed GitHub contract; TCG viability is measured separately.
- [x] No product/API/schema/dependency upgrade is required by the spike.
- [x] No placeholder/TBD requirement remains.
