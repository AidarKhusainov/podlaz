package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/AidarKhusainov/tunwarden/internal/client"
	"github.com/AidarKhusainov/tunwarden/internal/status"
)

func runStatusCommand(ctx context.Context, args []string, stdout io.Writer, opts options) error {
	if isHelp(args) {
		printStatusHelp(stdout)
		return nil
	}
	if len(args) > 0 {
		return unsupportedStatusArgument(args[0])
	}

	report := runStatus(ctx, opts)
	fmt.Fprint(stdout, report.String())
	if report.HasUnhealthyState() {
		return exitError{code: 3, err: errors.New("status found stale or incomplete local state")}
	}
	return nil
}

func unsupportedStatusArgument(arg string) error {
	switch arg {
	case "--json":
		return usageError("status --json is not implemented yet")
	default:
		return usageError("unsupported status argument %q", arg)
	}
}

func runStatus(ctx context.Context, opts options) status.Report {
	if opts.status != nil {
		return opts.status(ctx)
	}

	daemonStatus, err := runDaemonStatus(ctx, opts)
	if err == nil {
		return daemonStatus
	}

	local := status.Inspect(ctx)
	if client.IsDaemonUnavailable(err) {
		return status.WithDaemonUnavailable(local, client.UnavailableMessage(err))
	}

	local.Warnings = append(local.Warnings, status.Warning{
		Target:  "daemon status API",
		Message: err.Error(),
	})
	if local.Connection == "inactive" {
		local.Connection = "unknown (inspection incomplete)"
	}
	return local
}

func runDaemonStatus(ctx context.Context, opts options) (status.Report, error) {
	if opts.daemonStatus != nil {
		return opts.daemonStatus(ctx)
	}

	response, err := (client.StatusClient{}).Status(ctx)
	if err != nil {
		return status.Report{}, err
	}
	return status.FromDaemon(response), nil
}
