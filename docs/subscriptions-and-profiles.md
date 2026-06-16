# Subscriptions and Profiles

## 1. Purpose

TunWarden must support both direct/manual connections and subscription-based profiles.

The goal is not to preserve every provider-specific detail forever. The goal is to normalize different inputs into a stable internal profile model that can be validated, tested, and converted into runtime core configuration.

Implemented manual profile management behavior is documented in [Profile management](./profile-management.md).

## 2. Input sources

### 2.1 Manual profiles

Manual profiles should be supported from the beginning.

Initial protocols to consider:

- VLESS,
- VMess,
- Trojan,
- Shadowsocks.

Manual profiles are required for development because they make networking tests independent from subscription providers.

The v0.1 foundation implementation supports explicit manual `profile add`, `profile list`, `profile show`, and `profile delete --yes` commands for persistent user-owned local profile state.

### 2.2 Subscription URLs

TunWarden must support adding subscription URLs.

HTTP(S) subscription fetches must send an explicit `User-Agent: TunWarden` request header. The value intentionally identifies TunWarden without pretending to be a browser or another VPN/proxy client, and it must not include provider tokens, user identities, operating-system details, device details, or other fine-grained fingerprinting data.

Subscription client identity behavior is owned by [Subscription client identity](./subscription-client-identity.md). TunWarden only sends a generated client identity when the subscription URL explicitly contains the `{tunwarden-client-id}` placeholder; it never guesses HWID/device query parameters or headers and never sends raw `/etc/machine-id`, MAC addresses, hostnames, DMI serials, disk serials, CPU identifiers, or other raw hardware identifiers.

Initial command shape:

```bash
tunwarden subscription add personal https://example.com/sub
tunwarden subscription update personal
tunwarden subscription list
tunwarden subscription remove personal
```

### 2.3 Imported files

Future support:

- local JSON files,
- local YAML files,
- exported Xray configs,
- sing-box configs,
- Mihomo/Clash YAML.

## 3. Subscription format families

TunWarden should be designed around format adapters.

```text
SubscriptionSource
  -> Fetcher
  -> FormatDetector
  -> Parser
  -> Normalizer
  -> Validator
  -> ProfileStore
```

Expected format families:

- Base64 list of share links,
- plain text share links,
- Xray JSON,
- sing-box JSON,
- Mihomo/Clash YAML,
- provider-specific templates such as Remnawave,
- 3x-ui compatible subscription outputs.

## 4. Share link support

Initial URI schemes:

```text
vless://
vmess://
trojan://
ss://
```

Future URI schemes:

```text
hysteria://
hysteria2://
tuic://
wireguard://
```

Unsupported URI schemes must produce clear errors, not silent skips.

## 5. Internal profile model

Every imported node must be normalized to an internal model.

Suggested fields:

```text
Profile
  id
  name
  source
  protocol
  server
  port
  user_identity
  security
  transport
  mux
  packet_encoding
  udp_support
  dns_policy
  routing_policy
  tags
  provider_metadata
  raw_source_reference
  created_at
  updated_at
```

### 5.1 Source metadata

```text
ProfileSource
  type: manual | subscription | imported_file
  subscription_id
  provider_name
  original_url
  original_format
  last_updated_at
```

### 5.2 Security model

Security fields must not be flattened into unstructured strings.

Examples:

```text
Security
  tls_enabled
  server_name
  alpn
  fingerprint
  reality
  allow_insecure
```

Reality-specific example:

```text
RealitySettings
  public_key
  short_id
  spider_x
```

### 5.3 Transport model

Examples:

```text
Transport
  type: tcp | ws | grpc | httpupgrade | xhttp | quic | kcp
  path
  host
  service_name
  headers
```

## 6. Validation requirements

### VAL-001: Required fields

A profile must not be considered connectable unless it has at least:

- protocol,
- server address,
- port,
- required identity/authentication material for that protocol.

### VAL-002: Unsupported protocol behavior

Unsupported protocols from subscriptions must be reported clearly.

They should not silently disappear unless the UI explicitly says how many entries were skipped.

### VAL-003: Invalid entry behavior

Invalid entries must not corrupt existing profiles.

A bad subscription update should keep the last known good profile set until a safe replacement exists.

### VAL-004: Secret handling

Subscription URLs, profile IDs that contain user tokens, private keys, and generated core configs must follow the redaction rules in [State and Security Requirements](./state-and-security.md).

## 7. Storage expectations

Subscription metadata is user intent and should live in user-owned state/config, not daemon-private runtime state.

Local profile state must be readable by the CLI without requiring root.

## 8. Non-goals for the foundation phase

The foundation phase should not require:

- provider account APIs,
- automatic provider login,
- dynamic purchase/payment integration,
- GUI profile editing,
- router-specific subscription logic.
