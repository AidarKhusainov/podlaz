package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/AidarKhusainov/tunwarden/internal/doctor"
	"github.com/AidarKhusainov/tunwarden/internal/logs"
	"github.com/AidarKhusainov/tunwarden/internal/recovery"
	"github.com/AidarKhusainov/tunwarden/internal/status"
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
	doctor           func(context.Context) doctor.Report
	coreDoctor       func(context.Context, string) doctor.Report
	daemonDoctor     func(context.Context) (doctor.Report, error)
	logs             func(context.Context, io.Writer, logs.Options) error
	profileStorePath string
	recover          func(context.Context) recovery.PlanResult
	status           func(context.Context) status.Report
	daemonStatus     func(context.Context) (status.Report, error)
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
	case "profile":
		return runProfileCommand(ctx, commandArgs, stdout, opts)
	case "plan":
		return runPlanCommand(ctx, commandArgs, stdout, opts)
	case "status":
		return runStatusCommand(ctx, commandArgs, stdout, opts)
	case "doctor":
		return runDoctorCommand(ctx, commandArgs, stdout, opts)
	case "logs":
		return runLogsCommand(ctx, commandArgs, stdout, opts)
	case "recover":
		return runRecoverCommand(ctx, commandArgs, stdout, opts)
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
	case "profile":
		printProfileHelp(stdout)
	case "plan":
		printPlanHelp(stdout)
	case "status":
		printStatusHelp(stdout)
	case "doctor":
		printDoctorHelp(stdout)
	case "logs":
		printLogsHelp(stdout)
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
