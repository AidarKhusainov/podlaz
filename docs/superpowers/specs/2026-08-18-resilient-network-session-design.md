# Resilient Networking Target Architecture

Status: target architecture, not yet fully implemented.

This document defines the product-level networking contract Podlaz should converge toward. Existing implementation documents such as `state-and-security.md`, `packaged-tun-runtime.md`, and `tun-uplink-revalidation.md` continue to describe current behavior until follow-up changes explicitly bring them into conformance with this target.

## Goal

Podlaz should behave like a mature desktop VPN client: the user chooses a profile and connects or disconnects, while Linux routing, DNS, firewall, TUN, process supervision, restart recovery, package upgrades, suspend/resume, and network changes are handled internally.

The normal user workflow must not require `recover`, `ip rule`, `resolvectl`, `nft`, service restarts, or manual network repair.

At a stable product boundary, Podlaz should expose only two normal outcomes:

- `CONNECTED`: the VPN data plane is working and sufficiently proven safe.
- `DISCONNECTED`: Podlaz is not controlling the data plane and ordinary networking is working.

Intermediate states such as connecting, reconciling, reconnecting, or recovering are implementation details and must converge to one of those outcomes.

## 1. Coexistence over cleanliness

Podlaz must not require a pristine Linux networking state before connecting.

Another TUN interface, WireGuard device, VPN-created route, policy rule, DNS configuration, firewall state, or custom routing table is a normal possible part of the host baseline. The mere presence of such state must not block Podlaz.

Podlaz must not identify, catalogue, terminate, or otherwise manage other VPN clients. There is no product concept of a known or supported foreign VPN. Starting Podlaz must not depend on recognizing Throne, WireGuard, OpenVPN, Mullvad, ProtonVPN, NetworkManager VPN profiles, or any other implementation.

Instead, Podlaz must:

1. observe enough of the current host network to find a usable underlying path to its server;
2. select collision-free resources for the Podlaz session rather than assuming fixed routing identifiers are available;
3. install a Podlaz-owned data plane with precedence sufficient to carry the intended traffic;
4. leave unrelated host resources untouched;
5. remove only resources created or exactly owned by the Podlaz session.

The existence of foreign state is therefore not an error. A real conflict is an error only when Podlaz cannot establish a safe working data plane without modifying unowned state.

## 2. Reconcile over fail

Network change is expected behavior, not exceptional behavior.

Wi-Fi roaming, DHCP renewal, interface replacement, Ethernet/Wi-Fi changes, suspend/resume, temporary route disappearance, DNS convergence, NetworkManager transitions, reordered netlink events, temporary probe failures, or another VPN changing the routing environment must first trigger fresh observation and reconciliation.

Podlaz must not tear down an otherwise usable connection because of a single stale snapshot, one timeout, one unexpected command result, or a transient intermediate state.

The conceptual loop is:

```text
observe -> decide -> reconcile -> observe -> verify
```

A terminal failure is justified only after a bounded reconciliation attempt shows that Podlaz cannot safely provide or restore the required data plane.

The implementation may use retries, event coalescing, network generations, backoff, or condition-based waiting, but those are implementation details. The architectural requirement is progress-aware convergence rather than immediate failure on transient evidence.

## 3. Evidence over checklists

`CONNECTED` must mean there is sufficient evidence that the protected data plane is working and safe. It must not mean that every diagnostic probe is green.

No single external endpoint, DNS query, resolver inspection, NetworkManager observation, HTTPS request, or other soft diagnostic signal may by itself determine that the VPN is broken.

The implementation should distinguish conceptually between:

- hard invariants, whose confirmed violation means the protected data plane is unsafe or unusable; and
- soft evidence, which increases or decreases confidence but can fail transiently without forcing disconnect.

Examples of hard failures include a confirmed traffic leak while privacy protection is required, an unrecoverable routing loop, a core/TUN failure that cannot be restored, inability to reach the VPN server through a safe underlying path, or inability to establish routing without taking over unowned host state.

Examples of soft failures include one DNS timeout, one unavailable diagnostic target, temporarily incomplete `systemd-resolved` state, a short-lived NetworkManager transition, or a single connectivity probe timeout.

A soft failure should normally result in re-observation or reconciliation, not terminal disconnect.

## 4. Privacy first during recovery

Once the user has successfully connected, temporary loss of VPN health must not silently open direct traffic.

While Podlaz is reconciling or reconnecting an existing protected session, direct traffic remains blocked unless the implementation can prove it is part of the intended protected data plane.

This privacy protection must survive an unexpected `podlazd` crash long enough for the daemon to restart and continue the lifecycle.

If bounded recovery ultimately proves that the VPN cannot be restored safely, Podlaz must terminate the session cleanly, remove its protection and owned network state, verify that ordinary networking works, and transition to `DISCONNECTED`.

After such a terminal failure Podlaz does not continue reconnecting in the background. A new attempt requires a new explicit connect action, except for the separate autostart behavior at boot.

## 5. Podlaz Network Session

A connected VPN is represented conceptually as one Podlaz Network Session.

The session owns the complete Podlaz networking lifecycle needed for that connection, including the TUN/core process relationship, routing, DNS, firewall behavior, server reachability path, and the durable information required to undo or recover those changes.

The exact internal representation is deliberately not specified here.

The important contract is atomic lifecycle ownership:

- Podlaz must never lose the information required to recover or remove its own surviving network state.
- A crash, daemon restart, package upgrade, or partial teardown must not leave live Podlaz network resources whose cleanup authority has disappeared.
- Cleanup must be based on exact Podlaz session ownership, not merely on values that look conventional or reserved.
- Fixed historical identifiers may be recognized for migration or diagnostics, but they must not be the long-term mechanism used to decide that arbitrary live state is owned by Podlaz.

