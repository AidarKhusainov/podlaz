# Roadmap

## 1. Roadmap principles

TunWarden development must prioritize safety before feature breadth.

The roadmap is intentionally staged so that privileged networking is implemented only after planning, rollback, diagnostics, recovery behavior, state ownership, and output redaction are designed and testable.

Important ordering rule:

> Do not add convenience features that hide networking state before the client can reliably connect, disconnect, diagnose, roll back, and recover.

## 2. Current repository state

The repository currently contains a foundation build:

- `tunwarden` CLI skeleton,
- `tunwardend` daemon skeleton,
- local Unix socket HTTP/JSON daemon IPC for read-only status and doctor diagnostics,
- manual systemd service unit with `/run/tunwarden` runtime directory handling and journald logging,
- group-based daemon socket access model for the repository systemd unit,
- read-only `status` with daemon-backed status and conservative local fallback,
- read-only `doctor` with daemon-backed diagnostics and local fallback for platform, command availability, default route/interface, and stale TunWarden-owned resources,
- read-only `logs` with journald-backed daemon log inspection,
- dry-run `recover` command contract,
- initial transaction/profile/subscription models,
- manual profile management and VLESS share URI profile import,
- CI with `gofmt` and `go test`,
- canonical documentation under `docs/`.

The repository does **not** yet contain:

- real Xray process management,
- top-level `tunwarden import` format detection,
- subscription parsing,
- VMess, Trojan, or Shadowsocks URI import,
- TUN creation,
- route/DNS/nftables mutation,
- `.deb` package or installer,
- auto-start enablement policy.

## 3. Phase 0: Documentation and repository foundation

Status: mostly complete.

Goal: define the project before writing privileged code.

Deliverables:

- documentation index,
- product requirements,
- CLI contract,
- state and security requirements,
- architecture requirements,
- package boundary requirements,
- networking/reliability requirements,
- subscription/profile requirements,
- development guide,
- technical references,
- initial CI skeleton,
- initial CLI/daemon skeleton.

Exit criteria:

- MVP scope is clear.
- Non-goals are documented.
- Networking invariants are documented.
- Recovery and rollback are treated as first-class requirements.
- Filesystem state ownership is documented.
- JSON compatibility and output redaction rules are documented.
- Documentation has one canonical location per concern.

## 4. Phase 1: CLI, daemon, local IPC, and read-only diagnostics foundation

Goal: create the basic process model and diagnostic surface without risky networking changes.

Deliverables:

- `tunwarden` command structure,
- `tunwardend` daemon process,
- local IPC design and implementation,
- restricted daemon API,
- systemd service draft,
- journald logging,
- `status` command,
- `logs` command,
- structured error model,
- JSON output shape for `status`, `doctor`, and `plan`,
- shared redaction helpers for human and JSON output,
- daemon startup recovery scan in read-only mode,
- read-only Linux diagnostics for:
  - default route,
  - default interface,
  - NetworkManager availability/state,
  - systemd-resolved availability/state,
  - DNS mode,
  - nftables availability,
  - stale TunWarden-owned resources.

Implemented foundation subset:

- CLI skeleton,
- daemon skeleton,
- local Unix socket HTTP/JSON transport,
- manual systemd unit for `tunwardend`,
- journald logging through the systemd unit,
- daemon-backed `status` with local fallback,
- daemon-backed `doctor` with local fallback,
- journald-backed `logs` command for daemon logs,
- shared human-output redaction for implemented status, doctor, logs, and recover output,
- read-only local recovery dry-run scan.

Exit criteria:

- CLI can communicate with daemon.
- Daemon can run under systemd.
- Logs are visible through `journalctl` and `tunwarden logs`.
- No privileged networking changes are performed yet.
- Read-only recovery scan can report stale TunWarden-owned state without removing it.
- Default output and `--json` output redact secrets consistently.

## 5. Phase 2: Profile and subscription foundation

Goal: import and normalize profiles before connecting anything.

Deliverables:

- internal profile model,
- manual profile support,
- VLESS share URI parser for `tunwarden profile import`,
- convenience `tunwarden import` entrypoint,
- XDG-based user config/state layout,
- share link parsers for remaining initial protocols,
- Base64 subscription parser,
- subscription storage,
- update diff,
- validation and warnings,
- delete confirmation behavior,
- fixture-based tests.

Implemented foundation subset:

- internal profile model,
- manual profile add, list, show, and delete,
- VLESS share URI import through `tunwarden profile import`,
- persistent user-owned profile storage under the documented XDG state path,
- validation, warning, and fixture coverage for implemented manual and VLESS-import profile behavior.

Initial protocols:

- VLESS implemented for `tunwarden profile import`,
- VMess deferred,
- Trojan deferred,
- Shadowsocks deferred.

Exit criteria:

- Profiles can be imported, listed, shown, validated, and deleted.
- Subscriptions can be added, listed, shown, updated, and deleted.
- `tunwarden import` can detect supported share links and subscriptions.
- Subscription update failure preserves last known good state.
- Unsupported formats fail clearly.
- Unsafe profile settings are reported as warnings rather than silently accepted.
- Stored user intent follows the documented XDG layout.

