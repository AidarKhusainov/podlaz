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

func runAutostartEnableCommand(ctx context.Context, args []string, stdout io.Writer, opts options) error {
	if isHelp(args) {
		printAutostartHelp(stdout)
		return nil
	}
	selector, err := parseAutostartEnableArgs(args)
	if err != nil {
		return err
	}
	store, err := profile.NewStore(opts.profileStorePath)
	if err != nil {
		return err
	}
	p, err := resolveIntentProfile(store, selector)
	if err != nil {
		return profileCommandError(err)
	}
	if err := validateCanonicalVPNProfile(p); err != nil {
		return err
	}
	request := api.AutostartConfigureRequest{Mode: planner.ModeTun, Profile: profileSnapshot(p)}
	status, err := runAutostartEnable(ctx, request, opts)
	if err != nil {
		return lifecycleCommandError(err)
	}
	renderAutostartStatus(stdout, status)
	return nil
}

func parseAutostartEnableArgs(args []string) (string, error) {
	if len(args) > 1 {
		return "", usageError("autostart enable accepts at most one profile")
	}
	if len(args) == 1 {
		if strings.HasPrefix(args[0], "-") {
			return "", usageError("unsupported autostart enable argument %q", args[0])
		}
		return args[0], nil
	}
	return "", nil
}

func runAutostartDisableCommand(ctx context.Context, args []string, stdout io.Writer, opts options) error {
	if isHelp(args) {
		printAutostartHelp(stdout)
		return nil
	}
	if len(args) != 0 {
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
	if status.Mode == planner.ModeProxyOnly {
		fmt.Fprintln(w, "Protection: Proxy only")
	}
}
