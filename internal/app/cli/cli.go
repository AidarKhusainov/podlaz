package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/doctor"
	"github.com/AidarKhusainov/podlaz/internal/logs"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	"github.com/AidarKhusainov/podlaz/internal/recovery"
	"github.com/AidarKhusainov/podlaz/internal/status"
)

var version = "0.0.0-dev"
var commit = ""
var built = ""

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
	connect               connectRunner
	disconnect            disconnectRunner
	doctor                func(context.Context) doctor.Report
	coreDoctor            func(context.Context, string) doctor.Report
	daemonDoctor          func(context.Context) (doctor.Report, error)
	logs                  func(context.Context, io.Writer, logs.Options) error
	profileStorePath      string
	subscriptionStorePath string
	recover               func(context.Context) recovery.PlanResult
	recoverExecute        func(context.Context) (recovery.ExecuteResult, error)
	status                func(context.Context) status.Report
	daemonStatus          func(context.Context) (status.Report, error)
	systemSnapshot        func(context.Context, netsnapshot.Options) netsnapshot.Snapshot
	stdin                 io.Reader
	stdinIsTerminal       func() bool
}

// Run executes the user-facing podlaz command line interface.
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
	if err := guardSudoUserStateCommand(command, commandArgs); err != nil {
		return err
	}

	switch command {
	case "__complete":
		return runCompletionRuntimeCommand(commandArgs, stdout, opts)
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
	case "completion":
		return runCompletionCommand(commandArgs, stdout)
	case "import":
		return runImportCommand(ctx, commandArgs, stdout, opts)
	case "profile":
		return runProfileCommand(ctx, commandArgs, stdout, opts)
	case "subscription":
		return runSubscriptionCommand(ctx, commandArgs, stdout, opts)
	case "plan":
		return runPlanCommand(ctx, commandArgs, stdout, opts)
	case "connect":
		return runConnectCommand(ctx, commandArgs, stdout, opts)
	case "disconnect":
		return runDisconnectCommand(ctx, commandArgs, stdout, opts)
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
	case "completion":
		printCompletionHelp(stdout)
	case "import":
		printImportHelp(stdout)
	case "profile":
		printProfileHelp(stdout)
	case "subscription":
		printSubscriptionHelp(stdout)
	case "plan":
		printPlanHelp(stdout)
	case "connect":
		printConnectHelp(stdout)
	case "disconnect":
		printDisconnectHelp(stdout)
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

	fmt.Fprintf(stdout, "podlaz version %s\n", versionValue())
	fmt.Fprintf(stdout, "commit: %s\n", fallback(commit, "unknown"))
	fmt.Fprintf(stdout, "built: %s\n", fallback(built, "unknown"))
	return nil
}

func versionValue() string {
	return fallback(version, "dev")
}

func fallback(value string, defaultValue string) string {
	if value == "" {
		return defaultValue
	}
	return value
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