The implementation should prefer dynamically selected, collision-free resources where practical rather than assuming fixed rule priorities or routing tables are unused.

## 6. Connect contract

A normal connect request means: establish a protected Podlaz Network Session for the selected profile over the current usable host network.

Podlaz should not first attempt to normalize the whole host into a predetermined clean topology.

Conceptually:

```text
current Linux network
        |
        v
find usable server path
        |
        v
allocate Podlaz session resources
        |
        v
establish Podlaz data plane
        |
        v
verify sufficient safe evidence
        |
   +----+----+
   |         |
 success   terminal failure
   |         |
CONNECTED   remove Podlaz state
            verify ordinary network
            DISCONNECTED
```

If another VPN or custom routing setup is already active, Podlaz treats that as part of the current network. It does not identify or stop that software. If Podlaz can safely establish its own data plane, connect succeeds. If it cannot do so without modifying unowned state, connect fails without destructive foreign cleanup.

## 7. Disconnect contract

An explicit disconnect means: remove the active Podlaz Network Session and return control to the remaining host network.

Podlaz removes only its own session state. It does not restore, restart, or otherwise manage any previous VPN client.

A successful disconnect ends in `DISCONNECTED` with ordinary networking working according to the remaining host configuration.

## 8. External network changes while connected

There is no special product event called "another VPN appeared".

Any external routing, DNS, link, interface, or firewall change is simply a change in the surrounding Linux network.

Podlaz should:

- do nothing if its protected data plane remains sufficiently proven healthy;
- reconcile its own session if the change affects it;
- avoid modifying unrelated state;
- fail only if a serious problem remains after bounded reconciliation.

Podlaz must not automatically disconnect merely because a new TUN interface, route, policy rule, or VPN-like network object appears.

## 9. Service lifecycle, crashes, upgrades, and reboot

Technical maintenance of Podlaz must not unexpectedly change the user's connection state.

The externally visible behavior is:

```text
plz connect
-> VPN remains on until disconnect, terminal failure, or reboot policy says otherwise

plz disconnect
-> VPN off; ordinary network works

daemon restart
-> if VPN was on, it remains or returns on automatically

daemon crash + automatic service restart
-> privacy protection is preserved during recovery; VPN is restored if possible

package upgrade
-> same user-visible result as a safe daemon restart

explicit service stop
-> Podlaz shuts down cleanly and ordinary network works

machine reboot
-> VPN starts disconnected unless autostart/autoconnect is enabled
```

The implementation may persist minimal volatile connection state across daemon replacement or package upgrade, but a normal connection does not implicitly survive a machine reboot.

Autostart/autoconnect is a separate explicit persistent setting. Only that setting authorizes a new automatic connection after boot.

## 10. Failure semantics

Podlaz should fail only for serious problems that prevent a safe working outcome, not for ordinary timing races or imperfect diagnostics.

When a transient problem occurs, the preferred behavior is temporary reconciliation while preserving privacy.

When a serious terminal failure is established, the preferred outcome is deterministic:

1. stop attempting to maintain the failed VPN session;
2. remove or finish recovery of Podlaz-owned state;
3. remove privacy blocking only after the terminal transition is intentional;
4. verify that ordinary networking is usable;
5. report `DISCONNECTED` with a concise actionable reason;
6. do not keep retrying automatically.

A state where the VPN is unusable, ordinary networking is broken, and Podlaz no longer has enough authority to recover its own residue is an architectural defect.

## 11. User experience contract

Normal use should be simple:

```text
add subscription
select profile
connect
disconnect
update client
```

Everything else is internal lifecycle management.

`recover`, deep diagnostics, transaction inspection, Linux routing commands, resolver commands, and firewall commands remain engineering/operator tools. They must not be required as routine instructions to restore connectivity after normal product operations.

User-visible status should emphasize product states such as connected, reconnecting, disconnected, or an actionable terminal failure. Internal routing identifiers, ownership projections, resolver command envelopes, transaction phases, and similar implementation details belong in diagnostics, not in the primary UX.

## 12. Non-goals

This architecture does not require Podlaz to:

- identify or control other VPN products;
- restore a previously active foreign VPN after Podlaz disconnects or fails;
- clean arbitrary foreign routing, DNS, firewall, or TUN state;
- guarantee that every possible pair of competing VPN implementations can coexist indefinitely;
- hide a confirmed privacy or routing safety violation merely to keep the status green;
- retry a terminally failed VPN forever;
- automatically reconnect after reboot unless autostart/autoconnect is enabled.

## 13. Architectural acceptance criteria

Follow-up implementation work should be considered converged toward this architecture when the installed client can demonstrate all of the following as product-level scenarios:

- connect successfully on a host that already contains unrelated TUN/routing state, when a safe usable server path exists;
- avoid fixed-resource collisions by selecting safe session resources where appropriate;
- tolerate transient DNS, route, DHCP, NetworkManager, suspend/resume, and event-ordering changes without unnecessary disconnects;
- preserve privacy while reconciling an existing protected session;
- survive daemon crash/restart without silently opening direct traffic;
- survive package upgrade without losing recovery authority or requiring manual reconnect when the VPN was already on;
- recover or clean only exact Podlaz-owned state after interrupted lifecycle operations;
- return to usable ordinary networking after a confirmed terminal failure;
- stop automatic retries after that terminal failure;
- remain disconnected after reboot unless explicit autostart/autoconnect is enabled;
- require no routine manual `ip`, `resolvectl`, `nft`, or `recover --execute` workflow.

These are behavioral contracts. The implementation should remain free to evolve its internal state machine, persistence layout, routing mechanism, verifier composition, and process supervision as long as those behaviors and the project's security boundaries are preserved.
