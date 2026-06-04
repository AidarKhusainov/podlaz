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

Report local TunWarden runtime state.

Not implemented yet:
  --json, active profile/mode, proxy process lifecycle, core health
`)
}

func printDoctorHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  tunwarden doctor
  tunwarden doctor --core --xray <path> [--json]

Run read-only daemon-backed diagnostics or local fallback diagnostics. The core scope validates a local Xray binary without starting a long-running process.

Not implemented yet:
  doctor --json without --core, --network, --dns, --routes, --firewall
`)
}

func printLogsHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  tunwarden logs [--follow] [--daemon] [--since <duration>]
  tunwarden logs -f

Print recent tunwardend logs from the system journal using journalctl.

Not implemented yet:
  --json, --core
`)
}

func printRecoverHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  tunwarden recover

Print the read-only recovery dry-run plan. recover --execute is rejected in v0.1.
`)
}
