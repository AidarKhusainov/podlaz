# Maintainer laptop release acceptance

Use `scripts/acceptance/release-laptop.sh` to qualify an already-built native Podlaz Debian release package on a local maintainer laptop.

Canonical start:

```bash
sudo ./scripts/acceptance/release-laptop.sh ./podlaz_<candidate>_linux_<arch>.deb --profile <profile-id>
```

If no strictly lower release is installed, provide it explicitly:

```bash
sudo ./scripts/acceptance/release-laptop.sh ./podlaz_<candidate>_linux_<arch>.deb \
  --previous-deb ./podlaz_<previous>_linux_<arch>.deb \
  --profile <profile-id>
```

A full run is intentionally disruptive and spans three real operator-controlled reboots. The harness never reboots automatically. After every requested reboot run:

```bash
sudo ./scripts/acceptance/release-laptop.sh --resume
```

To abandon a persisted run and reconcile only exact harness-owned state:

```bash
sudo ./scripts/acceptance/release-laptop.sh --abort
```

The harness tests only supplied release `.deb` files. It does not build Podlaz, download a previous release, run `apt` dependency repair, execute broad route/rule/nftables cleanup, restart NetworkManager/systemd-resolved as repair, or use `podlaz recover --execute` to hide a failed product scenario.

The canonical full run covers lower-release active-TUN upgrade continuity, candidate protected data plane, daemon restart and crash recovery, interruption after durable `rolling_back` authority, explicit stop/start no-reconnect semantics, same-candidate reinstall, deterministic no-direct-egress privacy evidence, collision/coexistence fixtures, a 60-minute resource soak, controlled NetworkManager reconnect when applicable, timed suspend/resume when supported, terminal convergence, three boot-autostart phases, same-boot no-retry semantics, and final restoration.

Result values:

- `QUALIFIED_PASS` means all mandatory full-qualification coverage completed without product, privacy, cleanup, durable-authority, or evidence failure.
- `PARTIAL_PASS` means useful validation completed, but explicit skips, a shortened soak, omitted reboot phases, or missing full-qualification coverage prevent a complete qualification.
- `FAIL` means a mandatory product/safety/restoration property failed or could not be proved conclusively.

By default evidence lives under the original user's `$XDG_STATE_HOME/podlaz/release-acceptance/` tree. Private evidence is separated from sanitized public `summary.txt`, `report.json`, and `requirements-observation.json`. No artifact is uploaded automatically. Real profile IDs, endpoints, SSIDs, interface names, public addresses, boot/session/transaction identifiers, and credentials must remain private.

The candidate package stays installed. Harness-created fixtures, fault-injection drop-ins/markers, synthetic terminal profile, NetworkManager boundary, and original boot-autostart manifest are restored exactly when their identity can be proved. Ambiguous cleanup retains the checkpoint and fails closed rather than deleting state by resemblance.
