# TUN connect manual validation

This checklist is required before treating `connect --mode tun` as an end-to-end safe TUN preview on Ubuntu/Debian hosts.

## Environment

Test on a disposable Ubuntu LTS or Debian stable VM with:

- systemd and systemd-resolved enabled,
- nftables available,
- iproute2 available,
- Xray available through `PODLAZ_XRAY_PATH` or `PATH`,
- a tun2socks-compatible adapter available through `PODLAZ_TUN2SOCKS_PATH` or `PATH`,
- a non-production VLESS/Xray profile.

Do not run this checklist on a primary workstation until rollback and recovery behavior has been validated in a VM.

## Connect

```bash
sudo podlaz connect --mode tun <profile>
podlaz status
podlaz doctor
```

Expected high-level result:

- status shows an active TUN connection only after network apply, network verify, Xray startup verify, TUN adapter startup, and basic connectivity probe have passed;
- doctor reports daemon/core/TUN/routes/DNS/firewall/transaction state;
- warnings are acceptable only when they describe non-final diagnostic limitations, not failed required state.

## Host state verification

```bash
ip link show podlaz0
ip -4 rule show priority 51819
ip -4 rule show priority 51820
ip -4 route show table podlaz
resolvectl status podlaz0 --no-pager
sudo nft list table inet podlaz
```

Expected result:

- `podlaz0` exists and is up;
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
ip -4 rule show priority 51819
ip -4 rule show priority 51820
ip -4 route show table podlaz
resolvectl status podlaz0 --no-pager
sudo nft list table inet podlaz
find /run/podlaz/generated -maxdepth 1 -type f -print
find /run/podlaz/transactions -maxdepth 1 -type f -print
```

Expected cleanup result:

- status shows inactive;
- no supervised Xray process remains;
- no TUN adapter process remains;
- `podlaz0` is absent;
- podlaz policy rules are absent;
- table `podlaz` has no podlaz route state;
- resolved per-link state for `podlaz0` is absent or reverted;
- nftables table `inet podlaz` is absent;
- generated config and active transaction files are removed.

## Failure injection notes

Run these in a VM only:

1. Make `PODLAZ_XRAY_PATH` point to a binary that exits immediately. Connect must fail and roll back podlaz-owned networking state.
2. Make `PODLAZ_TUN2SOCKS_PATH` point to a missing binary. Connect must fail after network verification and roll back podlaz-owned networking state.
3. Temporarily break outbound connectivity for the probe. Connect must fail before commit and roll back podlaz-owned networking state.
4. Kill Xray while connected. Status/doctor must report the core failure and recovery must remain possible.
5. Run `podlaz recover` after simulated daemon interruption. It must remain read-only unless explicitly executed with the documented confirmation flags.
