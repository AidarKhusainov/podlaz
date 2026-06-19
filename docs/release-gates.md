# Release gates

This document defines what must be true before podlaz publishes a release or marks a major networking mode as stable.

It is a policy document, not a run record. Concrete evidence belongs in issues, pull requests, release notes, or separately reviewed redacted records.

## Evidence rules

Release-gate evidence must:

- name the exact commit under test;
- identify the Linux distribution, kernel, Go version, and relevant external binaries without exposing provider secrets;
- record automated checks and manual host checks;
- include enough before/after networking evidence to support the release claim;
- redact share URIs, subscription URLs, generated core configs, private keys, passwords, provider tokens, and user identities where required.

Do not commit pending evidence templates, placeholder tables, raw command transcripts, screenshots with secrets, or local machine-specific run logs to the repository.

## Proxy-only release gate

A proxy-only release must demonstrate that:

- normal build and test checks pass for the release commit;
- profile or subscription import creates user-owned state without starting network processes;
- proxy-only planning is read-only;
- proxy-only connect and disconnect are daemon-owned;
- proxy-only lifecycle starts and stops the core process without mutating TUN devices, routes, policy rules, DNS settings, nftables, or firewall state;
- status, doctor, logs, and recover remain redacted and actionable;
- package installation does not start the daemon, start the core process, or change host networking.

## TUN/full-tunnel release gate

A TUN/full-tunnel release must demonstrate that:

- planning starts from read-only host snapshots;
- apply, verify, commit, rollback, disconnect cleanup, and explicit recovery are daemon-owned;
- VPN server traffic bypasses the TUN path;
- route, policy-rule, DNS, TUN, nftables, core, and adapter state are verified while active;
- a forced post-apply failure rolls back only podlaz-owned state;
- clean disconnect leaves no podlaz-owned stale TUN, route, DNS, nftables, runtime config, process, or transaction state;
- recovery execution removes only clearly podlaz-owned volatile state and skips ambiguous resources;
- stable leak-protection claims are not made before the full flow is verified on a supported Linux host.

## Storage location

Use GitHub issues or release pull requests for release-gate checklists and evidence. Keep this repository documentation focused on reusable policy and contracts.
