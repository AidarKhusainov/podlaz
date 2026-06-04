# Architecture

## 1. Architectural goal

TunWarden must separate unprivileged user interaction from privileged Linux networking operations.

The architecture must make high-impact operations explicit, observable, reversible, and testable.

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
| privileged daemon     |
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
- manage user-owned configuration and user state through documented paths,
- submit selected user intent to daemon,
- print plans and errors,
- never directly mutate routes, DNS, nftables, or TUN state.

### 3.2 Daemon

The daemon must run under systemd and own privileged runtime behavior.

Responsibilities:

- validate user requests,
- manage privileged operations,
- own active connection state,
- manage core process lifecycle,
- perform network transactions,
- handle recovery,
- expose a restricted local API.

The daemon should be the only long-lived owner of privileged mutable state.

### 3.3 Core process

Xray should be treated as a child engine process, not as the application supervisor.

Responsibilities:

- execute proxy protocols,
- apply generated runtime config,
- expose logs/stats if configured,
- terminate cleanly on daemon request.

The core must not be the only holder of network state. TunWarden must know what system-level changes were applied.

Core process safety requirements are owned by [State and security requirements](./state-and-security.md).

### 3.4 Current code layout

The current foundation build uses this package layout:

```text
cmd/tunwarden              user-facing CLI entrypoint
cmd/tunwardend             daemon entrypoint
internal/app/cli           CLI command dispatch
internal/app/daemon        daemon process skeleton
internal/api               shared API contracts
internal/client            CLI-side daemon client adapters
internal/daemon            daemon coordination
internal/doctor            safe local diagnostics
internal/engine            core engine lifecycle coordination
internal/logs              read-only journald/system-log integration
internal/network           transaction and network planning model
internal/network/planner   pure network planning logic
internal/network/executor  narrow platform adapters
internal/profile           normalized VPN profile model and user-owned profile storage
internal/recovery          recovery plan and future cleanup behavior
internal/render            CLI output rendering helpers
internal/service           daemon-owned product orchestration
internal/state             runtime and durable state ownership helpers
internal/sub               subscription source model
```

This layout is expected to evolve, but the CLI/daemon boundary and planner/executor split should remain stable architectural constraints.

In the foundation build, `internal/app/cli` may call user-owned profile storage, local read-only diagnostics, read-only system-log inspection, and dry-run recovery planning directly. Privileged or daemon-owned behavior must move behind the daemon client/API boundary once it is implemented.

Package dependency direction is owned by [Package boundaries](./package-boundaries.md).

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
- `PlanConnect(profile_id, mode)`
- `Connect(profile_id, mode)`
- `Disconnect()`
- `Reconnect()`
- `Doctor(scope)`
- `RecoverPlan()`
- `RecoverExecute()`
- `ListProfiles()`
- `ImportProfile(source)`
- `ImportSubscription(source)`
- `Import(source)`

## 5. State model

TunWarden must distinguish three levels of state:

1. **User intent/state:** profiles, subscriptions, preferences, selected defaults, and import metadata.
2. **Daemon runtime/state:** active connection snapshot, locks, generated runtime config, child process state, and transaction state.
3. **System networking state:** TUN interfaces, routes, rules, DNS link configuration, and nftables state.

The canonical filesystem layout and ownership rules are defined in [State and security requirements](./state-and-security.md).

Important constraints:

- User intent must not be hidden only in daemon-private directories.
- Daemon runtime state must be enough to recover without the original CLI process.
- System networking state must be identifiable as TunWarden-owned.
- Generated core config is runtime output, not persistent source of truth.

### Logs

Use journald as the primary logging destination.

The CLI may provide:

```bash
tunwarden logs
tunwarden logs --follow
tunwarden logs --core
tunwarden logs --network
```

Logs must follow the redaction policy in [State and security requirements](./state-and-security.md).

## 6. Transaction model

All full-tunnel network changes must happen through a transaction object.

Proxy-only mode does not need a network transaction because it must not modify system networking. It still needs process lifecycle state for Xray supervision and recovery.

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

Planner output must be inspectable through `tunwarden plan`.

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

Generated config permissions, atomic writes, and logging rules are owned by [State and security requirements](./state-and-security.md).

## 11. Error handling principles

- Prefer explicit failure over silent partial success.
- Every failed apply step must include the command/operation, stderr, and rollback impact.
- Cleanup must be idempotent.
- Core crashes must not imply system networking cleanup was completed.
- NetworkManager limited connectivity must not automatically be treated as connection failure.
- Proxy-only failure must not trigger full network cleanup unless stale TunWarden-owned network state is detected separately.

## 12. systemd service hardening

The daemon service must start from least privilege. The canonical hardening requirements are defined in [State and security requirements](./state-and-security.md).

A privileged daemon release is blocked until the unit file documents the final hardening choices and justifies any relaxation from the documented baseline.

## 13. Testing architecture

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

## 14. Future GUI rule

A future GUI must be a client of the daemon API.

It must not:

- run as root,
- directly modify system networking,
- own the connection lifecycle independently from the daemon,
- become required for recovery.
