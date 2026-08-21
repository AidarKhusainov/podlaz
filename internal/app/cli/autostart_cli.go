package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/client"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	"github.com/AidarKhusainov/podlaz/internal/render"
)

type autostartEnableRunner func(context.Context, api.AutostartConfigureRequest) (api.AutostartStatusResponse, error)
type autostartDisableRunner func(context.Context) (api.AutostartStatusResponse, error)
type autostartStatusRunner func(context.Context) (api.AutostartStatusResponse, error)

func runAutostartCommand(ctx context.Context, args []string, stdout io.Writer, opts options) error {
	if isHelp(args) {
		printAutostartHelp(stdout)
		return nil
	}
	if len(args) == 0 {
		printAutostartHelp(stdout)
		return nil
	}

	switch strings.ToLower(args[0]) {
	case "enable":
		return runAutostartEnableCommand(ctx, args[1:], stdout, opts)
	case "disable":
		return runAutostartDisableCommand(ctx, args[1:], stdout, opts)
	case "status":
		return runAutostartStatusCommand(ctx, args[1:], stdout, opts)
	default:
		return usageError("unknown autostart subcommand %q", args[0])
	}
}

type autostartEnableArgs struct {
	mode       string
	profileRef string
}

func parseAutostartEnableArgs(args []string) (autostartEnableArgs, error) {
	parsed := autostartEnableArgs{mode: planner.ModeProxyOnly}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		value, hasInlineValue := cutFlagValue(arg)
		switch {
		case arg == "--mode" || strings.HasPrefix(arg, "--mode="):
			v, next, err := flagValue("autostart enable --mode", args, i, value, hasInlineValue)
			if err != nil {
				return parsed, err
			}
			parsed.mode = strings.ToLower(strings.TrimSpace(v))
			i = next
		case arg == "--json":
			return parsed, usageError("autostart --json is not implemented yet")
		default:
			if strings.HasPrefix(arg, "-") {
				return parsed, usageError("unsupported autostart enable argument %q", arg)
			}
			if parsed.profileRef != "" {
				return parsed, usageError("autostart enable accepts exactly one profile id")
			}
			parsed.profileRef = arg
		}
	}
	switch parsed.mode {
	case planner.ModeProxyOnly, planner.ModeTun:
	default:
		return parsed, usageError("unsupported autostart mode %q", parsed.mode)
	}
	if parsed.profileRef == "" {
		return parsed, usageError("autostart enable requires a profile id")
	}
	return parsed, nil
}

func runAutostartEnableCommand(ctx context.Context, args []string, stdout io.Writer, opts options) error {
	if isHelp(args) {
		printAutostartHelp(stdout)
		return nil
	}
	parsed, err := parseAutostartEnableArgs(args)
	if err != nil {
		return err
	}
	store, err := profile.NewStore(opts.profileStorePath)
	if err != nil {
		return err
	}
	p, err := store.Get(parsed.profileRef)
	if err != nil {
		return profileCommandError(err)
	}
	if err := validateConnectProfile(p, parsed.mode); err != nil {
		return err
	}
	request := api.AutostartConfigureRequest{Mode: parsed.mode, Profile: profileSnapshot(p)}
	status, err := runAutostartEnable(ctx, request, opts)
	if err != nil {
		return lifecycleCommandError(err)
	}
	renderAutostartStatus(stdout, status)
	return nil
}

func runAutostartDisableCommand(ctx context.Context, args []string, stdout io.Writer, opts options) error {
	if isHelp(args) {
		printAutostartHelp(stdout)
		return nil
	}
	if len(args) != 0 {
		if args[0] == "--json" {
			return usageError("autostart --json is not implemented yet")
		}
		return usageError("autostart disable does not accept arguments")
	}
	status, err := runAutostartDisable(ctx, opts)
	if err != nil {
		return lifecycleCommandError(err)
	}
	renderAutostartStatus(stdout, status)
	return nil
}

func runAutostartStatusCommand(ctx context.Context, args []string, stdout io.Writer, opts options) error {
	if isHelp(args) {
		printAutostartHelp(stdout)
		return nil
	}
	if len(args) != 0 {
		if args[0] == "--json" {
			return usageError("autostart --json is not implemented yet")
		}
		return usageError("autostart status does not accept arguments")
	}
	status, err := runAutostartStatus(ctx, opts)
	if err != nil {
		return lifecycleCommandError(err)
	}
	renderAutostartStatus(stdout, status)
	return nil
}

func runAutostartEnable(ctx context.Context, request api.AutostartConfigureRequest, opts options) (api.AutostartStatusResponse, error) {
	if opts.autostartEnable != nil {
		return opts.autostartEnable(ctx, request)
	}
	return (client.AutostartClient{}).Enable(ctx, request)
}

func runAutostartDisable(ctx context.Context, opts options) (api.AutostartStatusResponse, error) {
	if opts.autostartDisable != nil {
		return opts.autostartDisable(ctx)
	}
	return (client.AutostartClient{}).Disable(ctx)
}

func runAutostartStatus(ctx context.Context, opts options) (api.AutostartStatusResponse, error) {
	if opts.autostartStatus != nil {
		return opts.autostartStatus(ctx)
	}
	return (client.AutostartClient{}).Status(ctx)
}

func renderAutostartStatus(w io.Writer, status api.AutostartStatusResponse) {
	if !status.Enabled {
		fmt.Fprintln(w, "Autostart: Disabled")
		return
	}
	fmt.Fprintln(w, "Autostart: Enabled for next boot")
	if status.ProfileName != "" {
		fmt.Fprintf(w, "Profile: %s\n", render.Redact(status.ProfileName))
	}
	if status.Mode != "" {
		fmt.Fprintf(w, "Mode: %s\n", productModeLabel(status.Mode))
	}
}

func productModeLabel(mode string) string {
	switch strings.TrimSpace(mode) {
	case planner.ModeTun:
		return "TUN"
	case planner.ModeProxyOnly:
		return "Proxy only"
	default:
		return render.Redact(mode)
	}
}
