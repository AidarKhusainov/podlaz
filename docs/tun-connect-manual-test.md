# TUN connect manual validation

This checklist is required before treating `connect --mode tun` as an end-to-end safe TUN preview on Ubuntu/Debian hosts.

## Environment

Test on a disposable Ubuntu LTS or Debian stable VM with:

- systemd and systemd-resolved enabled,
- nftables available,
- iproute2 available,
- Xray available through `PODLAZ_XRAY_PATH` or `PATH`, with native `tun` inbound support,
- a non-production VLESS/Xray profile.

Do not run this checklist on a primary workstation until rollback and recovery behavior has been validated in a VM.

## Connect

```bash
sudo podlaz connect --mode tun <profile>
podlaz status
podlaz doctor
```

Expected high-level result:

- status shows an active TUN connection only after transaction metadata, Xray TUN config preflight, Xray startup verify, host network apply, network verify, and basic connectivity probe have passed;
- doctor reports daemon/core/TUN/routes/DNS/firewall/transaction state;
- warnings are acceptable only when they describe non-final diagnostic limitations, not failed required state.

## Host state verification

```bash
ip link show podlaz0
ip -4 rule show priority 9999
ip -4 rule show priority 10000
ip -4 route show table podlaz
resolvectl status podlaz0 --no-pager
sudo nft list table inet podlaz
```

Expected result:

- `podlaz0` exists and is up, owned by the native Xray `tun` inbound;
- podlaz policy rules exist at the planned priorities;
- routing table `podlaz` contains the planned default route;
- systemd-resolved shows the planned per-link DNS server(s) and route-only domain `~.`;
- nftables table `inet podlaz` exists with podlaz-owned rules.

## Connectivity smoke check

```bash
curl --max-time 10 https://example.com/
```

Expected result:

- request succeeds while TUN mode is active;
- `podlaz doctor` still reports the connection as healthy enough for the current preview gate.

## Disconnect cleanup

```bash
podlaz disconnect
podlaz status
podlaz doctor
ip link show podlaz0
ip -4 rule show priority 9999
ip -4 rule show priority 10000
ip -4 route show table podlaz
resolvectl status podlaz0 --no-pager
sudo nft list table inet podlaz
find /run/podlaz/generated -maxdepth 1 -type f -print
find /run/podlaz/transactions -maxdepth 1 -type f -print
```

Expected cleanup result:

- status shows inactive;
- no supervised Xray process remains;
- `podlaz0` is absent after Xray exits;
- podlaz policy rules are absent;
- table `podlaz` has no podlaz route state;
- resolved per-link state for `podlaz0` is absent or reverted;
- nftables table `inet podlaz` is absent;
- generated config and active transaction files are removed.

## Failure injection notes

Run these in a VM only:

1. Make `PODLAZ_XRAY_PATH` point to a binary whose `test -config` fails. Connect must fail before route, DNS, or nftables mutation and recovery must be able to remove any tracked generated config.
2. Make `PODLAZ_XRAY_PATH` point to a binary that exits immediately after startup. Connect must fail and roll back opened transaction/generated-config metadata.
3. Temporarily break outbound connectivity for the probe. Connect must fail before commit, roll back podlaz-owned nftables/DNS/routes/rules first, then stop Xray.
4. Kill Xray while connected. Status/doctor must report the core failure and recovery must remain possible.
5. Run `podlaz recover` after simulated daemon interruption. It must remain read-only unless explicitly executed with the documented confirmation flags.
