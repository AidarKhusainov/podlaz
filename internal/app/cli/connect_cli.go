package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/AidarKhusainov/tunwarden/internal/api"
	"github.com/AidarKhusainov/tunwarden/internal/client"
	"github.com/AidarKhusainov/tunwarden/internal/network/planner"
	"github.com/AidarKhusainov/tunwarden/internal/profile"
	"github.com/AidarKhusainov/tunwarden/internal/render"
)

type connectRunner func(context.Context, profile.Profile, string) (api.LifecycleResponse, error)
type disconnectRunner func(context.Context) (api.LifecycleResponse, error)

func runConnectCommand(ctx context.Context, args []string, stdout io.Writer, opts options) error {
	if isHelp(args) {
		printConnectHelp(stdout)
		return nil
	}
	parsed, err := parseConnectArgs(args)
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

	response, err := runConnect(ctx, p, parsed.mode, opts)
	if err != nil {
		return lifecycleCommandError(err)
	}
	renderConnectResponse(stdout, p, response)
	return nil
}

func runDisconnectCommand(ctx context.Context, args []string, stdout io.Writer, opts options) error {
	if isHelp(args) {
		printDisconnectHelp(stdout)
		return nil
	}
	if len(args) > 0 {
		if args[0] == "--json" {
			return usageError("disconnect --json is not implemented yet")
		}
		return usageError("unsupported disconnect argument %q", args[0])
	}

	response, err := runDisconnect(ctx, opts)
	if err != nil {
		return lifecycleCommandError(err)
	}
	renderDisconnectResponse(stdout, response)
	return nil
}

type connectArgs struct {
	mode       string
	profileRef string
}

func parseConnectArgs(args []string) (connectArgs, error) {
	parsed := connectArgs{mode: planner.ModeProxyOnly}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		value, hasInlineValue := cutFlagValue(arg)
		switch {
		case arg == "--mode" || strings.HasPrefix(arg, "--mode="):
			v, next, err := flagValue("connect --mode", args, i, value, hasInlineValue)
			if err != nil {
				return parsed, err
			}
			parsed.mode = strings.ToLower(strings.TrimSpace(v))
			i = next
		case arg == "--json":
			return parsed, usageError("connect --json is not implemented yet")
		default:
			if strings.HasPrefix(arg, "-") {
				return parsed, usageError("unsupported connect argument %q", arg)
			}
			if parsed.profileRef != "" {
				return parsed, usageError("connect accepts exactly one profile id")
			}
			parsed.profileRef = arg
		}
	}
	switch parsed.mode {
	case planner.ModeProxyOnly, planner.ModeTun:
	default:
		return parsed, usageError("unsupported connect mode %q", parsed.mode)
	}
	if parsed.profileRef == "" {
		return parsed, usageError("connect requires a profile id")
	}
	return parsed, nil
}

func validateConnectProfile(p profile.Profile, mode string) error {
	if err := profile.Validate(p); err != nil {
		return err
	}
	switch mode {
	case planner.ModeProxyOnly:
		return validateXrayVLESSConnectProfile(p, "proxy-only")
	case planner.ModeTun:
		return validateXrayVLESSConnectProfile(p, "TUN-mode")
	default:
		return usageError("unsupported connect mode %q", mode)
	}
}

func validateXrayVLESSConnectProfile(p profile.Profile, modeName string) error {
	if p.Engine != profile.EngineXray {
		return fmt.Errorf("%s connect requires engine %q, got %q", modeName, profile.EngineXray, p.Engine)
	}
	if strings.ToLower(strings.TrimSpace(p.Protocol)) != "vless" {
		return fmt.Errorf("%s connect supports VLESS profiles only, got %q", modeName, p.Protocol)
	}
	if strings.TrimSpace(p.UserIdentity) == "" {
		return fmt.Errorf("%s connect requires VLESS user_identity", modeName)
	}
	if encryption := connectVLESSEncryption(p); encryption != "none" {
		return fmt.Errorf("unsupported %s VLESS encryption %q", modeName, p.Encryption)
	}
	return nil
}

