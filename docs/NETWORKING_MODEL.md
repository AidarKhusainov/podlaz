# Networking model

TunWarden's main product value is safe Linux networking.

## Principles

1. Never apply a privileged network change without a snapshot.
2. Never leave daemon-owned state without an owner marker.
3. Prefer per-link DNS configuration over global resolver mutation.
4. Treat NetworkManager connectivity status as advisory, not authoritative.
5. Keep a route to the VPN server outside the managed TUN interface.
6. Make `panic-reset` possible even when the normal daemon state is corrupted.

## Planned managed resources

- TUN interface: `tunwarden0`
- nftables table: `inet tunwarden`
- policy routing rules owned by TunWarden
- routing table entries owned by TunWarden
- systemd-resolved per-link DNS settings
- daemon state under `/run/tunwarden`

## DNS strategy

Full tunnel DNS should use per-link DNS where possible:

```text
resolvectl dns tunwarden0 <dns-server>
resolvectl domain tunwarden0 '~.'
resolvectl default-route tunwarden0 yes
```

The actual implementation must also preserve enough snapshot state to revert DNS cleanly.

## Sleep and network changes

The daemon should eventually react to:

- suspend and resume
- default route changes
- Wi-Fi reconnects
- DHCP changes
- DNS changes
- interface disappearance

After a resume or network change, the daemon should re-detect the default interface, re-resolve server addresses, re-plan routes, and verify connectivity before committing.

## Health model

A healthy connection is not the same as a happy desktop icon. Health checks should include:

- daemon alive
- engine alive
- TUN interface exists
- VPN server route bypasses TUN
- DNS resolves through the expected path
- TCP probe succeeds
- optional UDP probe succeeds
- NetworkManager connectivity state as advisory metadata
