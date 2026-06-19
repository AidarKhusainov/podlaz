# Roadmap

This document defines podlaz sequencing constraints. It is not a repository status log, changelog, implementation inventory, or release verification record.

## Principles

podlaz development is ordered around safety before feature breadth.

Rules:

- do not add convenience features that hide Linux networking state before connect, disconnect, rollback, diagnostics, and recovery are reliable;
- do not declare full-tunnel behavior stable without verified apply, health check, rollback, disconnect cleanup, and recovery behavior;
- do not expand daemon privileges without updating the service hardening and security contracts;
- do not add broad protocol or GUI work before the CLI/daemon/networking foundation is dependable.

## Sequencing

| Order | Area | Gate |
| ---: | --- | --- |
| 1 | Documentation and repository foundation | Product, CLI, architecture, state/security, networking, package-boundary, and development contracts exist and have one canonical owner per concern. |
| 2 | CLI, daemon, local API, and diagnostics foundation | The CLI can communicate with the daemon, diagnostics are read-only, daemon logs are inspectable, and recovery can inspect podlaz-owned stale state. |
| 3 | Profile and subscription foundation | Profiles and subscriptions can be imported, normalized, validated, listed, shown, updated, and stored as user-owned state without starting network processes. |
| 4 | Proxy-only lifecycle | Xray can be started and stopped by the daemon without changing routes, DNS, TUN devices, nftables, or firewall state. |
| 5 | Network planning | Full-tunnel intent can be inspected from a read-only host snapshot before privileged mutation. |
| 6 | Safe TUN execution | Full-tunnel apply, verify, commit, rollback, disconnect cleanup, and explicit recovery execute paths are daemon-owned and auditable. |
| 7 | Laptop reliability | Suspend/resume, Wi-Fi roaming, DHCP/DNS changes, default-route changes, and reconnect loops are handled without stale state. |
| 8 | Packaging and release | Packages install files only under valid packaged locations, do not start VPN/networking implicitly, and pass package validation gates. |
| 9 | Convenience features | Split tunnel, auto-select, latency tests, GUI clients, provider metadata, and additional engines are considered only after the core Linux reliability path is strong. |

## Stable full-tunnel gate

Full-tunnel behavior must not be presented as stable until the project has conclusive evidence for:

- successful connect and disconnect on a Tier 1 Linux target;
- route, policy-rule, DNS, TUN, nftables, core, and adapter verification during an active connection;
- VPN server route bypass outside the TUN interface;
- rollback after a forced post-apply failure;
- clean final state after disconnect and rollback;
- explicit recovery cleanup for clearly podlaz-owned stale state;
- redacted evidence that does not expose provider secrets, share URIs, tokens, or generated core configs.

Release-gate evidence belongs in issues, pull requests, release notes, or separately reviewed records. It must not be stored as pending placeholder tables in the reference documentation.

## Explicit deferrals

The following must remain deferred until earlier gates justify them:

- GUI as a primary product path;
- mobile clients;
- router distribution support;
- complex visual routing editors;
- provider account management;
- plugin systems;
- broad non-Xray protocol expansion;
- automatic privileged core updater;
- privileged package defaults that are not justified by the service hardening contract.
