package cli

import (
	"fmt"
	"io"
)

func printUsage(w io.Writer) {
	fmt.Fprint(w, `TunWarden - Linux-first safe TUN VPN client foundation

Usage:
  tunwarden version
  tunwarden profile <add|import|list|show|delete>
  tunwarden plan --mode proxy-only <profile-id>
  tunwarden connect [--mode proxy-only] <profile-id>
  tunwarden disconnect
  tunwarden status
  tunwarden doctor
  tunwarden logs
  tunwarden recover
  tunwarden help [command]

Current status:
  This is an early foundation build. Commands manage local profiles, print
  proxy-only plans, start and stop daemon-managed proxy-only Xray, report
  daemon-backed or local status, diagnostics, daemon/core logs, and recovery
  plans; they do not mutate TUN, routes, DNS, nftables, or firewall state.
`)
}

func printVersionHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  tunwarden version

Print the TunWarden CLI version.
`)
}

func printStatusHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  tunwarden status

Report local TunWarden runtime state. The command uses daemon-backed status
when the local Unix socket API is reachable and falls back to read-only local
inspection when it is not.

Implemented in v0.1:
  daemon-backed inactive and active proxy-only status, conservative local
  fallback, runtime directory state, stale runtime candidate summary, Xray crash
  visibility through daemon warnings, and recovery guidance.

Not implemented yet:
  --json, TUN mode, route/DNS/firewall health status
`)
}

func printDoctorHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  tunwarden doctor
  tunwarden doctor --core --xray <path> [--json]

Run read-only diagnostics for the current Linux host. The command uses
daemon-backed diagnostics when the local Unix socket API is reachable and falls
back to local read-only diagnostics when it is not. The core scope validates a
local Xray binary without starting a long-running process.

Implemented in v0.1:
  daemon-backed source reporting, local fallback, platform, command
  availability, default route, default interface, stale TunWarden-owned
  resource detection, and explicit local Xray binary validation through
  doctor --core --xray <path>.

Not implemented yet:
  doctor --json without --core, --network, --dns, --routes, --firewall
`)
}

func printLogsHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  tunwarden logs [--follow] [--daemon] [--core] [--since <duration>]
  tunwarden logs -f

Print recent TunWarden logs from the system journal using journalctl. The default
source is tunwardend daemon logs. --core filters the same service journal to
Xray lifecycle and forwarded stdout/stderr lines marked by tunwardend. This
command is read-only and applies the standard TunWarden output redaction policy
before printing log lines.

Implemented in v0.1:
  recent daemon logs, recent core logs, --follow, -f, --daemon, --core, --since

Not implemented yet:
  --json
`)
}

func printRecoverHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  tunwarden recover

Print the read-only recovery dry-run plan for clearly TunWarden-owned resources.
Cleanup execution is intentionally not implemented in v0.1; recover --execute is
rejected.
`)
}
