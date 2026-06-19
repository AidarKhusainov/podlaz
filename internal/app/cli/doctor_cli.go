package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/client"
	"github.com/AidarKhusainov/podlaz/internal/doctor"
)

func runDoctorCommand(ctx context.Context, args []string, stdout io.Writer, opts options) error {
	if isHelp(args) {
		printDoctorHelp(stdout)
		return nil
	}

	doctorArgs, err := parseDoctorArgs(args)
	if err != nil {
		return err
	}

	if doctorArgs.core {
		report := runCoreDoctor(ctx, opts, doctorArgs.xrayPath)
		if doctorArgs.json {
			if err := writeJSON(stdout, doctorJSON(report)); err != nil {
				return err
			}
		} else {
			fmt.Fprint(stdout, renderDoctorCoreText(report))
		}
		if report.HasFailures() {
			return exitError{code: 3, err: errors.New("doctor --core found failing checks")}
		}
		return nil
	}

	report := runDoctor(ctx, opts)
	fmt.Fprint(stdout, report.String())
	if report.HasFailures() {
		return exitError{code: 3, err: errors.New("doctor found failing checks")}
	}
	return nil
}

type parsedDoctorArgs struct {
	core     bool
	json     bool
	xrayPath string
}

func parseDoctorArgs(args []string) (parsedDoctorArgs, error) {
	var parsed parsedDoctorArgs
	for i := 0; i < len(args); i++ {
		arg := args[i]
		value, hasInlineValue := cutFlagValue(arg)
		switch {
		case arg == "--core":
			parsed.core = true
		case arg == "--json":
			parsed.json = true
		case arg == "--xray" || strings.HasPrefix(arg, "--xray="):
			v, next, err := flagValue("doctor --core --xray", args, i, value, hasInlineValue)
			if err != nil {
				return parsed, err
			}
			parsed.xrayPath = v
			i = next
		case arg == "--network" || arg == "--dns" || arg == "--routes" || arg == "--firewall":
			return parsed, usageError("doctor %s is not implemented yet", arg)
		default:
			return parsed, usageError("unsupported doctor argument %q", arg)
		}
	}

	if !parsed.core {
		if parsed.json {
			return parsed, usageError("doctor --json is not implemented yet")
		}
		if parsed.xrayPath != "" {
			return parsed, usageError("doctor --xray requires --core")
		}
		return parsed, nil
	}
	if strings.TrimSpace(parsed.xrayPath) == "" {
		return parsed, usageError("doctor --core is not implemented yet without --xray <path>; pass --xray <path> to validate a local Xray binary")
	}
	return parsed, nil
}

func runDoctor(ctx context.Context, opts options) doctor.Report {
	if opts.daemonDoctor != nil {
		report, err := opts.daemonDoctor(ctx)
		if err == nil {
			return report
		}
		return localDoctorWithDaemonWarning(ctx, opts, err)
	}
	if opts.doctor != nil {
		return opts.doctor(ctx)
	}

	daemonDoctor, err := runDaemonDoctor(ctx, opts)
	if err == nil {
		return daemonDoctor
	}
	return localDoctorWithDaemonWarning(ctx, opts, err)
}

func runDaemonDoctor(ctx context.Context, opts options) (doctor.Report, error) {
	if opts.daemonDoctor != nil {
		return opts.daemonDoctor(ctx)
	}

	response, err := (client.DoctorClient{}).Doctor(ctx)
	if err != nil {
		return doctor.Report{}, err
	}
	return doctor.FromDaemon(response), nil
}

func localDoctorWithDaemonWarning(ctx context.Context, opts options, err error) doctor.Report {
	local := localDoctor(ctx, opts)
	message := err.Error()
	if client.IsDaemonUnavailable(err) {
		message = client.UnavailableMessage(err)
	} else {
		message = "daemon doctor API unavailable: " + message
	}
	return doctor.WithDaemonCheck(local, doctor.SeverityWarning, message)
}

func localDoctor(ctx context.Context, opts options) doctor.Report {
	if opts.doctor != nil {
		return opts.doctor(ctx)
	}
	return doctor.Run(ctx)
}

func runCoreDoctor(ctx context.Context, opts options, xrayPath string) doctor.Report {
	if opts.coreDoctor != nil {
		return opts.coreDoctor(ctx, xrayPath)
	}
	return doctor.RunCore(ctx, doctor.CoreOptions{XrayPath: xrayPath})
}
