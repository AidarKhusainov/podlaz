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
  tunwarden status
  tunwarden doctor
  tunwarden logs
  tunwarden recover
  tunwarden help [command]

Current status:
  This is an early foundation build. Commands manage local profiles, print
  proxy-only plans, daemon-backed or local status, diagnostics, daemon logs, and
  recovery plans; they do not yet mutate system networking state.
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
  daemon-backed inactive status, conservative local fallback, runtime directory
  state, stale runtime candidate summary, and recovery guidance.

Not implemented yet:
  --json, active profile/mode, proxy process lifecycle, core health
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
  tunwarden logs [--follow] [--daemon] [--since <duration>]
  tunwarden logs -f

Print recent tunwardend logs from the system journal using journalctl. This
command is read-only and applies the standard TunWarden output redaction policy
before printing log lines.

Implemented in v0.1:
  recent daemon logs, --follow, -f, --daemon, --since

Not implemented yet:
  --json, --core
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
