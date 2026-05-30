# Architecture

## 1. Architectural goal

TunWarden must separate unprivileged user interaction from privileged Linux networking operations.

The architecture must make dangerous operations explicit, observable, reversible, and testable.

The early architecture has two execution modes:

1. **Proxy-only mode:** starts and supervises Xray without changing system routes, DNS, firewall, or TUN state.
2. **TUN full-tunnel mode:** applies Linux networking changes only through the transaction model.

## 2. High-level components

```text
+-----------------------+
| tunwarden CLI         |
| unprivileged user     |
+-----------+-----------+
            |
            | Unix socket / D-Bus API
            v
+-----------------------+
| tunwardend            |
| privileged root daemon|
+-----------+-----------+
            |
            +----------------------------+
            |                            |
            v                            v
+-----------------------+      +-----------------------+
| Network Orchestrator  |      | Core Engine Manager   |
| routes/DNS/TUN/nft    |      | Xray, later others    |
+-----------+-----------+      +-----------+-----------+
            |                              |
            v                              v
+-----------------------+      +-----------------------+
| Linux system state    |      | Core processes        |
| iproute2/resolved/nft |      | xray                  |
+-----------------------+      +-----------------------+
```

## 3. Process model

### 3.1 CLI

The CLI must be unprivileged.

Responsibilities:

- parse user commands,
- render status and diagnostics,
- submit requests to daemon,
- print plans and errors,
- never directly mutate routes, DNS, nftables, or TUN state.

### 3.2 Daemon

The daemon must run as root under systemd.

Responsibilities:

- validate user requests,
- manage privileged operations,
- own connection state,
- manage core process lifecycle,
- perform network transactions,
- handle cleanup and recovery,
- expose a restricted local API.

The daemon should be the only long-lived owner of privileged mutable state.

### 3.3 Core process

Xray should be treated as a child engine process, not as the application supervisor.

Responsibilities:

- execute proxy protocols,
- apply generated Xray config,
- expose logs/stats if configured,
- terminate cleanly on daemon request.

The core must not be the only holder of network state. TunWarden must know what system-level changes were applied.

## 4. Privilege boundary

TunWarden must not use a SUID GUI/client binary as the primary privilege model.

Preferred model:

```text
systemd service: tunwardend.service
user command: tunwarden
IPC: Unix socket or D-Bus
optional authorization: polkit
```

The daemon API must be intentionally small.

Initial API operations:

- `Status()`
- `PlanConnect(profile_id)`
- `Connect(profile_id)`
- `Disconnect()`
- `Reconnect()`
- `Doctor()`
- `PanicReset()`
- `ListProfiles()`
- `ImportProfile(source)`
- `ImportSubscription(source)`

## 5. State model

TunWarden must distinguish persistent state from volatile state.

### 5.1 Persistent state

Suggested location:

```text
/var/lib/tunwarden/
```

Contents:

- profiles,
- subscriptions,
- subscription cache,
- user preferences,
- known provider metadata,
- last successful profile ID.

### 5.2 Volatile runtime state

Suggested location:

```text
/run/tunwarden/
```

Contents:

- active connection state,
- pending transaction state,
- applied transaction ID,
- generated core config,
- daemon socket,
- health status,
- lock files.

### 5.3 Logs

Use journald as the primary logging destination.

The CLI may provide:

```bash
tunwarden logs
tunwarden logs --follow
tunwarden logs --core
tunwarden logs --network
```

## 6. Transaction model

All full-tunnel network changes must happen through a transaction object.

Proxy-only mode does not need a network transaction because it must not modify system networking. It still needs process lifecycle state for Xray supervision and cleanup.

```text
NetworkTransaction
  id
  profile_id
  started_at
  state: planned | applying | verifying | committed | rolling_back | rolled_back | failed
  before_snapshot
  desired_plan
  applied_steps
  rollback_steps
  health_result
```

Required flow:

```text
1. Build plan
2. Acquire global network lock
3. Snapshot relevant state
4. Write pending transaction to /run/tunwarden
5. Apply steps in deterministic order
6. Verify health
7. Commit transaction
8. Remove pending marker or mark committed
```

If verification fails:

```text
1. Mark transaction as rolling_back
2. Execute rollback steps in reverse order
3. Verify direct connectivity if possible
4. Mark rolled_back or failed
```

On daemon startup:

```text
1. Read /run/tunwarden pending/active transaction state
2. Detect stale TunWarden-owned system state
3. Recover committed active connection or clean up incomplete transaction
4. Never assume previous daemon shutdown was clean
```

## 7. Planner/executor split

Network logic must be split into planners and executors.

### Planner

Pure or mostly pure code. Does not require root.

Inputs:

- current system snapshot,
- profile,
- daemon settings,
- platform capabilities.

Output:

- desired network plan,
- ordered apply steps,
- ordered rollback steps,
- warnings.

### Executor

Privileged code. Executes a validated plan.

Executors:

- `TunExecutor`,
- `RouteExecutor`,
- `DnsExecutor`,
- `FirewallExecutor`,
- `CoreExecutor`,
- `NetworkManagerExecutor`.

Executor implementations must be narrow and auditable. They should not contain hidden planning decisions.

## 8. Engine abstraction

TunWarden starts as Xray-first but should not make networking depend on Xray internals.

```text
VpnEngine
  GenerateConfig(profile, runtime_network_state) -> EngineConfig
  Start(config) -> EngineHandle
  Stop(handle) -> StopResult
  Health(handle) -> EngineHealth
  Logs(handle) -> LogStream
```

Initial engine:

- `XrayEngine`

Future engines:

- `AmneziaWgEngine`,
- `SingBoxEngine` if needed for compatibility/testing.

## 9. Network backends

TunWarden must support backend interfaces rather than hard-coding one environment.

```text
RouteBackend
  iproute2 implementation

DnsBackend
  systemd-resolved implementation
  resolvconf implementation later
  raw resolv.conf fallback only as last resort

FirewallBackend
  nftables implementation
  iptables fallback later only if needed

NetworkEventBackend
  NetworkManager implementation
  rtnetlink implementation
  systemd sleep hook implementation
```

## 10. Configuration generation

Generated core config must be treated as runtime output, not as the source of truth.

Source of truth:

```text
TunWarden profile model
TunWarden routing policy
TunWarden DNS policy
TunWarden runtime state
```

Generated files may live under:

```text
/run/tunwarden/generated/
```

## 11. Error handling principles

- Prefer explicit failure over silent partial success.
- Every failed apply step must include the command/operation, stderr, and rollback impact.
- Cleanup must be idempotent.
- Core crashes must not imply system networking cleanup was completed.
- NetworkManager limited connectivity must not automatically be treated as connection failure.
- Proxy-only failure must not trigger full network cleanup unless stale TunWarden-owned network state is detected separately.

## 12. Testing architecture

Required test layers:

1. Unit tests for profile parsing and normalization.
2. Unit tests for route/DNS/firewall planners.
3. Unit tests for engine config generation.
4. Integration tests in Linux network namespaces.
5. VM tests for Ubuntu/Debian/Fedora.
6. Suspend/resume simulation where possible.
7. Failure injection tests:
   - core crash,
   - daemon crash,
   - DNS apply failure,
   - route apply failure,
   - nft apply failure,
   - Wi-Fi/default route change.

## 13. Future GUI rule

A future GUI must be a client of the daemon API.

It must not:

- run as root,
- directly modify system networking,
- own the connection lifecycle independently from the daemon,
- become required for recovery.
