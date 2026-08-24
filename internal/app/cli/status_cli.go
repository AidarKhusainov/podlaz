package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/client"
	"github.com/AidarKhusainov/podlaz/internal/status"
)

func runStatusCommand(ctx context.Context, args []string, stdout io.Writer, opts options) error {
	if isHelp(args) {
		printStatusHelp(stdout)
		return nil
	}
	if len(args) > 0 {
		return unsupportedStatusArgument(args[0])
	}

	report, terminalReason := runProductStatus(ctx, opts)
	autostart := productAutostartStatus(ctx, opts)
	fmt.Fprint(stdout, report.ProductView(autostart, terminalReason).String())
	if statusCommandShouldFail(report) {
		return exitError{code: 3, err: errors.New("status found unhealthy lifecycle, stale, or incomplete state")}
	}
	return nil
}

func runProductStatus(ctx context.Context, opts options) (status.Report, api.TerminalReason) {
	if opts.status != nil || opts.daemonStatus != nil {
		return runStatus(ctx, opts), ""
	}
	response, err := (client.StatusClient{}).Status(ctx)
	if err == nil {
		return statusReportFromDaemonResponse(response), response.TerminalReason
	}
	return localStatusAfterDaemonError(ctx, err), ""
}

func productAutostartStatus(ctx context.Context, opts options) *api.AutostartStatusResponse {
	if opts.autostartStatus != nil {
		value, err := opts.autostartStatus(ctx)
		if err == nil {
			return &value
		}
		return nil
	}
	if opts.status != nil || opts.daemonStatus != nil {
		return nil
	}
	value, err := (client.AutostartClient{}).Status(ctx)
	if err != nil {
		return nil
	}
	return &value
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
	return localStatusAfterDaemonError(ctx, err)
}

func localStatusAfterDaemonError(ctx context.Context, err error) status.Report {
	local := status.InspectWithOptions(ctx, status.Options{DaemonSocketAccess: daemonSocketAccessFromError(err)})
	if local.Connection == "inactive" {
		local.Connection = "unknown (inspection incomplete)"
	}
	if client.IsDaemonUnavailable(err) {
		return status.WithDaemonUnavailable(local, client.UnavailableMessage(err))
	}

	local.Warnings = append(local.Warnings, status.Warning{Target: "daemon status API", Message: err.Error()})
	return local
}

func daemonSocketAccessFromError(err error) status.DaemonSocketAccess {
	if client.IsDaemonPermissionDenied(err) {
		return status.DaemonSocketAccessPermissionDenied
	}
	return status.DaemonSocketAccessUnknown
}

func runDaemonStatus(ctx context.Context, opts options) (status.Report, error) {
	if opts.daemonStatus != nil {
		return opts.daemonStatus(ctx)
	}

	response, err := (client.StatusClient{}).Status(ctx)
	if err != nil {
		return status.Report{}, err
	}
	return statusReportFromDaemonResponse(response), nil
}

func statusReportFromDaemonResponse(response api.StatusResponse) status.Report {
	report := status.FromDaemon(response)
	if response.LifecyclePhase == api.LifecycleConnecting {
		report.Connection = "connecting"
	}
	return status.WithTunHealth(report, response.TunHealth)
}

func statusCommandShouldFail(report status.Report) bool {
	return report.Health() == status.LifecycleHealthUnhealthy || report.HasUnhealthyState()
}
