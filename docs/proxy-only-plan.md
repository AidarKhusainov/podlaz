# Proxy-only plan

`podlaz plan --mode proxy-only <profile-id>` is the implemented v0.1 dry-run command for inspecting what a proxy-only Xray runtime setup would create for a stored profile.

Canonical CLI shape is owned by [CLI contract](./cli.md). Generated runtime config safety rules are owned by [State and security requirements](./state-and-security.md). This document describes the implemented proxy-only planning behavior.

## Command shape

```bash
podlaz plan --mode proxy-only <profile-id>
podlaz plan --mode proxy-only <profile-id> --json
```

## Behavior

The command reads a stored profile from user-owned podlaz profile state and builds an inspectable plan without writing files, starting Xray, or mutating host networking.

A successful human-readable plan prints:

```text
Proxy-only plan
Profile: my-vless-profile
Profile ID: my-vless-profile-a1b2c3d4e5
Mode: proxy-only
Will generate runtime Xray config: /run/podlaz/generated/xray.json
Will listen on SOCKS: 127.0.0.1:1080
Will listen on HTTP: 127.0.0.1:8080
Will not modify TUN, routes, DNS, nftables, or firewall.
Will not start Xray or write the generated config in this dry-run.
```

The generated Xray config is built in memory so the planner can validate support and deterministic output. The command intentionally does not print the generated config content by default because it contains VLESS identity and Reality/TLS metadata needed by Xray.

## Generated Xray config model

The implemented planner generates deterministic proxy-only Xray JSON with:

- one local SOCKS inbound at `127.0.0.1:1080` using `auth: noauth` and `udp: false`;
- one local HTTP inbound at `127.0.0.1:8080` using transparent proxying disabled;
- one VLESS outbound derived from the normalized profile;
- stream settings for supported VLESS transport/security combinations.

The runtime path shown in the plan is `/run/podlaz/generated/xray.json`, but v0.1 planning keeps the config as dry-run output and does not write that path. A later explicit execution command must own atomic writes, permissions, process start, and cleanup.

## Supported profile settings

The v0.1 proxy-only planner supports stored Xray VLESS profiles that include a UUID user identity.

Supported VLESS transport settings for generated config:

- `tcp` and `raw`, rendered as Xray `raw`;
- `ws`, rendered as Xray `websocket`;
- `grpc`;
- `httpupgrade`.

Supported security settings:

- empty or `none`;
- `tls` with optional SNI, fingerprint, and ALPN;
- `reality` for `raw` and `grpc`, requiring SNI and Reality public key.

Unsupported transports such as `xhttp`, `quic`, and `kcp` fail clearly during planning until the generated config contract and fixtures cover them.

## JSON output

`plan --json` implements the common top-level JSON contract:

```json
{
  "schema_version": "v1",
  "status": "ok",
  "warnings": [],
  "errors": [],
  "mode": "proxy-only",
  "plan": {},
  "steps": [],
  "rollback_steps": []
}
```

The `plan` object includes the redacted profile identity, runtime config path, local listeners, and booleans making it explicit that planning does not write the generated config, start Xray, or modify system networking.

## Safety boundary

Proxy-only planning is read-only. It must not:

- start Xray;
- discover or execute an Xray binary;
- write generated runtime config files;
- create TUN interfaces;
- mutate routes, DNS, nftables, firewall, or other system networking state;
- claim full-tunnel leak protection.

## Deferred behavior

The following are not implemented by this command in v0.1:

- generated config file writes;
- Xray binary discovery and version checks;
- Xray process lifecycle;
- proxy health checks;
- full-tunnel planning;
- TUN, route, DNS, nftables, or firewall mutation.
