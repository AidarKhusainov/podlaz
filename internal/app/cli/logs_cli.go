package cli

import (
	"context"
	"io"
	"strings"

	"github.com/AidarKhusainov/tunwarden/internal/logs"
)

func runLogsCommand(ctx context.Context, args []string, stdout io.Writer, opts options) error {
	if isHelp(args) {
		printLogsHelp(stdout)
		return nil
	}

	logOptions, err := parseLogsArgs(args)
	if err != nil {
		return err
	}
	return runLogs(ctx, stdout, opts, logOptions)
}

func parseLogsArgs(args []string) (logs.Options, error) {
	var opts logs.Options
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if value, ok := strings.CutPrefix(arg, "--since="); ok {
			if strings.TrimSpace(value) == "" {
				return opts, usageError("logs --since requires a value")
			}
			opts.Since = value
			continue
		}

		switch arg {
		case "--follow", "-f":
			opts.Follow = true
		case "--daemon":
			// Daemon logs are the default source.
		case "--core":
			opts.Core = true
		case "--since":
			i++
			if i >= len(args) || strings.TrimSpace(args[i]) == "" || isLogsOption(args[i]) {
				return opts, usageError("logs --since requires a value")
			}
			opts.Since = args[i]
		case "--json":
			return opts, usageError("logs --json is not implemented yet")
		default:
			return opts, usageError("unsupported logs argument %q", arg)
		}
	}
	return opts, nil
}

func isLogsOption(arg string) bool {
	if _, ok := strings.CutPrefix(arg, "--since="); ok {
		return true
	}
	switch arg {
	case "--follow", "-f", "--daemon", "--since", "--json", "--core":
		return true
	default:
		return false
	}
}

func runLogs(ctx context.Context, stdout io.Writer, opts options, logOptions logs.Options) error {
	if opts.logs != nil {
		return opts.logs(ctx, stdout, logOptions)
	}
	return logs.Run(ctx, stdout, logOptions)
}
