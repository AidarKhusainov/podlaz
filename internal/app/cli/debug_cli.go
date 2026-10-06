package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
)

func runDebugCommand(ctx context.Context, args []string, stdout io.Writer, opts options) error {
	if len(args) == 0 || isHelp(args) {
		printDebugHelp(stdout)
		return nil
	}

	switch strings.ToLower(args[0]) {
	case "doctor":
		return runDoctorCommand(ctx, args[1:], stdout, opts)
	case "logs":
		return runLogsCommand(ctx, args[1:], stdout, opts)
	case "proxy":
		return runProxyConnectCommand(ctx, args[1:], stdout, opts)
	case "recover":
		return runRecoverCommand(ctx, args[1:], stdout, opts)
	default:
		return usageError("unknown debug subcommand %q", args[0])
	}
}

func printDebugHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  podlaz debug doctor [options]
  podlaz debug logs [options]
  podlaz debug proxy <profile>
  podlaz debug recover [--execute]

Advanced diagnostics and reduced-protection operation. These commands are not
part of the normal VPN workflow. "debug proxy" is explicit Proxy-only operation;
it never silently substitutes for a failed full VPN connection.
`)
}

func printDebugProxyHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  podlaz debug proxy <profile>

Start the selected profile in Proxy-only mode. This is an explicit advanced path
for profiles that cannot participate in the canonical full VPN/TUN lifecycle.
It does not replace an active protected TUN session.
`)
}