## 6. Phase 3: Xray engine lifecycle in proxy-only mode

Goal: start and stop Xray safely without touching system routes, DNS, TUN, or firewall state.

Deliverables:

- Xray engine manager,
- `doctor --core` Xray validation,
- generated runtime config under `/run/tunwarden/`,
- generated core config permissions and atomic writes,
- local SOCKS/HTTP/mixed inbound where supported,
- core process supervision,
- core process privilege minimization,
- graceful stop,
- forced stop,
- core logs,
- basic health check,
- `plan --mode proxy-only`,
- proxy-only `connect` and `disconnect`.

Exit criteria:

- A manual profile can run in proxy-only mode.
- The daemon can stop Xray cleanly.
- Core crash is detected and reported.
- No system routes, DNS, firewall rules, or TUN devices are modified.
- Generated Xray config is runtime output, not persistent source of truth.
- Generated core config is not logged in full by default.

## 7. Phase 4: Network planner and dry-run

Goal: design network changes without applying them.

Deliverables:

- system snapshot model,
- route planner,
- DNS planner,
- firewall planner,
- TUN planner,
- transaction model,
- `tunwarden plan --mode tun <profile>`,
- planner unit tests,
- fake system snapshots for common desktop topologies.

Exit criteria:

- A full-tunnel plan can be generated from a fake system snapshot.
- Plan output is readable by technical users.
- Planner can detect route loop risk.
- Planner can produce rollback steps.
- Planner can explain warnings for DNS, IPv6, NetworkManager, and kill-switch behavior.
- Plan output redacts sensitive values.

## 8. Phase 5: Safe TUN MVP

Goal: implement full-tunnel mode with rollback and prove the safe TUN preview through manual acceptance.

Deliverables:

- TUN interface creation,
- routing table/rule apply,
- systemd-resolved DNS apply,
- nftables foundation,
- transaction apply/commit/rollback,
- `recover --execute --yes` explicit cleanup mode,
- `doctor` checks for route/DNS/TUN/firewall/core state,
- systemd hardening baseline for privileged daemon release,
- integration tests in Linux network namespaces where possible,
- manual `v0.2.0: safe TUN preview` acceptance checklist for one Tier 1 Linux desktop host.

Exit criteria:

- Failed connection attempts roll back.
- Disconnect leaves no TunWarden-owned routes, rules, DNS, nftables state, TUN interfaces, generated configs, or child processes.
- `recover --execute --yes` works when disconnected and after simulated failure.
- VPN server route bypasses TUN.
- Strict kill-switch behavior is explicit and recoverable.
- The systemd unit documents final hardening choices and justifies deviations from the documented baseline.
- The v0.2 acceptance checklist is completed with a redacted verification record before the milestone is declared complete.

## 9. Phase 6: Laptop reliability

Goal: make the client robust on real Linux laptops.

Deliverables:

- NetworkManager event handling,
- default route/interface change handling,
- DNS change handling,
- suspend/resume hooks,
- reconnect state machine,
- rate-limited retries,
- health checks after resume,
- Ubuntu LTS manual test checklist.

Exit criteria:

- Active connection recovers after suspend/resume.
- Active connection recovers after Wi-Fi reconnect.
- DHCP/DNS changes are handled without stale state.
- NetworkManager limited connectivity is reported but not blindly treated as VPN failure.
- Reconnect loops are rate-limited and observable.

## 10. Phase 7: Packaging

Goal: make installation and service management reliable.

Deliverables:

- `.deb` package,
- systemd units,
- default config files,
- uninstall cleanup policy,
- shell completions,
- man pages later,
- Fedora/Arch packaging plan.

Exit criteria:

- Fresh Ubuntu installation can install, start daemon, connect, disconnect, run `doctor`, run `recover`, and uninstall safely.
- Package removal has a documented cleanup policy.

## 11. Phase 8: Advanced features

Goal: add convenience only after reliability is solid.

Candidates:

- latency test,
- URL test,
- auto-select,
- split tunnel,
- routing rule DSL,
- IPv6 full support,
- AmneziaWG engine,
- optional sing-box compatibility experiments,
- GUI client,
- provider-specific subscription metadata,
- auto-update core with signature/checksum verification.

## 12. Explicit deferrals

The following should not be started until the earlier reliability phases are strong:

- GUI,
- mobile clients,
- complex visual routing editor,
- router mode,
- plugin system,
- broad non-Xray protocol expansion,
- automatic privileged core updater.

## 13. First milestone proposal

A realistic first public milestone:

```text
v0.1.0: proxy-only technical preview
```

Features:

- CLI + daemon + IPC,
- manual and VLESS share URI profile import,
- Base64 subscription import,
- Xray proxy-only mode,
- status/logs/doctor basics,
- `plan --mode proxy-only`,
- dry-run `recover`,
- no TUN mode yet.

Second milestone:

```text
v0.2.0: safe TUN preview
```

Features:

- TUN full-tunnel,
- transaction rollback,
- systemd-resolved backend,
- nftables foundation,
- `recover --execute --yes`,
- systemd hardening baseline,
- Ubuntu LTS test checklist,
- redacted manual acceptance record for one Tier 1 host.