func connectVLESSEncryption(p profile.Profile) string {
	encryption := strings.ToLower(strings.TrimSpace(p.Encryption))
	if encryption == "" {
		return "none"
	}
	return encryption
}

func runConnect(ctx context.Context, p profile.Profile, mode string, opts options) (api.LifecycleResponse, error) {
	if opts.connect != nil {
		return opts.connect(ctx, p, mode)
	}
	return (client.LifecycleClient{}).Connect(ctx, api.ConnectRequest{Mode: mode, Profile: profileSnapshot(p)})
}

func runDisconnect(ctx context.Context, opts options) (api.LifecycleResponse, error) {
	if opts.disconnect != nil {
		return opts.disconnect(ctx)
	}
	return (client.LifecycleClient{}).Disconnect(ctx)
}

func lifecycleCommandError(err error) error {
	if client.IsDaemonUnavailable(err) {
		return exitError{code: 5, err: errors.New(client.UnavailableMessage(err))}
	}
	return err
}

func renderConnectResponse(stdout io.Writer, p profile.Profile, response api.LifecycleResponse) {
	fmt.Fprintln(stdout, "TunWarden connection started")
	fmt.Fprintf(stdout, "Profile: %s\n", render.Redact(p.Name))
	fmt.Fprintf(stdout, "Profile ID: %s\n", render.Redact(p.ID))
	renderLifecycleFields(stdout, response)
}

func renderDisconnectResponse(stdout io.Writer, response api.LifecycleResponse) {
	fmt.Fprintln(stdout, "TunWarden disconnected")
	renderLifecycleFields(stdout, response)
}

func renderLifecycleFields(stdout io.Writer, response api.LifecycleResponse) {
	fmt.Fprintf(stdout, "Connection: %s\n", render.Redact(response.Connection))
	if response.Mode != "" {
		fmt.Fprintf(stdout, "Mode: %s\n", render.Redact(response.Mode))
	}
	fmt.Fprintf(stdout, "Proxy: %s\n", render.Redact(response.Proxy))
	fmt.Fprintf(stdout, "TUN: %s\n", render.Redact(response.TUN))
	if response.Routes != "" {
		fmt.Fprintf(stdout, "Routes: %s\n", render.Redact(response.Routes))
	}
	if response.DNS != "" {
		fmt.Fprintf(stdout, "DNS: %s\n", render.Redact(response.DNS))
	}
	if response.Firewall != "" {
		fmt.Fprintf(stdout, "Firewall: %s\n", render.Redact(response.Firewall))
	}
	if response.RuntimeConfigPath != "" {
		fmt.Fprintf(stdout, "Runtime config: %s\n", render.Redact(response.RuntimeConfigPath))
	}
	if len(response.Warnings) > 0 {
		fmt.Fprintf(stdout, "Warnings: %d\n", len(response.Warnings))
		for _, warning := range response.Warnings {
			fmt.Fprintf(stdout, "- %s\n", render.Redact(warning))
		}
	}
}

func profileSnapshot(p profile.Profile) api.ProfileSnapshot {
	encoded, err := json.Marshal(p)
	if err != nil {
		return api.ProfileSnapshot{}
	}
	var snapshot api.ProfileSnapshot
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		return api.ProfileSnapshot{}
	}
	return snapshot
}

func printConnectHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  tunwarden connect [--mode proxy-only|tun] <profile-id>

Start the stored profile through the daemon-managed lifecycle. The default mode
is proxy-only. TUN mode requires daemon networking privileges.
`)
}

func printDisconnectHelp(w io.Writer) {
	fmt.Fprint(w, `Usage:
  tunwarden disconnect

Stop proxy-only Xray or roll back an active TunWarden-owned TUN transaction.
Repeated disconnects are safe and leave the connection inactive.
`)
}
