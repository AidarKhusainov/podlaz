package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/AidarKhusainov/tunwarden/internal/doctor"
	"github.com/AidarKhusainov/tunwarden/internal/recovery"
)

const version = "0.0.0-dev"

type exitError struct {
	code int
	err  error
}

func (e exitError) Error() string {
	return e.err.Error()
}

func (e exitError) Unwrap() error {
	return e.err
}

// ExitCode returns the process exit code that should be used for err.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}

	var e exitError
	if errors.As(err, &e) {
		return e.code
	}
	return 1
}

type options struct {
	doctor  func(context.Context) doctor.Report
	recover func() recovery.PlanResult
}

// Run executes the user-facing TunWarden command line interface.
func Run(ctx context.Context, args []string) error {
	return run(ctx, args, os.Stdout)
}

func run(ctx context.Context, args []string, stdout io.Writer) error {
	return runWithOptions(ctx, args, stdout, options{})
}

func runWithOptions(ctx context.Context, args []string, stdout io.Writer, opts options) error {
	if len(args) == 0 {
		printUsage(stdout)
		return nil
	}

	command := strings.ToLower(args[0])
	commandArgs := args[1:]

	switch command {
	case "help":
		return runHelp(commandArgs, stdout)
	case "-h", "--help":
		if len(commandArgs) > 0 {
			return usageError("%s does not accept arguments", args[0])
		}
		printUsage(stdout)
		return nil
	case "version", "--version":
		return runVersionCommand(commandArgs, stdout)
	case "doctor":
		return runDoctorCommand(ctx, commandArgs, stdout, opts)
	case "recover":
		return runRecoverCommand(commandArgs, stdout, opts)
	default:
		return usageError("unknown command %q", args[0])
	}
}

func runHelp(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		printUsage(stdout)
		return nil
	}
	if len(args) > 1 {
		return usageError("help accepts at most one command")
	}

	switch strings.ToLower(args[0]) {
	case "version":
		printVersionHelp(stdout)
	case "doctor":
		printDoctorHelp(stdout)
	case "recover":
		printRecoverHelp(stdout)
	default:
		return usageError("unknown help topic %q", args[0])
	}
	return nil
}

func runVersionCommand(args []string, stdout io.Writer) error {
	if isHelp(args) {
		printVersionHelp(stdout)
		return nil
	}
	if len(args) > 0 {
		return usageError("version does not accept arguments")
	}

	fmt.Fprintf(stdout, "tunwarden %s\n", version)
	return nil
}

func runDoctorCommand(ctx context.Context, args []string, stdout io.Writer, opts options) error {
	if isHelp(args) {
		printDoctorHelp(stdout)
		return nil
	}
	if len(args) > 0 {
		return unsupportedDoctorArgument(args[0])
	}

	report := runDoctor(ctx, opts)
	fmt.Fprint(stdout, report.String())
	if report.HasFailures() {
		return exitError{code: 3, err: errors.New("doctor found failing checks")}
	}
	return nil
}

func runRecoverCommand(args []string, stdout io.Writer, opts options) error {
	if isHelp(args) {
		printRecoverHelp(stdout)
		return nil
	}
	if len(args) > 0 {
		if contains(args, "--execute") {
			return usageError("recover --execute is not implemented in v0.1")
		}
		return usageError("unsupported recover argument %q", args[0])
	}

	plan := runRecover(opts)
	fmt.Fprint(stdout, plan.String())
	return nil
}

func unsupportedDoctorArgument(arg string) error {
	switch arg {
	case "--json":
		return usageError("doctor --json is not implemented yet")
	case "--core", "--network", "--dns", "--routes", "--firewall":
		return usageError("doctor %s is not implemented yet", arg)
	default:
		return usageError("unsupported doctor argument %q", arg)
	}
}

func runDoctor(ctx context.Context, opts options) doctor.Report {
	if opts.doctor != nil {
		return opts.doctor(ctx)
	}
	return doctor.Run(ctx)
}

func runRecover(opts options) recovery.PlanResult {
	if opts.recover != nil {
		return opts.recover()
	}
	return recovery.Plan()
}

func isHelp(args []string) bool {
	return len(args) == 1 && (args[0] == "-h" || args[0] == "--help")
}

func contains(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func usageError(format string, args ...any) error {
	return exitError{code: 2, err: fmt.Errorf(format, args...)}
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `TunWarden - Linux-first safe TUN VPN client foundation

Usage:
  tunwarden version
  tunwarden doctor
  tunwarden recover
  tunwarden help [command]

Current status:
  This is an early foundation build. Commands print contracts and diagnostic
  plans; they do not yet mutate system networking state.
`)
}

func printVersionHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  tunwarden version

Print the TunWarden CLI version.
`)
}

func printDoctorHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  tunwarden doctor

Run read-only local diagnostics for the current Linux host.

Implemented in v0.1:
  platform, command availability, default route, default interface, and stale
  TunWarden-owned resource detection.

Not implemented yet:
  --json, --core, --network, --dns, --routes, --firewall
`)
}

func printRecoverHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  tunwarden recover

Print the read-only recovery plan. Cleanup execution is intentionally not
implemented in v0.1; recover --execute is rejected.
`)
}
