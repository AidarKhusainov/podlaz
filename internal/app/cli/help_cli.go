package cli

import (
	"fmt"
	"io"
)

func printUsage(w io.Writer) {
	fmt.Fprint(w, `podlaz - Linux VPN client.

Normal workflow:
  podlaz import <uri|url|file>
  podlaz connect [profile]
  podlaz status
  podlaz disconnect

Profiles and startup:
  podlaz profile <list|show|use|delete>
  podlaz subscription <list|show|update|delete>
  podlaz autostart <enable|disable|status>

Other:
  podlaz version
  podlaz completion <bash|zsh|fish>
  podlaz debug
  podlaz help [command]

Packaged installs also provide "plz" as a short alias for "podlaz".
Run "podlaz debug --help" for advanced diagnostics and Proxy-only operation.
`)
}

func printVersionHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  podlaz version

Print the podlaz CLI version, source commit, and build date.
`)
}

func printAutostartHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  podlaz autostart enable [profile]
  podlaz autostart disable
  podlaz autostart status

Enable snapshots the explicit profile, or the currently selected profile, as a
full VPN/TUN boot intent. It does not connect immediately. A later "profile use"
changes normal future user intent only and does not rewrite this saved boot
policy. Disable changes future-boot policy without disconnecting the current
session.
`)
}

func printStatusHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  podlaz status

Show the product connection state, selected or active profile, protection level,
and autostart policy. Detailed ownership, route, DNS, firewall, transaction, and
recovery evidence is intentionally kept under "podlaz debug doctor".
`)
}

func printDoctorHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  podlaz debug doctor
  podlaz debug doctor --tun [--verbose|--json]
  podlaz debug doctor --core --xray <path> [--json]

Run read-only diagnostics. The default scope uses daemon-backed diagnostics when
available and otherwise conservative local inspection. The TUN scope inspects
the active protected session without changing routes, DNS, MTU, firewall rules,
services, or browser state.
`)
}

func printLogsHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  podlaz debug logs [--follow] [--daemon] [--core] [--since <duration>]
  podlaz debug logs -f

Print redacted Podlaz journal output. --since accepts one positive decimal
integer followed by s, m, or h, up to 720h.
`)
}
