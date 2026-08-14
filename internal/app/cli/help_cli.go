package cli

import (
	"fmt"
	"io"
)

func printUsage(w io.Writer) {
	fmt.Fprint(w, `podlaz - Linux VPN client.

podlaz is a Linux VPN client with profile management and daemon-controlled runtime operations.
Packaged installs also provide plz as a short alias for podlaz.

Usage:
  podlaz version
  podlaz import <uri-or-file-or-url>
  podlaz profile <add|import|list|show|delete>
  podlaz subscription <add|list|show|update>
  podlaz plan --mode proxy-only|tun <profile-id>
  podlaz connect [--mode proxy-only|tun] <profile-id>
  podlaz disconnect
  podlaz check <profile-id>|--all [--target <target-id>]
  podlaz status
  podlaz doctor [--tun [--verbose|--json]]
  podlaz logs
  podlaz recover
  podlaz completion <bash|zsh|fish>
  podlaz help [command]

Alias:
  plz <command>  Short alias installed by the Debian package.
`)
}

func printVersionHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  podlaz version

Print the podlaz CLI version, source commit, and build date.
`)
}

func printStatusHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  podlaz status

Report local podlaz runtime state. The command uses daemon-backed status
when the local Unix socket API is reachable and falls back to read-only local
inspection when it is not.

Exit code 3 means stale or incomplete local state was detected.
`)
}

func printDoctorHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  podlaz doctor
  podlaz doctor --tun [--verbose|--json]
  podlaz doctor --core --xray <path> [--json]

Run read-only diagnostics for the current Linux host. The default command uses
daemon-backed diagnostics when the local Unix socket API is reachable and falls
back to local read-only diagnostics when it is not. The core scope validates a
local Xray binary without starting a long-running process.

The TUN scope is daemon-backed and inspects the active TUN session in layers:
server bypass, IPv4 policy routing, systemd-resolved ownership, DNS over UDP and
TCP, system resolution and NXDOMAIN integrity, TCP/443, TLS, HTTPS, two
independent DoH providers, IPv6 state, and guarded PMTU evidence. It does not
change routes, DNS, MTU, firewall rules, services, or browser state. --verbose
adds bounded evidence. --json emits the same schema_version=1 report model.

Exit code 3 means the TUN diagnostic status is unhealthy or unavailable.
`)
}

func printLogsHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  podlaz logs [--follow] [--daemon] [--core] [--since <duration>]
  podlaz logs -f

Print recent podlaz logs from the system journal using journalctl. The default
source is daemon logs. --core filters Xray lifecycle and forwarded stdout/stderr
lines marked by podlazd. --since accepts exactly one positive decimal integer
followed by s, m, or h (for example 30s, 15m, 2h, or 36h), up to 720h. Signed,
fractional, compound, date-like, and journalctl-native values are rejected.
This command is read-only and applies the standard podlaz output redaction
policy before printing log lines.
`)
}

func printRecoverHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  podlaz recover
  podlaz recover --execute --yes [--json]

Inspect clearly podlaz-owned resources and print the recovery plan. Without
--execute the command is read-only. Cleanup execution requires daemon access and
explicit confirmation.
`)
}
