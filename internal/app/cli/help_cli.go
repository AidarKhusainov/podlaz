package cli

import (
	"fmt"
	"io"
)

func printUsage(w io.Writer) {
	fmt.Fprint(w, `TunWarden - Linux-first safe TUN VPN client

Usage:
  tunwarden version
  tunwarden import <uri-or-file-or-url>
  tunwarden profile <add|import|list|show|delete>
  tunwarden subscription <add|list|show|update>
  tunwarden plan --mode proxy-only|tun <profile-id>
  tunwarden connect [--mode proxy-only|tun] <profile-ref>
  tunwarden disconnect
  tunwarden status
  tunwarden doctor
  tunwarden logs
  tunwarden recover
  tunwarden completion <bash|zsh|fish>
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

Report local TunWarden runtime state. The command uses daemon-backed status
when the local Unix socket API is reachable and falls back to read-only local
inspection when it is not.

Exit code 3 means stale or incomplete local state was detected.
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
`)
}

func printLogsHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  tunwarden logs [--follow] [--daemon] [--core] [--since <duration>]
  tunwarden logs -f

Print recent TunWarden logs from the system journal using journalctl. The default
source is daemon logs. --core filters Xray lifecycle and forwarded stdout/stderr
lines marked by tunwardend. This command is read-only and applies the standard
TunWarden output redaction policy before printing log lines.
`)
}

func printRecoverHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  tunwarden recover
  tunwarden recover --execute --yes [--json]

Inspect clearly TunWarden-owned resources and print the recovery plan. Without
--execute the command is read-only. Cleanup execution requires daemon access and
explicit confirmation.
`)
}
